import { Maximize2 } from "lucide-react"
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import { cn } from "../../../lib/cn"
import type { EasmSectionStatus } from "../../../lib/easm/data"
import { Button } from "../../ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../ui/dialog"
import { foldByKind, indexResult, kindLabel } from "../../../lib/easm/model"
import { useEasmText } from "../../../lib/easm/messages"
import type { EasmResult } from "../../../lib/easm/types"
import { AssetGraph } from "./asset-graph"
import { CloudTable } from "./cloud-table"
import { HeatTrend } from "./heat-trend"
import { SupplyTable } from "./supply-table"

/** The EASM graph and collection results, stacked in the side panel and side
 *  by side in the enlarged dialog. */
export function EasmPanel({ result, graphLoading, heatStatus, componentsStatus, cloudsStatus, visible = true, sheet, headerActions }: {
  result: EasmResult
  graphLoading?: boolean
  heatStatus?: EasmSectionStatus
  componentsStatus?: EasmSectionStatus
  cloudsStatus?: EasmSectionStatus
  visible?: boolean
  sheet?: HTMLElement | null
  /** Controls the host keeps for the panel, such as closing this instance.
   *  They share the subject row so the panel needs no separate control band. */
  headerActions?: ReactNode
}) {
  const text = useEasmText()
  // "Fold same-type nodes" only decides whether to fold; which groups are open
  // is view state, and turning the switch off returns the original graph.
  const [folded, setFolded] = useState(false)
  const shown = useMemo(() => folded ? foldByKind(result, (kind) => kindLabel(kind, text)) : result, [folded, result, text])
  const index = useMemo(() => indexResult(shown), [shown])
  const groups = useMemo(() => new Set(folded ? shown.nodes.filter((node) => node.id.startsWith("group:")).map((node) => node.id) : []), [folded, shown])
  const [open, setOpen] = useState(false)
  // One collapse state for the side panel and the dialog: enlarging the graph
  // must not unfold what the user folded. A new subject is a new tree.
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set())
  // Groups start folded, so a kind that arrives later is folded too; only the
  // groups the user opened are remembered.
  const [openGroups, setOpenGroups] = useState<ReadonlySet<string>>(() => new Set())
  useEffect(() => { setCollapsed(new Set()); setOpenGroups(new Set()) }, [index.rootId])
  const activeCollapsed = useMemo(() => {
    const all = new Set(collapsed)
    for (const id of groups) if (!openGroups.has(id)) all.add(id)
    for (const id of all) if ((index.descendants.get(id) ?? 0) === 0) all.delete(id)
    return all
  }, [collapsed, groups, openGroups, index.descendants])
  const changeCollapsed = useCallback((next: Set<string>) => {
    setOpenGroups(new Set([...groups].filter((id) => !next.has(id))))
    setCollapsed(new Set([...next].filter((id) => !groups.has(id))))
  }, [groups])
  const changeFolded = useCallback((next: boolean) => {
    setFolded(next)
    setCollapsed(new Set())
    setOpenGroups(new Set())
  }, [])
  const subject = (index.rootId && index.byId.get(index.rootId)?.label) || result.title
  const expand = sheet === undefined ? () => setOpen(true) : undefined
  const heatLoading = heatStatus === "loading"
  const componentsLoading = componentsStatus === "loading"
  const cloudsLoading = cloudsStatus === "loading"
  const showHeat = result.heat.length > 0 || heatLoading
  const showComponents = result.components.length > 0 || componentsLoading
  const showClouds = (result.clouds?.length ?? 0) > 0 || cloudsLoading
  const showMetrics = showHeat || showComponents || showClouds
  return (
    <section aria-label="EASM" className="flex h-full min-h-0 flex-col">
      <div className="flex items-center justify-between gap-1 border-b px-3 py-2" data-testid="easm-panel-header">
        <span className="truncate font-medium">{subject}</span>
        <div className="flex shrink-0 items-center gap-1">
          {expand ? <Button size="icon" variant="ghost" aria-label={text.expand} title={text.expandGraph} onClick={expand}><Maximize2 className="size-4" /></Button> : null}
          {headerActions}
        </div>
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-2">
        <AssetGraph result={shown} index={index} loading={graphLoading} visible={visible && !open} mobile={sheet !== undefined} collapsed={activeCollapsed} onCollapseChange={changeCollapsed} folded={folded} onFoldedChange={changeFolded} />
        {showHeat && <HeatTrend heat={result.heat} loading={heatLoading} className="flex h-[260px] flex-col rounded-xl border bg-card p-3" />}
        {showComponents && <SupplyTable components={result.components} loading={componentsLoading} className="h-[360px]" sheet={sheet} visible={visible && !open} />}
        {showClouds && <CloudTable clouds={result.clouds} loading={cloudsLoading} className="h-[300px]" />}
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="flex h-[94dvh] w-[96vw] max-w-none flex-col gap-0 overflow-hidden p-0">
          <header className="shrink-0 border-b px-5 py-4 pr-12">
            <DialogTitle>{text.graphTitle(subject)}</DialogTitle>
            <DialogDescription className="sr-only">{text.graphDescription}</DialogDescription>
          </header>
          <div className={cn("grid min-h-0 flex-1 grid-cols-1 gap-3 overflow-y-auto p-4 md:overflow-hidden", showMetrics && "md:grid-cols-[minmax(0,1.7fr)_minmax(300px,1fr)]")}>
            <AssetGraph result={shown} index={index} loading={graphLoading} visible={visible} expanded collapsed={activeCollapsed} onCollapseChange={changeCollapsed} folded={folded} onFoldedChange={changeFolded} />
            {showMetrics && <div className="flex min-h-0 flex-col gap-3">
              {showHeat && <HeatTrend heat={result.heat} loading={heatLoading} className="flex h-[260px] flex-col rounded-xl border bg-card p-3 md:h-auto md:min-h-0 md:flex-1" />}
              {showComponents && <SupplyTable components={result.components} loading={componentsLoading} className="h-[360px] md:h-auto md:flex-1" />}
              {showClouds && <CloudTable clouds={result.clouds} loading={cloudsLoading} className="h-[300px] md:h-auto md:flex-1" />}
            </div>}
          </div>
        </DialogContent>
      </Dialog>
    </section>
  )
}
