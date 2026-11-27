import { expect, test } from "@playwright/test"

for (const theme of ["light", "dark"] as const) {
  test(`home uses FOFA surfaces and the current account name in ${theme} mode`, async ({ page }, info) => {
    await page.setViewportSize({ width: 887, height: 1176 })
    await page.addInitScript(mode => localStorage.setItem("zwai.theme", mode), theme)
    await page.route("**/api/meta", async route => {
      const response = await route.fetch()
      const meta = await response.json()
      await route.fulfill({ response, json: { ...meta, ui: { ...meta.ui, palette: "fofa" } } })
    })
    const remote = { enabled: true, hub_url: "https://example.test", auth_mode: "account" }
    await page.route("**/api/settings", async route => {
      if (route.request().method() !== "GET") return route.continue()
      const response = await route.fetch()
      const data = await response.json()
      await route.fulfill({ response, json: { ...data, settings: { ...data.settings, remote: { ...data.settings.remote, ...remote } } } })
    })
    await page.route("**/api/remote/status", route => route.fulfill({
      json: { ...remote, has_token: true, online: true,
        login: { state: "authorized", display_name: "Alex" } },
    }))
    await page.goto("/")
    const title = page.locator("#welcome-title")
    await expect(title).toHaveText("Hello, Alex")
    await expect(page.getByTestId("thread-title")).toHaveCount(0)
    const stage = page.getByTestId("composer-stage")
    const earth = stage.locator(".wb-home-earth")
    await expect(earth.locator("canvas")).toBeVisible()
    await expect(earth.locator("img")).toHaveCount(0)
    const bounds = await earth.boundingBox()
    const ideas = await stage.locator(".wb-ideas").boundingBox()
    expect(bounds!.y).toBeGreaterThanOrEqual(ideas!.y + ideas!.height - 1)
    const main = await stage.boundingBox()
    const greeting = await title.boundingBox()
    const lastIdea = await stage.locator(".wb-idea").last().boundingBox()
    const centerY = (greeting!.y + lastIdea!.y + lastIdea!.height) / 2
    expect(Math.abs(centerY - main!.y - main!.height / 2)).toBeLessThan(2)
    expect(Math.abs(bounds!.y + bounds!.height - main!.y - main!.height)).toBeLessThan(2)
    const corners = await page.locator(".wb-conversation-surface").evaluate(el => {
      const style = getComputedStyle(el)
      return [style.borderTopRightRadius, style.borderBottomRightRadius, style.marginRight]
    })
    expect(corners).toEqual(["0px", "0px", "0px"])
    const first = await earth.screenshot()
    await expect.poll(async () => (await earth.screenshot()).equals(first)).toBe(false)
    await page.emulateMedia({ reducedMotion: "reduce" })
    const still = await earth.screenshot()
    expect((await earth.screenshot()).equals(still)).toBe(true)
    const box = page.locator("[data-composer-box]")
    const edge = await box.evaluate(el => getComputedStyle(el).backgroundImage)
    expect(edge).toContain(theme === "light" ? "96deg, rgb(35, 132, 254)" : "96deg, rgb(5, 242, 242)")
    const send = box.getByRole("button", { name: "Send", exact: true })
    const glyph = send.locator(".wb-submit-icon")
    const ink = theme === "light" ? "rgb(5, 181, 182)" : "rgb(5, 242, 242)"
    await expect(page.locator("html")).toHaveAttribute("data-palette", "fofa")
    for (const width of [1524, 1525]) {
      await page.setViewportSize({ width, height: 1212 })
      await expect.poll(async () => {
        const rect = await glyph.boundingBox()
        return rect !== null && Number.isInteger(rect.x)
      }).toBe(true)
    }
    await page.setViewportSize({ width: 887, height: 1176 })
    await expect(send).toBeDisabled()
    await expect(send).toHaveCSS("opacity", "1")
    await expect(glyph).toHaveCSS("color", ink)
    await expect(box).toHaveCSS("box-shadow", "none")
    const frame = (await box.boundingBox())!
    const button = (await send.boundingBox())!
    expect([button.width, button.height]).toEqual([28, 28])
    expect(frame.x + frame.width - button.x - button.width).toBeCloseTo(19, 0)
    expect(frame.y + frame.height - button.y - button.height).toBeCloseTo(12, 0)
    await page.screenshot({ path: info.outputPath(`home-${theme}.png`), animations: "disabled" })
    await page.getByTestId("composer-input").fill("Check this workspace")
    await expect(send).toBeEnabled()
    await send.hover()
    await expect(send).toHaveCSS("background-color", "rgba(0, 0, 0, 0)")
    await expect(glyph).toHaveCSS("color", ink)
    await page.mouse.down()
    await expect(send).toHaveCSS("scale", "none")
    await expect(glyph).toHaveCSS("color", ink)
    await page.mouse.up()
    await expect(page.getByTestId("thread-title")).toBeVisible()
    await expect(title).toHaveCount(0)
    await expect(earth).toHaveCount(0)
  })
}

test("new conversation keeps the welcome surface mounted while its log loads", async ({ page }) => {
  await page.goto("/")
  const heading = page.locator("#welcome-title")
  const dock = page.getByTestId("composer-dock")
  await expect(heading).toBeVisible()
  const welcome = await heading.elementHandle()
  const before = await dock.boundingBox()
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route("**/api/threads/*/log?**", async route => { await pending; await route.continue() })
  const requested = page.waitForRequest("**/api/threads/*/log?**")
  await page.getByRole("button", { name: "New conversation", exact: true }).click()
  try {
    await requested
    await expect(dock).toHaveCSS("opacity", "1")
    await expect(page.getByTestId("transcript")).toHaveCount(0)
    await expect(page.getByTestId("side-panel")).toHaveCount(0)
    expect(await welcome!.evaluate(el => el.isConnected)).toBe(true)
    expect(await dock.boundingBox()).toEqual(before)
  } finally { release() }
  await expect(page.getByTestId("composer-input")).toBeEnabled()
  await expect(page.getByTestId("composer-input")).toBeFocused()
})

test("sidebar width reverses in place and respects reduced motion", async ({ page }) => {
  await page.setViewportSize({ width: 1807, height: 1214 })
  await page.emulateMedia({ reducedMotion: "no-preference" })
  await page.goto("/")
  const toggle = page.locator("[data-sidebar-toggle]")
  const list = page.getByTestId("conversation-list")
  const start = (await toggle.boundingBox())!
  const expanded = (await list.boundingBox())!.width
  await toggle.click()
  const midway = await list.evaluate(el => {
    const transition = el.getAnimations().find(a => a instanceof CSSTransition && a.transitionProperty === "width")
    if (!transition) return null
    transition.pause()
    transition.currentTime = Number(transition.effect!.getTiming().duration) / 2
    return el.getBoundingClientRect().width
  })
  expect(midway).not.toBeNull()
  expect(midway!).toBeLessThan(expanded)
  expect(midway!).toBeGreaterThan(40)
  await toggle.click()
  await expect.poll(async () => (await list.boundingBox())!.width).toBe(expanded)
  expect((await toggle.boundingBox())!.x).toBe(start.x)
  await page.emulateMedia({ reducedMotion: "reduce" })
  await toggle.focus()
  await page.keyboard.press("ControlOrMeta+b")
  await expect(toggle).toHaveAttribute("aria-expanded", "false")
  expect(await list.evaluate(el => el.getAnimations().length)).toBe(0)
  await expect(toggle).toBeFocused()
})
