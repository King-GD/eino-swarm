import { speechSegments, speechText } from "./speech-text"

let audioOwner: (() => void) | undefined
function claimAudio(cancel: () => void) { audioOwner?.(); audioOwner = cancel }
function releaseAudio(cancel: () => void) { if (audioOwner === cancel) audioOwner = undefined }
const aborted = () => new DOMException("Speech operation cancelled", "AbortError")

export function speechRecordingSupported(): boolean {
  return typeof globalThis.navigator?.mediaDevices?.getUserMedia === "function" && typeof globalThis.MediaRecorder === "function"
}

export interface SpeechRecording {
  stop(): Promise<Blob>
  cancel(): void
}

export interface SpeechRecordingOptions {
  signal: AbortSignal
  maxDurationSeconds: number
  maxBytes: number
  format: "auto" | "wav"
  onLevel?: (level: number) => void
  onDuration?: (seconds: number) => void
  onLimit?: () => void
  onError?: (error: unknown) => void
  onCancel?: () => void
}

/** The recording owns all acquired tracks, including a late permission grant. */
export async function startSpeechRecording(options: SpeechRecordingOptions): Promise<SpeechRecording> {
  if (!speechRecordingSupported()) throw new Error("Microphone recording is not available in this browser.")
  if (options.signal.aborted) throw aborted()
  let stream: MediaStream | undefined
  let recorder: MediaRecorder | undefined
  let meter: AudioContext | undefined
  let analyser: AnalyserNode | undefined
  let timer: ReturnType<typeof setInterval> | undefined
  let cancelled = false
  let finished = false
  let stopping = false
  let failure: unknown
  let bytes = 0
  const chunks: Blob[] = []
  let resolve!: (blob: Blob) => void
  let reject!: (error: unknown) => void
  const result = new Promise<Blob>((yes, no) => { resolve = yes; reject = no })
  // A recording can be cancelled without ever calling stop().
  void result.catch(() => undefined)
  const cleanup = () => {
    if (timer) clearInterval(timer)
    stream?.getTracks().forEach((track) => track.stop())
    if (meter && meter.state !== "closed") void meter.close().catch(() => undefined)
    options.signal.removeEventListener("abort", cancel)
    releaseAudio(cancel)
  }
  const cancel = () => {
    if (cancelled) return
    cancelled = true
    if (recorder?.state === "recording") recorder.stop()
    cleanup()
    reject(aborted())
    options.onCancel?.()
  }
  const stop = (): Promise<Blob> => {
    if (cancelled) return Promise.reject(aborted())
    if (!stopping && !finished) {
      stopping = true
      if (recorder?.state === "recording") recorder.stop()
      // The recorder's last dataavailable/stop events retain the final bytes.
      stream?.getTracks().forEach((track) => track.stop())
    }
    return result
  }
  claimAudio(cancel)
  options.signal.addEventListener("abort", cancel, { once: true })
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false })
    if (cancelled || options.signal.aborted) throw aborted()
    const mime = ["audio/webm;codecs=opus", "audio/mp4", "audio/webm", "audio/ogg;codecs=opus"]
      .find((type) => typeof MediaRecorder.isTypeSupported === "function" && MediaRecorder.isTypeSupported(type))
    recorder = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined)
    recorder.ondataavailable = (event) => {
      if (cancelled || !event.data.size) return
      bytes += event.data.size
      if (bytes > options.maxBytes) {
        failure = new Error("The recording exceeds the configured audio limit.")
        options.onError?.(failure)
        void stop().catch(() => undefined)
      } else chunks.push(event.data)
    }
    recorder.onerror = () => {
      failure = new Error("Microphone recording failed.")
      options.onError?.(failure)
      if (recorder?.state === "recording") recorder.stop()
    }
    recorder.onstop = () => {
      finished = true
      const type = recorder?.mimeType || chunks[0]?.type || "application/octet-stream"
      cleanup()
      if (cancelled) { reject(aborted()); return }
      if (failure) { reject(failure); return }
      const blob = new Blob(chunks, { type })
      if (!blob.size) { reject(new Error("The recording contains no audio.")); return }
      if (options.format === "wav") {
        void recordingWAV(blob).then((wav) => {
          if (cancelled || options.signal.aborted) reject(aborted())
          else if (wav.size > options.maxBytes) reject(new Error("The WAV recording exceeds the configured audio limit."))
          else resolve(wav)
        }, reject)
      } else resolve(blob)
    }
    if (globalThis.AudioContext) {
      try {
        meter = new AudioContext()
        analyser = meter.createAnalyser()
        analyser.fftSize = 256
        meter.createMediaStreamSource(stream).connect(analyser)
        void meter.resume().catch(() => undefined)
      } catch { /* Metering is optional; the recorder still owns the tracks. */ }
    }
    const samples = new Uint8Array(256)
    const began = performance.now()
    timer = setInterval(() => {
      const elapsed = (performance.now() - began) / 1000
      options.onDuration?.(elapsed)
      if (analyser) {
        analyser.getByteTimeDomainData(samples)
        const power = samples.reduce((sum, value) => sum + ((value - 128) / 128) ** 2, 0) / samples.length
        options.onLevel?.(Math.min(1, Math.sqrt(power) * 3))
      }
      if (!stopping && elapsed >= options.maxDurationSeconds) {
        stopping = true
        options.onLimit?.()
        if (recorder?.state === "recording") recorder.stop()
        stream?.getTracks().forEach((track) => track.stop())
      }
    }, 100)
    recorder.start(500)
    return { stop, cancel }
  } catch (error) {
    cleanup()
    reject(error)
    throw error
  }
}

/** Convert an actual captured container to mono PCM WAV for local ASR. */
async function recordingWAV(blob: Blob): Promise<Blob> {
  const context = new AudioContext()
  try {
    const buffer = await context.decodeAudioData(await blob.arrayBuffer())
    const sampleRate = 16000
    const length = Math.ceil(buffer.duration * sampleRate)
    const data = new ArrayBuffer(44 + length * 2)
    const view = new DataView(data)
    const word = (offset: number, text: string) => [...text].forEach((char, i) => view.setUint8(offset + i, char.charCodeAt(0)))
    word(0, "RIFF"); view.setUint32(4, data.byteLength - 8, true); word(8, "WAVE")
    word(12, "fmt "); view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true)
    view.setUint32(24, sampleRate, true); view.setUint32(28, sampleRate * 2, true); view.setUint16(32, 2, true); view.setUint16(34, 16, true)
    word(36, "data"); view.setUint32(40, length * 2, true)
    const channels = Array.from({ length: buffer.numberOfChannels }, (_, channel) => buffer.getChannelData(channel))
    for (let i = 0; i < length; i++) {
      const position = Math.min(buffer.length - 1, i * buffer.sampleRate / sampleRate)
      const left = Math.floor(position)
      const fraction = position - left
      const sample = channels.reduce((sum, channel) => sum + channel[left] * (1 - fraction) + channel[Math.min(left + 1, buffer.length - 1)] * fraction, 0) / channels.length
      view.setInt16(44 + i * 2, Math.max(-1, Math.min(1, sample)) * (sample < 0 ? 32768 : 32767), true)
    }
    return new Blob([data], { type: "audio/wav" })
  } finally { await context.close().catch(() => undefined) }
}

export interface SpeechPlaybackSnapshot { key?: string; state: "idle" | "loading" | "playing" }
export interface SpeechPlaybackOptions { chunkChars: number; synthesize: (text: string, signal: AbortSignal) => Promise<Blob> }

class SpeechPlayback {
  private current?: { key: string; controller: AbortController; context: AudioContext; source?: AudioBufferSourceNode; ended?: () => void; cancel: () => void }
  private snapshot: SpeechPlaybackSnapshot = { state: "idle" }
  private listeners = new Set<() => void>()
  getSnapshot = () => this.snapshot
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  private publish(snapshot: SpeechPlaybackSnapshot) { this.snapshot = snapshot; for (const listener of this.listeners) listener() }
  stop = () => {
    const current = this.current
    if (!current) return
    this.current = undefined
    current.controller.abort()
    if (current.source) { try { current.source.stop() } catch { /* Already ended. */ } current.source.disconnect() }
    current.ended?.()
    if (current.context.state !== "closed") void current.context.close().catch(() => undefined)
    releaseAudio(current.cancel)
    this.publish({ state: "idle" })
  }

  /** Resume audio synchronously from the click, before waiting for synthesis. */
  play = async (key: string, markdown: string, options: SpeechPlaybackOptions): Promise<void> => {
    this.stop()
    const parts = speechSegments(speechText(markdown), options.chunkChars)
    if (!parts.length) return
    if (!globalThis.AudioContext) throw new Error("Audio playback is not available in this browser.")
    const context = new AudioContext()
    const resume = context.resume()
    const current = { key, controller: new AbortController(), context, cancel: this.stop } as NonNullable<SpeechPlayback["current"]>
    claimAudio(current.cancel)
    this.current = current
    this.publish({ key, state: "loading" })
    const signal = current.controller.signal
    try {
      await resume
      for (const part of parts) {
        if (signal.aborted) return
        this.publish({ key, state: "loading" })
        const blob = await options.synthesize(part, signal)
        if (signal.aborted) return
        if (!blob.size || !/^audio\//i.test(blob.type)) throw new Error("The speech service did not return playable audio.")
        const audio = await context.decodeAudioData(await blob.arrayBuffer())
        if (signal.aborted) return
        if (!audio.duration) throw new Error("The speech service returned empty audio.")
        const source = context.createBufferSource()
        current.source = source
        source.buffer = audio
        source.connect(context.destination)
        this.publish({ key, state: "playing" })
        await new Promise<void>((resolve) => {
          current.ended = resolve
          source.onended = () => resolve()
          source.start()
        })
        source.disconnect()
        current.source = undefined
        current.ended = undefined
      }
    } catch (error) {
      if (!signal.aborted) throw error
    } finally {
      if (this.current === current) this.stop()
    }
  }
}

/** One playback owner per renderer, shared by direct and PC conversations. */
export const speechPlayback = new SpeechPlayback()
