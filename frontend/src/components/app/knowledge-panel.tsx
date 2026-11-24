import { ArrowLeft, ChevronDown, ChevronRight, FileText, FolderOpen, RefreshCw } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"

import { KnowledgeNetwork } from "@/components/app/knowledge-network"
import { KnowledgePageView, type KnowledgePageActions } from "@/components/app/knowledge-page"
import { Button } from "@/components/ui/button"
import { ProjectDistillMenu, ProjectDistillStatus } from "@/components/app/project-distill"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api, ApiError } from "@/lib/api"
import {
  filterKnowledgeIssues,
  filterKnowledgeTree,
  KNOWLEDGE_PENDING_VIEW,
  KNOWLEDGE_TYPE_VIEW,
  type KnowledgeIssue,
  type KnowledgeNode,
} from "@/lib/knowledge"
import { desktopShell } from "@/lib/shell"
import type { Project, Thread } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { useApp } from "@/store/app"
import { knowledgeStack, useKnowledge } from "@/store/knowledge"
import { errorMessage } from "@/store/toasts"

export interface KnowledgePanelProps extends Omit<KnowledgePageActions, "onPage"> {
  project?: Project
  thread?: Pick<Thread, "id" | "title">
}

export function KnowledgePanel({ project, ...props }: KnowledgePanelProps) {
  const t = useT()
  return (
    <div data-testid="knowledge-panel" className="flex min-h-0 min-w-0 flex-1 flex-col">
      {project ? <ProjectKnowledge key={project.id} project={project} {...props} /> : (
        <p className="p-3 text-sm text-muted-foreground">{t("knowledge.moveToProject")}</p>
      )}
    </div>
  )
}

function ProjectKnowledge({ project, thread, onOpenThread, onTrace }: KnowledgePanelProps & { project: Project }) {
  const t = useT()
  const refresh = useKnowledge((state) => state.refreshByProject[project.id] ?? 0)
  const page = useKnowledge((state) => knowledgeStack(state, project.id).at(-1))
  const canReveal = useApp((state) => Boolean(state.meta?.capabilities?.reveal)) && desktopShell()
  const [view, setView] = useState(KNOWLEDGE_TYPE_VIEW)
  const [query, setQuery] = useState("")
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string>()
  const busyRef = useRef(false)

  const readStatus = useCallback((signal: AbortSignal) => api.knowledgeStatus(project.id, signal), [project.id])
  const status = useKnowledgeRead(project.id, refresh, readStatus)
  const views = status.data?.schema?.views ?? []
  const currentView = view === KNOWLEDGE_PENDING_VIEW || views.some(({ id }) => id === view) ? view : KNOWLEDGE_TYPE_VIEW
  const readTree = useCallback((signal: AbortSignal) => api.knowledgeTree(project.id, currentView, signal), [project.id, currentView])
  const tree = useKnowledgeRead(status.data?.enabled && !page ? currentView : undefined, refresh, readTree)
  const nodes = useMemo(() => filterKnowledgeTree(tree.data?.nodes ?? [], query), [tree.data, query])
  const issues = useMemo(() => filterKnowledgeIssues(tree.data?.issues ?? [], query), [tree.data, query])
  const current = useCallback((id: string) => useKnowledge.getState().openPage(project.id, id), [project.id])
  const invalidate = () => useKnowledge.getState().invalidate(project.id)

  const act = async (action: () => Promise<unknown>) => {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setActionError(undefined)
    try {
      await action()
    } catch (error) {
      setActionError(errorMessage(error))
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }
  const enable = async () => {
    await api.enableKnowledge(project.id)
    invalidate()
  }
  const distillMenu = <ProjectDistillMenu projectId={project.id} threadId={thread?.id} knowledgeEnabled={status.data?.enabled} />
  const wikiMenu = <ProjectDistillMenu projectId={project.id} threadId={thread?.id} wikiGraph />
  const wikiStatus = <ProjectDistillStatus projectId={project.id} wikiGraph />
  const error = actionError ?? (status.error ? errorMessage(status.error) : undefined)
  if (!status.data) {
    if (!error) return <KnowledgeLoading />
    return (
      <div className="space-y-2 px-3 py-2">
        <KnowledgeError error={error} />
        <Button variant="outline" size="sm" onClick={invalidate}><RefreshCw />{t("files.refresh")}</Button>
      </div>
    )
  }
  if (!status.data.enabled) {
    return (
      <div className="space-y-2 p-3">
        <p className="text-sm text-muted-foreground">{t("knowledge.disabled")}</p>
        <div className="flex flex-wrap items-center gap-2">
          <Button data-testid="knowledge-enable" size="sm" disabled={busy} onClick={() => void act(enable)}>{t("knowledge.enable")}</Button>
          {distillMenu}
          {wikiMenu}
        </div>
        <ProjectDistillStatus projectId={project.id} />
        {wikiStatus}
        {error ? <KnowledgeError error={error} /> : null}
      </div>
    )
  }
  if (page) {
    return (
      <>
        <div className="px-3"><ProjectDistillStatus projectId={project.id} />{wikiStatus}</div>
        <PageDrilldown
          key={page}
          page={page}
          projectId={project.id}
          refresh={refresh}
          onPage={current}
          onBack={() => useKnowledge.getState().back(project.id)}
          onOpenThread={onOpenThread}
          onTrace={onTrace}
        />
      </>
    )
  }
  return (
    <>
      <div className="flex shrink-0 flex-col gap-2 border-b px-3 py-2">
        <div className="flex flex-wrap items-center gap-2">
          <Select value={currentView} onValueChange={setView}>
            <SelectTrigger data-testid="knowledge-view" aria-label={t("knowledge.view")} className="min-w-0 flex-1">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={KNOWLEDGE_TYPE_VIEW}>{t("knowledge.byType")}</SelectItem>
              {views.map(({ id, label }) => <SelectItem key={id} value={id}>{label || id}</SelectItem>)}
              <SelectItem value={KNOWLEDGE_PENDING_VIEW}>{t("knowledge.pending")}</SelectItem>
            </SelectContent>
          </Select>
          <KnowledgeNetwork projectId={project.id} refresh={refresh} onPage={current} />
          {distillMenu}
          {wikiMenu}
          {canReveal ? (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={t("knowledge.reveal")}
              title={t("knowledge.reveal")}
              onClick={() => void act(() => api.revealKnowledge(project.id))}
              disabled={busy}
            ><FolderOpen /></Button>
          ) : null}
        </div>
        <div className="flex min-w-0 items-center gap-2">
          <Input
            data-testid="knowledge-filter"
            type="search"
            value={query}
            onChange={(event) => setQuery(event.currentTarget.value)}
            aria-label={t("knowledge.filter")}
            placeholder={t("knowledge.filter")}
            className="h-8 min-w-0 text-sm"
          />
          <Button
            size="sm"
            variant="ghost"
            aria-pressed={currentView === KNOWLEDGE_PENDING_VIEW}
            onClick={() => setView(KNOWLEDGE_PENDING_VIEW)}
          >{t("knowledge.pendingCount", { count: status.data.pending_count })}</Button>
        </div>
        <ProjectDistillStatus projectId={project.id} />
        {wikiStatus}
        {error ? <KnowledgeError error={error} /> : null}
      </div>
      <div data-testid="knowledge-tree" className="thin-scrollbar min-h-0 flex-1 overflow-y-auto p-2">
        {!tree.data && !tree.error ? <KnowledgeLoading /> : null}
        {tree.error ? <KnowledgeError error={errorMessage(tree.error)} /> : null}
        {tree.data && currentView === KNOWLEDGE_PENDING_VIEW ? (
          issues.length ? <PendingIssues issues={issues} onPage={current} /> : (
            <div className="px-1 text-sm text-muted-foreground">{t(query.trim() ? "knowledge.noMatches" : "knowledge.noPending")}</div>
          )
        ) : null}
        {tree.data?.truncated ? <p role="status" className="px-1 pb-1 text-xs text-muted-foreground">{t("knowledge.truncated")}</p> : null}
        {tree.data && currentView !== KNOWLEDGE_PENDING_VIEW ? (
          nodes.length && status.data.page_count > 0 ? <TreeLevel nodes={nodes} onPage={current} filtering={Boolean(query.trim())} /> : (
            <div className="px-1 text-sm text-muted-foreground">{t(query.trim() ? "knowledge.noMatches" : "knowledge.noPages")}</div>
          )
        ) : null}
      </div>
    </>
  )
}

function PageDrilldown({ page, projectId, refresh, onBack, ...actions }: KnowledgePageActions & {
  page: string
  projectId: string
  refresh: number
  onBack: () => void
}) {
  const t = useT()
  const read = useCallback((signal: AbortSignal) => api.knowledgePage(projectId, page, signal), [projectId, page])
  const result = useKnowledgeRead(page, refresh, read)
  const missing = result.error instanceof ApiError && result.error.status === 404
  const data = missing ? undefined : result.data
  return (
    <div data-testid="knowledge-page" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="border-b px-2 py-1">
        <Button data-testid="knowledge-back" size="sm" variant="ghost" onClick={onBack}><ArrowLeft />{t("knowledge.back")}</Button>
      </div>
      {data ? <KnowledgePageView page={data} {...actions} /> : null}
      {missing ? (
        <div className="space-y-1 px-3 py-2">
          <h2 className="break-all font-semibold">{page}</h2>
          <p className="text-sm text-muted-foreground">{t("knowledge.missingPage")}</p>
        </div>
      ) : result.error ? <div className="p-3"><KnowledgeError error={errorMessage(result.error)} /></div>
        : !data ? <KnowledgeLoading /> : null}
    </div>
  )
}

function TreeLevel({ nodes, filtering, onPage }: {
  nodes: readonly KnowledgeNode[]
  filtering: boolean
  onPage: (id: string) => void
}) {
  return <ul>{nodes.map((node) => <TreeItem key={`${node.kind}:${node.id}`} node={node} filtering={filtering} onPage={onPage} />)}</ul>
}

function TreeItem({ node, filtering, onPage }: {
  node: KnowledgeNode
  filtering: boolean
  onPage: (id: string) => void
}) {
  const t = useT()
  const [expanded, setExpanded] = useState(true)
  const open = filtering || expanded
  const nested = Boolean(node.children?.length)
  const label = node.label || node.id
  return (
    <li className="min-w-0">
      <div className="flex min-w-0 items-center py-0.5">
        {nested ? (
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            className="size-6 shrink-0"
            aria-label={t(open ? "knowledge.collapseBranch" : "knowledge.expandBranch", { title: label })}
            aria-expanded={open}
            disabled={filtering}
            onClick={() => setExpanded((state) => !state)}
          >{open ? <ChevronDown /> : <ChevronRight />}</Button>
        ) : <span className="w-4 shrink-0" />}
        {node.kind === "page" ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-auto min-w-0 justify-start px-1.5 py-1 font-normal"
            onClick={() => onPage(node.id)}
            title={node.cycle ? t("knowledge.cycle") : label}
          >
            <FileText aria-hidden className="text-muted-foreground" /><span className="truncate">{label}</span>
          </Button>
        ) : <span className="min-w-0 truncate pl-1.5 text-sm font-medium" title={label}>{label}</span>}
      </div>
      {open && node.children?.length ? (
        <div className="ml-3 border-l border-border pl-1">
          <TreeLevel nodes={node.children} filtering={filtering} onPage={onPage} />
        </div>
      ) : null}
    </li>
  )
}

function PendingIssues({ issues, onPage }: { issues: KnowledgeIssue[]; onPage: (id: string) => void }) {
  const t = useT()
  return (
    <ul className="divide-y divide-border">
      {issues.map(({ kind, target, relation, page_id, path, detail }, index) => (
        <li key={`${kind}:${page_id ?? path}:${target ?? relation}:${index}`} className="space-y-1 px-1 py-2 text-sm">
          <div className="text-muted-foreground">{t(`knowledge.issue.${kind}`)}</div>
          {target || relation || path ? <div className="break-all">{target || relation || path}</div> : null}
          {page_id ? <Button variant="link" size="sm" className="h-auto p-0" onClick={() => onPage(page_id)}>{page_id}</Button> : null}
          {detail ? <div className="wrap-break-word text-xs text-muted-foreground">{detail}</div> : null}
        </li>
      ))}
    </ul>
  )
}

function KnowledgeLoading() {
  const t = useT()
  return <p role="status" className="px-3 py-2 text-sm text-muted-foreground">{t("knowledge.loading")}</p>
}

function KnowledgeError({ error }: { error: string }) {
  return <p role="alert" className="wrap-break-word text-sm text-destructive">{error}</p>
}

function useKnowledgeRead<T>(key: string | undefined, refresh: number, read: (signal: AbortSignal) => Promise<T>): { data?: T; error?: Error } {
  const [result, setResult] = useState<{ key: string; data?: T; error?: Error }>()
  useEffect(() => {
    if (!key) return
    const controller = new AbortController()
    void read(controller.signal).then((data) => {
      if (!controller.signal.aborted) setResult({ key, data })
    }).catch((error: Error) => {
      if (controller.signal.aborted) return
      setResult((previous) => ({ key, data: previous?.key === key ? previous.data : undefined, error }))
    })
    return () => controller.abort()
  }, [key, refresh, read])
  return key && result?.key === key ? result : {}
}
