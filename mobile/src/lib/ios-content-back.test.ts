import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { installAndroidBack } from "./android-back"
import { installIOSContentBack } from "./ios-content-back"

function touch(type: string, target: Element, clientX: number, clientY = 300, identifier = 1, count = 1) {
  const point = { clientX, clientY, identifier }
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperties(event, {
    touches: { value: type === "touchend" ? [] : Array.from({ length: count }, () => point) },
    changedTouches: { value: [point] },
  })
  target.dispatchEvent(event)
  return event.defaultPrevented
}

const back = vi.fn(() => true)
let removeBack: () => void
let removeGesture: () => void

beforeEach(() => {
  back.mockClear()
  removeBack = installAndroidBack(back)
  removeGesture = installIOSContentBack()
})

afterEach(() => {
  removeGesture()
  removeBack()
  document.body.replaceChildren()
})

it("returns one current layer for a committed center swipe without consuming the touch", () => {
  const content = document.body
  touch("touchstart", content, window.innerWidth / 2)
  const modalBack = vi.fn(() => true)
  const removeModal = installAndroidBack(modalBack)
  try {
    const end = window.innerWidth - 15
    expect(touch("touchmove", content, end)).toBe(false)
    expect(modalBack).not.toHaveBeenCalled()
    expect(touch("touchend", content, end)).toBe(false)
    touch("touchend", content, end)
    expect(modalBack).toHaveBeenCalledOnce()
    expect(back).not.toHaveBeenCalled()
  } finally {
    removeModal()
  }
})

it("leaves native edge, short, early, vertical, cancelled and multitouch gestures alone", () => {
  const content = document.body
  const end = window.innerWidth - 15
  for (const [start, finish, y] of [[30, end, 300], [end - 50, end, 300], [500, end - 70, 300], [500, end, 800]]) {
    touch("touchstart", content, start)
    touch("touchend", content, finish, y)
  }
  touch("touchstart", content, 500)
  touch("touchmove", content, 502, 330)
  touch("touchend", content, end)
  touch("touchstart", content, 500)
  touch("touchcancel", content, 550)
  touch("touchend", content, end)
  touch("touchstart", content, 500)
  touch("touchstart", content, 520, 300, 2, 2)
  touch("touchend", content, end)
  expect(back).not.toHaveBeenCalled()
})

it("leaves nested horizontal scrollers and editable fields to their own gestures", () => {
  const rail = document.createElement("nav")
  rail.style.overflowX = "auto"
  Object.defineProperties(rail, { scrollWidth: { value: 600 }, clientWidth: { value: 390 } })
  const button = document.createElement("button")
  const input = document.createElement("textarea")
  rail.append(button)
  document.body.append(rail, input)
  for (const target of [button, input]) {
    touch("touchstart", target, 500)
    touch("touchend", target, window.innerWidth - 15)
  }
  expect(back).not.toHaveBeenCalled()
})
