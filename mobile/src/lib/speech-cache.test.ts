import { describe, expect, it } from "vitest"
import { loadSpeechCache, saveSpeechCache, speechBinding, pruneSpeechCaches } from "./speech-cache"
import type { SavedLink } from "./store"

const pc: SavedLink = { fingerprint: "pc-a", hostPub: "host-key", hubURL: "https://hub.test", ticket: "private-ticket", sessionID: "session-a", gatewayOrigin: "https://gateway.test", gatewayOwnerID: "owner" }
const snapshot = {
  execution: "gateway", gateway_origin: "https://gateway.test",
  input: { enabled: true, ready: true, model: "transcription", label: "ASR", language: "", prompt: "", recording_format: "auto", max_duration_seconds: 120, timeout_seconds: 120 },
  output: { enabled: true, ready: true, model: "speech", label: "TTS", voice: "", speed: 1, format: "mp3", instructions: "", timeout_seconds: 120 },
  limits: { input_bytes: 25 * 1024 * 1024, output_bytes: 32 * 1024 * 1024, chunk_chars: 1000, staging_ttl_seconds: 300 },
}

describe("PC-owned phone speech configuration", () => {
  it("retains the PC's streaming PCM choice for the exact pairing", () => {
    expect(saveSpeechCache(pc, { ...snapshot, output: { ...snapshot.output, format: "pcm" } })?.snapshot.output.format).toBe("pcm")
    expect(loadSpeechCache(pc)?.snapshot.output.format).toBe("pcm")
  })
  it("retains safe gateway settings for this exact pairing when the PC is offline", () => {
    saveSpeechCache(pc, { ...snapshot, api_key: "must-not-copy", input: { ...snapshot.input, token: "must-not-copy" } }, 123)
    const stored = loadSpeechCache(pc)
    expect(stored?.snapshot).toEqual(snapshot)
    expect(stored?.syncedAt).toBe(123)
    expect(localStorage.getItem("zwai.phone.speech")).not.toContain("must-not-copy")
    expect(localStorage.getItem("zwai.phone.speech")).not.toContain("private-ticket")
    expect(localStorage.getItem("zwai.phone.speech")).not.toContain("host-key")
  })

  it("never borrows another PC, a replacement pairing or another account's cache", () => {
    saveSpeechCache(pc, snapshot, 123)
    for (const change of [{ fingerprint: "pc-b" }, { ticket: "replacement" }, { gatewayOwnerID: "different-owner" }, { gatewayOrigin: "https://other.test" }]) {
      expect(loadSpeechCache({ ...pc, ...change })).toBeUndefined()
      expect(speechBinding({ ...pc, ...change })).not.toBe(speechBinding(pc))
    }
  })

  it("deletes cached configuration when its PC is unlinked, and rejects malformed snapshots", () => {
    saveSpeechCache(pc, snapshot, 123)
    pruneSpeechCaches([])
    expect(loadSpeechCache(pc)).toBeUndefined()
    expect(saveSpeechCache(pc, { ...snapshot, gateway_origin: "https://gateway.test/steal" }, 123)).toBeUndefined()
    expect(saveSpeechCache(pc, { ...snapshot, limits: { ...snapshot.limits, input_bytes: -1 } }, 123)).toBeUndefined()
  })

  it("accepts the PC's full instruction budget and enforces UTF-8 field limits", () => {
    const prompt = "中".repeat(2730)
    expect(saveSpeechCache(pc, { ...snapshot, input: { ...snapshot.input, prompt } })?.snapshot.input.prompt).toBe(prompt)
    expect(saveSpeechCache(pc, { ...snapshot, input: { ...snapshot.input, prompt: prompt + "中" } })).toBeUndefined()
    expect(saveSpeechCache(pc, { ...snapshot, output: { ...snapshot.output, voice: "中".repeat(86) } })).toBeUndefined()
    expect(saveSpeechCache(pc, { ...snapshot, limits: { ...snapshot.limits, staging_ttl_seconds: 3601 } })).toBeUndefined()
  })
})
