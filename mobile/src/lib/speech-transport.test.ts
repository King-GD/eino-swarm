import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { clearGatewaySession, saveGatewaySession, type GatewaySession } from "./gateway-account"
import { gatewaySpeechCall, localSpeechCall } from "./speech-transport"
import type { RemoteLink } from "./link"
import type { RemoteRequest, RemoteResponse } from "./rpc"

const session: GatewaySession = { origin: "https://voice.test", token: "agm_" + "a".repeat(64), user: { id: "u", username: "user", approval_status: "approved" }, device: { id: "m", expires_at: "2099-01-01T00:00:00Z" } }
const config: SpeechSnapshot = {
  execution: "gateway", gateway_origin: session.origin,
  input: { enabled: true, ready: true, model: "transcription", label: "ASR", language: "en", prompt: "", recording_format: "auto", max_duration_seconds: 120, timeout_seconds: 120 },
  output: { enabled: true, ready: true, model: "speech", label: "TTS", voice: "", speed: 1, format: "wav", instructions: "", timeout_seconds: 120 },
  limits: { input_bytes: 1024 * 1024, output_bytes: 1024 * 1024, chunk_chars: 1000, staging_ttl_seconds: 300 },
}
const blob = () => Object.assign(new Blob([new Uint8Array([82, 73, 70, 70, 0, 255])], { type: "audio/wav" }), { arrayBuffer: async () => new Uint8Array([82, 73, 70, 70, 0, 255]).buffer })
beforeEach(() => saveGatewaySession(session))
afterEach(() => vi.useRealTimers())

describe("phone speech calls follow the PC's execution mode", () => {
  it("uploads directly to the same Gateway with the phone credential, never a PC credential", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => new Response(JSON.stringify({ text: "recognized" }), { headers: { "Content-Type": "application/json" } }))
    await expect(gatewaySpeechCall(config, session, "transcribe", blob(), new AbortController().signal, fetcher)).resolves.toBe("recognized")
    const [url, init] = fetcher.mock.calls[0]
    expect(url).toBe(session.origin + "/v1/audio/transcriptions")
    expect(init).toMatchObject({ redirect: "error", credentials: "omit", cache: "no-store", headers: { Authorization: "Bearer " + session.token } })
    const body = init?.body as FormData
    expect(body.get("model")).toBe("transcription")
    expect(body.get("language")).toBe("en")
    expect((body.get("file") as File).name).toBe("recording.wav")
    expect((body.get("file") as File).type).toBe("audio/wav")
  })

  it("refuses a different gateway or expired authority before any network request", async () => {
    const fetcher = vi.fn<typeof fetch>()
    await expect(gatewaySpeechCall({ ...config, gateway_origin: "https://elsewhere.test" }, session, "speak", "text", new AbortController().signal, fetcher)).rejects.toThrow("unavailable")
    clearGatewaySession()
    await expect(gatewaySpeechCall(config, session, "speak", "text", new AbortController().signal, fetcher)).rejects.toThrow("unavailable")
    expect(fetcher).not.toHaveBeenCalled()
  })

  it("cancels an outstanding binary request on logout without retry or PC fallback", async () => {
    let activeSignal: AbortSignal | undefined
    const fetcher = vi.fn<typeof fetch>(async (_url, init) => {
      activeSignal = init?.signal as AbortSignal
      return new Promise((_resolve, reject) => activeSignal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError"))))
    })
    const pending = gatewaySpeechCall(config, session, "speak", "text", new AbortController().signal, fetcher)
    clearGatewaySession()
    await expect(pending).rejects.toMatchObject({ name: "AbortError" })
    expect(activeSignal?.aborted).toBe(true)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it("validates audio MIME and response byte budgets before returning synthesized audio", async () => {
    const signal = new AbortController().signal
    await expect(gatewaySpeechCall(config, session, "speak", "text", signal, async () => new Response("not audio", { headers: { "Content-Type": "text/html" } }))).rejects.toThrow("invalid_audio")
    await expect(gatewaySpeechCall({ ...config, limits: { ...config.limits, output_bytes: 2 } }, session, "speak", "text", signal, async () => new Response(new Uint8Array([0, 1, 2]), { headers: { "Content-Type": "audio/wav" } }))).rejects.toThrow("too_large")
    const result = await gatewaySpeechCall(config, session, "speak", "text", signal, async () => new Response(new Uint8Array([0, 255, 4]), { headers: { "Content-Type": "audio/wav" } }))
    expect(result).toMatchObject({ type: "audio/wav", size: 3 })
  })

  it.each(["audio/wav", "audio/mpeg"])("rejects Gateway %s instead of buffering a requested PCM stream", async (mime) => {
    const streaming = { ...config, output: { ...config.output, format: "pcm" as const } }
    const response = new Response(new Uint8Array([0, 1]), { headers: { "Content-Type": mime } })
    const cancel = vi.spyOn(response.body!, "cancel")
    const fetcher = vi.fn<typeof fetch>(async () => response)
    await expect(gatewaySpeechCall(streaming, session, "speak", "text", new AbortController().signal, fetcher)).rejects.toMatchObject({ code: "invalid_audio" })
    expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toMatchObject({ response_format: "pcm", stream_format: "audio" })
    expect(cancel).toHaveBeenCalledOnce()
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it("returns PCM before synthesis finishes and retains logout cancellation after the headers", async () => {
    const streaming = { ...config, output: { ...config.output, format: "pcm" as SpeechSnapshot["output"]["format"] } }
    let body!: ReadableStreamDefaultController<Uint8Array>
    const cancelled = vi.fn()
    const fetcher = vi.fn<typeof fetch>(async () => new Response(new ReadableStream<Uint8Array>({
      start(controller) { body = controller; controller.enqueue(new Uint8Array([1, 2])) }, cancel: cancelled,
    }), { headers: { "Content-Type": "audio/pcm" } }))
    let settled = false
    const pending = gatewaySpeechCall(streaming, session, "speak", "text", new AbortController().signal, fetcher).then(value => { settled = true; return value })
    try {
      await vi.waitFor(() => expect(settled).toBe(true), { timeout: 100 })
      const result = await pending
      expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toMatchObject({ response_format: "pcm", stream_format: "audio" })
      expect(result).toHaveProperty("chunks")
      if (typeof result === "string" || !("chunks" in result)) throw new Error("Expected PCM stream")
      const reader = result.chunks[Symbol.asyncIterator]()
      expect(await reader.next()).toMatchObject({ done: false, value: new Uint8Array([1, 2]) })
      const next = reader.next()
      clearGatewaySession()
      await expect(next).rejects.toMatchObject({ name: "AbortError" })
      expect(cancelled).toHaveBeenCalledOnce()
    } finally { if (!settled) { body.close(); await pending } }
  })

  it("keeps the PCM timeout and total byte limit active while consuming the stream", async () => {
    vi.useFakeTimers()
    const streaming = { ...config, output: { ...config.output, format: "pcm" as SpeechSnapshot["output"]["format"], timeout_seconds: 1 } }
    const cancelled = vi.fn()
    const result = await gatewaySpeechCall(streaming, session, "speak", "text", new AbortController().signal, async () => new Response(new ReadableStream({ cancel: cancelled }), { headers: { "Content-Type": "audio/pcm" } }))
    if (typeof result === "string" || !("chunks" in result)) throw new Error("Expected PCM stream")
    const next = result.chunks[Symbol.asyncIterator]().next().catch(error => error)
    await vi.advanceTimersByTimeAsync(1001)
    expect(await next).toMatchObject({ code: "timeout" })
    expect(cancelled).toHaveBeenCalledOnce()

    const limited = await gatewaySpeechCall({ ...streaming, limits: { ...streaming.limits, output_bytes: 3 } }, session, "speak", "text", new AbortController().signal, async () => new Response(new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array([1, 2])); controller.enqueue(new Uint8Array([3, 4])); controller.close() } }), { headers: { "Content-Type": "audio/pcm" } }))
    if (typeof limited === "string" || !("chunks" in limited)) throw new Error("Expected PCM stream")
    const reader = limited.chunks[Symbol.asyncIterator]()
    await expect(reader.next()).resolves.toMatchObject({ value: new Uint8Array([1, 2]), done: false })
    await expect(reader.next()).rejects.toMatchObject({ code: "too_large" })
  })

  it("retains Gateway expiry after PCM EOF until queued playback releases the stream", async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-11-16T00:00:00Z"))
    const expiring = { ...session, device: { ...session.device, expires_at: new Date(Date.now() + 1000).toISOString() } }
    saveGatewaySession(expiring)
    const streaming = { ...config, output: { ...config.output, format: "pcm" as const } }
    let requestSignal!: AbortSignal
    const result = await gatewaySpeechCall(streaming, expiring, "speak", "text", new AbortController().signal, async (_url, init) => {
      requestSignal = init!.signal as AbortSignal
      return new Response(new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(new Uint8Array([1, 2])); controller.close() } }), { headers: { "Content-Type": "audio/pcm" } })
    })
    if (typeof result === "string" || !("chunks" in result)) throw new Error("Expected PCM stream")
    try {
      const reader = result.chunks[Symbol.asyncIterator]()
      await expect(reader.next()).resolves.toMatchObject({ done: false, value: new Uint8Array([1, 2]) })
      await expect(reader.next()).resolves.toMatchObject({ done: true })
      // Network EOF can precede the final scheduled AudioBufferSource ending.
      // Its authority must still be live, and expiry must reach that consumer.
      expect(requestSignal.aborted).toBe(false)
      const playbackSignal = (result as typeof result & { signal?: AbortSignal }).signal
      expect(playbackSignal).toBe(requestSignal)
      await vi.advanceTimersByTimeAsync(1001)
      expect(playbackSignal?.aborted).toBe(true)
      expect(vi.getTimerCount()).toBe(0)
    } finally { result.cancel() }
  })

  it("uses only local audio RPCs for a configured local PC and always releases the temporary audio", async () => {
    const rpc = vi.fn<RemoteLink["rpc"]>(async (req) => {
      if (req.op === "audio_transcribe") return { ok: true, audio: { audio_id: req.audio_id!, text: "from PC" } } as RemoteResponse
      return { ok: true, audio: { audio_id: req.audio_id! } } as RemoteResponse
    })
    const link = { alive: () => true, rpc } as unknown as RemoteLink
    await expect(localSpeechCall({ ...config, execution: "pc_local" }, link, "transcribe", blob(), new AbortController().signal)).resolves.toBe("from PC")
    expect(rpc.mock.calls[0][0]).toMatchObject({ op: "put", purpose: "audio", name: "recording.wav", data: "UklGRgD/" })
    const request = rpc.mock.calls[1][0]
    expect(request).toMatchObject({ op: "audio_transcribe", put_id: request.audio_id })
    expect(rpc.mock.calls[2][0]).toMatchObject({ op: "audio_cancel", audio_id: request.audio_id })
    expect(rpc.mock.calls[1][1]).toMatchObject({ dropOnTimeout: false })
  })

  it("assembles bounded binary chunks and rejects wrong offsets while cancelling the PC job", async () => {
    const run = async (bad: boolean) => {
      const calls: Omit<RemoteRequest, "v" | "id">[] = []
      const link = { alive: () => true, rpc: async (req: Omit<RemoteRequest, "v" | "id">) => {
        calls.push(req)
        const audio = req.op === "audio_speech" ? { audio_id: req.audio_id!, mime: "audio/wav", size: 3 }
          : req.op === "audio_chunk" ? { audio_id: req.audio_id!, mime: "audio/wav", size: 3, offset: bad ? 1 : 0, next: 3, more: false, data: "AP8E" }
          : { audio_id: req.audio_id! }
        return { ok: true, audio } as RemoteResponse
      } } as unknown as RemoteLink
      const pending = localSpeechCall({ ...config, execution: "pc_local" }, link, "speak", "text", new AbortController().signal)
      if (bad) await expect(pending).rejects.toThrow("invalid_audio")
      else expect(await pending).toMatchObject({ type: "audio/wav", size: 3 })
      expect(calls.at(-1)?.op).toBe("audio_cancel")
    }
    await run(false)
    await run(true)
  })
})
