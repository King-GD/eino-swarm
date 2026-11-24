import { capabilityAllows } from "../../../frontend/src/lib/capabilities"
import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { ArrowUp, Brain, Cpu, Loader2, Plus, Square, X } from "lucide-react"
import type { InsertMode } from "../../../frontend/src/lib/delivery-mode"

import { installAndroidBack } from "@/lib/android-back"
import { cn } from "@/lib/cn"
import { applySlashPick, type PhoneSlashCommand } from "@/lib/composer-commands"
import { t } from "@/lib/i18n"
import { formatQuotedMessage } from "@/lib/quote"
import type { ComposerPrediction } from "../../../frontend/src/lib/composer-prediction"
import {
  clearSlashCommand,
  filterSlashCommands,
  nextSlashIndex,
  normalizeSlashPrefix,
  parseSlashSubmit,
  slashAwaitingArg,
  slashDraft,
  stripSlashToken,
  type SlashCommand,
} from "../../../frontend/src/lib/slash-core"
import { isVisionFile } from "@/lib/put-chunks"
import type { ModelChoice } from "@/lib/rpc"
import { ComposerAddMenu, ComposerSlashMenu } from "./composer-menu"
import { DeliveryMenu } from "./delivery-menu"
import { ModelPicker, ThinkingPicker, thinkingLabel } from "./model-picker"
import { SpeechCapture, type SpeechCapturePhase } from "./speech-capture"
import { insertSpeechText } from "../../../frontend/src/lib/speech-text"

const noModels: ModelChoice[] = []
const noLevels: string[] = []
const settingClass = "flex h-9 min-w-0 items-center gap-1.5 rounded-full px-1.5 text-xs font-medium"
const settingButton = "text-foreground/85 active:bg-accent disabled:opacity-40"

export type ComposerExtra = {
  files: File[]
  images: File[]
  providerId: string
  model: string
  reasoning: string
}

type Send = (text: string, extra?: ComposerExtra) => void | Promise<void>

/** One input for the whole app: starting a conversation and answering inside
 *  one are the same gesture, so they are the same control. */
export function Composer({
  label,
  placeholder,
  prediction,
  sendLabel,
  disabled,
  pending,
  hint,
  above,
  quotes = [],
  onQuotesChange,
  onInsert,
  stopLabel,
  onStop,
  onSubmit,
  models = noModels,
  reasoningLevels = noLevels,
  providerId = "",
  model = "",
  reasoning = "",
  catalogBusy = false,
  onRefreshModels,
  refreshBusy,
  refreshError,
  onTune,
  hideModel = false,
  slashCommands,
}: {
  label: string
  prediction?: ComposerPrediction
  placeholder?: string
  sendLabel: string
  disabled?: boolean
  pending?: boolean
  hint?: string
  above?: ReactNode
  quotes?: string[]
  onQuotesChange?: (quotes: string[]) => void
  onInsert?: (mode: InsertMode, text: string, extra?: ComposerExtra) => void | Promise<void>
  stopLabel?: string
  onStop?: () => void | Promise<void>
  onSubmit: Send
  models?: ModelChoice[]
  reasoningLevels?: string[]
  providerId?: string
  model?: string
  reasoning?: string
  catalogBusy?: boolean
  onRefreshModels?: () => void | Promise<void>
  refreshBusy?: boolean
  refreshError?: string
  onTune?: (next: { providerId: string; model: string; reasoning: string }) => void
  hideModel?: boolean
  slashCommands?: readonly PhoneSlashCommand[]
}) {
  const [dismissedPrediction, setDismissedPrediction] = useState<string>()
  const [text, setText] = useState("")
  const textRef = useRef(text)
  textRef.current = text
  const speechAnchor = useRef({ text: "", caret: 0 })
  const [speechPhase, setSpeechPhase] = useState<SpeechCapturePhase>("idle")
  const capturingSpeech = speechPhase === "preparing" || speechPhase === "recording"
  const speechBusy = capturingSpeech || speechPhase === "transcribing"
  const [commandRejected, setCommandRejected] = useState<"text" | "prompt">()
  const [addOpen, setAddOpen] = useState(false)
  const [slashIndex, setSlashIndex] = useState(0)
  const [attached, setAttached] = useState<File[]>([])
  const [pick, setPick] = useState(() => chosen(models, providerId, model))
  const explicitPick = useRef(false)
  const [level, setLevel] = useState(reasoning)
  const [editing, setEditing] = useState(false)
  const [picker, setPicker] = useState<"model" | "thinking">()
  const [keyboardInset, setKeyboardInset] = useState(0)
  const modelTextId = useId()
  const thinkingTextId = useId()
  const dock = useRef<HTMLDivElement>(null)
  const mouseDown = useRef(false)
  const box = useRef<HTMLTextAreaElement>(null)
  const formRef = useRef<HTMLFormElement>(null)
  const addRef = useRef<HTMLButtonElement>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const pinCaret = useRef<number | null>(null)
  const imageAllowed = !pick.providerId.startsWith("gateway:") || capabilityAllows(models.find(row => row.provider_id === pick.providerId)?.capabilities, "ocr")
  const images = attached.filter((file) => isVisionFile(file))
  const files = attached.filter((file) => !isVisionFile(file))
  const hasDraft = text.trim().length > 0 || attached.length > 0 || quotes.some((quote) => quote.trim().length > 0)
  const suggestion = !hasDraft && !disabled && !pending && prediction?.turnId !== dismissedPrediction ? prediction : undefined
  const insertSuggestion = () => { if (suggestion) { setText(suggestion.text); box.current?.focus() } }
  const command = parseSlashSubmit(text)
  const imageCommand = command?.id === "image"
  const hasQuotes = quotes.some((quote) => quote.trim())
  const textOnlyRefusal = imageCommand
    ? hasQuotes || attached.length > 0
    : hasQuotes && (command?.id === "goal" || command?.id === "plan")
  const showStop = Boolean(onStop && stopLabel)
  const showSubmit = !capturingSpeech && (!showStop || (hasDraft && !disabled && !pending))
  const showModel = !hideModel && (models.length > 0 || Boolean(onRefreshModels))
  const showThinking = reasoningLevels.length > 0
  const expanded = !capturingSpeech && (editing || Boolean(picker))
  const followKeyboard = editing && !picker && !capturingSpeech
  const groupActions = expanded && speechPhase === "idle"
  const modelUnavailableNow = modelUnavailable(models, pick)
  const lockedModel = models.length === 1 && Boolean(models[0]?.locked)
  const modelName = choiceLabel(models, pick)
  const modelText = modelName || t("composer.model")
  const thinkingText = thinkingLabel(level)
  const modelIndicator = <>
    <Cpu className="size-3.5 shrink-0" aria-hidden />
    <span id={modelTextId} className="truncate">{modelText}</span>
  </>
  const thinkingIndicator = <>
    <Brain className="size-3.5 shrink-0" aria-hidden />
    <span id={thinkingTextId} className="sr-only">{thinkingText}</span>
  </>
  const catalog = slashCommands?.map((command) => ({
    ...command,
    description: t(`slash.${command.id}`),
  })) ?? []
  const draft = catalog.length ? slashDraft(text) : null
  const slashItems = draft ? filterSlashCommands(draft.query, catalog) : []
  const slashOpen = slashItems.length > 0 && !command?.arg
  const awaitingCommand = catalog.some((item) => item.id === slashAwaitingArg(text))
  const commandDraft = slashOpen || awaitingCommand
  const ready = hasDraft && !disabled && !pending && !speechBusy && !commandDraft

  // The conversation's selection arrives after catalog. A local edit is not
  // overwritten until those props themselves change.
  useEffect(() => {
    explicitPick.current = false
    setPick(chosen(models, providerId, model))
  }, [providerId, model])
  useEffect(() => {
    if (!explicitPick.current) setPick((current) => chosen(models, providerId, model, current))
  }, [models, providerId, model])
  useEffect(() => {
    setLevel(reasoning)
  }, [reasoning])

  useLayoutEffect(() => {
    if (!editing) return
    let frame = 0
    const press = () => { mouseDown.current = true }
    const release = () => {
      mouseDown.current = false
      // An outside button can move when the composer shrinks. Let the
      // browser finish dispatching the click before shifting the transcript.
      frame = requestAnimationFrame(() => {
        if (!dock.current?.contains(document.activeElement)) setEditing(false)
      })
    }
    window.addEventListener("mousedown", press, true)
    window.addEventListener("mouseup", release, true)
    return () => {
      window.removeEventListener("mousedown", press, true)
      window.removeEventListener("mouseup", release, true)
      cancelAnimationFrame(frame)
      mouseDown.current = false
    }
  }, [editing])

  useLayoutEffect(() => {
    const viewport = window.visualViewport
    const frame = dock.current?.parentElement
    if (!followKeyboard || !viewport || !frame) return
    // The iOS keyboard can shrink the visual viewport without resizing the layout.
    const measure = () => {
      const visibleBottom = viewport.pageTop - window.scrollY + viewport.height
      setKeyboardInset(viewport.scale === 1 ? Math.max(0, frame.getBoundingClientRect().bottom - visibleBottom) : 0)
    }
    measure()
    viewport.addEventListener("resize", measure)
    viewport.addEventListener("scroll", measure)
    window.addEventListener("resize", measure)
    window.addEventListener("scroll", measure)
    return () => {
      viewport.removeEventListener("resize", measure)
      viewport.removeEventListener("scroll", measure)
      window.removeEventListener("resize", measure)
      window.removeEventListener("scroll", measure)
    }
  }, [followKeyboard])

  // Grow with the text instead of scrolling a one-line slot, but stop before
  // the transcript loses its last answer.
  useLayoutEffect(() => {
    const el = box.current
    if (!el || capturingSpeech) return
    el.style.height = "auto"
    el.style.height = Math.min(el.scrollHeight, 132) + "px"
  }, [text, capturingSpeech, expanded])

  // A menu click blurs the field. Put the caret back after `/name ` lands.
  useLayoutEffect(() => {
    const pos = pinCaret.current
    if (pos == null) return
    pinCaret.current = null
    const el = box.current
    if (!el) return
    el.focus()
    el.setSelectionRange(pos, pos)
  }, [text])

  useEffect(() => {
    setSlashIndex(0)
  }, [draft?.query])

  useEffect(() => {
    if (!slashOpen) return
    return installAndroidBack(() => {
      setText((current) => {
        const open = slashDraft(current)
        return open ? stripSlashToken(current, open) : current
      })
      return true
    })
  }, [slashOpen])

  const extra = (): ComposerExtra | undefined => {
    if (attached.length === 0 && models.length === 0 && reasoningLevels.length === 0) return undefined
    return {
      files,
      images,
      providerId: pick.providerId,
      model: pick.model,
      reasoning: level,
    }
  }

  const pickCommand = (command: SlashCommand) => {
    const next = applySlashPick(text, command)
    pinCaret.current = next.caret
    setAddOpen(false)
    setText(next.text)
  }

  const fire = async (send: Send) => {
    if (images.length > 0 && !imageAllowed) return
    if (slashOpen) {
      const command = slashItems[Math.min(slashIndex, slashItems.length - 1)]
      if (command) pickCommand(command)
      return
    }
    if (awaitingCommand) return
    // The quote envelope hides the command from the PC. Refuse before clearing
    // or uploading any part of this draft, as the desktop composer does.
    if (textOnlyRefusal || (imageCommand && !command.arg)) {
      setCommandRejected(textOnlyRefusal ? "text" : "prompt")
      return
    }
    setCommandRejected(undefined)
    const payload = formatQuotedMessage(quotes, text)
    if ((!payload && attached.length === 0) || disabled || pending || speechBusy) return
    const shot = attached
    const selected = quotes
    const more = extra()
    setText("")
    setAttached([])
    onQuotesChange?.([])
    try {
      await (more ? send(payload, more) : send(payload))
    } catch {
      setText(text)
      setAttached(shot)
      onQuotesChange?.(selected)
    }
  }

  const addFiles = (list: File[]) => {
    list = list.filter(file => imageAllowed || !isVisionFile(file))
    if (list.length === 0) return
    setAttached((prev) => [...prev, ...list])
  }

  const tune = (next: { providerId: string; model: string; reasoning: string }) => {
    explicitPick.current = true
    setPick({ providerId: next.providerId, model: next.model })
    setLevel(next.reasoning)
    onTune?.(next)
  }

  const closePicker = () => {
    setPicker(undefined)
    // Keep focus restoration inside the tap so iOS can reopen the keyboard.
    box.current?.focus({ preventScroll: true })
  }

  return (
    <div
      ref={dock}
      className="min-w-0 shrink-0 bg-background px-3 pb-2 pt-1.5"
      style={followKeyboard && keyboardInset > 0 ? { marginBottom: keyboardInset } : undefined}
      onBlur={(event) => {
        if (!mouseDown.current && !event.currentTarget.contains(event.relatedTarget)) setEditing(false)
      }}
      onMouseDown={(event) => {
        // Keep the toolbar in place through click. Canceling pointerdown
        // would suppress WebKit's synthetic mouse events on a touch tap.
        if (document.activeElement === box.current && event.target instanceof Element &&
            event.target.closest("button")) event.preventDefault()
      }}
    >
      {above}
      {suggestion ? <div className="mb-1 flex min-w-0 items-start gap-1" data-testid="composer-prediction">
        <button type="button" className="line-clamp-2 min-w-0 flex-1 wrap-break-word rounded-lg px-2 py-1 text-left text-sm text-muted-foreground active:bg-accent" aria-label={`${t("prediction.insert")}: ${suggestion.text}`} onClick={insertSuggestion}>{suggestion.text}</button>
        <button type="button" className="flex size-9 shrink-0 items-center justify-center text-muted-foreground" aria-label={t("prediction.dismiss")} onClick={() => setDismissedPrediction(suggestion.turnId)}><X className="size-4" /></button>
      </div> : null}
      {quotes.length > 0 ? (
        <ul className="mb-2 flex flex-col gap-1" data-testid="quote-drafts">
          {quotes.map((quote, i) => (
            <li key={i} className="flex min-w-0 items-center gap-2 rounded-lg border border-border bg-muted px-3 py-1 text-xs">
              <textarea
                aria-label={t("quote.edit", { n: i + 1 })}
                className="min-h-12 max-h-24 min-w-0 flex-1 resize-y overflow-auto bg-transparent text-base focus-visible:outline-hidden"
                rows={2}
                value={quote}
                data-testid="quote-draft"
                onChange={(event) => onQuotesChange?.(quotes.map((current, j) => j === i ? event.target.value : current))}
              />
              <button
                type="button"
                aria-label={t("quote.remove", { n: i + 1 })}
                className="text-muted-foreground"
                onClick={() => onQuotesChange?.(quotes.filter((_, j) => j !== i))}
              >
                <X className="size-3.5" aria-hidden />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      {attached.length > 0 ? (
        <ul className="mb-2 flex flex-col gap-1">
          {attached.map((file, i) => (
            <li
              key={file.name + ":" + i}
              className="flex items-center gap-2 rounded-full bg-muted px-3 py-1 text-xs"
            >
              <span className="min-w-0 flex-1 truncate">{file.name || putFallback(file)}</span>
              <button
                type="button"
                aria-label={t("composer.remove", { name: file.name || putFallback(file) })}
                className="text-muted-foreground"
                onClick={() => setAttached((prev) => prev.filter((_, j) => j !== i))}
              >
                <X className="size-3.5" aria-hidden />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      {commandRejected === "text" && textOnlyRefusal ? (
        <p role="alert" className="mb-2 text-sm text-destructive">{t("composer.commandTextOnly")}</p>
      ) : commandRejected === "prompt" && imageCommand && !command.arg ? (
        <p role="alert" className="mb-2 text-sm text-destructive">{t("composer.imagePrompt")}</p>
      ) : null}
      <form
        ref={formRef}
        data-testid="message-composer"
        className={cn(
          "flex min-w-0 flex-wrap items-end gap-y-1 rounded-[1.5rem] border border-border bg-card p-1 shadow-sm",
          expanded && "border-ring/45",
          "focus-within:ring-1 focus-within:ring-ring/40",
        )}
        aria-busy={pending || undefined}
        onSubmit={(e) => {
          e.preventDefault()
          void fire(onSubmit)
        }}
      >
        <input
          ref={fileRef}
          type="file"
          multiple
          className="hidden"
          data-testid="file-input"
          onChange={(e) => {
            addFiles(Array.from(e.target.files ?? []))
            e.target.value = ""
          }}
        />
        {!capturingSpeech ? (
          <button
            ref={addRef}
            type="button"
            aria-label={t("composer.add")}
            aria-expanded={addOpen}
            aria-controls={addOpen ? "composer-add-menu" : undefined}
            aria-haspopup="menu"
            disabled={disabled || pending}
            className="flex size-9 shrink-0 items-center justify-center rounded-full text-muted-foreground active:bg-accent disabled:opacity-40"
            onClick={() => setAddOpen((open) => !open)}
          >
            <Plus className="size-4" aria-hidden />
          </button>
        ) : null}
        {addOpen && !capturingSpeech ? (
          <ComposerAddMenu
            anchor={addRef.current}
            commands={catalog}
            onAttach={() => {
              fileRef.current?.click()
              setAddOpen(false)
            }}
            onPick={pickCommand}
            onClose={() => setAddOpen(false)}
          />
        ) : null}
        {slashOpen && !addOpen ? (
          <ComposerSlashMenu
            anchor={formRef.current}
            commands={slashItems}
            activeIndex={slashIndex}
            onSelect={pickCommand}
          />
        ) : null}
        <textarea
          ref={box}
          aria-label={label}
          placeholder={placeholder ?? label}
          value={text}
          rows={1}
          hidden={capturingSpeech}
          disabled={disabled || pending}
          aria-expanded={slashOpen || undefined}
          aria-controls={slashOpen ? "slash-menu" : undefined}
          aria-autocomplete={catalog.length ? "list" : undefined}
          onFocus={() => setEditing(true)}
          onChange={(e) => {
            const next = catalog.length ? normalizeSlashPrefix(e.target.value) : e.target.value
            if (catalog.length && slashDraft(next)) setAddOpen(false)
            setText(next)
          }}
          onPaste={(e) => {
            const pasted = Array.from(e.clipboardData?.files ?? []).filter((file) => isVisionFile(file))
            if (pasted.length === 0) return
            e.preventDefault()
            addFiles(pasted)
          }}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (slashOpen && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
              e.preventDefault()
              setSlashIndex((index) => nextSlashIndex(index, slashItems.length, e.key === "ArrowDown" ? 1 : -1))
              return
            }
            if (slashOpen && e.key === "Escape") {
              e.preventDefault()
              setText(draft ? stripSlashToken(text, draft) : text)
              return
            }
            if (slashOpen && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey && (e.key === "Enter" || e.key === "Tab")) {
              e.preventDefault()
              const command = slashItems[Math.min(slashIndex, slashItems.length - 1)]
              if (command) pickCommand(command)
              return
            }
            if (e.key === "Tab" && !e.shiftKey && suggestion) { e.preventDefault(); insertSuggestion(); return }
            if (e.key === "Escape" && suggestion) { e.preventDefault(); setDismissedPrediction(suggestion.turnId); return }
            if (e.key === "Escape" && awaitingCommand) {
              e.preventDefault()
              setText(clearSlashCommand(text))
              return
            }
            if (e.key !== "Enter" || e.shiftKey) return
            if (awaitingCommand) { e.preventDefault(); return }
            e.preventDefault()
            void fire(onSubmit)
          }}
          className={cn(
            "min-h-9 min-w-0 resize-none bg-transparent py-2 text-base leading-5",
            "placeholder:text-muted-foreground focus-visible:outline-hidden disabled:opacity-60",
            expanded ? "order-first min-h-11 w-full flex-none px-2.5 pt-3" : "flex-1",
          )}
        />
        {expanded && (showModel || modelName || showThinking || level || catalogBusy && !hideModel) ? (
          <div className="flex min-w-0 flex-1 items-center gap-0.5">
            {catalogBusy && models.length === 0 && !hideModel ? (
              <span role="status" className="flex size-9 shrink-0 items-center justify-center text-muted-foreground">
                <Loader2 className="size-4 motion-safe:animate-spin" aria-hidden />
                <span className="sr-only">{t("composer.loading")}</span>
              </span>
            ) : null}
            {showModel ? <button
              type="button"
              aria-label={t("composer.model")}
              aria-haspopup="dialog"
              aria-describedby={modelTextId}
              title={modelUnavailableNow && !lockedModel ? t("composer.modelUnavailable") : modelText}
              data-unavailable={modelUnavailableNow && !lockedModel ? "true" : undefined}
              className={cn(settingClass, settingButton)}
              disabled={disabled || pending}
              onClick={() => { setPicker("model"); box.current?.blur() }}
            >
              {modelIndicator}
            </button> : modelName ? (
              <span className={cn(settingClass, "text-muted-foreground")} title={modelName}>{modelIndicator}</span>
            ) : null}
            {showThinking ? <button
              type="button"
              aria-label={t("composer.thinking")}
              aria-haspopup="dialog"
              aria-describedby={thinkingTextId}
              title={thinkingText}
              className={cn(settingClass, settingButton, "w-9 shrink-0 justify-center")}
              disabled={disabled || pending}
              onClick={() => { setPicker("thinking"); box.current?.blur() }}
            >
              {thinkingIndicator}
            </button> : level ? (
              <span className={cn(settingClass, "shrink-0 text-muted-foreground")} title={thinkingText}>{thinkingIndicator}</span>
            ) : null}
          </div>
        ) : null}
        {expanded && !groupActions ? <span className="flex-1" aria-hidden /> : null}
        <div className={groupActions ? "ms-auto flex shrink-0 items-end" : "contents"}>
          <SpeechCapture disabled={disabled || pending}
            onPhaseChange={setSpeechPhase}
            onStart={() => {
              setAddOpen(false)
              speechAnchor.current = { text: textRef.current, caret: box.current?.selectionStart ?? textRef.current.length }
              box.current?.blur()
            }}
            onText={(spoken) => {
              const next = insertSpeechText(textRef.current, speechAnchor.current.text, speechAnchor.current.caret, spoken)
              textRef.current = next.text
              setText(next.text)
              requestAnimationFrame(() => { box.current?.focus(); box.current?.setSelectionRange(next.caret, next.caret) })
            }} />
          {!capturingSpeech && onInsert && (!showStop || hasDraft) && !command ? (
            <DeliveryMenu disabled={!ready} onSelect={(mode) => void fire((content, extra) => onInsert(mode, content, extra))} />
          ) : null}
          {showSubmit ? <button
            type="submit"
            aria-label={sendLabel}
            title={sendLabel}
            disabled={!ready}
            className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity disabled:opacity-30"
          >
            {pending ? <Loader2 className="size-4 motion-safe:animate-spin" aria-hidden /> : <ArrowUp className="size-4" aria-hidden />}
          </button> : null}
          {showStop ? <button
            type="button"
            aria-label={stopLabel}
            title={stopLabel}
            onClick={() => void onStop?.()}
            className={cn("flex size-9 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground active:opacity-75",
              capturingSpeech ? "ms-auto" : "ms-3")}
          >
            <Square className="size-3.5" fill="currentColor" aria-hidden />
          </button> : null}
        </div>
      </form>
      {picker === "model" && showModel ? (
        <ModelPicker
          models={models}
          providerId={pick.providerId}
          model={pick.model}
          readOnlyLabel={lockedModel ? modelText : undefined}
          onSelect={(nextProviderId, nextModel) => {
            tune({ providerId: nextProviderId, model: nextModel, reasoning: level })
            closePicker()
          }}
          onRefresh={lockedModel ? undefined : onRefreshModels}
          refreshBusy={refreshBusy}
          refreshError={refreshError}
          onClose={closePicker}
        />
      ) : null}
      {picker === "thinking" && showThinking ? (
        <ThinkingPicker levels={reasoningLevels} value={level}
          onSelect={(next) => {
            tune({ providerId: pick.providerId, model: pick.model, reasoning: next })
            closePicker()
          }}
          onClose={closePicker} />
      ) : null}
      {hint ? <p className="px-3 pt-1.5 text-[11px] text-muted-foreground">{hint}</p> : null}
    </div>
  )
}

function chosen(
  models: ModelChoice[],
  providerId: string,
  model: string,
  current?: { providerId: string; model: string },
) {
  const rows = models.filter((row) => row.model)
  if (rows.length === 1 && rows[0].locked) return { providerId: rows[0].provider_id, model: rows[0].model }
  if (providerId && model) return { providerId, model }
  // A known provider that left the catalog was switched off. Keep the
  // conversation on it instead of silently sending the next endpoint.
  const stillListed = !providerId || rows.some((row) => row.provider_id === providerId)
  if (!stillListed) return { providerId, model }
  const candidates = providerId ? rows.filter((row) => row.provider_id === providerId) : rows
  const defaultRow = candidates.find((row) => row.default)
  if (defaultRow && (defaultRow.default_follows_setting || current?.providerId !== defaultRow.provider_id)) {
    return { providerId: defaultRow.provider_id, model: defaultRow.model }
  }
  // A catalog-order fallback already on the button stays there when the
  // list is reordered. A settings default above is allowed to move it.
  if (current?.model && (!providerId || !current.providerId || current.providerId === providerId)) {
    return { providerId: current.providerId || providerId, model: current.model }
  }
  const hit = defaultRow ?? candidates[0]
  return { providerId: hit?.provider_id ?? providerId, model: hit?.model ?? model }
}

function choiceLabel(models: ModelChoice[], pick: { providerId: string; model: string }) {
  const row = models.find((item) => item.provider_id === pick.providerId && item.model === pick.model)
  return row?.label || pick.model
}

function modelUnavailable(models: ModelChoice[], pick: { providerId: string; model: string }) {
  if (!pick.model) return false
  const row = models.find((item) => item.provider_id === pick.providerId && item.model === pick.model)
  return !row || row.ready === false
}

function putFallback(file: File) {
  return file.type.startsWith("image/") ? "image" : "file"
}
