import { afterEach, describe, expect, it, vi } from "vitest"
import { synthesizeSpeech, transcribeSpeech } from "./speech-api"

afterEach(() => vi.unstubAllGlobals())

describe("speech HTTP transport", () => {
  it.each(["language English<asr_text>Recognized prose.", "language None<asr_text>"])("does not expose decoder metadata from ASR HTTP: %s", async text => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ text }), { headers: { "Content-Type": "application/json" } })))
    const result = transcribeSpeech(new Blob([new Uint8Array([1])], { type: "audio/wav" }), new AbortController().signal)
    if (text.includes("None")) await expect(result).rejects.toThrow("No speech was recognized")
    else await expect(result).resolves.toBe("Recognized prose.")
  })

  it("returns a PCM stream before the speech service finishes generating", async () => {
    let body!: ReadableStreamDefaultController<Uint8Array>
    const response = new Response(new ReadableStream<Uint8Array>({ start(controller) { body = controller } }), {
      headers: { "Content-Type": "audio/pcm" },
    })
    const fetcher = vi.fn(async () => response)
    vi.stubGlobal("fetch", fetcher)
    const owner = new AbortController()
    let received = false
    const pending = synthesizeSpeech("Read this text", owner.signal, 1024).then((audio) => { received = true; return audio })
    body.enqueue(new Uint8Array([0, 1, 2, 3]))
    try {
      // An open upstream must not keep the player waiting for a complete Blob.
      await vi.waitFor(() => expect(received).toBe(true), { timeout: 200 })
      const audio = await pending
      expect(audio).toHaveProperty("chunks")
      expect(fetcher.mock.calls[0]).toEqual(["/api/audio/speech", expect.objectContaining({ signal: owner.signal, redirect: "error" })])
    } finally {
      owner.abort()
      await pending.catch(() => undefined)
    }
  })
})
