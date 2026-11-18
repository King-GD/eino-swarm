import { expect, it } from "vitest"
import { setLocale, t } from "@/lib/i18n"
import { SpeechCallError } from "@/lib/speech-transport"
import { speechFailureMessage } from "./speech-capture"

it("distinguishes an unreadable Gateway response from denied microphone access", () => {
  setLocale("zh")
  expect(speechFailureMessage(new SpeechCallError("network"))).toBe(t("speech.network"))
  expect(speechFailureMessage(new DOMException("private cause", "NotAllowedError"))).toBe(t("speech.permission"))
  expect(t("speech.network")).not.toBe(t("speech.failed"))
  setLocale("en")
})

it("identifies a speech service HTTP failure instead of showing an unexplained generic error", () => {
  setLocale("zh")
  expect(speechFailureMessage(new SpeechCallError("failed", 500))).toBe(t("speech.serviceFailed", { status: 500 }))
  setLocale("en")
})
