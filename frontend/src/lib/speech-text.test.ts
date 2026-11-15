import { describe, expect, it } from "vitest"
import { insertSpeechText, speechSegments, speechText } from "./speech-text"

describe("speech text", () => {
  it("reads prose and link labels without speaking code or markup", () => {
    const markdown = "# Result\n\nUse **care** with [the service](https://example.invalid).\n\n```sh\nsecret_command\n```\n\nFinal `identifier`.\n\n![diagram](image.png)"
    expect(speechText(markdown)).toBe("Result\n\nUse care with the service.\n\nFinal identifier.")
    expect(speechText("```mermaid\ngraph TD; A-->B\n```")).toBe("")
  })

  it("handles indented code and escaped punctuation as markdown", () => {
    expect(speechText("Before\n\n    hidden()\n\nAfter \\*literal\\*.")).toBe("Before\n\nAfter *literal*.")
  })

  it("segments long prose without losing text or splitting Unicode characters", () => {
    const text = "第一句。第二句非常长😀😀😀😀。\n\nLast sentence."
    const parts = speechSegments(text, 8)
    expect(parts.every((part) => [...part].length <= 8 && part.trim())).toBe(true)
    expect(parts.join("").replace(/\s/g, "")).toBe(text.replace(/\s/g, ""))
    expect(parts.some((part) => part.includes("\ud83d") && !part.includes("😀"))).toBe(false)
  })

  it("inserts at the original caret and appends after concurrent editing", () => {
    expect(insertSpeechText("ac", "ac", 1, "b")).toEqual({ text: "abc", caret: 2 })
    expect(insertSpeechText("new draft", "old", 1, "spoken")).toEqual({ text: "new draft spoken", caret: 16 })
    expect(insertSpeechText("保留", "保留", 2, "补充")).toEqual({ text: "保留补充", caret: 4 })
  })
})
