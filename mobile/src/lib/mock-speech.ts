import type { SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { defaultSpeechSettings } from "../../../frontend/src/lib/speech-types"
import { scanChimeSrc } from "./scan-chime"
import { type RemoteRequest, type RemoteResponse } from "./rpc"

type Request = Omit<RemoteRequest, "v" | "id">
type Fields = Partial<RemoteResponse>
type Upload = { mime: string; parts: number; bytes: Map<number, Uint8Array> }

/** The offline provider exercises the same private audio protocol as a PC.
 * No fixture audio can become a conversation attachment or workspace file. */
export class MockSpeech {
  private uploads = new Map<string, Upload>()
  private results = new Map<string, Uint8Array>()
  private cancelled = new Set<string>()
  private closed = false
  accepts(req: Request) { return req.op === "speech_config" || req.op.startsWith("audio_") || req.op === "put" && req.purpose === "audio" }
  close() { this.closed = true; this.uploads.clear(); this.results.clear() }

  async handle(req: Request): Promise<Fields> {
    const params = new URLSearchParams(location.search)
    const execution = params.get("speech")
    if (req.op === "speech_config") {
      if (execution !== "local" && execution !== "gateway" && execution !== "off") return { ok: false, code: "unknown_op" }
      const settings = defaultSpeechSettings()
      const enabled = execution !== "off"
      const speech: SpeechSnapshot = {
        execution: execution === "gateway" ? "gateway" : "pc_local",
        ...(execution === "gateway" ? { gateway_origin: location.origin } : {}),
        input: { ...settings.input, enabled, ready: enabled, model: "transcription", label: "transcription" },
        output: { ...settings.output, enabled, ready: enabled, model: "speech", label: "speech", format: "wav" },
        limits: settings.limits,
      }
      return { speech }
    }
    const id = req.audio_id || req.put_id || ""
    if (req.op === "audio_cancel") {
      this.cancelled.add(id); this.uploads.delete(id); this.results.delete(id)
      return { audio: { audio_id: id } }
    }
    if (execution !== "local") return { ok: false, code: "audio_unavailable" }
    if (req.op === "put") {
      let upload = this.uploads.get(id)
      if (!upload) { upload = { mime: req.mime || "", parts: req.parts || 0, bytes: new Map() }; this.uploads.set(id, upload) }
      upload.bytes.set(req.part || 0, Uint8Array.from(atob(req.data || ""), (char) => char.charCodeAt(0)))
      return { put: { id, ready: upload.parts > 0 && upload.bytes.size === upload.parts } }
    }
    if (req.op === "audio_transcribe" || req.op === "audio_speech") {
      await new Promise((resolve) => setTimeout(resolve, Number(params.get("speech_delay")) || 60))
      if (this.closed || this.cancelled.has(id)) return { ok: false, code: "audio_cancelled" }
      if (req.op === "audio_transcribe") {
        const upload = this.uploads.get(id)
        if (!upload || upload.parts !== upload.bytes.size) return { ok: false, code: "audio_invalid" }
        const blob = new Blob([...upload.bytes.entries()].sort(([a], [b]) => a - b).map(([, bytes]) => new Uint8Array(bytes).buffer), { type: upload.mime })
        // A real browser-recorded fixture must actually decode, rather than
        // accepting a filename or a few magic bytes as proof of audio.
        const context = new AudioContext()
        try { const decoded = await context.decodeAudioData(await blob.arrayBuffer()); if (!decoded.duration || !decoded.numberOfChannels) return { ok: false, code: "audio_invalid" } }
        catch { return { ok: false, code: "audio_invalid" } }
        finally { await context.close() }
        this.uploads.delete(id)
        return { audio: { audio_id: id, text: params.get("dictated") || "Voice draft" } }
      }
      const original = Uint8Array.from(atob(scanChimeSrc().split(",")[1]), (char) => char.charCodeAt(0))
      const bytes = new Uint8Array(44 + (original.length - 44) * 12)
      bytes.set(original.subarray(0, 44))
      for (let i = 0; i < 12; i++) bytes.set(original.subarray(44), 44 + i * (original.length - 44))
      const header = new DataView(bytes.buffer)
      header.setUint32(4, bytes.length - 8, true); header.setUint32(40, bytes.length - 44, true)
      this.results.set(id, bytes)
      return { audio: { audio_id: id, mime: "audio/wav", size: bytes.length } }
    }
    const bytes = this.results.get(id)
    const offset = req.offset || 0
    if (!bytes || offset < 0 || offset >= bytes.length) return { ok: false, code: "audio_invalid" }
    const part = bytes.slice(offset, offset + 36 * 1024)
    let data = ""
    for (const byte of part) data += String.fromCharCode(byte)
    return { audio: { audio_id: id, mime: "audio/wav", size: bytes.length, offset, next: offset + part.length, more: offset + part.length < bytes.length, data: btoa(data) } }
  }
}
