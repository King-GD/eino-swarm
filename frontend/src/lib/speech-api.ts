import { ApiError } from "./api"

async function speechResponse(path: string, init: RequestInit): Promise<Response> {
  const response = await fetch(path, { ...init, cache: "no-store", redirect: "error" })
  if (response.ok) return response
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
  if (!body || typeof body !== "object" || !("text" in body) || typeof body.text !== "string" || !body.text.trim()) {
    throw new Error("No speech was recognized. Try recording again.")
  }
  return body.text
}

export async function synthesizeSpeech(text: string, signal: AbortSignal, maxBytes: number): Promise<Blob> {
  const response = await speechResponse("/api/audio/speech", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text }), signal,
  })
  const mime = response.headers.get("Content-Type")?.split(";")[0] ?? ""
  if (!mime.startsWith("audio/")) {
    await response.body?.cancel().catch(() => undefined)
    throw new Error("The speech service did not return audio.")
  }
  const declared = Number(response.headers.get("Content-Length"))
  if (declared > maxBytes) {
    await response.body?.cancel().catch(() => undefined)
    throw new Error("The speech audio exceeds the configured limit.")
  }
  if (!response.body) throw new Error("The speech service returned empty audio.")
  const reader = response.body.getReader()
  const chunks: Uint8Array<ArrayBuffer>[] = []
  let size = 0
  const onAbort = () => { void reader.cancel().catch(() => undefined) }
  signal.addEventListener("abort", onAbort, { once: true })
  try {
    for (;;) {
      if (signal.aborted) throw new DOMException("Speech cancelled", "AbortError")
      const next = await reader.read()
      if (next.done) break
      size += next.value.byteLength
      if (size > maxBytes) throw new Error("The speech audio exceeds the configured limit.")
      chunks.push(new Uint8Array(next.value))
    }
    if (signal.aborted) throw new DOMException("Speech cancelled", "AbortError")
    if (!size) throw new Error("The speech service returned empty audio.")
    return new Blob(chunks, { type: mime })
  } finally {
    signal.removeEventListener("abort", onAbort)
    await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
}
