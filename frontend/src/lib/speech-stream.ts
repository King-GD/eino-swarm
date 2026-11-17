/** Gateway PCM is signed 16-bit little-endian, mono, at 24 kHz. */
export interface SpeechAudioStream {
  chunks: AsyncIterable<Uint8Array>
  sampleRate: 24000
  cancel(): void
}
export type SpeechAudio = Blob | SpeechAudioStream

export class SpeechStreamError extends Error {
  constructor(readonly code: "too_large" | "invalid_audio" | "empty" | "failed", message: string) { super(message); this.name = "SpeechStreamError" }
}
const invalid = () => new SpeechStreamError("invalid_audio", "The speech service returned invalid audio.")
const oversized = () => new SpeechStreamError("too_large", "The speech audio exceeds the configured limit.")
const aborted = () => new DOMException("Speech cancelled", "AbortError")
const maxFrameChars = 1 << 20

function unencodedLength(response: Response): number | undefined {
  const encoding = response.headers.get("Content-Encoding")?.trim().toLowerCase()
  // Fetch decodes compressed bodies but retains the encoded Content-Length.
  if (encoding && encoding !== "identity") return undefined
  return Number(response.headers.get("Content-Length"))
}

/** Own the body immediately, including cancellation before iteration begins. */
function responseBytes(response: Response, signal: AbortSignal, maximum: number) {
  const reader = response.body?.getReader()
  if (!reader) throw invalid()
  let closed = false
  let cancelled = false
  const finish = () => {
    if (closed) return
    closed = true
    signal.removeEventListener("abort", cancel)
    void reader.cancel().catch(() => undefined).finally(() => reader.releaseLock())
  }
  const cancel = () => { cancelled = true; finish() }
  if (signal.aborted) cancel()
  else signal.addEventListener("abort", cancel, { once: true })
  const chunks = (async function* () {
    let total = 0
    try {
      for (;;) {
        if (cancelled || signal.aborted) throw aborted()
        const next = await reader.read()
        if (cancelled || signal.aborted) throw aborted()
        if (next.done) break
        total += next.value.byteLength
        if (total > maximum) throw oversized()
        if (next.value.byteLength) yield next.value
      }
      const declared = unencodedLength(response)
      if (declared !== undefined && declared > 0 && total !== declared) throw invalid()
    } finally { finish() }
  })()
  return { chunks, cancel }
}

function decodeAudio(value: unknown): Uint8Array {
  if (typeof value !== "string" || !value.length || value.length > maxFrameChars || value.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(value)) throw invalid()
  let raw: string
  try { raw = atob(value) } catch { throw invalid() }
  return Uint8Array.from(raw, (char) => char.charCodeAt(0))
}

function pcmParameters(contentType: string): boolean {
  return contentType.split(";").slice(1).every((parameter) => {
    const match = parameter.match(/^\s*(rate|channels)\s*=\s*(?:"([0-9]+)"|([0-9]+))\s*$/i)
    return Boolean(match && (match[2] ?? match[3]) === (match[1].toLowerCase() === "rate" ? "24000" : "1"))
  })
}

function pcmMetadata(value: Record<string, unknown>): boolean {
  return (value.sample_rate === undefined || value.sample_rate === 24000) &&
    (value.channels === undefined || value.channels === 1) &&
    (value.response_format === undefined || value.response_format === "pcm") &&
    (value.mime_type === undefined || (typeof value.mime_type === "string" &&
      value.mime_type.split(";")[0].trim().toLowerCase() === "audio/pcm" && pcmParameters(value.mime_type)))
}

async function* eventAudio(chunks: AsyncIterable<Uint8Array>, maximum: number): AsyncGenerator<Uint8Array> {
  const decoder = new TextDecoder("utf-8", { fatal: true })
  let pending = ""
  let data: string[] = []
  let event = ""
  let frameChars = 0
  let total = 0
  const frame = () => {
    const body = data.join("\n")
    const name = event
    data = []; event = ""; frameChars = 0
    if (!body) return undefined
    let value: Record<string, unknown>
    try { value = JSON.parse(body) as typeof value } catch { throw invalid() }
    if (!value || typeof value !== "object" || Array.isArray(value)) throw invalid()
    if (name && name !== "message" && value.type !== undefined && name !== value.type) throw invalid()
    const type = value.type ?? name
    if (type === "error" || value.error !== undefined) throw new SpeechStreamError("failed", "The speech stream failed.")
    if (type === "speech.audio.done") return "done"
    if (type !== "speech.audio.delta" || !pcmMetadata(value)) throw invalid()
    const bytes = decodeAudio(value.audio)
    total += bytes.byteLength
    if (total > maximum) throw oversized()
    return bytes
  }
  for await (const chunk of chunks) {
    try { pending += decoder.decode(chunk, { stream: true }) } catch { throw invalid() }
    for (;;) {
      const index = pending.search(/[\r\n]/)
      if (index < 0 || (pending[index] === "\r" && index === pending.length - 1)) break
      const line = pending.slice(0, index)
      pending = pending.slice(index + (pending[index] === "\r" && pending[index + 1] === "\n" ? 2 : 1))
      frameChars += line.length
      if (frameChars > maxFrameChars) throw oversized()
      if (!line) {
        const result = frame()
        if (result === "done") {
          if (!total) throw new SpeechStreamError("empty", "The speech service returned empty audio.")
          return
        }
        if (result) yield result
      } else {
        const separator = line.indexOf(":")
        const field = separator < 0 ? line : line.slice(0, separator)
        const value = separator < 0 ? "" : line.slice(separator + 1).replace(/^ /, "")
        if (field === "data") data.push(value)
        else if (field === "event") event = value
      }
    }
    if (pending.length + frameChars > maxFrameChars) throw oversized()
  }
  throw new SpeechStreamError("invalid_audio", "The speech stream ended before completion.")
}

/** Preserve incremental PCM/SSE responses; ordinary audio remains a Blob. */
export async function speechAudioResponse(response: Response, signal: AbortSignal, maxBytes: number, pcm = false): Promise<SpeechAudio> {
  const contentType = response.headers.get("Content-Type")?.toLowerCase() ?? ""
  const mime = contentType.split(";")[0].trim()
  const events = mime === "text/event-stream"
  const raw = mime === "audio/pcm" || (mime === "application/octet-stream" && pcm)
  if ((!events && !raw && (!mime.startsWith("audio/") || mime === "audio/l16")) ||
      (raw && !pcmParameters(contentType))) {
    await response.body?.cancel().catch(() => undefined)
    throw invalid()
  }
  const declared = unencodedLength(response)
  if (!events && declared !== undefined && declared > maxBytes) { await response.body?.cancel().catch(() => undefined); throw oversized() }
  const source = responseBytes(response, signal, events ? maxBytes * 2 + maxFrameChars : maxBytes)
  if (raw || events) {
    const chunks = (async function* () {
      let size = 0
      try {
        for await (const bytes of events ? eventAudio(source.chunks, maxBytes) : source.chunks) { size += bytes.length; yield bytes }
        if (!size) throw new SpeechStreamError("empty", "The speech service returned empty audio.")
      } finally { source.cancel() }
    })()
    return { sampleRate: 24000, chunks, cancel: source.cancel }
  }
  const parts: Uint8Array<ArrayBuffer>[] = []
  let size = 0
  try {
    for await (const bytes of source.chunks) { parts.push(new Uint8Array(bytes)); size += bytes.length }
    if (!size) throw new SpeechStreamError("empty", "The speech service returned empty audio.")
    return new Blob(parts, { type: mime })
  } finally { source.cancel() }
}
