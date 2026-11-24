import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  type RefObject,
} from "react"

import { eventIsFromNestedScroller } from "@/lib/follow-scroll"
import { readerNearOlderHistory, shouldLoadOlderHistory } from "@/lib/thread-log"
import { TURN_NAV_ATTR, offsetInScroller } from "@/lib/turn-nav"

const MESSAGE = "[data-history-message]"

type ScrollAnchor = { id: string; top: number; height: number }

/** Fill a short pane, then page when the reader moves toward its older edge. */
export function useHistoryWindow({
  scrollerRef,
  loaded,
  hasMore,
  loading,
  pinned,
  growthKey,
  loadOlder,
}: {
  scrollerRef: RefObject<HTMLDivElement | null>
  loaded: boolean
  hasMore: boolean
  loading: boolean
  pinned: boolean
  growthKey: string
  loadOlder: (clientHeight?: number) => Promise<void>
}) {
  const sentinelRef = useRef<HTMLDivElement>(null)
  const anchor = useRef<ScrollAnchor | undefined>(undefined)
  const lastFill = useRef<string | undefined>(undefined)
  const requestedTop = useRef<number | undefined>(undefined)

  const requestOlder = useCallback(
    (source: "scroll" | "wheel" | "sentinel" | "fill") => {
      const el = scrollerRef.current
      if (!el || !hasMore || loading) return
      if (source !== "fill" && source !== "wheel" && requestedTop.current === el.scrollTop) return
      if (source === "scroll" || source === "wheel") {
        const first = el.querySelector<HTMLElement>(MESSAGE) ?? el.querySelector<HTMLElement>(`[${TURN_NAV_ATTR}]`)
        const firstTop = first && offsetInScroller(first, el) - el.scrollTop
        if (!shouldLoadOlderHistory(hasMore, loading, el.scrollTop, el.scrollHeight, el.clientHeight) &&
          !readerNearOlderHistory(el.scrollTop, el.scrollHeight, el.clientHeight, firstTop ?? undefined)) return
      } else if (source === "fill") {
        if (el.scrollHeight > el.clientHeight + 8 || lastFill.current === growthKey) return
        lastFill.current = growthKey
      }

      // At the top the reader wants to see the prepended message. Above that
      // threshold keep an existing message at the same viewport position.
      if (el.scrollTop >= 48 && !anchor.current) {
        const visible = Array.from(el.querySelectorAll<HTMLElement>(MESSAGE)).find(
          (row) => offsetInScroller(row, el) + row.offsetHeight > el.scrollTop,
        )
        anchor.current = {
          id: visible?.dataset.historyMessage ?? "",
          top: visible ? offsetInScroller(visible, el) : 0,
          height: el.scrollHeight,
        }
      }
      requestedTop.current = el.scrollTop
      void loadOlder(el.clientHeight)
    },
    [growthKey, hasMore, loading, loadOlder, scrollerRef],
  )

  const request = useRef(requestOlder)
  useLayoutEffect(() => {
    request.current = requestOlder
  }, [requestOlder])

  useLayoutEffect(() => {
    if (loading) return
    const previous = anchor.current
    anchor.current = undefined
    const el = scrollerRef.current
    if (!el || !previous || pinned || el.scrollTop < 48) return
    const row = Array.from(el.querySelectorAll<HTMLElement>(MESSAGE)).find(
      (candidate) => candidate.dataset.historyMessage === previous.id,
    )
    el.scrollTop += row ? offsetInScroller(row, el) - previous.top : el.scrollHeight - previous.height
  }, [growthKey, loading, pinned, scrollerRef])

  useEffect(() => {
    if (!loaded) {
      lastFill.current = undefined
      requestedTop.current = undefined
      anchor.current = undefined
      return
    }
    requestOlder("fill")
  }, [loaded, requestOlder])

  useEffect(() => {
    const el = scrollerRef.current
    if (!el || !loaded) return
    let top = el.scrollTop
    const onScroll = () => {
      const upward = el.scrollTop <= top
      top = el.scrollTop
      if (upward) request.current("scroll")
      else requestedTop.current = undefined
    }
    // A wheel-up at scrollTop=0 does not dispatch a scroll event.
    const onWheel = (event: WheelEvent) => {
      if (event.deltaY < 0 && el.scrollTop === 0 && !eventIsFromNestedScroller(event.target, el)) {
        request.current("wheel")
      }
    }
    el.addEventListener("scroll", onScroll, { passive: true })
    el.addEventListener("wheel", onWheel, { passive: true })
    return () => {
      el.removeEventListener("scroll", onScroll)
      el.removeEventListener("wheel", onWheel)
    }
  }, [loaded, scrollerRef])

  useEffect(() => {
    const root = scrollerRef.current
    const sentinel = sentinelRef.current
    if (!root || !sentinel || !loaded || !hasMore || typeof IntersectionObserver === "undefined") return
    // Keep one observer through a prepend. Re-observing while still at the
    // top reports the same intersection and can page again without a gesture.
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) request.current("sentinel")
    }, { root, rootMargin: "80px 0px 0px 0px" })
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [loaded, hasMore, scrollerRef])

  return sentinelRef
}
