import { Blob as NodeBlob } from "node:buffer"
import { afterEach, describe, expect, it, vi } from "vitest"
import { audioInputChoices, listAudioInputDevices, speechPlayback, startSpeechRecording } from "./speech-media"
import type { SpeechAudioStream } from "./speech-stream"

afterEach(() => { speechPlayback.stop(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe("microphone selection", () => {
  it("lists physical inputs and keeps the browser default as the system choice", () => {
    expect(audioInputChoices([
      { kind: "audioinput", deviceId: "default", label: "Default - MacBook Pro Microphone" },
      { kind: "audioinput", deviceId: "communications", label: "Communications" },
      { kind: "audioinput", deviceId: "camera", label: " Camera 100-Audio " },
      { kind: "audioinput", deviceId: "camera", label: "duplicate" },
      { kind: "audiooutput", deviceId: "speakers", label: "Speakers" },
      { kind: "audioinput", deviceId: " ", label: "blank" },
    ])).toEqual([{ id: "camera", label: "Camera 100-Audio" }])
  })

  it("asks for the chosen device and does not replace a blank choice", async () => {
    const getUserMedia = vi.fn(async () => ({ getTracks: () => [{ stop: vi.fn() }] }))
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } })
    class Recorder {
      static isTypeSupported() { return false }
      state = "inactive"
      onstop?: () => void
      start() { this.state = "recording" }
      stop() { this.state = "inactive"; this.onstop?.() }
    }
    vi.stubGlobal("MediaRecorder", Recorder)
    const chosen = await startSpeechRecording({ signal: new AbortController().signal, maxDurationSeconds: 5, maxBytes: 1000, format: "auto", deviceId: " camera " })
    expect(getUserMedia).toHaveBeenLastCalledWith({ audio: { deviceId: { exact: "camera" } }, video: false })
    chosen.cancel()
    const standard = await startSpeechRecording({ signal: new AbortController().signal, maxDurationSeconds: 5, maxBytes: 1000, format: "auto", deviceId: "  " })
    expect(getUserMedia).toHaveBeenLastCalledWith({ audio: true, video: false })
    standard.cancel()
  })

  it.each([
    { reason: "device removed", error: new DOMException("missing", "NotFoundError") },
    { reason: "device ID mismatch", error: Object.assign(new Error("mismatch"), { name: "OverconstrainedError", constraint: "deviceId" }) },
    { reason: "constraint hidden", error: Object.assign(new Error("hidden"), { name: "OverconstrainedError", constraint: "" }) },
  ])("records from the system input when the preferred device is missing ($reason)", async ({ error }) => {
    const stopTrack = vi.fn()
    const stream = { getTracks: () => [{ stop: stopTrack }] }
    const getUserMedia = vi.fn().mockRejectedValueOnce(error).mockResolvedValueOnce(stream)
    const start = vi.fn()
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } })
    vi.stubGlobal("MediaRecorder", class { start = start })
    const capture = await startSpeechRecording({
      signal: new AbortController().signal, maxDurationSeconds: 5, maxBytes: 1000, format: "auto", deviceId: "missing",
    })
    expect(getUserMedia.mock.calls).toEqual([
      [{ audio: { deviceId: { exact: "missing" } }, video: false }],
      [{ audio: true, video: false }],
    ])
    expect(start).toHaveBeenCalledOnce()
    capture.cancel()
    expect(stopTrack).toHaveBeenCalledOnce()
  })

  it.each([
    { reason: "permission denied", deviceId: "missing", errors: [new DOMException("denied", "NotAllowedError")] },
    { reason: "device busy", deviceId: "missing", errors: [new DOMException("busy", "NotReadableError")] },
    { reason: "other constraint", deviceId: "missing", errors: [Object.assign(new Error("sample rate"), { name: "OverconstrainedError", constraint: "sampleRate" })] },
    { reason: "both devices missing", deviceId: "missing", errors: [new DOMException("removed", "NotFoundError"), new DOMException("no default", "NotFoundError")] },
    { reason: "default device missing", deviceId: "", errors: [new DOMException("no default", "NotFoundError")] },
  ])("surfaces the capture error after the allowed attempts ($reason)", async ({ deviceId, errors }) => {
    const getUserMedia = vi.fn()
    for (const error of errors) getUserMedia.mockRejectedValueOnce(error)
    const recorder = vi.fn()
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } })
    vi.stubGlobal("MediaRecorder", recorder)
    await expect(startSpeechRecording({
      signal: new AbortController().signal, maxDurationSeconds: 5, maxBytes: 1000, format: "auto", deviceId,
    })).rejects.toBe(errors.at(-1))
    expect(getUserMedia).toHaveBeenCalledTimes(errors.length)
    expect(recorder).not.toHaveBeenCalled()
  })

  it("does not request a fallback after the recording was cancelled", async () => {
    const controller = new AbortController()
    const getUserMedia = vi.fn(async () => {
      controller.abort()
      throw new DOMException("device removed", "NotFoundError")
    })
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } })
    vi.stubGlobal("MediaRecorder", vi.fn())
    await expect(startSpeechRecording({
      signal: controller.signal, maxDurationSeconds: 5, maxBytes: 1000, format: "auto", deviceId: "missing",
    })).rejects.toMatchObject({ name: "AbortError" })
    expect(getUserMedia).toHaveBeenCalledOnce()
  })

  it("only unlocks hidden names after an explicit request and keeps the list when denied", async () => {
    const stop = vi.fn()
    const enumerateDevices = vi.fn()
      .mockResolvedValueOnce([{ kind: "audioinput", deviceId: "iphone", label: "" }])
      .mockResolvedValueOnce([{ kind: "audioinput", deviceId: "iphone", label: "赵武's iPhone Microphone" }])
    const getUserMedia = vi.fn(async () => ({ getTracks: () => [{ stop }] }))
    vi.stubGlobal("navigator", { mediaDevices: { enumerateDevices, getUserMedia } })
    await expect(listAudioInputDevices()).resolves.toEqual([{ id: "iphone", label: "" }])
    expect(getUserMedia).not.toHaveBeenCalled()
    await expect(listAudioInputDevices(getUserMedia() as unknown as Promise<MediaStream>)).resolves.toEqual([{ id: "iphone", label: "赵武's iPhone Microphone" }])
    expect(stop).toHaveBeenCalledOnce()
    enumerateDevices.mockReset()
    getUserMedia.mockRejectedValueOnce(new DOMException("denied", "NotAllowedError"))
    enumerateDevices.mockResolvedValue([{ kind: "audioinput", deviceId: "iphone", label: "" }])
    await expect(listAudioInputDevices(getUserMedia() as unknown as Promise<MediaStream>)).resolves.toEqual([{ id: "iphone", label: "" }])
  })

  it("keeps the system input available without requesting access when WebKit hides all device IDs", async () => {
    const stop = vi.fn()
    const enumerateDevices = vi.fn()
      .mockResolvedValueOnce([{ kind: "audioinput", deviceId: "", label: "" }])
      .mockResolvedValueOnce([
        { kind: "audioinput", deviceId: "default", label: "Default - MacBook Pro Microphone" },
        { kind: "audioinput", deviceId: "iphone", label: "赵武's iPhone Microphone" },
        { kind: "audioinput", deviceId: "camera", label: "Camera 100-Audio" },
      ])
    const getUserMedia = vi.fn(async () => ({ getTracks: () => [{ stop }] }))
    vi.stubGlobal("navigator", { mediaDevices: { enumerateDevices, getUserMedia } })
    await expect(listAudioInputDevices()).resolves.toEqual([])
    expect(getUserMedia).not.toHaveBeenCalled()
    await expect(listAudioInputDevices(getUserMedia() as unknown as Promise<MediaStream>)).resolves.toEqual([
      { id: "iphone", label: "赵武's iPhone Microphone" },
      { id: "camera", label: "Camera 100-Audio" },
    ])
    expect(getUserMedia).toHaveBeenCalledOnce()
    expect(stop).toHaveBeenCalledOnce()
  })

  it("uses the capture started by the menu click instead of asking again after enumeration", async () => {
    const stop = vi.fn()
    const enumerateDevices = vi.fn().mockResolvedValue([{ kind: "audioinput", deviceId: "camera", label: "Camera 100-Audio" }])
    const getUserMedia = vi.fn()
    vi.stubGlobal("navigator", { mediaDevices: { enumerateDevices, getUserMedia } })
    const permission = Promise.resolve({ getTracks: () => [{ stop }] } as unknown as MediaStream)
    await expect(listAudioInputDevices(permission)).resolves.toEqual([{ id: "camera", label: "Camera 100-Audio" }])
    expect(getUserMedia).not.toHaveBeenCalled()
    expect(stop).toHaveBeenCalledOnce()
  })
})

describe("speech media ownership", () => {
  it.each((["auto", "wav"] as const).flatMap(format =>
    ["audio/mp4;codecs=mp4a.40.2", "audio/webm; codecs=opus", "audio/ogg;codecs=opus"].map(mime => ({ format, mime }))))(
    "converts the complete $mime recording to interoperable WAV in $format mode", async ({ format, mime }) => {
    vi.stubGlobal("Blob", NodeBlob)
    const trackStop = vi.fn()
    let recorder!: Recorder
    class Recorder {
      static isTypeSupported(type: string) { return type.split(";")[0] === mime.split(";")[0] }
      mimeType = mime
      state = "inactive"
      ondataavailable?: (event: { data: Blob }) => void
      onstop?: () => void
      constructor() { recorder = this }
      start() { this.state = "recording" }
      stop() {
        this.state = "inactive"
        this.ondataavailable?.({ data: new Blob([new Uint8Array([3, 4])], { type: this.mimeType }) })
        this.onstop?.()
      }
    }
    const decode = vi.fn(async (bytes: ArrayBuffer) => {
      expect([...new Uint8Array(bytes)]).toEqual([1, 2, 3, 4])
      return { duration: 4 / 16000, sampleRate: 16000, length: 4, numberOfChannels: 1,
        getChannelData: () => new Float32Array([0.25, -0.25, 0.5, -0.5]) }
    })
    class Context {
      state = "running"
      close = vi.fn(async () => { this.state = "closed" })
      decodeAudioData = decode
    }
    vi.stubGlobal("AudioContext", Context)
    vi.stubGlobal("MediaRecorder", Recorder)
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: async () => ({ getTracks: () => [{ stop: trackStop }] }) } })
    const capture = await startSpeechRecording({ signal: new AbortController().signal, maxDurationSeconds: 120, maxBytes: 1024, format })
    recorder.ondataavailable?.({ data: new Blob([new Uint8Array([1, 2])], { type: recorder.mimeType }) })
    const wav = await capture.stop()
    expect(wav.type).toBe("audio/wav")
    const bytes = await wav.arrayBuffer()
    expect(new TextDecoder().decode(bytes.slice(0, 4))).toBe("RIFF")
    const view = new DataView(bytes)
    expect(view.getUint32(24, true)).toBe(16000)
    expect(view.getUint32(40, true)).toBe(8)
    expect([44, 46, 48, 50].map(offset => view.getInt16(offset, true))).toEqual([8191, -8192, 16383, -16384])
    expect(decode).toHaveBeenCalledOnce()
    expect(trackStop).toHaveBeenCalled()
  })


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

describe("incremental PCM playback", () => {
  function contextFixture() {
    const sources: { start: ReturnType<typeof vi.fn>; stop: ReturnType<typeof vi.fn>; onended?: () => void; buffer?: unknown }[] = []
    const samples: Float32Array[] = []
    class Context {
      state = "running"
      currentTime = 0
      destination = {}
      resume() { return Promise.resolve() }
      close() { this.state = "closed"; return Promise.resolve() }
      createBuffer(_channels: number, length: number, rate: number) {
        const data = new Float32Array(length); samples.push(data)
        return { duration: length / rate, getChannelData: () => data }
      }
      createBufferSource() {
        const source = { connect: vi.fn(), disconnect: vi.fn(), start: vi.fn(), stop: vi.fn(), onended: undefined as (() => void) | undefined }
        sources.push(source)
        return source
      }
    }
    vi.stubGlobal("AudioContext", Context)
    return { sources, samples }
  }

  it("starts audio while synthesis is unfinished, joins odd byte boundaries and drains queued sources", async () => {
    const { sources, samples } = contextFixture()
    let release!: () => void
    const tail = new Promise<void>((resolve) => { release = resolve })
    const cancel = vi.fn()
    const stream: SpeechAudioStream = { sampleRate: 24000, cancel, chunks: (async function* () {
      yield new Uint8Array([0, 128, 255])
      await tail
      yield new Uint8Array([127])
    })() }
    let settled = false
    const playing = speechPlayback.play("answer", "Reply", { chunkChars: 100, synthesize: async () => stream }).then(() => { settled = true })
    await vi.waitFor(() => expect(sources).toHaveLength(1))
    expect(sources[0].start).toHaveBeenCalledOnce()
    expect(samples[0][0]).toBe(-1)
    expect(settled).toBe(false)
    release()
    await vi.waitFor(() => expect(sources).toHaveLength(2))
    expect(samples[1][0]).toBeCloseTo(32767 / 32768)
    expect(sources[1].start.mock.calls[0][0]).toBeGreaterThan(sources[0].start.mock.calls[0][0])
    sources[0].onended?.()
    await Promise.resolve()
    expect(settled).toBe(false)
    sources[1].onended?.()
    await playing
    expect(cancel).toHaveBeenCalled()
    expect(speechPlayback.getSnapshot()).toEqual({ state: "idle" })
  })

  it("cancels every queued source and late old-owner streams cannot stop a new owner", async () => {
    const { sources } = contextFixture()
    let late!: (stream: SpeechAudioStream) => void
    const first = speechPlayback.play("old", "Old reply", { chunkChars: 100, synthesize: () => new Promise((resolve) => { late = resolve }) })
    await vi.waitFor(() => expect(late).toBeDefined())
    const oldCancel = vi.fn()
    const currentCancel = vi.fn()
    const current = speechPlayback.play("new", "New reply", { chunkChars: 100, synthesize: async () => ({ sampleRate: 24000, cancel: currentCancel, chunks: (async function* () { yield new Uint8Array([0, 1]); yield new Uint8Array([0, 2]) })() }) })
    await vi.waitFor(() => expect(sources).toHaveLength(2))
    late({ sampleRate: 24000, cancel: oldCancel, chunks: (async function* () { yield new Uint8Array([0, 0]) })() })
    await first
    expect(oldCancel).toHaveBeenCalledOnce()
    expect(speechPlayback.getSnapshot().key).toBe("new")
    speechPlayback.stop()
    await current
    expect(sources.every((source) => source.stop.mock.calls.length === 1)).toBe(true)
    expect(currentCancel).toHaveBeenCalled()
  })

  it("stops queued PCM when its authority expires after the response has reached EOF", async () => {
    const { sources } = contextFixture()
    const authority = new AbortController()
    const cancel = vi.fn()
    let eof = false
    const stream: SpeechAudioStream & { signal: AbortSignal } = {
      sampleRate: 24000, signal: authority.signal, cancel,
      chunks: (async function* () { yield new Uint8Array(48000); eof = true })(),
    }
    const playing = speechPlayback.play("answer", "Reply", { chunkChars: 100, synthesize: async () => stream })
    try {
      await vi.waitFor(() => expect(eof).toBe(true))
      expect(sources).toHaveLength(4)
      expect(sources.every((source) => source.stop.mock.calls.length === 0)).toBe(true)
      // No account/logout event occurs here: the stream's expiry alone must
      // cancel the still-scheduled tail after network consumption completed.
      authority.abort()
      expect(sources.every((source) => source.stop.mock.calls.length === 1)).toBe(true)
      await playing
      expect(cancel).toHaveBeenCalled()
      expect(speechPlayback.getSnapshot()).toEqual({ state: "idle" })
    } finally { speechPlayback.stop(); await playing }
  })

  it("an expired retired PCM stream cannot stop the replacement playback owner", async () => {
    const { sources } = contextFixture()
    const oldAuthority = new AbortController()
    const oldStream: SpeechAudioStream & { signal: AbortSignal } = {
      sampleRate: 24000, signal: oldAuthority.signal, cancel: vi.fn(),
      chunks: (async function* () { yield new Uint8Array([0, 1]) })(),
    }
    const first = speechPlayback.play("old", "Old reply", { chunkChars: 100, synthesize: async () => oldStream })
    await vi.waitFor(() => expect(sources).toHaveLength(1))
    const current = speechPlayback.play("new", "New reply", { chunkChars: 100, synthesize: async () => ({
      sampleRate: 24000, cancel: vi.fn(), chunks: (async function* () { yield new Uint8Array([0, 2]) })(),
    }) })
    try {
      await vi.waitFor(() => expect(sources).toHaveLength(2))
      await first
      oldAuthority.abort()
      expect(speechPlayback.getSnapshot().key).toBe("new")
      expect(sources[1].stop).not.toHaveBeenCalled()
    } finally { speechPlayback.stop(); await current }
  })

  it("releases completed PCM only after its queued tail and continues with the next segment", async () => {
    const { sources } = contextFixture()
    const signals: AbortSignal[] = []
    const released = vi.fn()
    const synthesize = vi.fn(async (): Promise<SpeechAudioStream> => {
      const authority = new AbortController()
      signals.push(authority.signal)
      return { sampleRate: 24000, signal: authority.signal, cancel: () => { released(); authority.abort() },
        chunks: (async function* () { yield new Uint8Array([0, 1]) })() }
    })
    const playing = speechPlayback.play("answer", "One. Two.", { chunkChars: 5, synthesize })
    try {
      await vi.waitFor(() => expect(sources).toHaveLength(1))
      expect(released).not.toHaveBeenCalled()
      expect(signals[0].aborted).toBe(false)
      sources[0].onended?.()
      await vi.waitFor(() => expect(sources).toHaveLength(2))
      expect(released).toHaveBeenCalledOnce()
      expect(signals[0].aborted).toBe(true)
      expect(signals[1].aborted).toBe(false)
      expect(speechPlayback.getSnapshot().key).toBe("answer")
      sources[1].onended?.()
      await playing
      expect(released).toHaveBeenCalledTimes(2)
      expect(sources.every((source) => source.stop.mock.calls.length === 0)).toBe(true)
    } finally { speechPlayback.stop(); await playing }
  })

  it("preserves a timeout failure while cancelling PCM already queued after EOF", async () => {
    const { sources } = contextFixture()
    const deadline = new AbortController()
    let eof = false
    const playing = speechPlayback.play("answer", "Reply", { chunkChars: 100, synthesize: async () => ({
      sampleRate: 24000, signal: deadline.signal, cancel: vi.fn(),
      chunks: (async function* () { yield new Uint8Array([0, 1]); eof = true })(),
    }) }).catch((error: unknown) => error)
    try {
      await vi.waitFor(() => expect(eof).toBe(true))
      const failure = Object.assign(new Error("timeout"), { code: "timeout" })
      deadline.abort(failure)
      expect(await playing).toBe(failure)
      expect(sources[0].stop).toHaveBeenCalledOnce()
      expect(speechPlayback.getSnapshot()).toEqual({ state: "idle" })
    } finally { speechPlayback.stop(); await playing }
  })

  it("refuses a truncated PCM sample and releases its response", async () => {
    contextFixture()
    const cancel = vi.fn()
    await expect(speechPlayback.play("answer", "Reply", { chunkChars: 100, synthesize: async () => ({ sampleRate: 24000, cancel, chunks: (async function* () { yield new Uint8Array([1]) })() }) })).rejects.toThrow(/incomplete|truncated/i)
    expect(cancel).toHaveBeenCalled()
  })

  it("applies backpressure before scheduling more than one second of PCM", async () => {
    const { sources, samples } = contextFixture()
    let reads = 0
    const playing = speechPlayback.play("answer", "Reply", { chunkChars: 100, synthesize: async () => ({ sampleRate: 24000, cancel: vi.fn(), chunks: (async function* () {
      for (let i = 0; i < 10; i++) { reads++; yield new Uint8Array(12000) }
    })() }) })
    await vi.waitFor(() => expect(sources).toHaveLength(4))
    expect(reads).toBe(5)
    expect(samples.every((sample) => sample.length <= 6000)).toBe(true)
    sources[0].onended?.()
    await vi.waitFor(() => expect(sources).toHaveLength(5))
    speechPlayback.stop()
    await playing
    expect(reads).toBeLessThan(10)
  })

  it("stops already queued sources when a later streamed sample fails", async () => {
    const { sources } = contextFixture()
    const cancel = vi.fn()
    const playing = speechPlayback.play("answer", "Reply", { chunkChars: 100, synthesize: async () => ({ sampleRate: 24000, cancel, chunks: (async function* () {
      yield new Uint8Array([0, 1])
      throw new Error("Stream failed")
    })() }) })
    await expect(playing).rejects.toThrow("Stream failed")
    expect(sources[0].stop).toHaveBeenCalledOnce()
    expect(cancel).toHaveBeenCalled()
    expect(speechPlayback.getSnapshot()).toEqual({ state: "idle" })
  })
})
