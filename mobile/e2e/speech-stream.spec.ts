import { createServer, type ServerResponse } from "node:http"
import { expect, test } from "@playwright/test"

for (const { mime, ordinary } of [{ mime: "audio/pcm", ordinary: false }, { mime: "text/event-stream", ordinary: false }, { mime: "text/event-stream", ordinary: true }]) {
  test(`${ordinary ? "Configurable" : "Legacy"} Gateway ${mime} starts playing before EOF and Stop cancels the phone request`, async ({ page }, info) => {
    let response: ServerResponse | undefined
    let received: { authorization?: string; body: unknown } | undefined
    let closed = false
    let preflights = 0
    const networkFailures: Array<{ url: string; error: string | null }> = []
    page.on("requestfailed", request => networkFailures.push({ url: new URL(request.url()).pathname, error: request.failure()?.errorText ?? null }))
    page.on("pageerror", error => networkFailures.push({ url: "page", error: error.message }))
    const upstream = createServer((req, res) => {
      if (req.method === "OPTIONS") {
        preflights++
        res.writeHead(204, { "Access-Control-Allow-Origin": "*", "Access-Control-Allow-Methods": "POST, OPTIONS", "Access-Control-Allow-Headers": "authorization, content-type" }); res.end(); return
      }
      void (async () => {
        const chunks: Buffer[] = []
        for await (const chunk of req) chunks.push(Buffer.from(chunk))
        received = { authorization: req.headers.authorization, body: JSON.parse(Buffer.concat(chunks).toString()) }
        response = res
        res.on("close", () => { closed = true })
        res.writeHead(200, { "Content-Type": mime, "Access-Control-Allow-Origin": "*", "Cache-Control": "no-store" })
        const pcm = Buffer.alloc(24000 * 2)
        for (let sample = 0; sample < 24000; sample++) pcm.writeInt16LE(Math.round(Math.sin(sample * Math.PI / 40) * 1200), sample * 2)
        res.write(mime === "audio/pcm" ? pcm : `data: ${JSON.stringify({ type: "speech.audio.delta", audio: pcm.toString("base64") })}\n\n`)
        // Hold the real HTTP body open until the user presses Stop. A player
        // that buffers to EOF can never reach its playing state in this case.
      })().catch(() => { res.destroy() })
    })
    await new Promise<void>(resolve => upstream.listen(0, "127.0.0.1", resolve))
    const address = upstream.address()
    if (!address || typeof address === "string") throw new Error("Speech fixture did not listen")
    try {
      await page.addInitScript(() => {
        const origin = location.origin
        localStorage.setItem("zwai.phone.gateway.origin", origin)
        localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, token: "agm_" + "a".repeat(64), user: { id: "voice-owner", username: "voice-owner", approval_status: "approved", enabled: true }, device: { id: "voice-mobile", expires_at: "2099-01-01T00:00:00Z" } }))
      })
      await page.route("**/client/api/v1/**", async route => {
        const url = new URL(route.request().url())
        const user = { id: "voice-owner", username: "voice-owner", approval_status: "approved", enabled: true }
        const device = { id: "voice-mobile", expires_at: "2099-01-01T00:00:00Z" }
        const data = url.pathname.endsWith("/auth/me") ? { user, device }
          : url.pathname.endsWith("/bootstrap") ? { user, device, models: { source_id: "gateway:voice", source_kind: "gateway", label: "Voice", execution: "phone", api_base_url: url.origin + "/v1", catalog_url: url.origin + "/v1/models", api_access_enabled: true }, pairlink: { enabled: false, hub_url: url.origin, redeem_url: url.origin + "/pairlink/v1/pairings/redeem", info_url: url.origin + "/pairlink/v1/info", websocket_url: url.origin.replace(/^http/, "ws") + "/pairlink/v1/ws" }, heartbeat_interval: 30, presence_ttl: 90 } : url.pathname.endsWith("/heartbeat") ? {} : []
        await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data }) })
      })
      await page.route("**/v1/models", route => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: "chat", model_type: "chat" }] }) }))
      await page.route("**/v1/audio/speech", route => route.continue({ url: `http://127.0.0.1:${address.port}/v1/audio/speech` }))
      await page.goto(ordinary ? "/?mock=1&tick=0&speech=local&speech_output=gateway&speech_format=pcm" : "/?mock=1&tick=0&speech=gateway&speech_format=pcm")
      if (ordinary) {
        // This cache is produced by the selected PC's speech_config RPC, not
        // injected browser state. A global-only phone routes this to PC RPC.
        await expect.poll(() => page.evaluate(() => Object.values(JSON.parse(localStorage.getItem("zwai.phone.speech") || "{}"))
          .map(row => (row as { snapshot: unknown }).snapshot))).toContainEqual(expect.objectContaining({
          execution: "pc_local", output: expect.objectContaining({ execution: "gateway", gateway_origin: new URL(page.url()).origin }),
        }))
      }
      await page.getByLabel("消息", { exact: true }).fill("Read this completed response")
      await page.locator("form button[type=submit]").click()
      const read = page.getByRole("button", { name: "朗读", exact: true }).last()
      await expect(read).toBeVisible()
      await read.click()
      const stop = page.getByRole("button", { name: "停止朗读", exact: true })
      await expect(stop).toHaveAttribute("data-playback-state", "playing")
      expect(received).toMatchObject({ authorization: "Bearer agm_" + "a".repeat(64), body: { model: "speech", response_format: "pcm", stream_format: "sse" } })
      expect(response?.writableEnded).toBe(false)
      expect(closed).toBe(false)
      if (info.project.name === "iphone-layout") expect(preflights).toBeGreaterThan(0)
      await page.screenshot({ path: info.outputPath("phone-streaming-before-eof.png") })
      await stop.click()
      await expect(stop).toHaveCount(0)
      await expect.poll(() => closed).toBe(true)
      expect(response?.writableEnded).toBe(false)
      await expect(page.getByRole("alert")).toHaveCount(0)
      if (ordinary) {
        await page.getByRole("button", { name: "返回", exact: true }).click()
        await page.getByRole("button", { name: "菜单", exact: true }).click()
        await page.getByRole("menuitem", { name: "用户中心", exact: true }).click()
        const center = page.getByRole("dialog", { name: "用户中心", exact: true })
        await expect(center.getByRole("heading", { name: "我的电脑", exact: true })).toBeVisible()
        await expect(center.getByRole("button", { name: "登录", exact: true })).toHaveCount(0)
        await expect(center.getByText("接口拒绝了这次请求。", { exact: true })).toHaveCount(0)
        await expect(center.getByRole("alert")).toHaveCount(0)
        const status = page.getByTestId("speech-status")
        await expect(status.getByText(/^语音输入:/)).toContainText("由此电脑调用本地语音模型")
        await expect(status.getByText(/^语音朗读:/)).toContainText("手机直接调用网关")
        await status.scrollIntoViewIfNeeded()
        await page.screenshot({ path: info.outputPath("phone-mixed-speech-status.png") })
      }
    } catch (error) {
      await info.attach("stream-network-failure", { contentType: "application/json", body: JSON.stringify({ failures: networkFailures, received: Boolean(received), ended: response?.writableEnded, closed, preflights }) })
      throw error
    } finally {
      response?.end()
      upstream.closeAllConnections()
      await new Promise<void>(resolve => upstream.close(() => resolve()))
    }
  })
}
