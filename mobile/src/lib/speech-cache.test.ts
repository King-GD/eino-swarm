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
  it("retains the input's independent gateway route without credentials or endpoint paths", () => {
    const saved = saveSpeechCache(pc, { ...snapshot, execution: "pc_local", gateway_origin: undefined,
      input: { ...snapshot.input, execution: "gateway", gateway_origin: "https://gateway.test/", api_key: "private-input" } })
    expect(saved?.snapshot.input).toMatchObject({ execution: "gateway", gateway_origin: "https://gateway.test" })
    expect(loadSpeechCache(pc)?.snapshot.input).toEqual(saved?.snapshot.input)
    expect(localStorage.getItem("zwai.phone.speech")).not.toContain("private-input")
    expect(saveSpeechCache(pc, { ...snapshot, input: { ...snapshot.input, execution: "gateway" } })).toBeUndefined()
    expect(saveSpeechCache(pc, { ...snapshot, input: { ...snapshot.input, execution: "pc_local", gateway_origin: "https://gateway.test" } })).toBeUndefined()
  })
  it("retains a configurable Gateway output independently of the legacy input route without copying credentials", () => {
    const raw = { ...snapshot, execution: "pc_local", gateway_origin: undefined,
      output: { ...snapshot.output, execution: "gateway", gateway_origin: "https://gateway.test/", format: "pcm", api_key: "output-secret", token: "output-secret", base_url: "https://private-endpoint.test/v1" } }
    const saved = saveSpeechCache(pc, raw)
    expect(saved?.snapshot.output).toMatchObject({ execution: "gateway", gateway_origin: "https://gateway.test", format: "pcm" })
    expect(loadSpeechCache(pc)?.snapshot.output).toEqual(saved?.snapshot.output)
    const stored = localStorage.getItem("zwai.phone.speech")!
    expect(stored).not.toContain("output-secret")
    expect(stored).not.toContain("private-endpoint")
  })

  it("retains an explicit personal PC output when legacy Gateway input is unavailable", () => {
    const raw = { ...snapshot, gateway_origin: undefined, input: { ...snapshot.input, enabled: false, ready: false },
      output: { ...snapshot.output, execution: "pc_local" } }
    expect(saveSpeechCache(pc, raw)?.snapshot.output).toMatchObject({ execution: "pc_local" })
    expect(saveSpeechCache(pc, { ...raw, input: snapshot.input })).toBeUndefined()
  })

  it.each([
    { execution: "gateway" },
    { execution: "gateway", gateway_origin: "https://gateway.test/path" },
    { execution: "gateway", gateway_origin: "https://user:secret@gateway.test" },
    { execution: "other", gateway_origin: "https://gateway.test" },
    { gateway_origin: "https://gateway.test" },
    { execution: "pc_local", gateway_origin: "https://gateway.test" },
  ])("refuses an incomplete or mixed output authority tuple: %j", (route) => {
    expect(saveSpeechCache(pc, { ...snapshot, execution: "pc_local", gateway_origin: undefined,
      output: { ...snapshot.output, ...route } })).toBeUndefined()
  })

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
