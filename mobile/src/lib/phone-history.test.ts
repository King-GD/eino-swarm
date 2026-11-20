import { describe, expect, it } from "vitest"
import { hasPhoneHistoryContent, hasPhoneTurnStart } from "./phone-history"
import type { CompactBlock } from "./transcript"

describe("phone opening content", () => {
  const block = (kind: CompactBlock["kind"], text = "", extra = {}): CompactBlock => ({ id: "b", kind, text, ...extra })
  it("ignores separators, blank bubbles, worker output and omitted bookkeeping", () => {
    expect(hasPhoneHistoryContent([
      block("turn_end", "2026-01-01T00:00:00Z"), block("answer", "  "),
      block("answer", "Worker output", { agentId: "worker" }), block("spawn"),
    ])).toBe(false)
  })
  it.each([
    block("user", "", { hasImages: true }), block("answer", "Answer"),
    block("reasoning", "Checking"), block("notice", "Paused"),
    block("error", "Failed"), block("question", "Choose"),
  ])("keeps visible $kind content without an older read", (content) => {
    expect(hasPhoneHistoryContent([content])).toBe(true)
  })
})

it("keeps seeking the selected prompt while only the turn's reply is loaded", () => {
  const reply: CompactBlock = { id: "reply", kind: "answer", text: "Reply", turnId: "selected" }
  const prompt: CompactBlock = { id: "prompt", kind: "user", text: "", turnId: "selected", hasImages: true }
  const adjacentEnd: CompactBlock = { id: "boundary", kind: "turn_end", text: "", turnId: "adjacent" }

  expect(hasPhoneTurnStart([reply, adjacentEnd], "selected", true)).toBe(false)
  expect(hasPhoneTurnStart([{ ...prompt, agentId: "worker" }, reply], "selected", true)).toBe(false)
  expect(hasPhoneTurnStart([prompt, reply], "selected", true)).toBe(true)
  expect(hasPhoneTurnStart([adjacentEnd, reply], "selected", true)).toBe(true)
  expect(hasPhoneTurnStart([reply], "selected", false)).toBe(true)
  expect(hasPhoneTurnStart([adjacentEnd], "selected", false)).toBe(false)
})
