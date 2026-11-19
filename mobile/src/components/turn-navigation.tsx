import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react"
import { t } from "@/lib/i18n"
import type { TurnView } from "@/lib/rpc"
import { dropPackedJson } from "@/lib/tool-preview"
import type { CompactBlock } from "@/lib/transcript"

export type TurnDestination = { id: string; label: string }

const promptInset = 8

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
    ...turns.map((turn) => make(turn.id, prompts.get(turn.id) ?? dropPackedJson(turn.user_text ?? "").trim())),
    ...extra.filter(([id]) => (loaded.get(id) ?? Infinity) >= firstListed || !Number.isFinite(firstListed))
      .map(([id, label]) => make(id, label)),
  ]
}

export function TurnNavigation({ turns, scroller, pending, onJump }: {
  turns: TurnDestination[]
  scroller: RefObject<HTMLElement | null>
  pending?: boolean
  onJump: (id: string) => void
}) {
  const rail = useRef<HTMLElement>(null)
  const [current, setCurrent] = useState<string>()

  useEffect(() => {
    const viewport = scroller.current
    if (!viewport || turns.length < 3) return
    const ids = new Set(turns.map((turn) => turn.id))
    const update = () => {
      const prompts = Array.from(viewport.querySelectorAll<HTMLElement>("[data-phone-turn][data-phone-prompt]"))
        .filter((row) => ids.has(row.dataset.phoneTurn ?? ""))
      const maxScroll = viewport.scrollHeight - viewport.clientHeight
      if (maxScroll > 1 && viewport.scrollTop >= maxScroll - 1) {
        setCurrent(prompts.at(-1)?.dataset.phoneTurn)
        return
      }
      const readingLine = viewport.getBoundingClientRect().top + promptInset + 1
      let active = prompts[0]
      for (const prompt of prompts) {
        if (prompt.getBoundingClientRect().top > readingLine) break
        active = prompt
      }
      setCurrent(active?.dataset.phoneTurn)
    }
    update()
    viewport.addEventListener("scroll", update, { passive: true })
    const observer = new ResizeObserver(update)
    observer.observe(viewport)
    if (viewport.firstElementChild) observer.observe(viewport.firstElementChild)
    return () => {
      viewport.removeEventListener("scroll", update)
      observer.disconnect()
    }
  }, [scroller, turns])

  useLayoutEffect(() => {
    const strip = rail.current
    const active = strip?.querySelector('[aria-current="location"]')
    if (!strip || !active) return
    const bounds = strip.getBoundingClientRect()
    const button = active.getBoundingClientRect()
    if (button.left < bounds.left) strip.scrollLeft += button.left - bounds.left
    else if (button.right > bounds.right) strip.scrollLeft += button.right - bounds.right
  }, [current, turns])

  if (turns.length < 3) return null
  return (
    <nav ref={rail} aria-label={t("thread.turnNav")} className="rail flex min-h-10 shrink-0 items-center gap-1 overflow-x-auto border-b border-border px-2">
      {turns.map((turn, index) => (
        <button
          key={turn.id}
          type="button"
          aria-label={t("thread.jumpTurn", { n: index + 1, text: turn.label })}
          aria-current={turn.id === current ? "location" : undefined}
          disabled={pending}
          className="inline-flex h-9 min-w-9 shrink-0 items-center justify-center rounded-lg px-2 text-xs font-medium text-muted-foreground hover:bg-accent focus-visible:bg-accent active:bg-accent aria-[current=location]:bg-accent aria-[current=location]:text-accent-foreground disabled:opacity-50"
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
  scroller.scrollTop += row.getBoundingClientRect().top - scroller.getBoundingClientRect().top - promptInset
  return true
}
