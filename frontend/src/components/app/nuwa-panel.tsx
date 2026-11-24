import { FileCheck2, Layers3, ListTodo, RefreshCw, Radio, ChevronRight } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { MemoMarkdown } from "@/components/app/markdown"
import { api } from "@/lib/api"
import { partitionNuwaCollaboration, pruneNuwaNodes, type NuwaEvidence, type NuwaNode, type NuwaWorker } from "@/lib/nuwa"
import type { Project } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { cn } from "@/lib/utils"
import { useApp } from "@/store/app"
import { useNuwa } from "@/store/nuwa"
import { useTraceFocus } from "@/store/trace-focus"
import { NuwaGraph, nuwaRecorder } from "./nuwa-graph"
import { NuwaStatus, NuwaStale, nuwaKind, nuwaKindSurface } from "./nuwa-status"

export function NuwaPanel({
  project,
  projectId = project?.id,
  threadId,
  dataGeneration = 0,
  activity,
  onSelectAgent,
}: {
  project?: Project
  projectId?: string
  threadId?: string
  dataGeneration?: number
  activity?: { id: string; status: string; onOpenFiles: () => void }
  onSelectAgent: (id: string) => void
}) {
  const t = useT()
  const {
    owner, snapshot, tag, loading: snapshotLoading, connected: streamConnected, error: snapshotError,
    connect, refresh, promote, promoting, promoteError,
  } = useNuwa()
  const defaultScope = threadId ? "thread" : "project"
  const [scope, setScope] = useState<"project" | "thread">(defaultScope)
  const [selected, setSelected] = useState<string>()
  const [graphOpen, setGraphOpen] = useState(false)
  const [showPruned, setShowPruned] = useState(false)
  useEffect(() => {
    setScope(defaultScope)
    setSelected(undefined)
    setGraphOpen(false)
    setShowPruned(false)
    if (threadId || projectId) return connect({ threadId, projectId, generation: dataGeneration })
  }, [threadId, projectId, dataGeneration, defaultScope, connect])
  const ownsProjection = owner?.threadId === threadId && owner?.projectId === projectId && owner?.generation === dataGeneration
  const current = ownsProjection ? snapshot : undefined
  const loading = !ownsProjection || snapshotLoading
  const connected = ownsProjection && streamConnected
  const error = ownsProjection ? snapshotError : undefined
  const currentProjectId = current?.project_id ?? projectId
  const currentProject = currentProjectId === project?.id ? project : undefined
  const effectiveScope = currentProjectId ? scope : "thread"
  const nodes = useMemo(
    () => effectiveScope === "thread" ? current?.nodes ?? [] : current?.nodes.filter((node) => node.scope === "project") ?? [],
    [current, effectiveScope],
  )
  const { active, shown, collapsed } = useMemo(() => pruneNuwaNodes(nodes), [nodes])
  const graphNodes = showPruned ? nodes : shown
  const indexed = useMemo(() => partitionNuwaCollaboration(graphNodes).path, [graphNodes])
  const detail = nodes.find((node) => node.id === selected)
  const workers = current?.workers ?? []
  const promotion = detail?.scope === "thread" && threadId && current?.project_id && tag ? {
    submit: () => void promote(detail.id),
    pending: promoting,
    error: promoteError?.id === detail.id
      ? promoteError.conflict ? t("nuwa.promoteConflict") : promoteError.message
      : undefined,
  } : undefined

  const openSource = async (source: Pick<NuwaEvidence, "thread_id" | "turn_id">) => {
    if (!source.thread_id) return
    if (useApp.getState().activeId !== source.thread_id)
      await useApp.getState().openThread(source.thread_id)
    if (useApp.getState().activeId !== source.thread_id) return
    if (source.turn_id) {
      await useApp.getState().loadUntilTurn(source.turn_id)
      if (useApp.getState().activeId === source.thread_id)
        useTraceFocus.getState().open(source.turn_id)
    }
  }
  const openWorker = async (worker: { thread_id: string; agent_id: string }) => {
    if (useApp.getState().activeId !== worker.thread_id)
      await useApp.getState().openThread(worker.thread_id)
    if (useApp.getState().activeId === worker.thread_id) onSelectAgent(worker.agent_id)
  }

  return (
    <div data-testid="nuwa-panel" className="thin-scrollbar min-h-0 flex-1 overflow-y-auto p-3">
      <div className="mb-3 flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-(length:--chrome-font-size) font-semibold">{currentProject?.name ?? t("nuwa.tab")}</h2>
          <span
            className={cn(
              "mt-1 flex items-center gap-1 text-[11px]",
              connected ? "text-done" : "text-muted-foreground"
            )}
          >
            <Radio className="size-3" />
            {connected ? t("nuwa.connected") : t("nuwa.connecting")}
          </span>
          {currentProject?.nuwa_enabled ? (
            <p className="mt-1 truncate text-[11px] text-muted-foreground">
              {currentProject.nuwa_executor_model?.trim()
                ? t("nuwa.executorModel", { model: currentProject.nuwa_executor_model })
                : t("nuwa.executorUnset")}
            </p>
          ) : null}
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={t("nuwa.refresh")}
          onClick={() => void refresh()}
          disabled={loading}
        >
          <RefreshCw className={loading ? "animate-spin" : ""} />
        </Button>
      </div>
      {currentProject?.nuwa_configured && !currentProject.nuwa_enabled ? (
        <p className="mb-3 text-xs text-muted-foreground">{t("nuwa.paused")}</p>
      ) : null}
      <div
        role="group"
        aria-label={t("nuwa.scope")}
        className="mb-3 grid grid-cols-2 gap-1 rounded-md bg-muted p-1"
      >
        <Button
          size="sm"
          variant={effectiveScope === "project" ? "secondary" : "ghost"}
          aria-pressed={effectiveScope === "project"}
          disabled={!currentProjectId}
          onClick={() => {
            setScope("project")
            setSelected(undefined)
          }}
        >
          {t("nuwa.project")}
        </Button>
        <Button
          size="sm"
          variant={effectiveScope === "thread" ? "secondary" : "ghost"}
          disabled={!threadId}
          aria-pressed={effectiveScope === "thread"}
          onClick={() => {
            setScope("thread")
            setSelected(undefined)
          }}
        >
          {t("nuwa.thread")}
        </Button>
      </div>
      {error ? (
        <div role="alert" className="mb-3 rounded-md border border-destructive/30 p-2 text-xs">
          <p className="wrap-break-word text-destructive">{error}</p>
          <Button className="mt-1" size="sm" variant="ghost" onClick={() => void refresh()}>
            {t("nuwa.retry")}
          </Button>
        </div>
      ) : null}
      {loading ? (
        <p role="status" className="py-6 text-center text-xs text-muted-foreground">
          {t("nuwa.loading")}
        </p>
      ) : null}
      {!loading && current ? (
        <>
          <div className="mb-4 grid grid-cols-3 gap-2">
            {(
              [
                ["task", ListTodo],
                ["fact", Layers3],
                ["artifact", FileCheck2],
              ] as const
            ).map(([kind, Icon]) => (
              <div
                key={kind}
                data-kind={kind}
                className={cn(
                  "rounded-lg border border-border px-2 py-2",
                  nuwaKindSurface(kind) ?? "bg-muted/20"
                )}
              >
                <Icon className="mb-1 size-3.5 text-muted-foreground" />
                <strong className="block text-lg font-semibold tabular-nums">
                  {active.filter((node) => node.kind === kind).length}
                </strong>
                <span className="text-[11px] text-muted-foreground">{nuwaKind(kind, t)}</span>
              </div>
            ))}
          </div>
          {collapsed.length ? (
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
              <span>{t("nuwa.pruned", { count: collapsed.length })}</span>
              <label className="inline-flex items-center gap-2">
                <Switch checked={showPruned} onCheckedChange={setShowPruned} aria-label={t("nuwa.showPruned")} />
                {t("nuwa.showPruned")}
              </label>
            </div>
          ) : null}
          {nodes.length ? (
            <NuwaGraph
              nodes={graphNodes}
              collapsed={showPruned ? [] : collapsed}
              conversation={effectiveScope === "thread"}
              workers={workers}
              selected={selected}
              onSelect={setSelected}
              onExpand={() => setGraphOpen(true)}
            />
          ) : (
            <>
              <p className="py-6 text-center text-xs text-muted-foreground">
                {t(effectiveScope === "thread" ? "nuwa.threadEmpty" : "nuwa.empty")}
              </p>
              {threadId && activity && !workers.length ? (
                <section aria-label={t("nuwa.lastTurn")} className="rounded-md border border-border p-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-xs font-medium">{t("nuwa.lastTurn")}</span>
                    <span role="status">
                      <NuwaStatus status={activity.status === "error" ? "failed" : activity.status} />
                    </span>
                  </div>
                  <div className="mt-1 flex flex-wrap gap-1">
                    <Button size="sm" variant="ghost" onClick={() => void openSource({ thread_id: threadId, turn_id: activity.id })}>
                      {t("nuwa.turnTrace")}
                    </Button>
                    <Button size="sm" variant="ghost" onClick={activity.onOpenFiles}>
                      {t("panel.files")}
                    </Button>
                  </div>
                </section>
              ) : null}
            </>
          )}
          {detail ? (
            <NodeDetail
              key={detail.id}
              node={detail}
              nodes={current.nodes}
              workers={workers}
              promotion={promotion}
              onSelect={setSelected}
              onSource={openSource}
              onWorker={openWorker}
            />
          ) : null}
          {indexed.length ? (
            <div role="list" aria-label={t("nuwa.list")} className="mt-4 grid gap-1">
              {indexed.map((node) => (
                <div role="listitem" key={node.id} className="min-w-0">
                  <button
                    type="button"
                    onClick={() => setSelected(node.id)}
                    aria-pressed={selected === node.id}
                    className={cn(
                      "flex w-full min-w-0 items-center gap-2 rounded-md border px-2 py-2 text-left hover:bg-accent/50 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring",
                      nuwaKindSurface(node.kind),
                      selected === node.id ? "border-primary" : "border-transparent"
                    )}
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">{node.title}</span>
                      <span className="block truncate text-[11px] text-muted-foreground">
                        {node.summary}
                      </span>
                      <NuwaStale node={node} />
                    </span>
                    <NuwaStatus status={node.status} kind={node.kind} />
                    <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
                  </button>
                </div>
              ))}
            </div>
          ) : null}
          {workers.length ? (
            <section className="mt-4 border-t border-border pt-3">
              <h3 className="mb-2 text-xs font-medium text-muted-foreground">
                {t("nuwa.workers")}
              </h3>
              {workers.map((worker) => (
                <WorkerRow
                  key={`${worker.thread_id}:${worker.agent_id}:${worker.generation ?? 0}`}
                  worker={worker}
                  onOpen={() => void openWorker(worker)}
                />
              ))}
            </section>
          ) : null}
          <Dialog open={graphOpen} onOpenChange={setGraphOpen}>
            <DialogContent className="flex h-[90vh] max-h-[900px] w-[96vw] max-w-[1200px] flex-col">
              <DialogHeader>
                <DialogTitle>{t("nuwa.path")}</DialogTitle>
                <DialogDescription>
                  {currentProject?.name ?? t("nuwa.tab")} · {t(effectiveScope === "thread" ? "nuwa.thread" : "nuwa.project")}
                </DialogDescription>
              </DialogHeader>
              <div className="grid min-h-0 flex-1 gap-4 md:grid-cols-[minmax(0,1fr)_280px]">
                <NuwaGraph
                  nodes={graphNodes}
                  collapsed={showPruned ? [] : collapsed}
                  conversation={effectiveScope === "thread"}
                  workers={workers}
                  selected={selected}
                  onSelect={setSelected}
                  expanded
                />
                <div className="thin-scrollbar min-h-0 overflow-y-auto">
                  {detail ? (
                    <NodeDetail
                      key={detail.id}
                      node={detail}
                      nodes={current.nodes}
                      workers={workers}
                      promotion={promotion}
                      onSelect={setSelected}
                      onSource={async (source) => {
                        setGraphOpen(false)
                        await openSource(source)
                      }}
                      onWorker={async (worker) => {
                        setGraphOpen(false)
                        await openWorker(worker)
                      }}
                    />
                  ) : (
                    <p className="p-3 text-xs text-muted-foreground">{t("nuwa.selectNode")}</p>
                  )}
                </div>
              </div>
            </DialogContent>
          </Dialog>
        </>
      ) : null}
    </div>
  )
}

function WorkerRow({ worker, onOpen }: { worker: NuwaWorker; onOpen: () => void }) {
  const t = useT()
  return (
    <button
      type="button"
      onClick={onOpen}
      className="mb-1 flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-accent/50 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
    >
      <span className="sr-only">{t("nuwa.worker")}: </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs">{worker.role}</span>
        <span
          className="block truncate text-[11px] text-muted-foreground"
          title={worker.model_binding?.provider_id}
        >
          {worker.model_binding?.model || t("nuwa.modelUnknown")}
          {worker.model_binding?.reasoning_effort
            ? ` · ${worker.model_binding.reasoning_effort}`
            : ""}
        </span>
        {worker.parent_id ? (
          <span className="block break-all text-[10px] text-muted-foreground">
            {t("nuwa.parentAgent", { thread: worker.thread_id, agent: worker.parent_id })}
          </span>
        ) : null}
      </span>
      <NuwaStatus status={worker.status} />
    </button>
  )
}

function NodeDetail({
  node,
  nodes,
  workers,
  promotion,
  onSelect,
  onSource,
  onWorker,
}: {
  node: NuwaNode
  nodes: NuwaNode[]
  workers: NuwaWorker[]
  promotion?: { submit: () => void; pending: boolean; error?: string }
  onSelect: (id: string) => void
  onSource: (source: NuwaEvidence) => Promise<void>
  onWorker: (worker: { thread_id: string; agent_id: string }) => Promise<void>
}) {
  const t = useT()
  const [expanded, setExpanded] = useState(false)
  const recorder = nuwaRecorder(node, workers)
  const author = recorder.manager ? t("nuwa.originManager") : recorder.name
  const model = recorder.model
  return (
    <section
      data-testid="nuwa-node-detail"
      className={cn(
        "mt-3 rounded-lg border border-border p-3",
        nuwaKindSurface(node.kind) ?? "bg-muted/20"
      )}
    >
      <div className="mb-2 flex items-start justify-between gap-2">
        <h3 className="wrap-break-word text-(length:--chrome-font-size) font-medium">{node.title}</h3>
        <NuwaStatus status={node.status} kind={node.kind} />
      </div>
      <p className="whitespace-pre-wrap wrap-break-word text-xs text-muted-foreground">
        {node.summary}
      </p>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{t(node.scope === "thread" ? "nuwa.scopeThread" : "nuwa.scopeProject")}</span>
        {promotion ? (
          <Button size="sm" variant="outline" disabled={promotion.pending} onClick={promotion.submit}>
            {t(promotion.pending ? "nuwa.promoting" : "nuwa.promote")}
          </Button>
        ) : null}
      </div>
      {promotion?.error ? <p role="alert" className="mt-2 text-xs text-destructive">{promotion.error}</p> : null}
      {node.stale_dependencies?.length ? (
        <p className="mt-2 text-xs text-running">{t("nuwa.stale")}</p>
      ) : null}
      {node.updated_by ? (
        <p className="mt-2 text-[11px] text-muted-foreground">
          {t("nuwa.updatedBy", { agent: node.updated_by })}
        </p>
      ) : null}
      {author && node.turn_id ? (
        <p className="mt-2 text-[11px] text-muted-foreground">
          {t("nuwa.originDescription", { agent: author, turn: node.turn_id })}
        </p>
      ) : null}
      {model ? (
        <p className="mt-2 break-all text-[11px] text-muted-foreground">
          {model}
          {recorder.effort ? ` · ${recorder.effort}` : ""}
        </p>
      ) : null}
      {node.depends_on?.length ? (
        <div className="mt-3 flex flex-wrap items-center gap-1 text-[11px]">
          <span className="text-muted-foreground">{t("nuwa.depends")}</span>
          {node.depends_on.map((id) => (
            <button
              key={id}
              type="button"
              className="max-w-full truncate rounded border border-border px-1.5 py-0.5 hover:bg-accent disabled:cursor-not-allowed disabled:opacity-60"
              onClick={() => onSelect(id)}
              disabled={!nodes.some((parent) => parent.id === id)}
            >
              {nodes.find((parent) => parent.id === id)?.title ?? id}
            </button>
          ))}
        </div>
      ) : null}
      {node.artifact_path ? (
        <div className="mt-3 break-all text-xs">
          <p className="mb-1 text-muted-foreground">
            {t("nuwa.file", { path: node.artifact_path })}
          </p>
          <a
            className="text-primary underline underline-offset-2"
            href={api.downloadURL(node.thread_id, node.artifact_path)}
            download
          >
            {t("nuwa.download")}
          </a>
        </div>
      ) : null}
      {node.agent_id ? (
        <Button
          variant="link"
          size="sm"
          className="mt-2 h-auto p-0 text-xs"
          onClick={() => void onWorker({ thread_id: node.thread_id, agent_id: node.agent_id! })}
        >
          {t("nuwa.worker")}
        </Button>
      ) : null}
      <Button
        variant="ghost"
        size="sm"
        className="mt-2 w-full justify-start px-0 text-xs"
        aria-expanded={expanded}
        onClick={() => setExpanded(!expanded)}
      >
        <ChevronRight className={cn("size-3 transition-transform", expanded && "rotate-90")} />
        {t("nuwa.details")}
      </Button>
      {expanded ? (
        <div className="mt-2 min-w-0">
          {node.detail ? (
            <div className="prose prose-sm max-w-none wrap-break-word text-xs dark:prose-invert">
              <MemoMarkdown text={node.detail} />
            </div>
          ) : null}
          <h4 className="mb-1 mt-3 text-xs font-medium">{t("nuwa.evidence")}</h4>
          {node.evidence?.length ? (
            node.evidence.map((source, index) => (
              <button
                key={`${source.thread_id}:${source.seq}:${index}`}
                type="button"
                className="mb-1 block w-full break-all rounded-md border border-border p-2 text-left text-xs hover:bg-accent focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-60"
                onClick={() => void onSource(source)}
                disabled={node.unavailable_evidence?.some(
                  (missing) => missing.thread_id === source.thread_id && missing.seq === source.seq
                )}
              >
                <span className="block">
                  {source.label || t(source.turn_id ? "nuwa.trace" : "nuwa.source")}
                </span>
                <span className="text-[10px] text-muted-foreground">
                  {source.thread_id} · #{source.seq}
                </span>
                {node.unavailable_evidence?.some(
                  (missing) => missing.thread_id === source.thread_id && missing.seq === source.seq
                ) ? (
                  <span className="mt-1 block text-[10px] text-muted-foreground">
                    {t("nuwa.sourceUnavailable")}
                  </span>
                ) : null}
              </button>
            ))
          ) : (
            <p className="text-xs text-muted-foreground">{t("nuwa.noEvidence")}</p>
          )}
        </div>
      ) : null}
    </section>
  )
}
