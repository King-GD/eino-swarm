import { afterEach, describe, expect, it, vi } from "vitest"
import { synthesizeSpeech } from "./speech-api"

afterEach(() => vi.unstubAllGlobals())

describe("speech HTTP transport", () => {
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
