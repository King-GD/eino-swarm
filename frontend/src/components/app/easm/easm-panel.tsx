import { Maximize2 } from "lucide-react"
import { useMemo, useState } from "react"
import { Button } from "../../ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../ui/dialog"
import { indexResult } from "../../../lib/easm/model"
import { useEasmText } from "../../../lib/easm/messages"
import type { EasmResult } from "../../../lib/easm/types"
import { AssetGraph } from "./asset-graph"
import { CloudTable } from "./cloud-table"
import { HeatTrend } from "./heat-trend"
import { SupplyTable } from "./supply-table"

/** The EASM tab: relationship graph, heat trend, supply chain and hosting
 *  providers for one result, stacked in the side panel and side by side in the
 *  enlarged dialog. */
export function EasmPanel({ result, graphLoading, cloudsLoading, heatLoading, componentsLoading, visible = true, sheet }: {
  result: EasmResult; graphLoading?: boolean; cloudsLoading?: boolean; heatLoading?: boolean; componentsLoading?: boolean; visible?: boolean; sheet?: HTMLElement | null
}) {
  const text = useEasmText()
  const index = useMemo(() => indexResult(result), [result])
  const [open, setOpen] = useState(false)
  const subject = (index.rootId && index.byId.get(index.rootId)?.label) || result.title
  const cloudsPending = result.collection.steps.some((step) => step.id === "clouds" && step.status === "pending")
  const expand = sheet === undefined ? () => setOpen(true) : undefined
  return (
    <section aria-label="EASM" className="flex h-full min-h-0 flex-col">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="truncate font-medium">{subject}</span>
        {expand ? <Button size="icon" variant="ghost" aria-label={text.expand} title={text.expandGraph} onClick={expand}><Maximize2 className="size-4" /></Button> : null}
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-2">
        <AssetGraph result={result} index={index} loading={graphLoading} visible={visible && !open} onExpand={expand} mobile={sheet !== undefined} />
        <HeatTrend heat={result.heat} loading={heatLoading} className="flex h-[260px] flex-col rounded-xl border bg-card p-3" />
        <SupplyTable components={result.components} loading={componentsLoading} className="h-[360px]" sheet={sheet} visible={visible && !open} />
        <CloudTable clouds={result.clouds} loading={cloudsLoading} pending={cloudsPending} className="h-[300px]" />
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="flex h-[94dvh] w-[96vw] max-w-none flex-col gap-0 overflow-hidden p-0">
          <header className="shrink-0 border-b px-5 py-4 pr-12">
            <DialogTitle>{text.graphTitle(subject)}</DialogTitle>
            <DialogDescription className="sr-only">{text.graphDescription}</DialogDescription>
          </header>
          <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 overflow-y-auto p-4 md:grid-cols-[minmax(0,1.7fr)_minmax(300px,1fr)] md:overflow-hidden">
            <AssetGraph result={result} index={index} loading={graphLoading} visible={visible} expanded />
            <div className="flex min-h-0 flex-col gap-3">
              <HeatTrend heat={result.heat} loading={heatLoading} className="flex h-[260px] flex-col rounded-xl border bg-card p-3 md:h-auto md:min-h-0 md:flex-1" />
              <SupplyTable components={result.components} loading={componentsLoading} className="h-[360px] md:h-auto md:flex-1" />
              <CloudTable clouds={result.clouds} loading={cloudsLoading} pending={cloudsPending} className="h-[300px] md:h-auto md:flex-1" />
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </section>
  )
}
