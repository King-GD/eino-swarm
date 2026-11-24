import { speechSegments, speechText } from "./speech-text"
import type { SpeechAudio, SpeechAudioStream } from "./speech-stream"

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

export interface AudioInputDevice {
  id: string
  label: string
}

const syntheticInputs = new Set(["default", "communications"])

/** Real inputs only. The browser's default alias stays the separate system choice. */
export function audioInputChoices(devices: Array<{ kind: string; deviceId: string; label: string }>): AudioInputDevice[] {
  const seen = new Set<string>()
  const choices: AudioInputDevice[] = []
  for (const device of devices) {
    const id = device.deviceId.trim()
    if (device.kind !== "audioinput" || !id || syntheticInputs.has(id) || seen.has(id) || id.length > 256) continue
    seen.add(id)
    choices.push({ id, label: device.label.trim() })
  }
  return choices
}

const releaseStream = (stream?: MediaStream) => stream?.getTracks().forEach((track) => track.stop())

/** Passive enumeration can return hidden device IDs. A caller may pass a
 * capture started by a user gesture to reveal names while the stream is live. */
export async function listAudioInputDevices(permission?: Promise<MediaStream>): Promise<AudioInputDevice[]> {
  const media = globalThis.navigator?.mediaDevices
  const stream = await permission?.catch(() => undefined)
  try {
    return media?.enumerateDevices ? audioInputChoices(await media.enumerateDevices()) : []
  } finally {
    releaseStream(stream)
  }
}

export interface SpeechRecordingOptions {
  signal: AbortSignal
  maxDurationSeconds: number
  maxBytes: number
  format: "auto" | "wav"
  deviceId?: string
  onLevel?: (level: number) => void
  onDuration?: (seconds: number) => void
  onLimit?: () => void
  onError?: (error: unknown) => void
  onCancel?: () => void
}

function isMissingInput(error: unknown): boolean {
  if (!(error instanceof DOMException) && !(error instanceof Error)) return false
  if (error.name === "NotFoundError") return true
  // Browsers can withhold the name of the failed constraint before permission.
  return error.name === "OverconstrainedError" &&
    (!("constraint" in error) || !error.constraint || error.constraint === "deviceId")
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
    const deviceId = options.deviceId?.trim()
    const media = navigator.mediaDevices
    try {
      stream = await media.getUserMedia({ audio: deviceId ? { deviceId: { exact: deviceId } } : true, video: false })
    } catch (error) {
      if (cancelled || options.signal.aborted) throw aborted()
      if (!deviceId || !isMissingInput(error)) throw error
      stream = await media.getUserMedia({ audio: true, video: false })
    }
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
      // Browsers also record WebM/Opus (including newer WebKit). ASR services
      // need an interoperable container regardless of the recorder's choice.
      if (options.format === "wav" || /^(audio|video)\/(?:mp4|webm|ogg)(?:;|$)/i.test(type)) {
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

/** Convert an actual captured container to mono PCM WAV for ASR. */
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
export interface SpeechPlaybackOptions { chunkChars: number; synthesize: (text: string, signal: AbortSignal) => Promise<SpeechAudio> }

interface QueuedSource { source: AudioBufferSourceNode; ended: Promise<void>; finish(): void }
interface PlaybackOwner {
  key: string; controller: AbortController; context: AudioContext; sources: Set<QueuedSource>; cancel(): void; audio?: SpeechAudioStream
}

class SpeechPlayback {
  private current?: PlaybackOwner
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
    current.audio?.cancel()
    for (const queued of current.sources) {
      try { queued.source.stop() } catch { /* Already ended. */ }
      queued.source.disconnect()
      queued.finish()
    }
    if (current.context.state !== "closed") void current.context.close().catch(() => undefined)
    releaseAudio(current.cancel)
    this.publish({ state: "idle" })
  }

  private queue(current: PlaybackOwner, buffer: AudioBuffer, when?: number): QueuedSource {
    const source = current.context.createBufferSource()
    source.buffer = buffer
    source.connect(current.context.destination)
    let resolve!: () => void
    const ended = new Promise<void>((yes) => { resolve = yes })
    const queued = { source, ended, finish: () => { current.sources.delete(queued); resolve() } }
    current.sources.add(queued)
    source.onended = () => { source.disconnect(); queued.finish() }
    this.publish({ key: current.key, state: "playing" })
    source.start(when)
    return queued
  }

  private async playPCM(current: PlaybackOwner, audio: SpeechAudioStream): Promise<void> {
    current.audio = audio
    const cancelled = () => {
      if (this.current !== current) return
      current.controller.abort(audio.signal?.reason)
      this.stop()
    }
    audio.signal?.addEventListener("abort", cancelled, { once: true })
    let leftover: number | undefined
    let count = 0
    let nextTime = current.context.currentTime
    try {
      if (audio.signal?.aborted) { cancelled(); return }
      for await (const chunk of audio.chunks) {
        if (current.controller.signal.aborted) return
        let bytes = chunk
        if (leftover !== undefined) { bytes = new Uint8Array(chunk.length + 1); bytes[0] = leftover; bytes.set(chunk, 1); leftover = undefined }
        if (bytes.length % 2) leftover = bytes[bytes.length - 1]
        const samples = Math.floor(bytes.length / 2)
        for (let offset = 0; offset < samples;) {
          // Backpressure bounds the scheduled queue even for one large read.
          while (current.sources.size >= 4) {
            await current.sources.values().next().value!.ended
            if (current.controller.signal.aborted) return
          }
          const length = Math.min(samples - offset, audio.sampleRate / 4)
          const buffer = current.context.createBuffer(1, length, audio.sampleRate)
          const channel = buffer.getChannelData(0)
          const view = new DataView(bytes.buffer, bytes.byteOffset + offset * 2, length * 2)
          for (let i = 0; i < length; i++) channel[i] = view.getInt16(i * 2, true) / 32768
          nextTime = Math.max(nextTime, current.context.currentTime)
          this.queue(current, buffer, nextTime)
          nextTime += buffer.duration
          count += length; offset += length
        }
      }
      if (current.controller.signal.aborted) return
      if (leftover !== undefined) throw new Error("The speech service returned an incomplete PCM sample.")
      if (!count) throw new Error("The speech service returned empty audio.")
      await Promise.all([...current.sources].map((queued) => queued.ended))
    } finally {
      audio.signal?.removeEventListener("abort", cancelled)
      audio.cancel()
      if (current.audio === audio) current.audio = undefined
      const cause = current.controller.signal.reason
      if (current.controller.signal.aborted && cause instanceof Error && cause.name !== "AbortError") throw cause
    }
  }

  /** Resume audio synchronously from the click, before waiting for synthesis. */
  play = async (key: string, markdown: string, options: SpeechPlaybackOptions): Promise<void> => {
    this.stop()
    const parts = speechSegments(speechText(markdown), options.chunkChars)
    if (!parts.length) return
    if (!globalThis.AudioContext) throw new Error("Audio playback is not available in this browser.")
    const context = new AudioContext()
    const resume = context.resume()
    const current: PlaybackOwner = { key, controller: new AbortController(), context, sources: new Set(), cancel: this.stop }
    claimAudio(current.cancel)
    this.current = current
    this.publish({ key, state: "loading" })
    const signal = current.controller.signal
    try {
      await resume
      for (const part of parts) {
        if (signal.aborted) return
        this.publish({ key, state: "loading" })
        const result = await options.synthesize(part, signal)
        if (signal.aborted) { if ("chunks" in result) result.cancel(); return }
        if ("chunks" in result) { await this.playPCM(current, result); continue }
        const blob = result
        if (!blob.size || !/^audio\//i.test(blob.type)) throw new Error("The speech service did not return playable audio.")
        if (blob.type.split(";")[0].toLowerCase() === "audio/pcm") {
          await this.playPCM(current, { sampleRate: 24000, cancel: () => undefined, chunks: (async function* () { yield new Uint8Array(await blob.arrayBuffer()) })() })
          continue
        }
        if (blob.type.split(";")[0].toLowerCase() === "audio/l16") throw new Error("The speech service returned an unsupported audio format.")
        const audio = await context.decodeAudioData(await blob.arrayBuffer())
        if (signal.aborted) return
        if (!audio.duration) throw new Error("The speech service returned empty audio.")
        await this.queue(current, audio).ended
      }
    } catch (error) {
      if (!signal.aborted) throw error
      if (signal.reason instanceof Error && signal.reason.name !== "AbortError") throw signal.reason
    } finally {
      if (this.current === current) this.stop()
    }
  }
}

/** One playback owner per renderer, shared by direct and PC conversations. */
export const speechPlayback = new SpeechPlayback()
