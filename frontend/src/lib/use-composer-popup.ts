import { useLayoutEffect, useState, type RefObject } from "react"

export function popupPlacement(trigger: DOMRect, viewportHeight: number, inComposer: boolean) {
  const gap = 4
  const above = Math.max(0, trigger.top - gap - 8)
  const below = Math.max(0, viewportHeight - trigger.bottom - gap - 8)
  // Overlap the draft, keeping the toolbar available for one-click switching.
  // A settings picker can use whichever side has more space.
  const top = above >= (inComposer ? 150 : 280) || above >= below
  return {
    side: top ? "top" as const : "bottom" as const,
    sideOffset: gap,
    maxHeight: Math.min(320, top ? above : below),
    edge: top ? viewportHeight - trigger.top + gap : trigger.bottom + gap,
    left: trigger.left,
  }
}

export function useComposerPopup(open: boolean, anchor: RefObject<HTMLElement | null>) {
  const [place, setPlace] = useState(() => popupPlacement(new DOMRect(), 0, false))
  useLayoutEffect(() => {
    const button = anchor.current
    if (!open || !button) return
    const box = button.closest("[data-composer-box]")
    const measure = () => setPlace(popupPlacement(button.getBoundingClientRect(), window.innerHeight, Boolean(box)))
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(box ?? button)
    window.addEventListener("resize", measure)
    window.addEventListener("scroll", measure, true)
    return () => {
      observer.disconnect()
      window.removeEventListener("resize", measure)
      window.removeEventListener("scroll", measure, true)
    }
  }, [open, anchor])
  return place
}

/** Radix restores focus on a later tick. A new menu may already own it. */
export function switchingComposerPopup(trigger: HTMLElement | null, closing: string) {
  const next = trigger?.closest<HTMLElement>("[data-composer-box]")?.dataset.menuOpen
  return Boolean(next && next !== closing)
}
