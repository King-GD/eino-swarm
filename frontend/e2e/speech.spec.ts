import { createServer } from "node:http"
import { expect, test } from "@playwright/test"
import { composer, freshConversation, send, waitForIdle, waitForDraftSaved } from "./session"

test.use({ permissions: ["microphone"], launchOptions: { args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream"] } })

function wave(): Buffer {
  const samples = 1600
  const bytes = Buffer.alloc(44 + samples * 2)
  bytes.write("RIFF", 0); bytes.writeUInt32LE(bytes.length - 8, 4); bytes.write("WAVEfmt ", 8)
  bytes.writeUInt32LE(16, 16); bytes.writeUInt16LE(1, 20); bytes.writeUInt16LE(1, 22)
  bytes.writeUInt32LE(16000, 24); bytes.writeUInt32LE(32000, 28); bytes.writeUInt16LE(2, 32); bytes.writeUInt16LE(16, 34)
  bytes.write("data", 36); bytes.writeUInt32LE(samples * 2, 40)
  for (let i = 0; i < samples; i++) bytes.writeInt16LE(Math.round(Math.sin(i * Math.PI / 20) * 1000), 44 + i * 2)
  return bytes
}

function recordingFromMultipart(bytes: Buffer, contentType: string) {
  const boundary = contentType.match(/(?:^|;)\s*boundary=(?:"([^"]+)"|([^;\s]+))/i)
  if (!boundary) throw new Error("Recording upload omitted its multipart boundary")
  const delimiter = Buffer.from(`\r\n--${boundary[1] ?? boundary[2]}`)
  let offset = 0
  while (offset < bytes.length) {
    const headerEnd = bytes.indexOf("\r\n\r\n", offset)
    if (headerEnd < 0) break
    const bodyEnd = bytes.indexOf(delimiter, headerEnd + 4)
    if (bodyEnd < 0) break
    const headers = bytes.subarray(offset, headerEnd).toString("latin1")
    const disposition = headers.match(/(?:^|\r\n)Content-Disposition:\s*form-data;([^\r\n]*)/i)?.[1] ?? ""
    if (/(?:^|;)\s*name=(?:"file"|file)(?:;|$)/.test(disposition)) {
      const mime = headers.match(/(?:^|\r\n)Content-Type:\s*([^\r\n]+)/i)?.[1].trim()
      if (!mime) throw new Error("Recording upload omitted its audio media type")
      const audio = bytes.subarray(headerEnd + 4, bodyEnd)
      return { data: audio.toString("base64"), mime, size: audio.length }
    }
    offset = bodyEnd + delimiter.length + 2
  }
  throw new Error("Recording upload omitted its file part")
}

for (const recordingFormat of ["auto", "wav"] as const) {
test(`speech ${recordingFormat} settings gate real recording, preserve edits and read a completed answer`, async ({ page, request }) => {
  const previous = (await (await request.get("/api/settings")).json()).settings.speech
  let receive!: (audio: { data: string; mime: string; size: number }) => void
  const received = new Promise<{ data: string; mime: string; size: number }>((resolve) => { receive = resolve })
  let release!: () => void
  const recognitionGate = new Promise<void>((resolve) => { release = resolve })
  const spoken: string[] = []
  const upstream = createServer((req, res) => {
    void (async () => {
      const chunks: Buffer[] = []
      for await (const chunk of req) chunks.push(Buffer.from(chunk))
      const bytes = Buffer.concat(chunks)
      if (req.url === "/v1/audio/transcriptions") {
        // Node 22's formData parser refuses Go's filename-before-name header order.
        receive(recordingFromMultipart(bytes, req.headers["content-type"] ?? ""))
        await recognitionGate
        res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify({ text: "recognized speech" }))
      } else if (req.url === "/v1/audio/speech") {
        spoken.push(JSON.parse(bytes.toString()).input)
        res.writeHead(200, { "Content-Type": "audio/wav" }); res.end(wave())
      } else { res.writeHead(404); res.end() }
    })().catch(() => { res.writeHead(500); res.end() })
  })
  await new Promise<void>((resolve) => upstream.listen(0, "127.0.0.1", resolve))
  const address = upstream.address()
  if (!address || typeof address === "string") throw new Error("Fixture did not listen")
  const baseURL = `http://127.0.0.1:${address.port}/v1`
  try {
    const configured = await request.put("/api/settings", { data: { speech: {
      input: { enabled: false, source: "custom", base_url: baseURL, model: "fixture-asr", recording_format: recordingFormat },
      output: { enabled: true, source: "custom", base_url: baseURL, model: "fixture-tts", format: "wav" },
      limits: { chunk_chars: 64 },
    } } })
    expect(configured.ok()).toBe(true)
    await freshConversation(page)
    await expect(page.getByLabel("Start voice input")).toHaveCount(0)
    await page.getByRole("button", { name: "Settings", exact: true }).click()
    const settings = page.getByRole("dialog")
    await expect(settings.getByRole("tab", { name: "Speech", exact: true })).toHaveCount(0)
    await settings.getByRole("tab", { name: "Models", exact: true }).click()
    await expect(settings.getByRole("heading", { name: "Voice input", exact: true })).toBeVisible()
    await settings.getByLabel("Enabled", { exact: true }).first().click()
    await settings.getByRole("button", { name: "Back to app" }).click()
    await expect(page.getByLabel("Start voice input")).toBeVisible()
    expect((await (await request.get("/api/settings")).json()).settings.speech.input.enabled).toBe(true)
    await composer(page).fill("original draft")
    await page.getByLabel("Start voice input").click()
    await expect(page.getByLabel("Finish and transcribe")).toBeVisible()
    await expect(page.getByTestId("voice-input-control").getByRole("status")).toHaveText("0:01")
    await page.getByLabel("Finish and transcribe").click()
    const captured = await received
    expect(captured.size).toBeGreaterThan(100)
    expect(captured.mime).toMatch(/^audio\//)
    const decoded = await page.evaluate(async ({ data }) => {
      const context = new AudioContext()
      try {
        const bytes = Uint8Array.from(atob(data), (char) => char.charCodeAt(0))
        const audio = await context.decodeAudioData(bytes.buffer)
        return { duration: audio.duration, channels: audio.numberOfChannels, sampleRate: audio.sampleRate }
      } finally { await context.close() }
    }, captured)
    expect(decoded.duration).toBeGreaterThan(0.5)
    expect(decoded.channels).toBeGreaterThan(0)
    if (recordingFormat === "wav") {
      expect(captured.mime).toBe("audio/wav")
      expect(Buffer.from(captured.data, "base64").subarray(0, 4).toString()).toBe("RIFF")
      expect(decoded.channels).toBe(1)
    }
    await composer(page).fill("edited during recognition")
    await expect(page.getByLabel("Send", { exact: true })).toBeDisabled()
    release()
    await expect(composer(page)).toHaveValue("edited during recognition recognized speech")
    await waitForDraftSaved(page)
    await send(page, "Summarize the current state")
    await waitForIdle(page)
    const answer = page.getByTestId("assistant-message").filter({ has: page.getByLabel("Read aloud", { exact: true }) }).last()
    await answer.hover()
    await answer.getByLabel("Read aloud", { exact: true }).click()
    await expect.poll(() => spoken.length).toBeGreaterThan(0)
    await expect(answer.getByLabel("Read aloud", { exact: true })).toBeVisible()
    expect(spoken.every((text) => [...text].length <= 64)).toBe(true)
    expect(spoken.join(" ")).not.toContain("```")
    expect(spoken.join(" ")).not.toContain("https://")
  } finally {
    release()
    await request.put("/api/settings", { data: { speech: previous } })
    upstream.closeAllConnections()
    await new Promise<void>((resolve) => upstream.close(() => resolve()))
  }
})
}
