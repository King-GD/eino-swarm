import { expect, it } from "vitest"
import { acceptPhoneSpeechConfig, setPhoneSpeechSource, transcribePhoneSpeech } from "./phone-speech"
import type { RemoteLink } from "./link"
import type { SavedLink } from "./store"
import { defaultSpeechSettings } from "../../../frontend/src/lib/speech-types"

it.each(["language English<asr_text>Recognized prose.", "language None<asr_text>"])(
  "projects PC ASR output into plain draft text: %s", async raw => {
    const pc = { fingerprint: "transcript-pc", hostPub: "key", hubURL: "https://hub.test", ticket: "fixture", sessionID: "fixture" } satisfies SavedLink
    const link = { alive: () => true, rpc: async request => request.op === "audio_transcribe"
      ? { v: 1, id: "r", ok: true, audio: { audio_id: request.audio_id!, text: raw } }
      : { v: 1, id: "r", ok: true } } as RemoteLink
    setPhoneSpeechSource(pc, link)
    const defaults = defaultSpeechSettings()
    acceptPhoneSpeechConfig(pc, { ...defaults, execution: "pc_local", input: { ...defaults.input, enabled: true, model: "asr", ready: true, label: "ASR" },
      output: { ...defaults.output, ready: false, label: "" } })
    const blob = Object.assign(new Blob([new Uint8Array([1, 2])], { type: "audio/wav" }), { arrayBuffer: async () => new Uint8Array([1, 2]).buffer })
    try {
      const result = transcribePhoneSpeech(blob, new AbortController().signal)
      if (raw.includes("None")) await expect(result).rejects.toMatchObject({ code: "empty" })
      else await expect(result).resolves.toBe("Recognized prose.")
    } finally { setPhoneSpeechSource(undefined, null) }
  },
)
