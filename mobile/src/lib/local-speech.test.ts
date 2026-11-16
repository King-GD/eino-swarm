import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { defaultSpeechSettings } from "../../../frontend/src/lib/speech-types"
import { loadLocalSpeech, localPhoneSpeechCall, localSpeechSnapshot, saveLocalSpeech } from "./local-speech"

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem("zwai.phone.mode", "local")
  localStorage.setItem("zwai.phone.providers", JSON.stringify([{ id: "own", baseURL: "https://own.invalid/v1", apiKey: "own-key", model: "chat" }]))
})
afterEach(() => vi.unstubAllGlobals())

it("keeps phone voice preferences separate from retained gateway credentials", async () => {
  const settings = defaultSpeechSettings()
  settings.input = { ...settings.input, enabled: true, provider_id: "own", model: "recognition", api_key: "must-not-save", base_url: "https://must-not-save.invalid" }
  saveLocalSpeech(settings)
  expect(localStorage.getItem("zwai.phone.localSpeech")).not.toContain("must-not-save")
  expect(localSpeechSnapshot().input).toMatchObject({ ready: true, model: "recognition" })
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ text: "recognized" }), { headers: { "Content-Type": "application/json" } }))
  vi.stubGlobal("fetch", fetcher)
  await expect(localPhoneSpeechCall("transcribe", new Blob(["recording"], { type: "audio/wav" }), new AbortController().signal)).resolves.toBe("recognized")
  expect(fetcher).toHaveBeenCalledWith("https://own.invalid/v1/audio/transcriptions", expect.objectContaining({ headers: { Authorization: "Bearer own-key" } }))
  localStorage.setItem("zwai.phone.mode", "account")
  expect(() => saveLocalSpeech(settings)).toThrow("unavailable")
  await expect(localPhoneSpeechCall("transcribe", new Blob(), new AbortController().signal)).rejects.toThrow("unavailable")
  expect(fetcher).toHaveBeenCalledTimes(1)
})

it("refuses damaged, gateway, or deleted provider references", async () => {
  localStorage.setItem("zwai.phone.localSpeech", "broken")
  expect(loadLocalSpeech().input.enabled).toBe(false)
  localStorage.setItem("zwai.phone.localSpeech", JSON.stringify({ input: { enabled: true, provider_id: "gateway:retained", model: "hidden" }, output: { enabled: true, provider_id: "deleted", model: "voice" } }))
  expect(loadLocalSpeech().input.provider_id).toBe("")
  expect(localSpeechSnapshot().output.ready).toBe(false)
  await expect(localPhoneSpeechCall("speak", "text", new AbortController().signal)).rejects.toThrow("unavailable")
})
