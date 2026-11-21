import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react"
import { t } from "@/lib/i18n"
import type { TurnView } from "@/lib/rpc"
import { pairedPromptPreview } from "@/lib/turn-previews"
import type { CompactBlock } from "@/lib/transcript"

export type TurnDestination = { id: string; label: string }

const promptInset = 8

export function pairedTurnDestinations(turns: TurnView[], blocks: CompactBlock[], runningTurnId?: string): TurnDestination[] {
  const prompts = new Map<string, string>()
  const loaded = new Map<string, number>()
  blocks.forEach((block, index) => {
    if (!block.turnId) return
    if (!loaded.has(block.turnId)) loaded.set(block.turnId, index)
    if (block.kind === "user") {
      const label = pairedPromptPreview(block.text, block.hasImages)
      if (label) prompts.set(block.turnId, label)
    }
  })
  const listed = new Set(turns.map((turn) => turn.id))
  const firstListed = Math.min(...turns.map((turn) => loaded.get(turn.id) ?? Infinity))
  const extra = [...prompts].filter(([id]) => !listed.has(id))
  if (runningTurnId && !listed.has(runningTurnId) && !prompts.has(runningTurnId)) extra.push([runningTurnId, ""])
  const make = (id: string, label: string) => ({ id, label })
  return [
    ...extra.filter(([id]) => (loaded.get(id) ?? Infinity) < firstListed && Number.isFinite(firstListed))
      .map(([id, label]) => make(id, label)),
    ...turns.map((turn) => make(turn.id, prompts.get(turn.id) ?? pairedPromptPreview(turn.user_text ?? ""))),
    ...extra.filter(([id]) => (loaded.get(id) ?? Infinity) >= firstListed || !Number.isFinite(firstListed))
      .map(([id, label]) => make(id, label)),
  ]
}

export function TurnNavigation({ turns, scroller, runningTurnId, pending, onJump }: {
  turns: TurnDestination[]
  scroller: RefObject<HTMLElement | null>
  runningTurnId?: string
  pending?: boolean
  onJump: (id: string) => void
}) {
  const rail = useRef<HTMLElement>(null)
  const [current, setCurrent] = useState<string>()
  const following = useRef(true)
  const visible = turns.length >= 3

  useEffect(() => {
    const viewport = scroller.current
    if (!viewport || turns.length < 3) return
    const ids = new Set(turns.map((turn) => turn.id))
    const update = () => {
      const rows = Array.from(viewport.querySelectorAll<HTMLElement>("[data-phone-turn]"))
      const byTurn = new Map<string, HTMLElement>()
      for (const row of rows) {
        const id = row.dataset.phoneTurn ?? ""
        if (!ids.has(id)) continue
        const start = byTurn.get(id)
        if (!start || (row.hasAttribute("data-phone-prompt") && !start.hasAttribute("data-phone-prompt"))) {
          byTurn.set(id, row)
        }
      }
      const starts = rows.filter((row) => byTurn.get(row.dataset.phoneTurn ?? "") === row)
      const maxScroll = viewport.scrollHeight - viewport.clientHeight
      if ((maxScroll > 1 || runningTurnId) && viewport.scrollTop >= maxScroll - 1) {
        setCurrent(runningTurnId && ids.has(runningTurnId) ? runningTurnId : starts.at(-1)?.dataset.phoneTurn)
        return
      }
      const readingLine = viewport.getBoundingClientRect().top + promptInset + 1
      let active = starts[0]
      for (const start of starts) {
        if (start.getBoundingClientRect().top > readingLine) break
        active = start
      }
      setCurrent(active?.dataset.phoneTurn)
    }
    const resumeFollowing = () => { following.current = true }
    update()
    viewport.addEventListener("scroll", update, { passive: true })
    viewport.addEventListener("touchmove", resumeFollowing, { passive: true })
    viewport.addEventListener("wheel", resumeFollowing, { passive: true })
    viewport.addEventListener("keydown", resumeFollowing)
    const observer = new ResizeObserver(update)
    observer.observe(viewport)
    if (viewport.firstElementChild) observer.observe(viewport.firstElementChild)
    return () => {
      viewport.removeEventListener("scroll", update)
      viewport.removeEventListener("touchmove", resumeFollowing)
      viewport.removeEventListener("wheel", resumeFollowing)
      viewport.removeEventListener("keydown", resumeFollowing)
      observer.disconnect()
    }
  }, [scroller, turns, runningTurnId])

  // A new render or newly loaded label cannot move a manually browsed strip.
  useLayoutEffect(() => {
    const strip = rail.current
    const active = strip?.querySelector('[aria-current="location"]')
    if (!strip || !active) return
    const align = () => {
      if (following.current) revealTurnWithPrevious(strip, active)
    }
    align()
    const observer = new ResizeObserver(align)
    observer.observe(strip)
    return () => observer.disconnect()
  }, [current, visible])

  if (!visible) return null
  return (
    <nav ref={rail} aria-label={t("thread.turnNav")}
      className="rail flex min-h-14 shrink-0 touch-pan-x items-center gap-1 overflow-x-auto overscroll-x-contain border-b border-border px-2"
      onPointerDown={() => { following.current = false }}
      onWheel={(event) => { if (event.deltaX || event.shiftKey) following.current = false }}>
      {turns.map((turn, index) => {
        const label = turn.label || t("thread.turnNoPreview")
        return (
          <button
            key={turn.id}
            type="button"
            aria-label={t("thread.jumpTurn", { n: index + 1, text: label })}
            aria-current={turn.id === current ? "location" : undefined}
            disabled={pending}
            className="flex h-12 w-36 shrink-0 items-center gap-2 rounded-lg px-2 text-left text-xs text-muted-foreground hover:bg-accent focus-visible:bg-accent active:bg-accent aria-[current=location]:bg-accent aria-[current=location]:text-accent-foreground disabled:opacity-50"
            onClick={(event) => {
              following.current = true
              if (rail.current) revealTurnWithPrevious(rail.current, event.currentTarget)
              onJump(turn.id)
            }}
          >
            <span className="shrink-0 font-medium tabular-nums">{index + 1}</span>
            <span className="line-clamp-2 min-w-0 wrap-anywhere leading-4">{label}</span>
          </button>
        )
      })}
    </nav>
  )
}

function revealTurnWithPrevious(strip: HTMLElement, active: Element) {
  const bounds = strip.getBoundingClientRect()
  const turn = active.getBoundingClientRect()
  const previous = active.previousElementSibling?.getBoundingClientRect()
  // Keep the preceding turn within reach when moving backward through history.
  const left = previous && turn.right - previous.left <= bounds.width ? previous.left : turn.left
  if (left < bounds.left) strip.scrollLeft += left - bounds.left
  else if (turn.right > bounds.right) strip.scrollLeft += turn.right - bounds.right
}

export function scrollToTranscriptRow(scroller: HTMLElement | null, row: HTMLElement | null | undefined): boolean {
  if (!scroller || !row) return false
  scroller.scrollTop += row.getBoundingClientRect().top - scroller.getBoundingClientRect().top - promptInset
  return true
}
