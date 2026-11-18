import { readFile } from "node:fs/promises"
import { expect, test, type Page } from "@playwright/test"

const origin = "https://mobile-images.invalid"
const token = "agm_" + "a".repeat(64)
const user = { id: "image-owner", username: "fixture", enabled: true, approval_status: "approved" }
const device = { id: "phone", expires_at: "2099-01-01T00:00:00Z" }
const png = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAADCAIAAAA2iEnWAAAAEklEQVR4nGP4bySgcOYDAwoFAF/KCVhRsx0PAAAAAElFTkSuQmCC"
const cors = {
  "access-control-allow-origin": "*",
  "access-control-allow-headers": "authorization, content-type, accept-encoding, x-aigateway-model-roles",
  "access-control-allow-methods": "GET,POST,OPTIONS",
}

function defer() {
  let release!: () => void
  const wait = new Promise<void>(resolve => { release = resolve })
  return { wait, release }
}

async function signedImageFixture(page: Page, { api = "chat", held, invalidResponse = false, imageAllowed = true, explicitDefault = true, fixedAccount = false }: {
  api?: "responses" | "chat"; held?: ReturnType<typeof defer>; invalidResponse?: boolean; imageAllowed?: boolean; explicitDefault?: boolean; fixedAccount?: boolean
} = {}) {
  let chats = 0
  let catalogs = 0
  const images: unknown[] = []
  const conversationModel = fixedAccount ? "conversation" : "chat-model"
  const imageModel = fixedAccount ? "image" : "image-a"
  await page.addInitScript(({ origin, token, user, device, fixedAccount }) => {
    if (fixedAccount) localStorage.removeItem("zwai.phone.mode")
    localStorage.setItem("zwai.phone.gateway.origin", origin)
    localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, token, user, device }))
    localStorage.setItem("zwai.phone.locale", "zh")
  }, { origin, token, user, device, fixedAccount })
  await page.route(origin + "/**", async route => {
    const request = route.request()
    if (request.method() === "OPTIONS") { await route.fulfill({ headers: cors }); return }
    expect(request.headers().authorization).toBe("Bearer " + token)
    const path = new URL(request.url()).pathname
    const respond = (data: unknown) => route.fulfill({ headers: cors, json: { data } })
    if (path === "/client/api/v1/auth/me") {
      await respond({ user, device })
    } else if (path === "/client/api/v1/bootstrap") {
      await respond({
        user, device, capabilities_url: origin + "/client/models/v1/capabilities",
        models: {
          source_id: "gateway:image", source_kind: "gateway", label: "测试网关", execution: "phone",
          api_base_url: origin + "/v1", catalog_url: origin + "/v1/models", api_access_enabled: true, configurable: !fixedAccount,
          defaults: { conversation: conversationModel, ...(explicitDefault ? { image: imageModel } : {}) },
          ...(fixedAccount ? { roles: [
            { id: "conversation", enabled: true, label: "企业对话" },
            { id: "image", enabled: true, label: "企业生图" },
          ] } : {}),
        },
        pairlink: { enabled: false, hub_url: origin, redeem_url: origin + "/pairlink/v1/pairings/redeem",
          websocket_url: origin.replace("https:", "wss:") + "/pairlink/v1/ws", info_url: origin + "/pairlink/v1/info" },
        heartbeat_interval: 30, presence_ttl: 90,
      })
    } else if (path === "/client/models/v1/capabilities") {
      const cap = (endpoints: string[], available = true) => ({ supported: available, available, configurable: available && !fixedAccount, endpoints })
      await respond({
        version: 1, model_roles_version: 3, sources: {
          preset: { authorized: true, available: true, configurable: !fixedAccount, capabilities: {
            conversation: cap([api === "chat" ? "chat_completions" : "responses"]),
            image: cap(imageAllowed ? ["images"] : [], imageAllowed),
          } },
          gateway: { authorized: false, available: false, configurable: false, capabilities: {} },
        },
      })
    } else if (path === "/client/api/v1/heartbeat") {
      await respond({})
    } else if (path === "/v1/models") {
      catalogs++
      await respond([{ id: "image-z", model_type: "image" }, { id: conversationModel, model_type: "chat" }, { id: imageModel, model_type: "image" }])
    } else if (path === "/v1/images/generations") {
      const body = request.postDataJSON()
      expect(body).toEqual({ model: imageModel, prompt: expect.any(String), n: 1 })
      images.push(body)
      await held?.wait
      await respond([{ b64_json: invalidResponse ? "Tm90IGFuIGltYWdl" : png }]).catch(() => undefined)
    } else if (path === "/v1/chat/completions" || path === "/v1/responses") {
      chats++
      const body = request.postDataJSON()
      const args = '{"prompt":"两色图案"}'
      const repeated = chats === 2
      const call = { type: "function_call", call_id: "call-image-" + chats, name: "generate_image", arguments: args }
      let frames: unknown[]
      if (chats === 1) {
        expect(JSON.stringify(body.tools)).toContain("generate_image")
      } else {
        expect(JSON.stringify(body)).not.toContain(png)
        expect(JSON.stringify(body.tools ?? [])).not.toContain("generate_image")
        expect(JSON.stringify(body)).toContain(repeated ? "completed" : "image_limit")
      }
      if (api === "chat") {
        frames = chats <= 2
          ? [{ choices: [{ delta: { tool_calls: [{ index: 0, id: call.call_id, type: "function", function: { name: call.name, arguments: args } }] }, finish_reason: "tool_calls" }] }, "[DONE]"]
          : [{ choices: [{ delta: { content: "图片已生成。" }, finish_reason: "stop" }] }, "[DONE]"]
      } else {
        const output = chats <= 2 ? [{ ...call, id: "item-" + chats }] : []
        frames = [...(chats <= 2 ? [] : [{ type: "response.output_text.delta", delta: "图片已生成。" }]),
          { type: "response.completed", response: { id: "response-" + chats, status: "completed", output } }]
      }
      await route.fulfill({ headers: cors, contentType: "text/event-stream", body: frames.map(event => "data: " + (typeof event === "string" ? event : JSON.stringify(event)) + "\n\n").join("") })
    } else {
      await route.abort()
    }
  })
  await page.goto("/")
  const chat = page.getByRole("tab", { name: "对话", exact: true })
  if (fixedAccount) {
    await expect(page.getByTestId("pc-wait")).toBeVisible()
    await expect(page.getByLabel("消息", { exact: true })).toHaveCount(0)
    await chat.click()
  }
  await expect(chat).toHaveAttribute("aria-selected", "true")
  await expect(page.getByLabel("消息")).toBeEnabled()
  if (!explicitDefault) await expect.poll(() => catalogs).toBeGreaterThanOrEqual(2)
  return { images, chats: () => chats }
}

async function sendText(page: Page, message: string) {
  await page.getByLabel("消息").fill(message)
  await page.getByRole("button", { name: "发送", exact: true }).click()
}

test("direct /image waits, downloads exact pixels, and reopens IndexedDB history", async ({ page }, info) => {
  const held = defer()
  const calls = await signedImageFixture(page, { held })
  await sendText(page, "/image 两色图案")
  const result = page.getByTestId("generated-image-result")
  try {
    await expect(result).toHaveAttribute("data-status", "running")
    await expect.poll(() => calls.images.length).toBe(1)
    await page.screenshot({ path: info.outputPath("direct-image-running.png") })
  } finally { held.release() }

  const image = result.getByRole("img")
  await expect(image).toBeVisible()
  await expect.poll(() => image.evaluate(async (node: HTMLImageElement) =>
    [node.naturalWidth, node.naturalHeight, (await fetch(node.src).then(res => res.blob())).type])).toEqual([2, 3, "image/png"])
  await result.getByRole("button", { name: "查看大图" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toBeVisible()
  await dialog.getByRole("button", { name: "关闭" }).click()
  const [download] = await Promise.all([page.waitForEvent("download"), result.getByRole("link", { name: "下载" }).click()])
  expect((await readFile((await download.path())!)).toString("base64")).toBe(png)

  await page.reload()
  await page.getByRole("button", { name: "打开 /image 两色图案" }).click()
  await expect(page.getByTestId("generated-image-result").getByRole("img")).toBeVisible()
  expect(calls.images).toEqual([{ model: "image-a", prompt: "两色图案", n: 1 }])
  expect(calls.chats()).toBe(0)
  expect(await page.evaluate(() => localStorage.getItem("zwai.phone.directThreads"))).not.toContain(png)
  await page.screenshot({ path: info.outputPath("direct-image-reopened.png") })
})

for (const api of ["chat", "responses"] as const) {
  test(`direct ${api} offers image generation, stores it, and refuses a second paid call`, async ({ page }) => {
    const calls = await signedImageFixture(page, { api, explicitDefault: api === "chat" })
    await sendText(page, "请生成一张两色图案")
    await expect(page.getByText("图片已生成。", { exact: true })).toBeVisible()
    await expect(page.getByTestId("generated-image-result").getByRole("img")).toBeVisible()
    expect(calls.chats()).toBe(3)
    expect(calls.images).toHaveLength(1)
  })
}

test("Stop cancels the pending image and a late result stays suppressed", async ({ page }) => {
  const held = defer()
  const calls = await signedImageFixture(page, { held })
  await sendText(page, "/image 取消这张图")
  const result = page.getByTestId("generated-image-result")
  try {
    await expect.poll(() => calls.images.length).toBe(1)
    await page.getByRole("button", { name: "停止", exact: true }).first().click()
    await expect(result).toHaveAttribute("data-status", "cancelled")
  } finally { held.release() }
  await page.reload()
  await page.getByRole("button", { name: "打开 /image 取消这张图" }).click()
  await expect(result).toHaveAttribute("data-status", "cancelled")
  expect(calls.images).toHaveLength(1)
  expect(calls.chats()).toBe(0)
})

test("invalid image bytes settle as a failed call without retry", async ({ page }) => {
  const calls = await signedImageFixture(page, { invalidResponse: true })
  await sendText(page, "/image 无效结果")
  await expect(page.getByTestId("generated-image-result")).toHaveAttribute("data-status", "failed")
  await expect(page.getByText("网关未返回可用的图片。")).toBeVisible()
  expect(calls.images).toHaveLength(1)
  expect(calls.chats()).toBe(0)
})

test("an unauthorized /image preserves the draft and never requests a model", async ({ page }) => {
  const calls = await signedImageFixture(page, { imageAllowed: false })
  await sendText(page, "/image 保留草稿")
  await expect(page.getByText("当前模型来源无法生成图片。")).toBeVisible()
  await expect(page.getByLabel("消息")).toHaveValue("/image 保留草稿")
  expect(calls.images).toHaveLength(0)
  expect(calls.chats()).toBe(0)
})

test("reloading a pending call shows unknown without a second paid request", async ({ page }) => {
  const held = defer()
  const calls = await signedImageFixture(page, { held })
  await sendText(page, "/image 等待后重开")
  const result = page.getByTestId("generated-image-result")
  try {
    await expect(result).toHaveAttribute("data-status", "running")
    await expect.poll(() => calls.images.length).toBe(1)
    await page.reload()
    await page.getByRole("button", { name: "打开 /image 等待后重开" }).click()
    await expect(result).toHaveAttribute("data-status", "unknown")
    expect(calls.images).toHaveLength(1)
    expect(calls.chats()).toBe(0)
  } finally { held.release() }
})

test("Account Chat uses its fixed image role when no PC is connected", async ({ page }) => {
  const calls = await signedImageFixture(page, { fixedAccount: true })
  await expect(page.getByRole("button", { name: "模型", exact: true })).toHaveCount(0)
  await sendText(page, "/image 两色图案")
  await expect(page.getByTestId("generated-image-result").getByRole("img")).toBeVisible()
  expect(calls.images).toEqual([{ model: "image", prompt: "两色图案", n: 1 }])
  expect(calls.chats()).toBe(0)
})
