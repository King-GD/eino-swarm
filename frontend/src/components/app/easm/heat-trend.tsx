import { Loader2 } from "lucide-react"
import { useId } from "react"
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import type { TooltipProps } from "recharts"
import { useEasmText } from "../../../lib/easm/messages"
import type { EasmResult } from "../../../lib/easm/types"

const LINE = "hsl(var(--chart-1))"

/** Only the date is named: the curve carries the trend, and a daily figure
 *  would be read as an exact number the source does not vouch for. */
function HeatTooltip({ active, payload, label }: TooltipProps<number, string>) {
  if (!active || !payload?.length) return null
  return (
    <div className="rounded-md border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md">{label}</div>
  )
}

/** Asset heat over time: a smooth line with a light fill, the date on hover. */
export function HeatTrend({ heat, loading, className }: {
  heat: EasmResult["heat"]; loading?: boolean; className?: string
}) {
  // The panel and the enlarged dialog can both be mounted; ids must stay unique.
  const titleId = useId()
  const text = useEasmText()
  return (
    <section aria-labelledby={titleId} className={className ?? "flex flex-col rounded-xl border bg-card p-3"}>
      <h2 id={titleId} className="mb-2 text-[13px] font-semibold">{text.heatTitle}</h2>
      {heat.length ? (
        <div role="img" aria-label={text.heatChart} data-testid="easm-heat" className="min-h-[180px] flex-1">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={heat} margin={{ top: 12, right: 12, bottom: 0, left: 12 }}>
              <CartesianGrid stroke="hsl(var(--border))" vertical={false} />
              <XAxis dataKey="date" tick={{ fill: "hsl(var(--muted-foreground))", fontSize: 10 }} tickLine={false} axisLine={{ stroke: "hsl(var(--border))" }} minTickGap={16} />
              <YAxis hide />
              {/* Same hover language as every other chart: a hairline
                  crosshair and a token-ringed point, not Recharts' gray. */}
              <Tooltip
                cursor={{ stroke: "hsl(var(--muted-foreground))", strokeOpacity: 0.35, strokeWidth: 1 }}
                content={<HeatTooltip />}
              />
              <Area type="monotone" dataKey="value" isAnimationActive={false} stroke={LINE} strokeWidth={2} fill={LINE} fillOpacity={0.12}
                dot={{ r: 3, stroke: LINE, strokeWidth: 1.5, fill: "hsl(var(--card))" }}
                activeDot={{ r: 4, fill: LINE, stroke: "hsl(var(--background))", strokeWidth: 2 }} />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      ) : loading ? (
        <div role="progressbar" aria-label={text.heatLoading} className="flex min-h-0 flex-1 items-center justify-center text-muted-foreground">
          <Loader2 className="size-5 motion-safe:animate-spin" aria-hidden />
        </div>
      ) : (
        <p className="flex min-h-0 flex-1 items-center justify-center text-xs text-muted-foreground">
          {text.heatEmpty}
        </p>
      )}
    </section>
  )
}
