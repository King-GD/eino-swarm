import { fixtureCapabilities } from "../src/test/capabilities"
import { expect, test } from "@playwright/test"

const origin = "https://sources.invalid"
const token = "agm_" + "a".repeat(64)
const user = { id: "owner", username: "Owner", approval_status: "approved", enabled: true }
const device = { id: "phone", expires_at: "2099-01-01T00:00:00Z" }
const preset = "/client/models/v1/preset/models"
const gateway = "/client/models/v1/gateway"

// Identical names in all three sources must preserve their routing identities.
test("LOCAL groups both catalogs, routes duplicates and drops only a revoked source", async ({ page }, info) => {
  await page.addInitScript(({ origin, token, user, device }) => {
    localStorage.setItem("zwai.phone.mode", "local")
    localStorage.setItem("zwai.phone.gateway.origin", origin)
    localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, token, user, device }))
    localStorage.setItem("zwai.phone.providers", JSON.stringify([{ id: "personal", label: "个人接口", baseURL: "https://personal.invalid/v1", apiKey: "personal-key", api: "chat", model: "conversation", catalog: ["conversation"], timeoutSeconds: 300 }]))
  }, { origin, token, user, device })
  const bootstrap = {
    user, device,
    capabilities_url: origin + "/client/capabilities",
    models: { source_id: "gateway:preset", source_kind: "gateway", execution: "phone", label: "Partner", api_base_url: origin + "/v1", catalog_url: origin + "/v1/models", client_catalog_url: origin + preset, api_access_enabled: true, configurable: false },
    gateway_models: { source_id: "gateway:global", source_kind: "gateway", execution: "phone", label: "Main", api_base_url: origin + gateway, catalog_url: origin + gateway + "/models", api_access_enabled: true },
    pairlink: { enabled: false, hub_url: origin, redeem_url: origin + "/pairlink/v1/pairings/redeem", info_url: origin + "/pairlink/v1/info", websocket_url: origin.replace("https:", "wss:") + "/pairlink/v1/ws" }, heartbeat_interval: 30, presence_ttl: 90,
  }
  let revoked = false
  const inference: { path: string; model: string }[] = []
  await page.route(origin + "/**", async route => {
    const request = route.request(), path = new URL(request.url()).pathname
    expect(request.headers().authorization).toBe("Bearer " + token)
    if (path.startsWith(gateway) && revoked) { await route.fulfill({ status: 403, json: { error: { code: "permission_denied" } } }); return }
    if (path.endsWith("/chat/completions")) {
      inference.push({ path, model: request.postDataJSON().model })
      await route.fulfill({ contentType: "text/event-stream", body: `data: ${JSON.stringify({ choices: [{ delta: { content: path.startsWith(gateway) ? "GATEWAY REPLY" : "PRESET REPLY" } }] })}\n\ndata: [DONE]\n\n` }); return
    }
    const data = path === "/client/capabilities" ? fixtureCapabilities() : path.endsWith("/auth/me") ? { user, device } : path.endsWith("/bootstrap") ? bootstrap
      : path === preset ? ["compact", "title", "translation", "conversation"].map(id => ({ id, model_type: "chat" }))
      : path === gateway + "/models" ? [{ id: "conversation" }] : {}
    expect(path).not.toBe("/v1/models")
    await route.fulfill({ json: { data } })
  })
  await page.route("https://personal.invalid/**", async route => {
    expect(route.request().headers().authorization).toBe("Bearer personal-key")
    inference.push({ path: "personal", model: route.request().postDataJSON().model })
    await route.fulfill({ contentType: "text/event-stream", body: 'data: {"choices":[{"delta":{"content":"PERSONAL REPLY"}}]}\n\ndata: [DONE]\n\n' })
  })
  await page.goto("/")
  const openPicker = async () => {
    await page.getByRole("button", { name: "模型", exact: true }).click()
    return page.getByRole("dialog", { name: "模型", exact: true })
  }
  let picker = await openPicker()
  for (const name of ["个人接口", "Partner", "网关 · Main"]) await expect(picker.getByRole("group", { name, exact: true }).getByRole("radio", { name: "conversation", exact: true })).toBeVisible()
  await expect(picker.getByRole("group", { name: "Partner", exact: true }).getByRole("radio")).toHaveCount(1)
  await page.screenshot({ path: info.outputPath("local-three-source-groups.png") })
  await picker.getByRole("button", { name: "关闭", exact: true }).click()
  for (const [group, reply] of [["个人接口", "PERSONAL REPLY"], ["Partner", "PRESET REPLY"], ["网关 · Main", "GATEWAY REPLY"]]) {
    picker = await openPicker()
    await picker.getByRole("group", { name: group, exact: true }).getByRole("radio", { name: "conversation", exact: true }).click()
    await page.getByLabel("消息", { exact: true }).fill("source routing fixture")
    await page.getByRole("button", { name: "发送", exact: true }).click()
    await expect(page.getByText(reply, { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "返回", exact: true }).click()
  }
  expect(inference).toEqual([{ path: "personal", model: "conversation" }, { path: "/v1/chat/completions", model: "conversation" }, { path: gateway + "/chat/completions", model: "conversation" }])
  revoked = true
  picker = await openPicker()
  await picker.getByRole("button", { name: "刷新模型", exact: true }).click()
  await expect(picker.getByRole("group", { name: "网关 · Main", exact: true })).toHaveCount(0)
  for (const name of ["Partner", "个人接口"]) await expect(picker.getByRole("group", { name, exact: true })).toBeVisible()
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("zwai.phone.gateway.session") || "null")?.user.id)).toBe(user.id)
  const settings = await page.evaluate(() => localStorage.getItem("zwai.phone.providers"))
  expect(settings).toContain("personal-key"); expect(settings).not.toContain(token); expect(settings).not.toContain("gateway:")
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: info.outputPath("local-gateway-revoked.png") })
})
