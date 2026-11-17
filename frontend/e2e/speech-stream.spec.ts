import { createServer } from "node:http"
import { expect, test } from "@playwright/test"
import { freshConversation, send, waitForIdle } from "./session"

for (const wire of ["pcm", "sse"] as const) {
  test(`${wire} speech starts before generation ends and releases its request`, async ({ page, request }) => {
    const previous = (await (await request.get("/api/settings")).json()).settings.speech
    let release!: () => void
    const finish = new Promise<void>((resolve) => { release = resolve })
    let started = false
    let ended = false
    let disconnected = false
    const upstream = createServer((req, res) => {
      void (async () => {
        const parts: Buffer[] = []
        for await (const part of req) parts.push(Buffer.from(part))
        const body = JSON.parse(Buffer.concat(parts).toString())
        expect(req.url).toBe("/v1/audio/speech")
        expect(body).toMatchObject({ response_format: "pcm", stream_format: "audio" })
        res.on("close", () => { disconnected = true })
        const audio = Buffer.alloc(4800)
        for (let i = 0; i < audio.length / 2; i++) audio.writeInt16LE(Math.round(Math.sin(i / 10) * 1000), i * 2)
        res.writeHead(200, { "Content-Type": wire === "pcm" ? "application/octet-stream" : "text/event-stream" })
        res.write(wire === "pcm" ? audio : `data: ${JSON.stringify({ type: "speech.audio.delta", audio: audio.toString("base64") })}\n\n`)
        started = true
        await finish
        ended = true
        if (wire === "sse") res.write('data: {"type":"speech.audio.done"}\n\n')
        res.end()
      })().catch(() => { res.destroy() })
    })
    await new Promise<void>((resolve) => upstream.listen(0, "127.0.0.1", resolve))
    const address = upstream.address()
    if (!address || typeof address === "string") throw new Error("Speech fixture did not listen")
    let primaryFailure: unknown
    try {
      expect((await request.put("/api/settings", { data: { speech: {
        output: { enabled: true, source: "custom", base_url: `http://127.0.0.1:${address.port}/v1`, model: "streaming-voice", format: "pcm" },
      } } })).ok()).toBe(true)
      await page.addInitScript(() => {
        const original = AudioBufferSourceNode.prototype.start
        AudioBufferSourceNode.prototype.start = function (...args) {
          const target = window as typeof window & { speechSamples?: Array<{ rate: number; samples: number; peak: number }> }
          const samples = this.buffer?.getChannelData(0)
          ;(target.speechSamples ??= []).push({ rate: this.buffer?.sampleRate ?? 0, samples: samples?.length ?? 0,
            peak: samples ? Math.max(...samples.map(Math.abs)) : 0 })
          original.apply(this, args)
        }
      })
      await freshConversation(page)
      await send(page, "Describe the current task")
      await waitForIdle(page)
      const answer = page.getByTestId("assistant-message").filter({ has: page.getByLabel("Read aloud", { exact: true }) }).last()
      await answer.hover()
      await answer.getByLabel("Read aloud", { exact: true }).click()
      await expect.poll(() => started).toBe(true)
      await expect.poll(() => page.evaluate(() => (window as typeof window & { speechSamples?: unknown[] }).speechSamples?.length ?? 0)).toBeGreaterThan(0)
      const audio = await page.evaluate(() => (window as typeof window & { speechSamples?: Array<{ rate: number; samples: number; peak: number }> }).speechSamples![0])
      expect(audio.rate).toBe(24000)
      expect(audio.samples).toBeGreaterThan(0)
      expect(audio.peak).toBeCloseTo(1000 / 32768, 3)
      expect(ended).toBe(false)
      if (wire === "pcm") {
        // The answer's Read-aloud filter changes while its label becomes Stop.
        await page.getByLabel("Stop reading", { exact: true }).click()
        await expect.poll(() => disconnected).toBe(true)
        expect(ended).toBe(false)
      } else release()
      await expect(answer.getByLabel("Read aloud", { exact: true })).toBeVisible()
    } catch (error) {
      primaryFailure = error
      throw error
    } finally {
      release()
      upstream.closeAllConnections()
      await new Promise<void>((resolve) => upstream.close(() => resolve()))
      try { await request.put("/api/settings", { data: { speech: previous } }) }
      catch (error) { if (!primaryFailure) throw error }
    }
  })
}
