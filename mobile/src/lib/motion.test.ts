import { afterEach, describe, expect, it, vi } from "vitest"

import { scrollBehavior } from "./motion"

function reduced(matches: boolean) {
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches })))
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("scrollBehavior", () => {
  it("glides for a normal preference", () => {
    reduced(false)
    expect(scrollBehavior()).toBe("smooth")
  })

  it("jumps when the person asked for less motion", () => {
    // A smooth glide over a long transcript is exactly the motion this
    // preference exists to remove.
    reduced(true)
    expect(scrollBehavior()).toBe("auto")
  })
})
