import { test, expect } from "@playwright/test"

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
  await expect(page.getByLabel("消息", { exact: true })).toHaveCount(0)
  await page.screenshot({ path: info.outputPath("account-login.png") })
})

test("signed-in avatar toggles Local menus and returns to Account without changing PC mode", async ({ page }, info) => {
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
  await expect(page.getByRole("tab", { name: "对话", exact: true })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("button", { name: "模型", exact: true })).toHaveCount(0)
  const center = page.getByRole("button", { name: "用户中心", exact: true })
  await expect(center.locator("img")).toHaveAttribute("src", user.avatar_url)
  await page.getByRole("button", { name: "菜单", exact: true }).click()
  for (const name of ["模型", "添加 PC", "解除绑定"]) await expect(page.getByRole("menuitem", { name, exact: true })).toHaveCount(0)
  await page.getByRole("menuitem", { name: "用户中心", exact: true }).click()
  const portrait = page.getByTestId("account-portrait")
  await expect(portrait.locator("img")).toHaveAttribute("src", user.avatar_url)
  await portrait.tap()
  await portrait.tap()
  expect(await page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBeNull()
  await portrait.tap()
  expect(await page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("local")
  await page.getByRole("button", { name: "关闭", exact: true }).click()
  await page.getByRole("button", { name: "菜单", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: "模型", exact: true })).toBeVisible()
  await page.getByRole("menuitem", { name: "用户中心", exact: true }).click()
  await page.getByTestId("account-portrait").tap()
  await page.getByTestId("account-portrait").tap()
  await page.getByTestId("account-portrait").tap()
  expect(await page.evaluate(() => localStorage.getItem("zwai.phone.mode"))).toBe("account")
  await page.screenshot({ path: info.outputPath("account-portrait.png") })
  await page.getByRole("button", { name: "退出登录", exact: true }).click()
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible()
  await expect(page.getByLabel("消息", { exact: true })).toHaveCount(0)
})
