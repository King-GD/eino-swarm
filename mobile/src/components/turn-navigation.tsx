import { t } from "@/lib/i18n"
import type { TurnView } from "@/lib/rpc"
import { dropPackedJson } from "@/lib/tool-preview"
import type { CompactBlock } from "@/lib/transcript"

export type TurnDestination = { id: string; label: string }

export function pairedTurnDestinations(turns: TurnView[], blocks: CompactBlock[]): TurnDestination[] {
  const prompts = new Map<string, string>()
  const loaded = new Map<string, number>()
  blocks.forEach((block, index) => {
    if (!block.turnId) return
    if (!loaded.has(block.turnId)) loaded.set(block.turnId, index)
    if (block.kind === "user") {
      const text = dropPackedJson(block.text).trim()
      if (text || block.hasImages) prompts.set(block.turnId, text || t("thread.image"))
    }
  })
  const listed = new Set(turns.map((turn) => turn.id))
  const firstListed = Math.min(...turns.map((turn) => loaded.get(turn.id) ?? Infinity))
  const extra = [...prompts].filter(([id]) => !listed.has(id))
  const make = (id: string, label: string) => ({ id, label: label.slice(0, 66) })
  return [
    ...extra.filter(([id]) => (loaded.get(id) ?? Infinity) < firstListed && Number.isFinite(firstListed))
      .map(([id, label]) => make(id, label)),
    ...turns.map((turn) => make(turn.id, prompts.get(turn.id) ?? turn.text ?? "")),
    ...extra.filter(([id]) => (loaded.get(id) ?? Infinity) >= firstListed || !Number.isFinite(firstListed))
      .map(([id, label]) => make(id, label)),
  ]
}

export function TurnNavigation({ turns, pending, onJump }: {
  turns: TurnDestination[]
  pending?: boolean
  onJump: (id: string) => void
}) {
  if (turns.length < 3) return null
  return (
    <nav aria-label={t("thread.turnNav")} className="rail flex min-h-10 shrink-0 items-center gap-1 overflow-x-auto border-b border-border px-2">
      {turns.map((turn, index) => (
        <button
          key={turn.id}
          type="button"
          aria-label={t("thread.jumpTurn", { n: index + 1, text: turn.label })}
          disabled={pending}
          className="inline-flex h-9 min-w-9 shrink-0 items-center justify-center rounded-lg px-2 text-xs font-medium text-muted-foreground hover:bg-accent focus-visible:bg-accent active:bg-accent disabled:opacity-50"
          onClick={() => onJump(turn.id)}
        >
          <span className="mr-1 tabular-nums">{index + 1}</span>
          {turn.label ? <span className="max-w-24 truncate">{turn.label}</span> : null}
        </button>
      ))}
    </nav>
  )
}

export function scrollToTranscriptRow(scroller: HTMLElement | null, row: HTMLElement | null | undefined): boolean {
  if (!scroller || !row) return false
  scroller.scrollTop += row.getBoundingClientRect().top - scroller.getBoundingClientRect().top - 8
  return true
}
