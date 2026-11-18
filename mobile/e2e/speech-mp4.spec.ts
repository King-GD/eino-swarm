import { fixtureCapabilities } from "../src/test/capabilities"
import { readFileSync } from "node:fs"
import { createServer, request as proxyRequest } from "node:http"
import { expect, test } from "@playwright/test"

test.use({ permissions: [] })

test("automatic iPhone MP4 capture reaches ASR as complete decodable WAV", async ({ page, baseURL }, info) => {
  // Two generated tones in a real AAC/MP4 container, not a microphone accuracy
  // fixture. Splitting before stop also exercises the recorder's final bytes.
  const capture = readFileSync(new URL("./fixtures/speech-capture.m4a", import.meta.url)).toString("base64")
  await page.addInitScript((base64) => {
    const origin = location.origin
    localStorage.setItem("zwai.phone.mode", "local")
    localStorage.setItem("zwai.phone.gateway.origin", origin)
    localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, token: "agm_" + "a".repeat(64), user: { id: "voice-owner", username: "voice-owner", approval_status: "approved", enabled: true }, device: { id: "voice-mobile", expires_at: "2099-01-01T00:00:00Z" } }))
    const bytes = Uint8Array.from(atob(base64), char => char.charCodeAt(0))
    const middle = Math.floor(bytes.length / 2)
    Object.defineProperty(navigator, "mediaDevices", { value: { getUserMedia: async () => new MediaStream() } })
    class Recorder {
      static isTypeSupported(mime: string) { return mime === "audio/mp4" }
      mimeType = "audio/mp4;codecs=mp4a.40.2"
      state = "inactive"
      ondataavailable?: (event: { data: Blob }) => void
      onstop?: () => void
      start() {
        this.state = "recording"
        this.ondataavailable?.({ data: new Blob([bytes.slice(0, middle)], { type: this.mimeType }) })
      }
      stop() {
        this.state = "inactive"
        queueMicrotask(() => {
          this.ondataavailable?.({ data: new Blob([bytes.slice(middle)], { type: this.mimeType }) })
          this.onstop?.()
        })
      }
    }
    Object.defineProperty(window, "MediaRecorder", { value: Recorder })
  }, capture)
  await page.route("**/client/api/v1/**", async route => {
    const url = new URL(route.request().url())
    const user = { id: "voice-owner", username: "voice-owner", approval_status: "approved", enabled: true }
    const device = { id: "voice-mobile", expires_at: "2099-01-01T00:00:00Z" }
    const data = url.pathname.endsWith("/capabilities") ? fixtureCapabilities() : url.pathname.endsWith("/auth/me") ? { user, device }
      : url.pathname.endsWith("/bootstrap") ? { user, device, capabilities_url: url.origin + "/client/api/v1/capabilities", models: { source_id: "gateway:voice", source_kind: "gateway", label: "Voice", defaults: { speech: "speech", transcription: "transcription" }, execution: "phone", api_base_url: url.origin + "/v1", catalog_url: url.origin + "/v1/models", api_access_enabled: true }, pairlink: { enabled: false, hub_url: url.origin, redeem_url: url.origin + "/pairlink/v1/pairings/redeem", info_url: url.origin + "/pairlink/v1/info", websocket_url: url.origin.replace(/^http/, "ws") + "/pairlink/v1/ws" }, heartbeat_interval: 30, presence_ttl: 90 } : []
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data }) })
  })
  await page.route("**/v1/models", route => route.fulfill({ json: { data: [{ id: "chat", model_type: "chat" }] } }))
  let uploads = 0
  let receive!: (file: { name: string; type: string; bytes: Buffer }) => void
  let refuse!: (error: unknown) => void
  const received = new Promise<{ name: string; type: string; bytes: Buffer }>((resolve, reject) => { receive = resolve; refuse = reject })
  void received.catch(() => undefined)
  // Read the real multipart body: WebKit's debugger omits binary file parts
  // from postDataBuffer(), which cannot be used as evidence of an empty upload.
  const upstream = createServer((req, res) => {
    if (req.url !== "/v1/audio/transcriptions") {
      const proxy = proxyRequest(new URL(req.url!, baseURL), { method: req.method, headers: { ...req.headers, host: new URL(baseURL!).host } }, response => {
        res.writeHead(response.statusCode!, response.headers)
        response.pipe(res)
      })
      proxy.on("error", error => { refuse(error); res.destroy() })
      req.pipe(proxy)
      return
    }
    void (async () => {
      uploads++
      const parts: Buffer[] = []
      for await (const part of req) parts.push(Buffer.from(part))
      const form = await new Request("http://fixture.invalid", { method: "POST", headers: { "Content-Type": req.headers["content-type"]! }, body: new Uint8Array(Buffer.concat(parts)).buffer }).formData()
      const file = form.get("file") as File
      receive({ name: file.name, type: file.type, bytes: Buffer.from(await file.arrayBuffer()) })
      res.writeHead(200, { "Content-Type": "application/json" })
      res.end(JSON.stringify({ text: "Complete recording" }))
    })().catch(error => { refuse(error); res.destroy() })
  })
  await new Promise<void>(resolve => upstream.listen(0, "127.0.0.1", resolve))
  const address = upstream.address()
  if (!address || typeof address === "string") throw new Error("Speech fixture did not listen")
  try {
    await page.goto(`http://127.0.0.1:${address.port}/?mock=1&tick=0&speech=gateway`)
    const message = page.getByLabel("消息", { exact: true })
    await message.fill("Existing draft ")
    await page.getByRole("button", { name: "开始语音输入", exact: true }).click()
    const finish = page.getByRole("button", { name: "完成并转为文字", exact: true })
    await expect(finish).toBeEnabled()
    await finish.click()
    const file = await received
    expect(file.name).toBe("recording.wav")
    expect(file.type).toBe("audio/wav")
    const bytes = file.bytes
    expect(bytes.readUInt32LE(24)).toBe(16000)
    expect(bytes.readUInt16LE(22)).toBe(1)
    const audio = await page.evaluate(async ({ input, output }) => {
      const context = new AudioContext()
      try {
        const original = await context.decodeAudioData(Uint8Array.from(atob(input), char => char.charCodeAt(0)).buffer)
        const data = Uint8Array.from(atob(output), char => char.charCodeAt(0))
        const decoded = await context.decodeAudioData(data.buffer)
        const samples = decoded.getChannelData(0)
        const power = (start: number, end: number) => samples.slice(Math.floor(samples.length * start), Math.floor(samples.length * end)).reduce((sum, value) => sum + value ** 2, 0)
        return { inputDuration: original.duration, duration: decoded.duration, channels: decoded.numberOfChannels, first: power(0.1, 0.4), last: power(0.6, 0.9) }
      } finally { await context.close() }
    }, { input: capture, output: bytes.toString("base64") })
    // AAC priming is decoded differently by Chromium and WebKit. Conversion
    // must preserve this browser's actual source duration, within one ms.
    expect(audio.inputDuration).toBeGreaterThan(0.4)
    expect(audio.duration).toBeCloseTo(audio.inputDuration, 3)
    expect(audio.channels).toBe(1)
    expect(audio.first).toBeGreaterThan(0.1)
    expect(audio.last).toBeGreaterThan(0.1)
    await expect(message).toHaveValue("Existing draft Complete recording")
    expect(uploads).toBe(1)
    await expect(page.getByRole("alert")).toHaveCount(0)
    await page.screenshot({ path: info.outputPath("mp4-normalized-to-wav-draft.png") })
  } finally {
    upstream.closeAllConnections()
    await new Promise<void>(resolve => upstream.close(() => resolve()))
  }
})
