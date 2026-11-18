import { Maximize2 } from "lucide-react"
import { useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { indexResult } from "@/lib/easm/model"
import type { EasmResult } from "@/lib/easm/types"
import { AssetGraph } from "./asset-graph"
import { HeatTrend } from "./heat-trend"
import { SupplyTable } from "./supply-table"

/** The EASM tab: relationship graph, heat trend and supply chain for one result,
 *  stacked in the side panel and side by side in the enlarged dialog. */
export function EasmPanel({ result, visible = true }: { result: EasmResult; visible?: boolean }) {
  const index = useMemo(() => indexResult(result), [result])
  const [open, setOpen] = useState(false)
  const subject = (index.rootId && index.byId.get(index.rootId)?.label) || result.title
  return (
    <section aria-label="EASM" className="flex h-full min-h-0 flex-col">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="truncate font-medium">{subject}</span>
        <Button size="icon" variant="ghost" aria-label="展开 EASM" title="展开资产图" onClick={() => setOpen(true)}><Maximize2 className="size-4" /></Button>
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-2">
        <AssetGraph result={result} index={index} visible={visible && !open} onExpand={() => setOpen(true)} />
        <HeatTrend heat={result.heat} className="flex h-[260px] flex-col rounded-xl border bg-card p-3" />
        <SupplyTable components={result.components} className="h-[360px]" />
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="flex h-[94dvh] w-[96vw] max-w-none flex-col gap-0 overflow-hidden p-0">
          <header className="shrink-0 border-b px-5 py-4 pr-12">
            <DialogTitle>{subject} · 资产图谱</DialogTitle>
            <DialogDescription className="sr-only">资产关系图、热力趋势和供应链，点击节点查看详情。</DialogDescription>
          </header>
          <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 overflow-y-auto p-4 md:grid-cols-[minmax(0,1.7fr)_minmax(300px,1fr)] md:overflow-hidden">
            <AssetGraph result={result} index={index} visible={visible} expanded />
            <div className="flex min-h-0 flex-col gap-3">
              <HeatTrend heat={result.heat} className="flex h-[260px] flex-col rounded-xl border bg-card p-3 md:h-auto md:min-h-0 md:flex-1" />
              <SupplyTable components={result.components} className="h-[360px] md:h-auto md:flex-1" />
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </section>
  )
}
