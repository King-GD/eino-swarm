import { Network, RefreshCw } from "lucide-react"
import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import { matchKnowledgeGraphNodes, type KnowledgeGraph } from "@/lib/knowledge"
import { rootKnowledgeGraph, visibleKnowledgeGraph } from "@/lib/knowledge-layout"
import { useT } from "@/lib/use-t"
import { errorMessage } from "@/store/toasts"

const ForceLayout = lazy(() => import("./knowledge-force-layout"))

export function KnowledgeNetwork({ projectId, projectTitle, refresh, onPage }: {
  projectId: string
  projectTitle: string
  refresh: number
  onPage: (id: string) => void
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const openPage = useCallback((id: string) => {
    setOpen(false)
    onPage(id)
  }, [onPage])

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline" title={t("knowledge.graph")}><Network aria-hidden />{t("knowledge.graphOpen")}</Button>
      </DialogTrigger>
      <DialogContent className="flex h-[min(82dvh,48rem)] w-[min(94vw,72rem)] min-w-0 flex-col gap-3 overflow-hidden p-4 sm:max-w-none">
        <DialogHeader className="pr-8">
          <DialogTitle>{t("knowledge.graph")}</DialogTitle>
          <DialogDescription className="sr-only">{t("knowledge.graphDescription")}</DialogDescription>
        </DialogHeader>
        {open ? <NetworkBody projectId={projectId} projectTitle={projectTitle} refresh={refresh} onPage={openPage} /> : null}
      </DialogContent>
    </Dialog>
  )
}

function NetworkBody({ projectId, projectTitle, refresh, onPage }: {
  projectId: string
  projectTitle: string
  refresh: number
  onPage: (id: string) => void
}) {
  const t = useT()
  const [graph, setGraph] = useState<KnowledgeGraph>()
  const [error, setError] = useState<string>()
  const [retry, setRetry] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setError(undefined)
    void api.knowledgeGraph(projectId, controller.signal).then((data) => {
      if (!controller.signal.aborted) setGraph(data)
    }, (err: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(err))
    })
    return () => controller.abort()
  }, [projectId, refresh, retry])

  return (
    <>
      {error ? (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert" className="wrap-break-word text-sm text-destructive">{error}</p>
          <Button size="sm" variant="outline" onClick={() => setRetry((value) => value + 1)}>
            <RefreshCw aria-hidden />{t("files.refresh")}
          </Button>
        </div>
      ) : null}
      {graph ? graph.nodes.length ? <NetworkExplorer graph={graph} projectTitle={projectTitle} onPage={onPage} /> : (
        <p className="text-sm text-muted-foreground">{t("knowledge.noPages")}</p>
      ) : !error ? <p role="status" className="text-sm text-muted-foreground">{t("knowledge.loading")}</p> : null}
    </>
  )
}

const PAGE_RESULTS = 8

function NetworkExplorer({ graph, projectTitle, onPage }: { graph: KnowledgeGraph; projectTitle: string; onPage: (id: string) => void }) {
  const t = useT()
  const [query, setQuery] = useState("")
  const [selected, setSelected] = useState<string | undefined>()
  const [groupOpen, setGroupOpen] = useState(false)
  const [resultLimit, setResultLimit] = useState(PAGE_RESULTS)
  const rooted = useMemo(() => rootKnowledgeGraph(graph, projectTitle,
    (count) => t("knowledge.graphUnlinked", { count })), [graph, projectTitle, t])
  const unlinked = rooted.groupedPages
  const browsing = groupOpen && unlinked.length > 0
  const showResults = browsing || Boolean(query.trim())
  const searchNodes = browsing ? unlinked : graph.nodes
  const matches = useMemo(() => showResults ? matchKnowledgeGraphNodes(searchNodes, query) : [], [searchNodes, query, showResults])
  const selection = graph.nodes.find(({ id }) => id === selected)
  const groupedSelection = selected && rooted.groupedIDs.has(selected) ? selected : undefined
  const layout = useMemo(() => visibleKnowledgeGraph(rooted, groupedSelection), [rooted, groupedSelection])
  const shortlist = matches.slice(0, browsing ? resultLimit : PAGE_RESULTS)
  const toggleGroup = useCallback(() => {
    setGroupOpen((previous) => !previous)
    setQuery("")
    setResultLimit(PAGE_RESULTS)
  }, [])
  const choose = (node: KnowledgeGraph["nodes"][number]) => {
    setSelected(node.id)
    setQuery("")
    setGroupOpen(false)
  }

  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <form className="relative min-w-40 max-w-sm flex-1" onSubmit={(event) => {
          event.preventDefault()
          if (matches[0]) choose(matches[0])
        }}>
          <Input
            type="search"
            value={query}
            onChange={(event) => { setQuery(event.currentTarget.value); setResultLimit(PAGE_RESULTS) }}
            placeholder={t("knowledge.graphSearch")}
            aria-label={t("knowledge.graphSearch")}
          />
          {showResults ? (
            <ul className="absolute inset-x-0 top-full z-10 mt-1 max-h-[min(45vh,20rem)] overflow-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md">
              {shortlist.length ? shortlist.map((node) => (
                <li key={node.id}>
                  <Button type="button" variant="ghost" className="h-auto w-full justify-start" onClick={() => choose(node)}>
                    <span className="truncate">{node.title}</span>
                    <span className="ml-auto truncate text-xs text-muted-foreground">{node.type}</span>
                  </Button>
                </li>
              )) : <li className="px-2 py-1 text-sm text-muted-foreground">{t("knowledge.noMatches")}</li>}
              {browsing && matches.length > shortlist.length ? (
                <li><Button type="button" variant="ghost" className="w-full" onClick={() => setResultLimit((count) => count + PAGE_RESULTS)}>
                  {t("sidebar.showMore")}
                </Button></li>
              ) : null}
            </ul>
          ) : null}
        </form>
        {unlinked.length > 0 ? (
          <Button variant="outline" size="sm" aria-expanded={browsing} onClick={toggleGroup}>
            {t("knowledge.graphUnlinked", { count: unlinked.length })}
          </Button>
        ) : null}
        {selection ? (
          <Button variant="secondary" size="sm" className="min-w-0 max-w-full" onClick={() => onPage(selection.id)}>
            <span className="truncate">{t("knowledge.graphOpenPage", { title: selection.title })}</span>
          </Button>
        ) : null}
        <p className="text-xs text-muted-foreground sm:ml-auto">
          {t("knowledge.graphCounts", { pages: graph.nodes.length, edges: graph.edges.length })}
        </p>
      </div>
      <Suspense fallback={<p role="status" className="text-sm text-muted-foreground">{t("knowledge.loading")}</p>}>
        <ForceLayout graph={layout} selected={selection?.id} onPage={onPage} onGroup={toggleGroup} />
      </Suspense>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="w-4 border-t border-current" />{t("knowledge.graphRelation")}</span>
        <span className="flex items-center gap-1.5"><span className="w-4 border-t border-dashed border-current" />{t("knowledge.graphWikilink")}</span>
        <span className="flex items-center gap-1.5"><span className="w-4 border-t border-dotted border-current" />{t("knowledge.graphMembership")}</span>
      </div>
    </>
  )
}
