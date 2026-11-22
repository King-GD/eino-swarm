import { Loader2 } from "lucide-react"
import { useEffect, useId, useMemo, useState } from "react"
import { Badge } from "../../ui/badge"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../ui/dialog"
import { hotDays, isSafeLink, sortComponents, type ComponentRow } from "../../../lib/easm/model"
import { formatCount, useEasmText } from "../../../lib/easm/messages"
import type { EasmResult, TagType } from "../../../lib/easm/types"
import { cn } from "@/lib/cn"

const TAG_VARIANT: Record<TagType, "warning" | "danger" | "ask"> = { hot: "warning", kev: "danger", cve: "ask" }

function TagEvidence({ row }: { row: ComponentRow }) {
  const text = useEasmText()
  const days = hotDays(row)
  const sections = row.tags.filter((tag) => tag.type !== "hot")
  if (days == null && !sections.length) return <p className="m-0 text-muted-foreground">{text.noIntel}</p>
  return (
    <div className="divide-y text-xs leading-relaxed">
      {days != null ? (
        <section className="pb-3">
          <h3 className="m-0 text-sm font-semibold">{text.trending}</h3>
          <p className="m-0 mt-1">{text.trendingDays(days)}</p>
        </section>
      ) : null}
      {sections.map((tag) => (
        <section key={tag.type} className="py-3">
          <Badge variant={TAG_VARIANT[tag.type]}>{text.tags[tag.type]}</Badge>
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
export function SupplyTable({ components, loading = false, className, sheet, visible = true }: {
  components: EasmResult["components"]; loading?: boolean; className?: string; sheet?: HTMLElement | null; visible?: boolean
}) {
  const text = useEasmText()
  const rows = useMemo(() => sortComponents(components), [components])
  const [openName, setOpenName] = useState<string | null>(null)
  const open = rows.find((row) => row.name === openName)
  const titleId = useId()
  useEffect(() => { if (openName && (!open || !visible)) setOpenName(null) }, [openName, open, visible])
  return (
    <section aria-labelledby={titleId} className={cn("flex min-h-0 flex-col rounded-xl border bg-card p-3", className)}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 id={titleId} className="m-0 text-[13px] font-semibold whitespace-nowrap">{text.supplyTitle}</h2>
        {rows.length ? <span className="text-[11px] text-muted-foreground">{text.supplyCount(formatCount(rows.length, text))}</span> : null}
      </div>
      {!rows.length ? (
        loading ? (
          <div role="progressbar" aria-label={text.supplyLoading} className="flex min-h-0 flex-1 items-center justify-center text-muted-foreground">
            <Loader2 className="size-5 motion-safe:animate-spin" aria-hidden />
          </div>
        ) : <p className="m-0 flex min-h-0 flex-1 items-center justify-center text-xs text-muted-foreground">{text.supplyEmpty}</p>
      ) : <div tabIndex={0} role="region" aria-label={text.supplyRegion} className="min-h-0 flex-1 overflow-auto overscroll-contain">
        <table className="w-full table-fixed border-collapse text-xs" data-testid="easm-supply">
          <caption className="sr-only">{text.supplyCaption(rows.length)}</caption>
          <colgroup><col /><col className="w-[72px]" /><col className="w-[40%]" /></colgroup>
          <thead>
            <tr className="sticky top-0 z-[1] bg-muted text-[11px] text-muted-foreground">
              <th scope="col" className="px-1.5 py-2 text-left font-medium">{text.component}</th>
              <th scope="col" className="py-2 pr-3 pl-1.5 text-right font-medium">{text.count}</th>
              <th scope="col" className="px-1.5 py-2 text-left font-medium">{text.tagsColumn}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const name = row.display_name || row.name
              return (
                <tr key={row.name} className="border-b hover:bg-accent">
                  <td className="h-[43px] px-1.5">
                    <button type="button" title={row.name} aria-label={text.tagEvidence(name)} onClick={() => setOpenName(row.name)}
                      className="block w-full truncate py-1 text-left hover:text-primary">{name}</button>
                  </td>
                  <td className="pr-3 pl-1.5 text-right tabular-nums">{formatCount(row.count, text)}</td>
                  <td className="px-1.5">
                    <div className="flex flex-wrap items-center gap-1">
                      {row.tags.length ? row.tags.map((tag) => <Badge key={tag.type} variant={TAG_VARIANT[tag.type]}>{text.tags[tag.type]}</Badge>) : <span className="text-muted-foreground">—</span>}
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>}
      <Dialog open={visible && !!open} onOpenChange={(value) => { if (!value) setOpenName(null) }}>
        <DialogContent container={sheet} className="max-h-[80dvh] max-w-[min(92vw,32rem)] overflow-y-auto">
          <DialogTitle>{open ? open.display_name || open.name : ""}</DialogTitle>
          <DialogDescription className="sr-only">{text.tagDescription}</DialogDescription>
          {open ? <TagEvidence row={open} /> : null}
        </DialogContent>
      </Dialog>
    </section>
  )
}
