const nativeEdgeWidth = 44
const rightEdgeWidth = 44
const minTravel = 72
const directionSlop = 12
const horizontalBias = 1.25

function ownsHorizontalTouch(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return true
  if (target.closest("input, textarea, select, [contenteditable]")) return true
  for (let element: Element | null = target; element; element = element.parentElement) {
    if (element.scrollWidth <= element.clientWidth + 1) continue
    if (["auto", "scroll"].includes(getComputedStyle(element).overflowX)) return true
  }
  return false
}

export function installIOSContentBack(): () => void {
  let start: Touch | undefined
  const canTrack = () => (window.visualViewport?.scale ?? 1) === 1 && !window.getSelection()?.toString()
  const cancel = () => { start = undefined }
  const begin = (event: TouchEvent) => {
    cancel()
    if (event.touches.length !== 1 || !canTrack()) return
    const point = event.touches[0]
    if (point.clientX <= nativeEdgeWidth || ownsHorizontalTouch(event.target)) return
    start = point
  }
  const move = (event: TouchEvent) => {
    if (!start) return
    const point = event.touches[0]
    if (event.touches.length !== 1 || point.identifier !== start.identifier ||
      Math.abs(point.clientY - start.clientY) >= Math.max(directionSlop, Math.abs(point.clientX - start.clientX))) cancel()
  }
  const end = (event: TouchEvent) => {
    const origin = start
    cancel()
    const point = event.changedTouches[0]
    if (!origin || event.touches.length || point?.identifier !== origin.identifier || !canTrack()) return
    const travel = point.clientX - origin.clientX
    if (point.clientX >= window.innerWidth - rightEdgeWidth &&
      travel >= minTravel && travel > Math.abs(point.clientY - origin.clientY) * horizontalBias) {
      window.__zwaiAndroidBack?.()
    }
  }
  const options = { capture: true, passive: true }
  document.addEventListener("touchstart", begin, options)
  document.addEventListener("touchmove", move, options)
  document.addEventListener("touchend", end, options)
  document.addEventListener("touchcancel", cancel, options)
  return () => {
    document.removeEventListener("touchstart", begin, options)
    document.removeEventListener("touchmove", move, options)
    document.removeEventListener("touchend", end, options)
    document.removeEventListener("touchcancel", cancel, options)
    cancel()
  }
}
