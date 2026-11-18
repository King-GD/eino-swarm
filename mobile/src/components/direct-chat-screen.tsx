import { usePhoneAccount } from "@/components/gateway-account-shell"
import { useCallback, useEffect, useRef, useState } from "react"
import { ChevronLeft } from "lucide-react"

import { AccountButton } from "@/components/account-button"
import { Composer, type ComposerExtra } from "@/components/composer"
import { GeneratedImageResult } from "@/components/generated-image-result"
import { HostChrome } from "@/components/host-chrome"
import { PhoneMarkdown } from "@/components/markdown"
import { ThreadLog } from "@/components/thread-blocks"
import { Button } from "@/components/ui/button"
import {
  AttachmentTooBig,
  readComposerFiles,
  turnsFromMessages,
} from "@/lib/attachments"
import { defaultTimeoutSeconds, findProvider, mintID, providerChoices, type DirectProvider } from "@/lib/direct-provider"
import {
  loadThreads,
  newThread,
  saveThreads,
  threadTitle,
  type DirectMessage,
  type DirectThread,
} from "@/lib/direct-threads"
import { loadDirectImage, removeDirectImage, saveDirectImage, type DirectImageRef } from "@/lib/direct-images"
import { assertGatewayImageTarget, gatewayImageTarget, generatedImageFilename, generateGatewayImage, ImageGenerationError, validateImagePrompt } from "@/lib/gateway-image"
import { imageGenerationLabel } from "@/lib/image-generation-labels"
import { t } from "@/lib/i18n"
import { parseSlashSubmit } from "../../../frontend/src/lib/slash-core"
import type { CompactBlock } from "@/lib/transcript"
import { streamCompletion } from "@/lib/openai-client"
import { ModelCallError, redact } from "@/lib/openai-wire"
import type { SavedLink } from "@/lib/store"
import { timeAgo } from "@/lib/when"

const thinking = ["low", "medium", "high"]

/** A conversation that lives on the phone. Same composer as a PC thread:
 *  model, thinking level, image or file. The call does not go through the PC. */
export function DirectChatScreen({
  hosts,
  activeFingerprint,
  path,
  connected,
  reconnecting,
  providers,
  gatewaySourceID,
  gatewaySourceIDs,
  onSelectHost,
  onAddHost,
  onUnlink,
  onToggleLocale,
  onModels,
  onOpenAccount,
  onOpenChange,
  onBindClose,
  complete = streamCompletion,
}: {
  hosts: SavedLink[]
  activeFingerprint: string
  path: string
  connected?: boolean
  reconnecting?: boolean
  providers: DirectProvider[]
  gatewaySourceID?: string
  gatewaySourceIDs?: string[]
  onSelectHost: (fingerprint: string) => void
  onAddHost: () => void
  onUnlink: () => void
  onToggleLocale?: () => void
  onModels: () => void
  onOpenAccount?: () => void
  onOpenChange?: (open: boolean) => void
  onBindClose?: (close: () => void) => void
  complete?: typeof streamCompletion
}) {
  const sourceID = gatewaySourceID ?? providers.find((row) => row.sourceKind === "gateway")?.id ?? ""
  const account = usePhoneAccount()
  const sourceIDs = new Set(gatewaySourceIDs ?? [sourceID, ...providers.filter(row => row.sourceKind === "gateway").map(row => row.id)])
  const visible = (row: DirectThread) => (account.mode !== "account" && !row.providerId.startsWith("gateway:")) || sourceIDs.has(row.providerId)
  const [threads, setThreads] = useState(loadThreads)
  const threadsRef = useRef(threads)
  const [openID, setOpenID] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [notice, setNotice] = useState("")
  const [pick, setPick] = useState(() => firstPick(providers, loadThreads().find(visible)))
  const stopRef = useRef<AbortController | null>(null)
  const busyRef = useRef(false)
  const activeSource = useRef<{ id: string; key: string } | null>(null)
  const visibleThreads = threads.filter(visible)
  const open = visibleThreads.find((row) => row.id === openID) ?? null
  const models = providerChoices(providers)
  const selectedID = open?.providerId || pick.providerId
  const selectedUnavailable = Boolean(selectedID) && !findProvider(providers, selectedID)
  useEffect(() => {
    const source = activeSource.current
    if (source && !providers.some((row) => row.id === source.id && row.apiKey === source.key)) stopRef.current?.abort("stop")
  }, [providers])
  // Hiding an endpoint leaves it in place, so an open chat can stay on it.
  // Removing it does not: a new chat moves to a remaining endpoint. An open
  // thread keeps the provider id it already stored. Recheck when leaving history,
  // which may have selected a source that is no longer available for a new chat.
  useEffect(() => {
    setPick((current) => {
      if (findProvider(providers, current.providerId)) return current
      const choice = providerChoices(providers)[0]
      const next = {
        providerId: choice?.provider_id ?? "",
        model: choice?.model ?? "",
        reasoning: "",
      }
      if (next.providerId === current.providerId && next.model === current.model) return current
      return next
    })
  }, [providers, openID])

  useEffect(() => {
    onOpenChange?.(Boolean(openID))
  }, [openID, onOpenChange])

  useEffect(() => {
    onBindClose?.(() => {
      stopRef.current?.abort("stop")
      setOpenID(null)
    })
  }, [onBindClose])

  useEffect(() => {
    return () => {
      stopRef.current?.abort("stop")
    }
  }, [])

  const remember = (next: DirectThread[], required = false) => {
    if (!saveThreads(next, threadsRef.current) && required) throw new ImageGenerationError("image_storage_failed")
    threadsRef.current = next
    setThreads(next)
  }

  const stop = () => {
    stopRef.current?.abort("stop")
  }

  const leave = () => {
    stop()
    setOpenID(null)
  }

  const send = async (text: string, extra?: ComposerExtra) => {
    if (busyRef.current) throw new Error("busy")
    const providerId = extra?.providerId || pick.providerId
    const model = extra?.model || pick.model
    const reasoning = extra?.reasoning ?? pick.reasoning
    const provider = findProvider(providers, providerId)
    const command = parseSlashSubmit(text)
    let imageCommand: { prompt: string; target: NonNullable<ReturnType<typeof gatewayImageTarget>> } | undefined
    if (command?.id === "image") {
      try {
        if (extra?.images.length || extra?.files.length) throw new ImageGenerationError("image_text_only")
        const prompt = validateImagePrompt(command.arg)
        const target = provider && gatewayImageTarget(provider)
        if (!target) throw new ImageGenerationError("image_unavailable")
        imageCommand = { prompt, target }
      } catch (error) {
        if (error instanceof ImageGenerationError) setNotice(imageGenerationLabel(error.code))
        throw error
      }
    }
    if (!provider || !model) throw new Error("no-model")
    busyRef.current = true
    setPending(true)
    const ctrl = new AbortController()
    stopRef.current = ctrl
    activeSource.current = { id: provider.id, key: provider.apiKey }
    let committed = false
    try {
      const files = [...(extra?.images ?? []), ...(extra?.files ?? [])]
      let chips: DirectMessage["attachments"]
      try {
        chips = (await readComposerFiles(files)).chips
        setNotice("")
      } catch (err) {
        if (err instanceof AttachmentTooBig) setNotice(t("chat.tooBig"))
        throw err
      }
      if (ctrl.signal.aborted) return
      const user: DirectMessage = {
        id: mintID("m"),
        role: "user",
        text,
        attachments: chips,
      }
      const assistant: DirectMessage = { id: mintID("m"), role: "assistant", text: "" }
      const prev = open
      const base: DirectThread = prev
        ? { ...prev, messages: prev.messages.slice() }
        : newThread({ providerId, model, reasoning })
      base.title = base.title || threadTitle(text, chips?.[0]?.name ?? "") || t("chat.tab")
      base.providerId = providerId
      base.model = model
      base.reasoning = reasoning
      base.updatedAt = Date.now()
      base.messages = [...base.messages, user, assistant]
      const next = [base, ...threadsRef.current.filter((row) => row.id !== base.id)]
      remember(next, Boolean(imageCommand))
      setOpenID(base.id)
      setPick({ providerId, model, reasoning })
      committed = true
      const latest: DirectMessage = { ...assistant }
      const patch = (message: DirectMessage, required = false) => {
        const updated = threadsRef.current.map(row =>
          row.id !== base.id ? row : {
            ...row,
            messages: row.messages.map(item => item.id === assistant.id ? message : item),
          })
        remember(updated, required)
      }
      const runImage: NonNullable<Parameters<typeof streamCompletion>[0]["generateImage"]> = async (target, prompt, signal, timeout, transport) => {
        let saved: DirectImageRef | undefined
        try {
          latest.image = { status: "running" }
          patch({ ...latest }, true)
          const result = await generateGatewayImage(target, prompt, signal, timeout, transport)
          const id = mintID("img")
          const ref = { id, name: generatedImageFilename(id, result.mime), mime: result.mime, width: result.width, height: result.height }
          await saveDirectImage(ref, result.blob)
          saved = ref
          if (signal.aborted) throw new ImageGenerationError("image_cancelled")
          assertGatewayImageTarget(target)
          latest.image = { status: "completed", image: ref }
          patch({ ...latest }, true)
          return ref
        } catch (error) {
          if (saved) await removeDirectImage(saved.id).catch(() => undefined)
          const failure = error instanceof ImageGenerationError ? error : new ImageGenerationError("image_storage_failed")
          const status = failure.kind === "abort" ? "cancelled" : failure.code === "image_result_unknown" ? "unknown" : "failed"
          latest.image = { status, code: failure.code }
          patch({ ...latest })
          throw failure
        }
      }
      try {
        if (imageCommand) {
          const timeout = provider.timeoutSeconds > 0 ? provider.timeoutSeconds : defaultTimeoutSeconds
          await runImage(imageCommand.target, imageCommand.prompt, ctrl.signal, timeout * 1000)
          return
        }
        // Chips already carry the bytes. Rebuilding from them sends a file once.
        const history = turnsFromMessages(base.messages.filter((item) => item.id !== assistant.id))
        await complete({
          provider,
          model,
          reasoning,
          turns: history,
          signal: ctrl.signal,
          generateImage: runImage,
          onDelta: (piece) => {
            latest.text = piece.text
            latest.reasoning = piece.reasoning
            patch({ ...latest })
          },
          onTool: (event) => {
            const before = latest.tools ?? []
            latest.tools = before.some((tool) => tool.id === event.id)
              ? before.map((tool) => tool.id === event.id ? event : tool)
              : [...before, event]
            patch({ ...latest })
          },
        })
      } catch (err) {
        if (err instanceof ModelCallError && err.kind === "abort") return
        if (err instanceof ImageGenerationError) {
          if (!latest.image) patch({ ...latest, image: { status: "failed", code: err.code } })
          return
        }
        patch({ ...latest, error: explain(err, provider.apiKey) })
      }
    } catch (err) {
      if (!committed) {
        if (err instanceof ImageGenerationError) setNotice(imageGenerationLabel(err.code))
        throw err
      }
    } finally {
      if (stopRef.current === ctrl) { stopRef.current = null; activeSource.current = null }
      busyRef.current = false
      setPending(false)
    }
  }

  return (
    <main className="mx-auto flex h-full max-w-lg flex-col overflow-hidden">
      {open ? (
        <header className="flex h-12 shrink-0 items-center gap-1 border-b border-border px-1">
          <Button
            variant="ghost"
            className="size-10 shrink-0 px-0"
            aria-label={t("thread.back")}
            onClick={leave}
          >
            <ChevronLeft className="size-5" aria-hidden />
          </Button>
          <h1 className="min-w-0 flex-1 truncate text-sm font-medium">{open.title || t("chat.tab")}</h1>
          {pending ? (
            <Button variant="ghost" className="h-8 shrink-0 px-2 text-destructive" onClick={stop}>
              {t("thread.stop")}
            </Button>
          ) : null}
          {onOpenAccount ? <AccountButton onClick={onOpenAccount} /> : null}
        </header>
      ) : (
        <HostChrome
          hosts={hosts}
          activeFingerprint={activeFingerprint}
          path={path}
          connected={connected}
          reconnecting={reconnecting}
          showChat
          chatSelected
          onSelect={onSelectHost}
          onSelectChat={() => setOpenID(null)}
          onAdd={onAddHost}
          onNewChat={() => setOpenID(null)}
          onUnlink={onUnlink}
          onModels={onModels}
          onToggleLocale={onToggleLocale}
        />
      )}
      {open ? (
        <div data-testid="transcript" className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
          <div className="flex flex-col gap-1.5">
            {open.messages.map((message) => (
              <MessageRow key={message.id} message={message} live={pending && message.id === open.messages.at(-1)?.id} />
            ))}
          </div>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
          {visibleThreads.length === 0 ? (
            <div className="px-1">
              <p className="text-sm font-medium">{t("chat.emptyTitle")}</p>
              <p className="pt-1 text-[13px] text-muted-foreground">{t("chat.emptyHint")}</p>
            </div>
          ) : (
            <ul className="flex flex-col gap-2">
              {visibleThreads.map((row) => (
                <li key={row.id}>
                  <button
                    type="button"
                    className="flex w-full flex-col rounded-xl bg-muted px-3 py-2 text-left"
                    aria-label={t("home.open", { title: row.title || t("chat.tab") })}
                    onClick={() => {
                      setPick({
                        providerId: row.providerId || pick.providerId,
                        model: row.model || pick.model,
                        reasoning: row.reasoning,
                      })
                      setOpenID(row.id)
                    }}
                  >
                    <span className="truncate text-sm font-medium">{row.title || t("chat.tab")}</span>
                    <span className="truncate text-xs text-muted-foreground">
                      {[row.model, timeAgo(new Date(row.updatedAt).toISOString())].filter(Boolean).join(" · ")}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          {models.length === 0 ? (
            <p className="px-1 pt-3 text-[13px] text-muted-foreground">{t("chat.needModel")}</p>
          ) : null}
        </div>
      )}
      <Composer
        key={openID ?? "new"}
        label={t("thread.message")}
        sendLabel={t("thread.send")}
        disabled={models.length === 0}
        pending={pending}
        stopLabel={t("thread.stop")}
        onStop={pending ? stop : undefined}
        hint={notice || (selectedUnavailable ? t("account.sourceUnavailable") : undefined)}
        hideModel={account.mode === "account"}
        models={models}
        onRefreshModels={account.refreshModels}
        refreshBusy={account.modelsBusy}
        refreshError={account.modelsError ? t("account.modelsFailed") : undefined}
        reasoningLevels={models.length ? thinking : []}
        providerId={open?.providerId || pick.providerId}
        model={open?.model || pick.model}
        reasoning={open?.reasoning ?? pick.reasoning}
        onTune={(next) => {
          setPick(next)
          if (!open) return
          const updated = threadsRef.current.map((row) =>
            row.id === open.id
              ? { ...row, providerId: next.providerId, model: next.model, reasoning: next.reasoning }
              : row,
          )
          remember(updated)
        }}
        onSubmit={(text, extra) => send(text, extra)}
      />
    </main>
  )
}

function MessageRow({ message, live }: { message: DirectMessage; live: boolean }) {
  const image = message.image?.image
  const load = useCallback((signal: AbortSignal) => {
    if (!image) return Promise.reject(new ImageGenerationError("image_storage_failed"))
    return loadDirectImage(image, signal)
  }, [image])
  if (message.role === "user") {
    return (
      <div className="flex justify-end">
        <div className="max-w-[85%] rounded-2xl bg-muted px-3 py-2">
          <AttachmentChips items={message.attachments} />
          {message.text ? <PhoneMarkdown text={message.text} /> : null}
        </div>
      </div>
    )
  }
  return (
    <div className="min-w-0 max-w-full">
      <AttachmentChips items={message.attachments} />
      {message.image ? <GeneratedImageResult status={message.image.status} code={message.image.code} media={image} load={load} /> : null}
      <ThreadLog blocks={assistantBlocks(message, live)} running={live && !message.error} />
      {message.error ? (
        <p className="text-sm text-destructive" role="alert">
          {message.error}
        </p>
      ) : null}
    </div>
  )
}

function assistantBlocks(message: DirectMessage, live: boolean): CompactBlock[] {
  const blocks: CompactBlock[] = []
  const thought = message.reasoning ?? ""
  const thinking = live && !message.text && !message.error && !message.image
  if (thought || thinking) {
    blocks.push({
      id: `${message.id}:thought`,
      kind: "reasoning",
      text: thought,
      streaming: thinking,
    })
  }
  if (message.text) {
    blocks.push({
      id: `${message.id}:answer`,
      kind: "answer",
      text: message.text,
      streaming: live && !message.error,
    })
  }
  const toolBlocks: CompactBlock[] = (message.tools ?? []).map((tool) => ({
    id: `${message.id}:tool:${tool.id}`, kind: "tool", callId: tool.callId ?? tool.id, toolName: tool.name,
    args: tool.args, text: tool.text + (tool.truncated ? "\n…" : ""), pending: live && tool.status === "running",
    failed: tool.status === "failed" || !live && tool.status === "running",
    cancelled: tool.status === "cancelled",
  }))
  blocks.splice(thought || thinking ? 1 : 0, 0, ...toolBlocks)
  return blocks
}

function AttachmentChips({ items }: { items: DirectMessage["attachments"] }) {
  if (!items?.length) return null
  return (
    <ul className="mb-1 flex flex-col gap-1">
      {items.map((item) => (
        <li key={item.name} className="truncate text-xs text-muted-foreground">
          {item.name}
        </li>
      ))}
    </ul>
  )
}

function firstPick(
  providers: DirectProvider[],
  thread: DirectThread | undefined,
): { providerId: string; model: string; reasoning: string } {
  if (thread?.model) {
    return { providerId: thread.providerId, model: thread.model, reasoning: thread.reasoning }
  }
  const choice = providerChoices(providers)[0]
  return {
    providerId: choice?.provider_id ?? "",
    model: choice?.model ?? "",
    reasoning: "",
  }
}

function explain(err: unknown, secret: string): string {
  if (err instanceof AttachmentTooBig) return t("chat.tooBig")
  if (err instanceof ModelCallError) {
    if (err.kind === "timeout") return t("chat.timedOut")
    if (err.kind === "abort") return ""
    if (err.message === "gateway_login_required") return t("account.webLoginRequired")
    if (err.message === "gateway_access_denied") return t("account.accessDenied")
    if (err.message === "cloud_tools_model_unsupported") return t("account.webModelUnsupported")
    if (err.message === "cloud_tools_login") return t("account.expired")
    if (err.message === "cloud_tools_denied") return t("account.webDenied")
    if (err.message.startsWith("cloud_tools_") && err.message.endsWith("limit")) return t("account.webLimit")
    if (err.message.startsWith("cloud_tools_")) return t("account.webFailed")
    const message = redact(err.message, secret)
    if (message === "network" || message === "parse") return t("chat.quiet")
    if (!message || message === "bad-url") {
      return t("chat.failed")
    }
    return message
  }
  return t("chat.failed")
}
