import { expect, test, type Page } from "@playwright/test"

test.use({ permissions: ["microphone"] })

const input = (page: Page) => page.getByLabel("消息", { exact: true })
const start = (page: Page) => page.getByRole("button", { name: "开始语音输入", exact: true })
const panel = (page: Page) => page.getByRole("dialog", { name: "语音输入", exact: true })

async function record(page: Page) {
  await start(page).click()
  await expect(panel(page).getByRole("button", { name: "完成并转为文字" })).toBeEnabled()
  await expect(panel(page).getByRole("status")).toContainText("1 秒")
  await panel(page).getByRole("button", { name: "完成并转为文字" }).click()
}

test("disabled PC speech shows no microphone or read action", async ({ page }) => {
  await page.goto("/?mock=1&tick=0&speech=off")
  await expect(input(page)).toBeVisible()
  await expect(start(page)).toHaveCount(0)
  await expect(page.getByRole("button", { name: "朗读", exact: true })).toHaveCount(0)
})

test("a real browser recording crosses local audio chunks and edits only the original draft", async ({ page }, info) => {
  await page.goto("/?mock=1&tick=0&speech=local&speech_delay=1200")
  await input(page).fill("existing draft")
  await record(page)
  await expect(page.getByRole("status", { name: "", exact: true }).filter({ hasText: "正在识别" })).toBeVisible()
  await input(page).fill("edited while recognizing")
  await expect(input(page)).toHaveValue("edited while recognizing Voice draft")
  await expect(page.getByTestId("transcript")).not.toContainText("Voice draft")
  await page.screenshot({ path: info.outputPath("local-transcription-editable-draft.png") })
  await start(page).click()
  await expect(panel(page)).toBeVisible()
  expect(await page.evaluate(() => (window as Window & { __zwaiAndroidBack?: () => boolean }).__zwaiAndroidBack?.())).toBe(true)
  await expect(panel(page)).toHaveCount(0)
  await expect(input(page)).toHaveValue("edited while recognizing Voice draft")
})

test("a completed local reply decodes and plays audio, then Stop releases playback", async ({ page }, info) => {
  await page.goto("/?mock=1&tick=0&speech=local")
  await input(page).fill("complete this offline request")
  await page.locator("form button[type=submit]").click()
  const read = page.getByRole("button", { name: "朗读", exact: true }).last()
  await expect(read).toBeVisible()
  await read.click()
  const stop = page.getByRole("button", { name: "停止朗读", exact: true })
  await expect(stop).toHaveAttribute("data-playback-state", "playing")
  await page.screenshot({ path: info.outputPath("local-audio-playing.png") })
  await stop.click()
  await expect(page.getByRole("button", { name: "停止朗读", exact: true })).toHaveCount(0)
  await expect(page.getByRole("alert")).toHaveCount(0)
})

test("Gateway speech uses the phone token directly even while viewing a PC conversation", async ({ page }, info) => {
  let transcriptions = 0
  let synthesis = 0
  await page.addInitScript(() => {
    const origin = location.origin
    localStorage.setItem("zwai.phone.gateway.origin", origin)
    localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, token: "agm_" + "a".repeat(64), user: { id: "voice-owner", username: "voice-owner", approval_status: "approved", enabled: true }, device: { id: "voice-mobile", expires_at: "2099-01-01T00:00:00Z" } }))
  })
  await page.route("**/client/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    const user = { id: "voice-owner", username: "voice-owner", approval_status: "approved", enabled: true }
    const device = { id: "voice-mobile", expires_at: "2099-01-01T00:00:00Z" }
    const data = path.endsWith("/auth/me") ? { user, device }
      : path.endsWith("/bootstrap") ? { user, device, models: { source_id: "gateway:voice", source_kind: "gateway", label: "Voice", execution: "phone", api_base_url: new URL(route.request().url()).origin + "/v1", catalog_url: new URL(route.request().url()).origin + "/v1/models", api_access_enabled: true }, pairlink: { enabled: false }, heartbeat_interval: 30, presence_ttl: 90 } : []
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data }) })
  })
  await page.route("**/v1/models", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: "chat", model_type: "chat" }] }) }))
  await page.route("**/v1/audio/transcriptions", async (route) => {
    transcriptions += 1
    expect(route.request().headers().authorization).toBe("Bearer agm_" + "a".repeat(64))
    const wire = route.request().postDataBuffer()!
    expect(wire.toString()).toContain('name="model"\r\n\r\ntranscription')
    const fileStart = wire.indexOf(Buffer.from('name="file"'))
    const bodyStart = wire.indexOf(Buffer.from("\r\n\r\n"), fileStart) + 4
    const boundary = route.request().headers()["content-type"].split("boundary=")[1]
    const bodyEnd = wire.indexOf(Buffer.from("\r\n--" + boundary), bodyStart)
    const bytes = wire.subarray(bodyStart, bodyEnd)
    const decoded = await page.evaluate(async (base64) => {
      const context = new AudioContext()
      try { const data = Uint8Array.from(atob(base64), (char) => char.charCodeAt(0)); const audio = await context.decodeAudioData(data.buffer); return { channels: audio.numberOfChannels, duration: audio.duration } }
      finally { await context.close() }
    }, bytes.toString("base64"))
    expect(decoded.channels).toBeGreaterThan(0)
    expect(decoded.duration).toBeGreaterThan(0)
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ text: "Gateway voice draft" }) })
  })
  await page.route("**/v1/audio/speech", async (route) => {
    synthesis += 1
    expect(route.request().headers().authorization).toBe("Bearer agm_" + "a".repeat(64))
    expect(route.request().postDataJSON()).toMatchObject({ model: "speech", response_format: "wav" })
    const data = Buffer.alloc(44 + 16000 * 2 * 2)
    data.write("RIFF", 0); data.writeUInt32LE(data.length - 8, 4); data.write("WAVEfmt ", 8); data.writeUInt32LE(16, 16)
    data.writeUInt16LE(1, 20); data.writeUInt16LE(1, 22); data.writeUInt32LE(16000, 24); data.writeUInt32LE(32000, 28); data.writeUInt16LE(2, 32); data.writeUInt16LE(16, 34)
    data.write("data", 36); data.writeUInt32LE(data.length - 44, 40)
    await route.fulfill({ contentType: "audio/wav", body: data })
  })
  await page.goto("/?mock=1&tick=0&speech=gateway")
  await record(page)
  await expect(input(page)).toHaveValue("Gateway voice draft")
  expect(transcriptions).toBe(1)
  await page.locator("form button[type=submit]").click()
  await page.getByRole("button", { name: "朗读", exact: true }).last().click()
  const stop = page.getByRole("button", { name: "停止朗读", exact: true })
  await expect(stop).toHaveAttribute("data-playback-state", "playing")
  expect(synthesis).toBe(1)
  await page.screenshot({ path: info.outputPath("phone-direct-gateway-speech.png") })
  await stop.click()
})
