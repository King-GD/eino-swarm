import { describe, expect, it } from "vitest"

import { formatClockTime, formatMessageTime, formatTime } from "./utils"

describe("formatClockTime", () => {
  it("is empty when the clock cannot read the stamp", () => {
    expect(formatClockTime("nope")).toBe("")
  })

  // The fofa log line is a clock beside each tool row; the date already
  // belongs to the request above it.
  it("shows a clock without a date or seconds", () => {
    const got = formatClockTime("2026-09-09T12:34:56.789Z")
    expect(got).toMatch(/:\d{2}/)
    expect(got).not.toMatch(/:\d{2}:\d{2}/)
    expect(got).not.toBe(formatMessageTime("2026-09-09T12:34:56.789Z"))
  })
})

describe("formatMessageTime", () => {
  it("is empty when the clock cannot read the stamp", () => {
    expect(formatMessageTime("nope")).toBe("")
  })

  // Codex/Hermes sit a calendar time under the bubble. Seconds make every
  // message look like it just happened; the timeline already has those.
  it("names the day and the clock, not the seconds", () => {
    const at = "2026-09-09T12:34:56.789Z"
    const got = formatMessageTime(at)
    expect(got.length).toBeGreaterThan(0)
    expect(got).not.toMatch(/:\d{2}:\d{2}/)
    expect(got).not.toBe(formatTime(at))
  })
})
