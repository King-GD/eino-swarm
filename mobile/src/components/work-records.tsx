import { useEffect, useMemo, useState } from "react"
import { ChevronLeft, RefreshCw } from "lucide-react"
import { NuwaGraph } from "../../../frontend/src/components/app/nuwa-graph"
import { NuwaStale, NuwaStatus } from "../../../frontend/src/components/app/nuwa-status"
import { includeNuwaProjectPrerequisites, pruneNuwaNodes } from "../../../frontend/src/lib/nuwa"
import { workEn, workZh, type WorkTranslate } from "../../../frontend/src/lib/messages-work"
import { Button } from "./ui/button"
import { installAndroidBack } from "@/lib/android-back"
import { getLocale, t } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { OpThreadWork, type RemoteResponse } from "@/lib/rpc"

export const phoneWorkText: WorkTranslate = (key, vars) => {
  let text: string = (getLocale() === "zh" ? workZh : workEn)[key]
  for (const [name, value] of Object.entries(vars ?? {})) text = text.replaceAll(`{${name}}`, String(value))
  return text
}

type WorkLoad = { link: RemoteLink; snapshot?: RemoteResponse["work_snapshot"]; error?: keyof typeof workEn }

export function PhoneWorkRecords({ link, threadId, generation, covered, onBack }: {
  link: RemoteLink
  threadId: string
  generation: number
  covered: boolean
  onBack: () => void
}) {
  const [attempt, reload] = useState(0)
  const [load, setLoad] = useState<WorkLoad>()
  const [scope, setScope] = useState<"thread" | "project">("thread")
  const [selected, select] = useState<string>()
  const current = load?.link === link ? load : undefined
  const snapshot = current?.snapshot
  const conversation = scope === "thread" || !snapshot?.project_id
  const graph = useMemo(() => {
    const records = snapshot?.nodes.filter((record) => record.scope === (conversation ? "thread" : "project")) ?? []
    const { shown, collapsed } = pruneNuwaNodes(records)
    const nodes = conversation && snapshot ? includeNuwaProjectPrerequisites(shown, snapshot.nodes) : shown
    return { nodes, collapsed }
  }, [conversation, snapshot])
  const detail = snapshot?.nodes.find((record) => record.id === selected)
  const label = phoneWorkText(conversation ? "nuwa.thread" : "nuwa.project")

  useEffect(() => {
    if (covered) return
    return installAndroidBack(() => { onBack(); return true })
  }, [covered, onBack])
  useEffect(() => {
    const controller = new AbortController()
    setLoad(undefined)
    void link.rpc({ op: OpThreadWork, thread_id: threadId }, { signal: controller.signal, dropOnTimeout: false }).then((reply) => {
      if (controller.signal.aborted) return
      if (reply.ok && reply.data_generation === generation && reply.work_snapshot?.thread_id === threadId &&
          Array.isArray(reply.work_snapshot.nodes)) {
        setLoad({ link, snapshot: reply.work_snapshot })
        return
      }
      const error = reply.code === "work_disabled" ? "nuwa.workDisabled"
        : reply.code === "work_too_large" ? "nuwa.workTooLarge" : "nuwa.loadFailed"
      setLoad({ link, error })
    }).catch(() => {
      if (!controller.signal.aborted) setLoad({ link, error: "nuwa.loadFailed" })
    })
    return () => controller.abort()
  }, [link, threadId, generation, attempt])

  return <main inert={covered} aria-hidden={covered} className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background">
    <header className="flex h-12 shrink-0 items-center gap-1 border-b border-border px-1">
      <Button type="button" variant="ghost" className="size-10 shrink-0 px-0" onClick={onBack} aria-label={t("thread.back")}>
        <ChevronLeft className="size-5" aria-hidden />
      </Button>
      <h1 className="min-w-0 flex-1 truncate text-sm font-medium">{phoneWorkText("nuwa.tab")}</h1>
      <Button type="button" variant="ghost" className="size-10 shrink-0 px-0"
        disabled={!current} onClick={() => reload((value) => value + 1)} aria-label={phoneWorkText("nuwa.refresh")}>
        <RefreshCw className="size-4" aria-hidden />
      </Button>
    </header>
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 p-3">
      {snapshot?.project_id ? (
        <div role="group" aria-label={phoneWorkText("nuwa.scope")} className="grid shrink-0 grid-cols-2 gap-1 rounded-md bg-muted p-1">
          {(["thread", "project"] as const).map((value) => (
            <Button key={value} type="button" variant="ghost" className={scope === value ? "bg-background shadow-xs" : undefined}
              aria-pressed={scope === value} onClick={() => { setScope(value); select(undefined) }}>
              {phoneWorkText(value === "thread" ? "nuwa.thread" : "nuwa.project")}
            </Button>
          ))}
        </div>
      ) : null}
      {!current ? <p role="status" className="text-sm text-muted-foreground">{phoneWorkText("nuwa.loading")}</p> : null}
      {current?.error ? <p role="alert" className="text-sm text-destructive">{phoneWorkText(current.error)}</p> : null}
      {snapshot && !graph.nodes.length && !graph.collapsed.length ? (
        <p role="status" className="text-sm text-muted-foreground">{phoneWorkText(conversation ? "nuwa.threadEmpty" : "nuwa.empty")}</p>
      ) : null}
      {graph.nodes.length || graph.collapsed.length ? (
        <NuwaGraph key={label} t={phoneWorkText} nodes={graph.nodes} collapsed={graph.collapsed} selected={selected}
          onSelect={select} conversation={conversation} expanded />
      ) : null}
      {detail ? (
        <section aria-label={phoneWorkText("nuwa.details")} className="max-h-[38%] shrink-0 space-y-2 overflow-y-auto rounded-lg border border-border p-3">
          <h2 className="break-words text-sm font-medium">{detail.title}</h2>
          <div className="flex flex-wrap items-center gap-2">
            <NuwaStatus t={phoneWorkText} kind={detail.kind} status={detail.status} />
            <NuwaStale t={phoneWorkText} node={detail} />
          </div>
          {detail.summary ? <p className="whitespace-pre-wrap break-words text-sm">{detail.summary}</p> : null}
          {detail.detail ? <p className="whitespace-pre-wrap break-words text-xs text-muted-foreground">{detail.detail}</p> : null}
          {detail.artifact_path ? <p className="break-all text-xs text-muted-foreground">{phoneWorkText("nuwa.file", { path: detail.artifact_path })}</p> : null}
          {detail.evidence?.length ? (
            <div>
              <h3 className="text-xs font-medium">{phoneWorkText("nuwa.evidence")}</h3>
              <ul className="mt-1 space-y-1 text-xs text-muted-foreground">
                {detail.evidence.map((source) => {
                  const missing = detail.unavailable_evidence?.some((item) => item.thread_id === source.thread_id && item.seq === source.seq)
                  return <li key={`${source.thread_id}:${source.seq}`} className="break-words">
                    {source.label ? `${source.label} · ` : ""}#{source.seq}
                    {missing ? ` · ${phoneWorkText("nuwa.sourceUnavailable")}` : ""}
                  </li>
                })}
              </ul>
            </div>
          ) : null}
        </section>
      ) : null}
    </div>
  </main>
}
