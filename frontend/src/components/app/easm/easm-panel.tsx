import { Maximize2 } from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { cn } from "../../../lib/cn"
import type { EasmSectionStatus } from "../../../lib/easm/data"
import { Button } from "../../ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../ui/dialog"
import { indexResult } from "../../../lib/easm/model"
import { useEasmText } from "../../../lib/easm/messages"
import type { EasmResult } from "../../../lib/easm/types"
import { AssetGraph } from "./asset-graph"
import { CloudTable } from "./cloud-table"
import { HeatTrend } from "./heat-trend"
import { SupplyTable } from "./supply-table"

/** The EASM graph and collection results, stacked in the side panel and side
 *  by side in the enlarged dialog. */
export function EasmPanel({ result, graphLoading, heatStatus, componentsStatus, cloudsStatus, visible = true, sheet }: {
  result: EasmResult
  graphLoading?: boolean
  heatStatus?: EasmSectionStatus
  componentsStatus?: EasmSectionStatus
  cloudsStatus?: EasmSectionStatus
  visible?: boolean
  sheet?: HTMLElement | null
}) {
  const text = useEasmText()
  const index = useMemo(() => indexResult(result), [result])
  const [open, setOpen] = useState(false)
  // One collapse state for the side panel and the dialog: enlarging the graph
  // must not unfold what the user folded. A new subject is a new tree.
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set())
  useEffect(() => { setCollapsed(new Set()) }, [index.rootId])
  const activeCollapsed = useMemo(() => {
    let pruned: Set<string> | null = null
    for (const id of collapsed) {
      if ((index.descendants.get(id) ?? 0) > 0) continue
      pruned ??= new Set(collapsed)
      pruned.delete(id)
    }
    return pruned ?? collapsed
  }, [collapsed, index.descendants])
  const changeCollapsed = useCallback((next: Set<string>) => setCollapsed(next), [])
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
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="truncate font-medium">{subject}</span>
        {expand ? <Button size="icon" variant="ghost" aria-label={text.expand} title={text.expandGraph} onClick={expand}><Maximize2 className="size-4" /></Button> : null}
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-2">
        <AssetGraph result={result} index={index} loading={graphLoading} visible={visible && !open} mobile={sheet !== undefined} collapsed={activeCollapsed} onCollapseChange={changeCollapsed} />
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
            <AssetGraph result={result} index={index} loading={graphLoading} visible={visible} expanded collapsed={activeCollapsed} onCollapseChange={changeCollapsed} />
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
