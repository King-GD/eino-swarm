import { describe, expect, it } from "vitest"

import { englishSelection } from "./selection-language"

describe("englishSelection", () => {
  it("accepts English prose and words, including fullwidth letters", () => {
    expect(englishSelection("Keep the service running while the check finishes.")).toBe(true)
    expect(englishSelection("OK")).toBe(true)
    expect(englishSelection("it's done")).toBe(true)
    expect(englishSelection("Ready。")).toBe(true)
    expect(englishSelection("Ｈｉ")).toBe(true)
  })

  it("rejects text that is not an English selection", () => {
    for (const text of ["", "   ", "I", "a1", "12345", "这是中文", "check 这个", "Привет", "Καλημέρα"]) {
      expect(englishSelection(text), text).toBe(false)
    }
  })
})
