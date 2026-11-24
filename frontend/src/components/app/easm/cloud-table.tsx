import { Loader2 } from "lucide-react"
import { useId } from "react"
import { formatCount, useEasmText } from "../../../lib/easm/messages"
import type { EasmCloud } from "../../../lib/easm/types"
import { cn } from "@/lib/cn"

/** Hosting providers with the company's assets behind each, so a row answers
 *  "how much of us is there" rather than "how big is the provider". */
export function CloudTable({ clouds, loading = false, className }: {
  clouds?: EasmCloud[]; loading?: boolean; className?: string
}) {
  const text = useEasmText()
  const titleId = useId()
  const rows = clouds ?? []
  const categories = text.cloudCategories as Record<string, string>
  return (
    <section aria-labelledby={titleId} className={cn("flex min-h-0 flex-col rounded-xl border bg-card p-3", className)}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 id={titleId} className="m-0 text-[13px] font-semibold whitespace-nowrap">{text.cloudTitle}</h2>
        {rows.length ? <span className="text-[11px] text-muted-foreground">{text.cloudCount(formatCount(rows.length, text))}</span> : null}
      </div>
      {!rows.length ? (
        loading ? (
          <div role="progressbar" aria-label={text.cloudLoading} className="flex min-h-0 flex-1 items-center justify-center text-muted-foreground">
            <Loader2 className="size-5 motion-safe:animate-spin" aria-hidden />
          </div>
        ) : <p className="m-0 flex min-h-0 flex-1 items-center justify-center text-xs text-muted-foreground">{text.cloudEmpty}</p>
      ) : <div tabIndex={0} role="region" aria-label={text.cloudRegion} className="min-h-0 flex-1 overflow-auto overscroll-contain">
        <table className="w-full table-fixed border-collapse text-xs" data-testid="easm-clouds">
          <caption className="sr-only">{text.cloudCaption(rows.length)}</caption>
          <colgroup><col /><col className="w-[76px]" /><col className="w-[62px]" /><col className="w-[52px]" /><col className="w-[36%]" /></colgroup>
          <thead>
            <tr className="sticky top-0 z-[1] bg-muted text-[11px] text-muted-foreground">
              <th scope="col" className="px-1.5 py-2 text-left font-medium">{text.cloudProvider}</th>
              <th scope="col" className="px-1.5 py-2 text-left font-medium">{text.cloudCategory}</th>
              <th scope="col" className="px-1.5 py-2 text-right font-medium">{text.cloudAssets}</th>
              <th scope="col" className="px-1.5 py-2 text-right font-medium">{text.cloudIPs}</th>
              <th scope="col" className="px-1.5 py-2 text-left font-medium">{text.cloudServices}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((cloud) => {
              const name = cloud.display_name || cloud.name
              const services = cloud.services.length
                ? cloud.services.map((service) => text.cloudService(service.name, formatCount(service.count, text))).join(text.listSeparator)
                : "—"
              return (
                <tr key={cloud.name} className="border-b hover:bg-accent">
                  <td className="h-[43px] truncate px-1.5" title={cloud.name}>{name}</td>
                  <td className="px-1.5 text-muted-foreground" title={cloud.category}>{categories[cloud.category] ?? text.cloudCategories.unknown}</td>
                  <td className="px-1.5 text-right tabular-nums">{formatCount(cloud.assets, text)}</td>
                  <td className="px-1.5 text-right tabular-nums">{formatCount(cloud.ips, text)}</td>
                  <td className="truncate px-1.5 text-muted-foreground" title={services}>{services}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>}
    </section>
  )
}
