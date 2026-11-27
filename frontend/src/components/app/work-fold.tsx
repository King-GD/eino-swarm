import { ChevronRight, Loader2 } from "lucide-react"
import { useMemo, useState, type ReactNode } from "react"

import { SwapLine } from "@/components/app/swap-line"
import { Button } from "@/components/ui/button"
import { Disclosure } from "@/components/ui/collapsible"
import type { TranscriptModePref } from "@/lib/appearance"
import { chromeTypeClass } from "@/lib/chrome-type"
import type { Block } from "@/lib/transcript"
import { cn } from "@/lib/utils"
import { useT, type Translate } from "@/lib/use-t"
import { useHistoryWork } from "@/lib/use-history-work"
import { useApp } from "@/store/app"
import {
  foldTurnItems,
  formatWorkTicker,
  isOmittedBlock,
  liveWorkIndex,
  planningTailAfterAnswer,
  workFoldStats,
  workTickerFrames,
  type WorkFoldStats,
} from "@/lib/work-fold"

const EMPTY_REVEAL = new Set<string>()

export function TurnBlockList({
  blocks,
  revealIds = EMPTY_REVEAL,
  renderBlock,
  mode,
  running = false,
}: {
  blocks: Block[]
  revealIds?: Set<string>
  renderBlock: (b: Block) => ReactNode
  mode?: TranscriptModePref
  running?: boolean
}) {
  const stored = useApp((s) => s.transcriptMode)
  const view = mode ?? stored
  const items = useMemo(() => foldTurnItems(blocks, view), [blocks, view])
  const liveAt = liveWorkIndex(items, running)
  const planningTail = planningTailAfterAnswer(items, running)
  return (
    <>
      {items.map((item, i) => {
        if (item.type === "block") {
          const { block } = item
          const message = block.kind === "user" || block.kind === "answer"
          return <div key={block.id} data-history-message={message ? block.id : undefined}>{renderBlock(block)}</div>
        }
        const single = item.blocks.length === 1 ? item.blocks[0] : undefined
        // Pending registry calls have no own row; keep their activity ticker.
        if (
          single &&
          (single.kind === "reasoning" ||
            (single.kind === "tool" &&
              single.tool?.name !== "spawn_agent" &&
              single.tool?.name !== "close_agent"))
        ) {
          return <div key={single.id} data-testid="single-work" className="relative pl-[var(--work-rail-pad,0px)] before:absolute before:inset-y-1 before:left-px before:border-l-[length:var(--work-rail,0px)] before:border-dashed before:border-border">{renderBlock(single)}</div>
        }
        return (
          <WorkFold
            key={item.blocks[0]?.id ?? "work"}
            blocks={item.blocks}
            reveal={item.blocks.some((b) => revealIds.has(b.id))}
            running={i === liveAt}
            renderBlock={renderBlock}
          />
        )
      })}
      {planningTail ? <PlanningTail /> : null}
    </>
  )
}

function PlanningTail() {
  const t = useT()
  return (
    <div
      data-testid="planning-tail"
      role="status"
      className="flex w-full min-w-0 items-center gap-2 overflow-hidden rounded-md px-2 py-1 text-(length:--chrome-font-size) text-muted-foreground"
    >
      <Loader2 aria-hidden className="size-3.5 shrink-0 animate-spin" />
      <SwapLine itemKey="planning:tail" text={t("transcript.planningMoves")} active />
    </div>
  )
}

export function WorkFold({
  blocks,
  reveal,
  running = false,
  renderBlock,
}: {
  blocks: Block[]
  reveal?: boolean
  running?: boolean
  renderBlock: (b: Block) => ReactNode
}) {
  const t = useT()
  const threadId = useApp((s) => s.activeId)
  const ranges = useMemo(() => blocks.flatMap((b) => b.historyWork ? [b.historyWork] : []), [blocks])
  const stats = workFoldStats(blocks)
  const frames = useMemo(
    () => workTickerFrames(blocks, running),
    [blocks, running],
  )
  const frame = frames[0]
  const live = Boolean(frame)
  const [choice, setChoice] = useState<boolean | null>(null)
  const expanded = (choice ?? false) || Boolean(reveal)
  const tickerText = frame ? formatWorkTicker(frame, t) : ""
  const { detail, retry } = useHistoryWork(threadId, ranges, expanded)
  const rows = ranges.length === 0 ? blocks : [
    ...blocks.filter((b) => !b.historyWork),
    ...(detail && "blocks" in detail ? detail.blocks.filter((b) => !isOmittedBlock(b)) : []),
  ].sort((a, b) => a.seq - b.seq)

  return (
    <Disclosure
      open={expanded}
      onOpenChange={setChoice}
      failed={stats.failed}
      className="wb-thread-work"
      testId="work-fold"
      summary={
        <>
          {live ? (
            <Loader2
              aria-hidden
              className="size-3.5 shrink-0 animate-spin"
            />
          ) : (
            <ChevronRight
              aria-hidden
              className={cn(
                "size-3.5 shrink-0 opacity-60 transition-transform",
                expanded && "rotate-90",
              )}
            />
          )}
          {live && frame ? (
            <SwapLine
              itemKey={frame.id}
              text={tickerText}
              active
              className={stats.failed ? "text-destructive" : undefined}
            />
          ) : (
            <span className={cn("min-w-0 flex-1 truncate", chromeTypeClass)}>
              {foldLabel(stats, t)}
            </span>
          )}
          {live ? (
            <ChevronRight
              aria-hidden
              className={cn(
                "ml-auto size-3.5 shrink-0 opacity-60 transition-transform",
                expanded && "rotate-90",
              )}
            />
          ) : null}
        </>
      }
    >
      {expanded ? (
        <div className="flex flex-col gap-1">
          {ranges.length > 0 && !detail ? (
            <p role="status" className="px-2 text-muted-foreground">{t("transcript.historyWorkLoading")}</p>
          ) : null}
          {detail && "error" in detail ? (
            <div className="flex flex-wrap items-center gap-2 px-2">
              <span role="alert">{t("transcript.historyWorkFailed")}</span>
              <Button variant="outline" size="sm" onClick={retry}>{t("transcript.historyWorkRetry")}</Button>
            </div>
          ) : null}
          {rows.map((b) => (
            <div key={b.id}>{renderBlock(b)}</div>
          ))}
        </div>
      ) : null}
    </Disclosure>
  )
}

function foldLabel(stats: WorkFoldStats, t: Translate): string {
  const tools =
    stats.tools === 1
      ? t("transcript.workFoldTool")
      : t("transcript.workFoldTools", { n: String(stats.tools) })
  if (stats.thoughts > 0 && stats.tools > 0) {
    return t("transcript.workFoldBoth", { tools })
  }
  if (stats.tools > 0) return tools
  return t("transcript.thought")
}
