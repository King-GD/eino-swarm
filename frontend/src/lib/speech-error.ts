import { ApiError } from "./api"
import type { MessageKey, Vars } from "./i18n"

type Translate = (key: MessageKey, vars?: Vars) => string

const notReady = new Set(["speech_not_configured", "speech_login_required", "speech_role_unavailable", "speech_disabled"])

/** The voice API keeps upstream text private and returns one English sentence.
 *  Callers show the safe category in the interface language. Cancellation is silent. */
export function speechFailureMessage(error: unknown, translate: Translate): string | undefined {
  if (error instanceof DOMException) {
    if (error.name === "AbortError") return undefined
    if (error.name === "NotAllowedError" || error.name === "SecurityError") return translate("speech.permissionDenied")
    if (error.name === "NotFoundError" || error.name === "OverconstrainedError") return translate("speech.deviceUnavailable")
  }
  if (error instanceof ApiError && error.code && notReady.has(error.code)) return translate("speech.notReady")
  if (error instanceof ApiError && error.code?.startsWith("speech_")) return translate("speech.requestFailed")
  if (error instanceof Error && error.message && error.message !== "Voice request could not be completed.") return error.message
  return translate("speech.failed")
}
