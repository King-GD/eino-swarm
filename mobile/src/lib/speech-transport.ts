import type { SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { speechAudioResponse, type SpeechAudio, type SpeechAudioStream } from "../../../frontend/src/lib/speech-stream"
import { gatewayAuthorityCurrent, notifyGatewayRefusal, watchGatewayAuthority } from "./gateway-authority"
import type { GatewaySession } from "./gateway-account"
import { gatewayFetch } from "./gateway-fetch"
import type { RemoteLink } from "./link"
import { putFrames } from "./put-chunks"
import { OpAudioCancel, OpAudioChunk, OpAudioSpeech, OpAudioTranscribe, type RemoteResponse } from "./rpc"

export class SpeechCallError extends Error {
  constructor(readonly code: "unavailable" | "failed" | "timeout" | "too_large" | "invalid_audio" | "empty" | "permission" = "failed") { super(code); this.name = "SpeechCallError" }
}

/** A response retains timeout and authority through queued playback. Successful
 * EOF leaves ownership with the consumer until its explicit cancel(). */
export function retainSpeechStream(stream: SpeechAudioStream, release: () => void, check: () => void, mapFailure: (error: unknown) => unknown = error => error, signal?: AbortSignal): SpeechAudioStream {
  let closed = false
  const cancel = () => {
    if (closed) return
    closed = true
    signal?.removeEventListener("abort", cancel)
    stream.cancel()
    release()
  }
  if (signal?.aborted) cancel(); else signal?.addEventListener("abort", cancel, { once: true })
  return {
    sampleRate: stream.sampleRate,
    signal: stream.signal ?? signal,
    cancel,
    chunks: (async function* () {
      let completed = false
      try {
        check()
        for await (const chunk of stream.chunks) {
          check()
          yield chunk
        }
        check()
        completed = true
      } catch (error) { throw mapFailure(error) }
      finally { if (!completed) cancel() }
    })(),
  }
}

export function speechFileName(mime: string): string {
  return "recording." + (mime.includes("wav") ? "wav" : mime.includes("mp4") ? "m4a" : mime.includes("ogg") ? "ogg" : "webm")
}

async function boundedBody(response: Response, maximum: number, signal: AbortSignal): Promise<Uint8Array> {
  const declared = Number(response.headers.get("Content-Length") || "0")
  if (declared > maximum) { await response.body?.cancel(); throw new SpeechCallError("too_large") }
  const reader = response.body?.getReader()
  if (!reader) {
    const bytes = new Uint8Array(await response.arrayBuffer())
    if (bytes.length > maximum) throw new SpeechCallError("too_large")
    return bytes
  }
  const parts: Uint8Array[] = []
  let size = 0
  const abort = () => { void reader.cancel().catch(() => undefined) }
  signal.addEventListener("abort", abort, { once: true })
  try {
    for (;;) {
      if (signal.aborted) throw new DOMException("Aborted", "AbortError")
      const next = await reader.read()
      if (next.done) break
      size += next.value.length
      if (size > maximum) throw new SpeechCallError("too_large")
      parts.push(next.value)
    }
  } finally {
    signal.removeEventListener("abort", abort)
    await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
  if (signal.aborted) throw new DOMException("Aborted", "AbortError")
  const result = new Uint8Array(size)
  let offset = 0
  for (const part of parts) { result.set(part, offset); offset += part.length }
  return result
}

/** The PC supplies parameters, never a request URL or a credential. Construct
 * the ordinary Mobile data-plane endpoint from the phone's own live session. */
export async function gatewaySpeechCall(snapshot: SpeechSnapshot, session: GatewaySession, operation: "transcribe" | "speak", input: Blob | string, signal: AbortSignal, fetcher: typeof fetch = gatewayFetch): Promise<string | SpeechAudio> {
  if (snapshot.execution !== "gateway" || snapshot.gateway_origin !== session.origin || !gatewayAuthorityCurrent(session)) throw new SpeechCallError("unavailable")
  return endpointSpeechCall(snapshot, session.origin + "/v1", session.token, operation, input, signal, fetcher, session)
}

/** Also used by LOCAL phone bindings. The endpoint and credential are supplied
 * by this device's provider record, never by a cached PC speech snapshot. */
export async function endpointSpeechCall(snapshot: SpeechSnapshot, baseURL: string, apiKey: string, operation: "transcribe" | "speak", input: Blob | string, signal: AbortSignal, fetcher: typeof fetch = fetch, authority?: GatewaySession): Promise<string | SpeechAudio> {
  const option = operation === "transcribe" ? snapshot.input : snapshot.output
  if (!option.enabled || !option.ready) throw new SpeechCallError("unavailable")
  const aborter = new AbortController()
  let timedOut = false
  const abort = () => aborter.abort(timedOut ? new SpeechCallError("timeout") : undefined)
  if (signal.aborted) abort(); else signal.addEventListener("abort", abort, { once: true })
  const unwatch = authority ? watchGatewayAuthority(authority, abort) : () => undefined
  const timer = setTimeout(() => { timedOut = true; abort() }, option.timeout_seconds * 1000)
  let retained = false
  const release = () => { clearTimeout(timer); unwatch(); signal.removeEventListener("abort", abort); abort() }
  const check = () => {
    if ((authority && !gatewayAuthorityCurrent(authority)) || aborter.signal.aborted) throw new DOMException("Aborted", "AbortError")
  }
  const mapFailure = (error: unknown): unknown => {
    if (timedOut) return new SpeechCallError("timeout")
    if (aborter.signal.aborted) return new DOMException("Aborted", "AbortError")
    if (error instanceof SpeechCallError || (error instanceof Error && error.name === "AbortError")) return error
    const code = error && typeof error === "object" && "code" in error ? error.code : undefined
    return new SpeechCallError(code === "too_large" || code === "invalid_audio" || code === "empty" ? code : "failed")
  }
  try {
    let body: FormData | string
    const headers: Record<string, string> = apiKey ? { Authorization: "Bearer " + apiKey } : {}
    if (operation === "transcribe") {
      if (typeof input === "string" || !input.size || input.size > snapshot.limits.input_bytes) throw new SpeechCallError("too_large")
      body = new FormData()
      body.append("model", snapshot.input.model)
      body.append("file", input, speechFileName(input.type))
      body.append("response_format", "json")
      if (snapshot.input.language) body.append("language", snapshot.input.language)
      if (snapshot.input.prompt) body.append("prompt", snapshot.input.prompt)
    } else {
      if (typeof input !== "string" || !input.trim() || Array.from(input).length > snapshot.limits.chunk_chars) throw new SpeechCallError("too_large")
      headers["Content-Type"] = "application/json"
      body = JSON.stringify({ model: snapshot.output.model, input, response_format: snapshot.output.format, speed: snapshot.output.speed,
        ...(snapshot.output.format === "pcm" ? { stream_format: "audio" } : {}),
        ...(snapshot.output.voice ? { voice: snapshot.output.voice } : {}), ...(snapshot.output.instructions ? { instructions: snapshot.output.instructions } : {}) })
    }
    const response = await fetcher(baseURL.replace(/\/$/, "") + "/audio/" + (operation === "transcribe" ? "transcriptions" : "speech"), {
      method: "POST", headers, body, signal: aborter.signal, redirect: "error", credentials: "omit", cache: "no-store",
    })
    if (!response.ok) {
      await response.body?.cancel().catch(() => undefined)
      if (authority) notifyGatewayRefusal(authority, response.status)
      throw new SpeechCallError(response.status === 401 || response.status === 403 ? "permission" : "failed")
    }
    if (operation === "speak" && snapshot.output.format === "pcm") {
      const audio = await speechAudioResponse(response, aborter.signal, snapshot.limits.output_bytes, true)
      check()
      if ("chunks" in audio) {
        retained = true
        return retainSpeechStream(audio, release, check, mapFailure, aborter.signal)
      }
      return audio
    }
    const maximum = operation === "transcribe" ? snapshot.limits.input_bytes : snapshot.limits.output_bytes
    const bytes = await boundedBody(response, maximum, aborter.signal)
    if ((authority && !gatewayAuthorityCurrent(authority)) || aborter.signal.aborted) throw new DOMException("Aborted", "AbortError")
    if (operation === "transcribe") {
      let decoded: unknown
      try { decoded = JSON.parse(new TextDecoder().decode(bytes)) } catch { throw new SpeechCallError("failed") }
      const text = (decoded as { text?: unknown })?.text
      if (typeof text !== "string" || !text.trim()) throw new SpeechCallError("empty")
      return text
    }
    const mime = (response.headers.get("Content-Type") || "").split(";")[0].trim().toLowerCase()
    if (!bytes.length || !(mime.startsWith("audio/") || mime === "application/octet-stream")) throw new SpeechCallError("invalid_audio")
    return new Blob([new Uint8Array(bytes).buffer], { type: mime === "application/octet-stream" ? (snapshot.output.format === "wav" ? "audio/wav" : "audio/mpeg") : mime })
  } catch (error) {
    throw mapFailure(error)
  } finally { if (!retained) release() }
}

function audioResponse(response: RemoteResponse, id: string) {
  if (!response.ok) throw new SpeechCallError(response.code === "audio_too_large" ? "too_large" : "failed")
  if (!response.audio || response.audio.audio_id !== id) throw new SpeechCallError()
  return response.audio
}

/** Model calls are async RPCs. Cancelling the local waiter alone does not
 * cancel the PC's model request, so every exit explicitly releases its ID. */
export async function localSpeechCall(snapshot: SpeechSnapshot, link: RemoteLink, operation: "transcribe" | "speak", input: Blob | string, signal: AbortSignal): Promise<string | Blob> {
  if (snapshot.execution !== "pc_local" || !link.alive()) throw new SpeechCallError("unavailable")
  const option = operation === "transcribe" ? snapshot.input : snapshot.output
  if (!option.enabled || !option.ready) throw new SpeechCallError("unavailable")
  const id = crypto.randomUUID()
  const opts = { signal, dropOnTimeout: false, timeoutMs: (option.timeout_seconds + 15) * 1000 }
  try {
    if (operation === "transcribe") {
      if (typeof input === "string" || !input.size || input.size > snapshot.limits.input_bytes) throw new SpeechCallError("too_large")
      const bytes = new Uint8Array(await input.arrayBuffer())
      for (const frame of putFrames(id, speechFileName(input.type), input.type, bytes)) {
        const result = await link.rpc({ ...frame, purpose: "audio" }, opts)
        if (!result.ok) throw new SpeechCallError()
      }
      const response = audioResponse(await link.rpc({ op: OpAudioTranscribe, audio_id: id, put_id: id }, opts), id)
      if (typeof response.text !== "string" || !response.text.trim()) throw new SpeechCallError("empty")
      return response.text
    }
    if (typeof input !== "string" || !input.trim() || Array.from(input).length > snapshot.limits.chunk_chars) throw new SpeechCallError("too_large")
    const result = audioResponse(await link.rpc({ op: OpAudioSpeech, audio_id: id, text: input }, opts), id)
    if (!Number.isSafeInteger(result.size) || !result.size || result.size < 0 || result.size > snapshot.limits.output_bytes || !result.mime?.startsWith("audio/")) throw new SpeechCallError("invalid_audio")
    const parts: Uint8Array[] = []
    let offset = 0
    while (offset < result.size) {
      const part = audioResponse(await link.rpc({ op: OpAudioChunk, audio_id: id, offset }, opts), id)
      if (part.offset !== offset || part.mime !== result.mime || part.size !== result.size || typeof part.data !== "string") throw new SpeechCallError("invalid_audio")
      let bytes: Uint8Array
      try { bytes = Uint8Array.from(atob(part.data), (c) => c.charCodeAt(0)) } catch { throw new SpeechCallError("invalid_audio") }
      if (!bytes.length || bytes.length > 36 * 1024 || part.next !== offset + bytes.length || part.next > result.size || part.more !== (part.next < result.size)) throw new SpeechCallError("invalid_audio")
      parts.push(bytes); offset = part.next
    }
    if (signal.aborted) throw new DOMException("Aborted", "AbortError")
    return new Blob(parts.map((part) => new Uint8Array(part).buffer), { type: result.mime })
  } catch (error) {
    if (signal.aborted) throw new DOMException("Aborted", "AbortError")
    if (error instanceof SpeechCallError || (error instanceof Error && error.name === "AbortError")) throw error
    throw new SpeechCallError()
  } finally {
    if (link.alive()) await link.rpc({ op: OpAudioCancel, audio_id: id }, { dropOnTimeout: false, timeoutMs: 15000 }).catch(() => undefined)
  }
}
