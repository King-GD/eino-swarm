import { test, expect, type Page } from "@playwright/test"

const origin = "https://gateway.invalid"
const user = { id: "owner", username: "Test user", avatar_url: "https://images.fofa.info/account.png", approval_status: "approved", enabled: true }
const device = { id: "phone", expires_at: "2099-01-01T00:00:00Z" }
const token = "agm_" + "a".repeat(64)

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.removeItem("zwai.phone.mode"))
})

test("default Account mode blocks chat and QR until sign-in", async ({ page }, info) => {
  await page.goto("/")
  await expect(page.getByTestId("gateway-account")).toBeVisible()
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "关闭", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "其他方式", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "其他服务器", exact: true })).toHaveCount(0)
  await expect(page.getByLabel("手机直聊联网工具", { exact: true })).toHaveCount(0)
  await expect(page.getByLabel("消息", { exact: true })).toHaveCount(0)
  await page.screenshot({ path: info.outputPath("account-login.png") })
})

test("signed-in avatar toggles Local menus and returns to Account without a connected PC", async ({ page }, info) => {
  await page.addInitScript(({ origin, user, device, token }) => {
    localStorage.setItem("zwai.phone.gateway.origin", origin)
    localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, user, device, token }))
  }, { origin, user, device, token })
  await page.route(origin + "/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith("/auth/me") ? { user, device }
      : path.endsWith("/bootstrap") ? { user, device, models: { source_id: "gateway:owner", source_kind: "gateway", label: "Team", execution: "phone", api_base_url: origin + "/v1", catalog_url: origin + "/v1/models", api_access_enabled: true }, pairlink: { enabled: false, hub_url: origin, redeem_url: origin + "/pairlink/v1/pairings/redeem", info_url: origin + "/pairlink/v1/info", websocket_url: origin.replace("https:", "wss:") + "/pairlink/v1/ws" }, heartbeat_interval: 30, presence_ttl: 90 }
      : path === "/v1/models" ? [{ id: "shared-model" }] : []
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data }) })
  })
  await page.route(user.avatar_url, (route) => route.fulfill({ contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="36" height="36"><rect width="36" height="36" fill="#457dcc"/></svg>' }))
  await page.goto("/")
  await expect(page.getByTestId("pc-wait")).toBeVisible()
  await expect(page.getByLabel("消息",{exact:true})).toHaveCount(0)
  await expect(page.getByRole("button",{name:"其他方式",exact:true})).toHaveCount(0)
  const portrait = page.getByTestId("account-portrait")
  await expect(portrait.locator("img")).toHaveAttribute("src", user.avatar_url)
  await portrait.tap()
  await portrait.tap()
  expect(await page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBeNull()
  // The third touch may replace the account page as its catalog arrives.
  // Send that touch to its observed coordinates without waiting for the old
  // portrait to remain mounted after the action has already taken effect.
  const bounds = await portrait.boundingBox()
  expect(bounds).not.toBeNull()
  await page.touchscreen.tap(bounds!.x + bounds!.width / 2, bounds!.y + bounds!.height / 2)
  await expect.poll(() => page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("local")
  await expect(page.getByLabel("消息", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "用户中心", exact: true }).click()
  const center = page.getByRole("dialog", { name: "用户中心", exact: true })
  await expect(center.getByLabel("手机直聊联网工具", { exact: true })).toBeVisible()
  await expect(center.getByRole("button", { name: "其他服务器", exact: true })).toHaveCount(0)
  const dialogPortrait = center.getByTestId("account-portrait")
  const dialogBounds = await dialogPortrait.boundingBox()
  expect(dialogBounds).not.toBeNull()
  for (let tap = 0; tap < 3; tap++) await page.touchscreen.tap(dialogBounds!.x + dialogBounds!.width / 2, dialogBounds!.y + dialogBounds!.height / 2)
  expect(await page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("account")
  await page.screenshot({ path: info.outputPath("account-portrait.png") })
  await page.getByRole("button", { name: "退出登录", exact: true }).click()
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible()
  await expect(page.getByLabel("消息", { exact: true })).toHaveCount(0)
})

test("Account waits for its selected PC engine and restores the unchanged draft when ready", async ({ page }, info) => {
  await page.addInitScript(({ origin, user, device, token }) => {
    localStorage.setItem("zwai.phone.gateway.origin", origin)
    localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, user, device, token }))
    // Attach account identity to the walkthrough PC, as the Connect response
    // does in production. Account admission still checks that exact identity.
    const write = Storage.prototype.setItem
    Storage.prototype.setItem = function (key, value) {
      if (key === "zwai.remote.links") value = JSON.stringify(JSON.parse(value).map((link: { fingerprint: string }) => link.fingerprint === "walkthrough"
        ? { ...link, gatewayOrigin: origin, gatewayOwnerID: user.id, gatewayMobileID: device.id, gatewayHostID: "selected-pc" } : link))
      write.call(this, key, value)
    }
  }, { origin, user, device, token })
  let modelRequests = 0
  await page.route(origin + "/**", async route => {
    const path = new URL(route.request().url()).pathname
    if (path.startsWith("/v1/")) modelRequests++
    const data = path.endsWith("/auth/me") ? { user, device }
      : path.endsWith("/bootstrap") ? { user, device, models: { source_id: "gateway:owner", source_kind: "gateway", label: "Team", execution: "phone", api_base_url: origin + "/v1", catalog_url: origin + "/v1/models", api_access_enabled: true }, pairlink: { enabled: false, hub_url: origin, redeem_url: origin + "/pairlink/v1/pairings/redeem", info_url: origin + "/pairlink/v1/info", websocket_url: origin.replace("https:", "wss:") + "/pairlink/v1/ws" }, heartbeat_interval: 30, presence_ttl: 90 } : []
    await route.fulfill({ json: { data } })
  })
  await page.goto("/?mock=1&tick=0")
  const input = page.getByLabel("消息", { exact: true })
  await expect(input).toBeVisible()
  await input.fill("等待电脑恢复后再发送")
  const transcript = await page.getByTestId("transcript").textContent()
  await page.evaluate(() => history.replaceState(null, "", "?mock=1&tick=0&startup=restoring&restore=1,3"))
  await expect(page.getByTestId("pc-wait")).toBeVisible()
  await expect(input).toBeHidden()
  await expect(page.getByText("已连接电脑，正在等待引擎就绪。", { exact: true })).toBeVisible()
  await page.screenshot({ path: info.outputPath("account-engine-wait.png") })
  await page.evaluate(() => history.replaceState(null, "", "?mock=1&tick=0&startup=ready"))
  await expect(input).toHaveValue("等待电脑恢复后再发送")
  await expect(input).toBeVisible()
  await expect(page.getByTestId("transcript")).toHaveText(transcript || "")
  expect(modelRequests).toBe(0)
})

// A hidden catalog is a valid third-party configuration, independent of mode.
async function hiddenGateway(page: Page, bindMockPC = false) {
  await page.route(user.avatar_url, route => route.fulfill({ contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="36" height="36"/>' }))
  await page.addInitScript(({ origin, user, device, token, bindMockPC }) => {
    localStorage.setItem("zwai.phone.gateway.origin", origin)
    localStorage.setItem("zwai.phone.gateway.session", JSON.stringify({ origin, user, device, token }))
    if (bindMockPC) {
      const write = Storage.prototype.setItem
      Storage.prototype.setItem = function (key, value) {
        if (key === "zwai.remote.links") value = JSON.stringify(JSON.parse(value).map((link: { fingerprint: string }) => link.fingerprint === "walkthrough"
          ? { ...link, gatewayOrigin: origin, gatewayOwnerID: user.id, gatewayMobileID: device.id, gatewayHostID: "selected-pc" } : link))
        write.call(this, key, value)
      }
    }
  }, { origin, user, device, token, bindMockPC })
  let modelRequests = 0
  await page.route(origin + "/**", async route => {
    const path = new URL(route.request().url()).pathname
    if (path.startsWith("/v1/")) modelRequests++
    const data = path.endsWith("/auth/me") ? { user, device }
      : path.endsWith("/bootstrap") ? { user, device, models: { source_id: "gateway:owner", source_kind: "gateway", label: "Managed", execution: "phone", api_base_url: origin + "/v1", catalog_url: origin + "/v1/models", api_access_enabled: true, configurable: false, defaults: { conversation: "conversation" } }, pairlink: { enabled: false, hub_url: origin, redeem_url: origin + "/pairlink/v1/pairings/redeem", info_url: origin + "/pairlink/v1/info", websocket_url: origin.replace("https:", "wss:") + "/pairlink/v1/ws" }, heartbeat_interval: 30, presence_ttl: 90 } : []
    await route.fulfill({ json: { data } })
  })
  return () => modelRequests
}

test("debug mode switch works with a hidden Gateway catalog", async ({ page }, info) => {
  const modelRequests = await hiddenGateway(page)
  await page.goto("/")
  const portrait = page.getByTestId("account-portrait")
  await expect(portrait).toHaveAttribute("aria-label", user.username)
  await portrait.tap(); await portrait.tap(); await portrait.tap()
  await expect.poll(() => page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("local")
  await expect(page.getByText("无法加载服务器模型，可刷新重试。", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "服务器模型", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "其他方式", exact: true }).click()
  await page.getByRole("button", { name: "连接模型", exact: true }).click()
  await expect(page.getByRole("dialog")).toBeVisible()
  expect(modelRequests()).toBeGreaterThan(0)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("zwai.phone.gateway.session") || "null")?.user.id)).toBe(user.id)
  await page.screenshot({ path: info.outputPath("local-hidden-catalog.png") })
})


test("debug mode switch closes an open User center and preserves PC selection", async ({ page }, info) => {
  const modelRequests = await hiddenGateway(page, true)
  await page.goto("/?mock=1&tick=0")
  const input = page.getByLabel("消息", { exact: true })
  await expect(input).toBeVisible()
  const selectedPC = await page.evaluate(() => localStorage.getItem("zwai.remote.active"))
  for (const next of ["local", "account"]) {
    await page.getByRole("button", { name: "用户中心", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "用户中心", exact: true })
    const portrait = dialog.getByTestId("account-portrait")
    await portrait.tap(); await portrait.tap(); await portrait.tap()
    await expect.poll(() => page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe(next)
    await expect(dialog).toHaveCount(0)
    await expect(input).toBeVisible()
    await expect(input).toBeInViewport()
    expect(await page.evaluate(() => localStorage.getItem("zwai.remote.active"))).toBe(selectedPC)
  }
  expect(modelRequests()).toBeGreaterThan(0)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("zwai.phone.gateway.session") || "null")?.user.id)).toBe(user.id)
  await input.fill("切换后的输入验证")
  await expect(input).toHaveValue("切换后的输入验证")
  // Capture the completed page transition, not its transparent starting frame.
  await page.locator(".screen-push").evaluate(async element => {
    await Promise.all(element.getAnimations().map(animation => animation.finished))
  })
  await page.screenshot({ path: info.outputPath("mode-switch-pc-selection.png") })
})

test("three avatar taps switch the selected PC and expose both interface groups in its composer", async ({ page }, info) => {
  await hiddenGateway(page, true)
  await page.goto("/?mock=1&tick=0&pc-models=managed")
  await expect(page.getByLabel("消息", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "模型", exact: true })).toHaveCount(0)
  const selected = await page.evaluate(() => localStorage.getItem("zwai.remote.active"))
  const toggle = async () => {
    await page.getByRole("button", { name: "用户中心", exact: true }).click()
    const portrait = page.getByRole("dialog", { name: "用户中心", exact: true }).getByTestId("account-portrait")
    await portrait.tap(); await portrait.tap(); await portrait.tap()
  }
  await toggle()
  await expect.poll(() => page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("local")
  await page.getByRole("button", { name: "模型", exact: true }).click()
  const picker = page.getByRole("dialog", { name: "模型", exact: true })
  for (const name of ["第三方接口 · Partner", "原始接口 · Main"]) await expect(picker.getByRole("group", { name, exact: true }).getByRole("radio", { name: "shared-model", exact: true })).toBeVisible()
  await page.screenshot({ path: info.outputPath("pc-local-interface-groups.png") })
  await picker.getByRole("group", { name: "原始接口 · Main", exact: true }).getByRole("radio", { name: "shared-model", exact: true }).click()
  await page.getByRole("button", { name: "模型", exact: true }).click()
  await expect(picker.getByRole("group", { name: "原始接口 · Main", exact: true }).getByRole("radio", { name: "shared-model", exact: true })).toHaveAttribute("aria-checked", "true")
  await picker.getByRole("button", { name: "刷新模型", exact: true }).click()
  for (const name of ["第三方接口 · Partner", "原始接口 · Main"]) await expect(picker.getByRole("group", { name, exact: true })).toBeVisible()
  await picker.getByRole("button", { name: "关闭", exact: true }).click()
  await toggle()
  await expect.poll(() => page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("account")
  await expect(page.getByRole("button", { name: "模型", exact: true })).toHaveCount(0)
  expect(await page.evaluate(() => localStorage.getItem("zwai.remote.active"))).toBe(selected)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("zwai.phone.gateway.session") || "null")?.user.id)).toBe(user.id)
})

test("a PC mode refusal retains Account and offers an explicit retry without losing the PC", async ({ page }, info) => {
  await hiddenGateway(page, true)
  await page.goto("/?mock=1&tick=0&pc-models=managed&pc-mode-refusal=backup_busy")
  await expect(page.getByLabel("消息", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "用户中心", exact: true }).click()
  const portrait = page.getByTestId("account-portrait")
  await portrait.tap(); await portrait.tap(); await portrait.tap()
  await expect(page.getByRole("alert")).toHaveText("当前电脑正忙，请等待模型调用或恢复结束后重试切换。")
  expect(await page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBeNull()
  await page.evaluate(() => history.replaceState(null, "", "?mock=1&tick=0&pc-models=managed"))
  await portrait.tap(); await portrait.tap(); await portrait.tap()
  await expect.poll(() => page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("local")
  await expect(page.getByRole("button", { name: "模型", exact: true })).toBeVisible()
  await page.screenshot({ path: info.outputPath("pc-mode-retry.png") })
})
