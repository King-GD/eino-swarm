import { expect, it } from "vitest"
import { transcriptionText } from "./speech-text"

it.each(["Chinese", "English", "cantonese"])("keeps the transcript and removes the leading %s language envelope", language => {
  const text = "否。数值是 0.50。\nKeep the next line."
  expect(transcriptionText(`language ${language}<asr_text>${text}`)).toBe(text)
  expect(transcriptionText(` \nlanguage ${language}\n<asr_text> ${text} \n`)).toBe(text)
})

it.each(["", "  plain text  ", "language English", "<asr_text>literal", "Quote language Chinese<asr_text> literally."])(
  "does not remove ordinary text or incomplete/nonleading envelopes: %s", text => {
    expect(transcriptionText(text)).toBe(text)
  },
)

it("does not turn language-only metadata into a draft", () => {
  expect(transcriptionText("language None<asr_text> \n")).toBe("")
})
