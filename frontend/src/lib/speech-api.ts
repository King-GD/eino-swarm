import { ApiError } from "./api"
import { speechAudioResponse, type SpeechAudio } from "./speech-stream"
import { transcriptionText } from "./speech-text"

async function speechResponse(path: string, init: RequestInit): Promise<Response> {
  const response = await fetch(path, { ...init, cache: "no-store", redirect: "error" })
  if (response.ok) return response
  if ([401, 403].includes(response.status)) window.dispatchEvent(new Event("zwai.capabilities.rejected"))
  let message = `Speech request failed (${response.status}).`
  let code: string | undefined
  try {
    const body = await response.json()
    if (typeof body?.error === "string") message = body.error
    if (typeof body?.code === "string") code = body.code
  } catch { /* The HTTP status remains useful when no safe JSON error exists. */ }
  throw new ApiError(message, response.status, code)
}

export async function transcribeSpeech(blob: Blob, signal: AbortSignal): Promise<string> {
  const form = new FormData()
  const extension = blob.type.includes("wav") ? "wav" : blob.type.includes("mp4") ? "m4a" : blob.type.includes("ogg") ? "ogg" : "webm"
  form.append("file", blob, `recording.${extension}`)
  const response = await speechResponse("/api/audio/transcriptions", { method: "POST", body: form, signal })
  const body: unknown = await response.json()
  const text = body && typeof body === "object" && "text" in body && typeof body.text === "string" ? transcriptionText(body.text) : ""
  if (!text.trim()) {
    throw new Error("No speech was recognized. Try recording again.")
  }
  return text
}

export async function synthesizeSpeech(text: string, signal: AbortSignal, maxBytes: number): Promise<SpeechAudio> {
  const response = await speechResponse("/api/audio/speech", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text }), signal,
  })
  return speechAudioResponse(response, signal, maxBytes)
}
