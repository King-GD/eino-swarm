import { Blob as NodeBlob } from "node:buffer"
import { afterEach, expect, it, vi } from "vitest"
import { startSpeechRecording } from "./speech-media"

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

function captureFixture() {
  vi.stubGlobal("Blob", NodeBlob)
  const decode = vi.fn<(bytes: ArrayBuffer) => Promise<AudioBuffer>>()
  const contexts: Context[] = []
  const stopTrack = vi.fn()
  class Context {
    state = "running"
    close = vi.fn(async () => { this.state = "closed" })
    decodeAudioData = decode
    constructor() { contexts.push(this) }
  }
  class Recorder {
    static isTypeSupported(type: string) { return type === "audio/mp4" }
    mimeType = "audio/mp4"
    state = "inactive"
    ondataavailable?: (event: { data: Blob }) => void
    onstop?: () => void
    start() { this.state = "recording" }
    stop() {
      this.state = "inactive"
      queueMicrotask(() => {
        this.ondataavailable?.({ data: new Blob([new Uint8Array([1, 2])], { type: this.mimeType }) })
        this.onstop?.()
      })
    }
  }
  vi.stubGlobal("AudioContext", Context)
  vi.stubGlobal("MediaRecorder", Recorder)
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: async () => ({ getTracks: () => [{ stop: stopTrack }] }) } })
  const decoded = { duration: 4 / 16000, sampleRate: 16000, length: 4, numberOfChannels: 1,
    getChannelData: () => new Float32Array([0.25, -0.25, 0.5, -0.5]) } as unknown as AudioBuffer
  return { decode, decoded, contexts, stopTrack }
}

it("refuses converted WAV bytes that exceed the limit even when compressed capture fits", async () => {
  const fixture = captureFixture()
  fixture.decode.mockResolvedValue(fixture.decoded)
  const capture = await startSpeechRecording({ signal: new AbortController().signal, format: "auto", maxBytes: 40, maxDurationSeconds: 120 })
  await expect(capture.stop()).rejects.toThrow("WAV recording exceeds")
  expect(fixture.decode).toHaveBeenCalledOnce()
  expect(fixture.stopTrack).toHaveBeenCalled()
  expect(fixture.contexts.every(context => context.close.mock.calls.length === 1)).toBe(true)
})

it("discards a conversion that returns after its recording owner was cancelled", async () => {
  const fixture = captureFixture()
  let complete!: (buffer: AudioBuffer) => void
  fixture.decode.mockImplementation(() => new Promise(resolve => { complete = resolve }))
  const owner = new AbortController()
  const capture = await startSpeechRecording({ signal: owner.signal, format: "auto", maxBytes: 1024, maxDurationSeconds: 120 })
  const result = capture.stop()
  await vi.waitFor(() => expect(fixture.decode).toHaveBeenCalledOnce())
  owner.abort()
  complete(fixture.decoded)
  await expect(result).rejects.toMatchObject({ name: "AbortError" })
  expect(fixture.contexts.every(context => context.close.mock.calls.length === 1)).toBe(true)
})

it("returns a decoder failure without publishing an undecoded MP4 fallback", async () => {
  const fixture = captureFixture()
  const failure = new DOMException("Unsupported capture", "EncodingError")
  fixture.decode.mockRejectedValue(failure)
  const capture = await startSpeechRecording({ signal: new AbortController().signal, format: "auto", maxBytes: 1024, maxDurationSeconds: 120 })
  await expect(capture.stop()).rejects.toBe(failure)
  expect(fixture.contexts.every(context => context.close.mock.calls.length === 1)).toBe(true)
})
