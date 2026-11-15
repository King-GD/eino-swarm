import { afterEach, describe, expect, it, vi } from "vitest"
import { speechPlayback, startSpeechRecording } from "./speech-media"

afterEach(() => { speechPlayback.stop(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe("speech media ownership", () => {
  it("releases a microphone granted after cancellation", async () => {
    let grant!: (stream: MediaStream) => void
    const stop = vi.fn()
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: () => new Promise<MediaStream>((resolve) => { grant = resolve }) } })
    vi.stubGlobal("MediaRecorder", class { static isTypeSupported() { return true } })
    const cancel = new AbortController()
    const opening = startSpeechRecording({ signal: cancel.signal, maxDurationSeconds: 120, maxBytes: 1024, format: "auto" })
    cancel.abort()
    grant({ getTracks: () => [{ stop }] } as unknown as MediaStream)
    await expect(opening).rejects.toMatchObject({ name: "AbortError" })
    expect(stop).toHaveBeenCalledOnce()
  })

  it("stops pending synthesis and never plays a late response", async () => {
    const started = vi.fn()
    class Context {
      state = "running"
      destination = {}
      resume() { return Promise.resolve() }
      close() { return Promise.resolve() }
      decodeAudioData() { return Promise.resolve({ duration: 1 }) }
      createBufferSource() { return { connect: vi.fn(), disconnect: vi.fn(), start: started, stop: vi.fn() } }
    }
    vi.stubGlobal("AudioContext", Context)
    let resolve!: (blob: Blob) => void
    let signal!: AbortSignal
    const done = speechPlayback.play("answer", "A reply.", { chunkChars: 100, synthesize: (_text, passed) => {
      signal = passed
      return new Promise<Blob>((yes) => { resolve = yes })
    } })
    await vi.waitFor(() => expect(signal).toBeDefined())
    speechPlayback.stop()
    resolve(new Blob([new Uint8Array([1])], { type: "audio/wav" }))
    await done
    expect(signal.aborted).toBe(true)
    expect(started).not.toHaveBeenCalled()
    expect(speechPlayback.getSnapshot()).toEqual({ state: "idle" })
  })
})
