import { useEffect, useId, useMemo, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { TAG_LABELS, formatCount, hotDays, isSafeLink, sortComponents, type ComponentRow } from "@/lib/easm/model"
import type { EasmResult, TagType } from "@/lib/easm/types"
import { cn } from "@/lib/utils"

const TAG_VARIANT: Record<TagType, "warning" | "danger" | "ask"> = { hot: "warning", kev: "danger", cve: "ask" }

function TagEvidence({ row }: { row: ComponentRow }) {
  const days = hotDays(row)
  const sections = row.tags.filter((tag) => tag.type !== "hot")
  if (days == null && !sections.length) return <p className="m-0 text-muted-foreground">暂无相关情报。</p>
  return (
    <div className="divide-y text-xs leading-relaxed">
      {days != null ? (
        <section className="pb-3">
          <h3 className="m-0 text-sm font-semibold">热搜情况</h3>
          <p className="m-0 mt-1">近 30 天有 {days} 天上榜。</p>
        </section>
      ) : null}
      {sections.map((tag) => (
        <section key={tag.type} className="py-3">
          <Badge variant={TAG_VARIANT[tag.type]}>{TAG_LABELS[tag.type]}</Badge>
          {tag.summary ? <p className="m-0 mt-1.5">{tag.summary}</p> : null}
          {tag.evidence?.length ? (
            <ul className="mt-1.5 mb-0 list-disc space-y-1 pl-5">
              {tag.evidence.map((item, i) => (
                <li key={i}>
                  {isSafeLink(item.url) ? <a href={item.url} target="_blank" rel="noreferrer" className="text-primary hover:underline">{item.title}</a> : item.title}
                  {item.date ? <span className="text-muted-foreground"> · {item.date}</span> : null}
                  {item.detail ? <small className="block text-muted-foreground">{item.detail}</small> : null}
                </li>
              ))}
            </ul>
          ) : null}
        </section>
      ))}
    </div>
  )
}

/** Third-party components with their aggregate counts; a row opens its tag evidence. */
export function SupplyTable({ components, className }: { components: EasmResult["components"]; className?: string }) {
  const rows = useMemo(() => sortComponents(components), [components])
  const [openName, setOpenName] = useState<string | null>(null)
  const open = rows.find((row) => row.name === openName)
  const titleId = useId()
  useEffect(() => { if (openName && !open) setOpenName(null) }, [openName, open])
  return (
    <section aria-labelledby={titleId} className={cn("flex min-h-0 flex-col rounded-xl border bg-card p-3", className)}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 id={titleId} className="m-0 text-[13px] font-semibold whitespace-nowrap">供应链 / 第三方组件</h2>
        <span className="text-[11px] text-muted-foreground">{formatCount(rows.length)}项</span>
      </div>
      <div tabIndex={0} role="region" aria-label="全部供应链组件，可滚动查看" className="min-h-0 flex-1 overflow-auto overscroll-contain">
        <table className="w-full table-fixed border-collapse text-xs" data-testid="easm-supply">
          <caption className="sr-only">供应链组件，按热搜、KEV、CVE 优先级排序；共 {rows.length} 项</caption>
          <colgroup><col /><col className="w-[72px]" /><col className="w-[40%]" /></colgroup>
          <thead>
            <tr className="sticky top-0 z-[1] bg-muted text-[11px] text-muted-foreground">
              <th scope="col" className="px-1.5 py-2 text-left font-medium">组件</th>
              <th scope="col" className="py-2 pr-3 pl-1.5 text-right font-medium">聚合计数</th>
              <th scope="col" className="px-1.5 py-2 text-left font-medium">关注标签</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const name = row.display_name || row.name
              return (
                <tr key={row.name} className="border-b hover:bg-accent">
                  <td className="h-[43px] px-1.5">
                    <button type="button" title={row.name} aria-label={`查看${name}标签依据`} onClick={() => setOpenName(row.name)}
                      className="block w-full truncate py-1 text-left hover:text-primary">{name}</button>
                  </td>
                  <td className="pr-3 pl-1.5 text-right tabular-nums">{formatCount(row.count)}</td>
                  <td className="px-1.5">
                    <div className="flex flex-wrap items-center gap-1">
                      {row.tags.length ? row.tags.map((tag) => <Badge key={tag.type} variant={TAG_VARIANT[tag.type]}>{TAG_LABELS[tag.type]}</Badge>) : <span className="text-muted-foreground">—</span>}
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <Dialog open={!!open} onOpenChange={(value) => { if (!value) setOpenName(null) }}>
        <DialogContent className="max-h-[80dvh] overflow-y-auto">
          <DialogTitle>{open ? open.display_name || open.name : ""}</DialogTitle>
          <DialogDescription className="sr-only">该组件的热搜和漏洞标签依据</DialogDescription>
          {open ? <TagEvidence row={open} /> : null}
        </DialogContent>
      </Dialog>
    </section>
  )
}
