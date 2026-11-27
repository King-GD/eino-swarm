import { expect, test } from "@playwright/test"
import { composer, freshConversation, reloadOpenConversation } from "./session"

test("switches the thinking level and keeps it after a reload", async ({ page }) => {
  await freshConversation(page)
  const level = page.getByLabel("Thinking level")
  // a fresh conversation starts on the model's own default
  await expect(level).toContainText("Default")

  await level.click()
  await page.getByRole("menuitemradio", { name: "High thinking" }).click()
  await expect(level).toContainText("High")

  // the choice is stored on the conversation; reload goes home, then the row brings it back
  await reloadOpenConversation(page)
  await expect(page.getByLabel("Thinking level")).toContainText("High")

  await level.click()
  await page.getByRole("menuitemradio", { name: "Thinking off", exact: true }).click()
  await expect(level).toHaveText("Thinking off")
  await reloadOpenConversation(page)
  await expect(level).toHaveText("Thinking off")
  await level.click()
  await page.getByRole("menuitemradio", { name: "Default thinking", exact: true }).click()
  await expect(level).toHaveText("Default thinking")
})

test("sets Thinking off before the first message from the home composer", async ({ page }) => {
  await page.goto("/")
  await expect(page.locator("#welcome-title")).toBeVisible()
  const level = page.getByLabel("Thinking level")
  await level.click()
  await page.getByRole("menuitemradio", { name: "Thinking off", exact: true }).click()
  await expect(level).toHaveText("Thinking off")
  await reloadOpenConversation(page)
  await expect(level).toHaveText("Thinking off")
})

test("switches the model in the composer and keeps it after a reload", async ({
  page,
  request,
}) => {
  const { settings } = await (await request.get("/api/settings")).json()
  const provider = settings.models.providers[0]
  await request.put("/api/settings", {
    data: {
      models: {
        default: settings.models.default,
        providers: [
          { ...provider, model: "alpha", catalog: ["alpha", "beta"] },
        ],
      },
    },
  })
  try {
    await freshConversation(page)
    const picker = page.getByLabel("Model")
    await expect(picker).toContainText("alpha")
    await picker.click()
    await expect(page.getByLabel("Search models")).toBeVisible()
    await expect(page.getByRole("button", { name: "Refresh models" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Edit providers" })).toBeVisible()
    await page.getByLabel("Search models").fill("beta")
    await page.getByRole("option", { name: /beta/ }).click()
    await expect(picker).toContainText("beta")
    await picker.click()
    await page.getByRole("button", { name: "Edit providers" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.getByRole("heading", { name: "Models", exact: true })).toBeVisible()
    const heading = (provider.label || provider.id).trim()
    await expect(
      dialog.getByRole("button", { name: `${heading} details` }),
    ).toBeVisible()
    await expect(dialog.getByRole("button", { name: "Add a provider" })).toBeVisible()
    await expect(dialog.getByRole("textbox", { name: "Provider" })).toHaveCount(0)
    await dialog.getByRole("button", { name: "Back to app" }).click()
    await reloadOpenConversation(page)
    await expect(page.getByLabel("Model")).toContainText("beta")
  } finally {
    await request.put("/api/settings", {
      data: { models: settings.models },
    })
  }
})


test("switches directly between all composer pickers without flashing the frame or losing the draft", async ({page}) => {
  await page.goto("/")
  const sidebar = page.getByTestId("conversation-list")
  const newChat = sidebar.getByRole("button", {name: "New conversation", exact: true})
  await expect(sidebar.getByRole("button").first()).toHaveAccessibleName("New conversation")
  await newChat.click()
  const input = composer(page)
  await input.fill("Keep this draft")
  await page.locator("#welcome-title").click()
  const frame = page.locator("[data-composer-box]")
  const surface = () => frame.evaluate(el => {
    const css = getComputedStyle(el)
    const rect = el.getBoundingClientRect()
    return [rect.x, rect.y, rect.width, rect.height, css.borderColor, css.outlineStyle, css.boxShadow]
  })
  const idle = await surface()
  await input.click()
  expect(await surface()).toEqual(idle)
  const pickers = {
    model: page.getByRole("button", {name: "Model", exact: true}),
    tools: page.getByTestId("composer-tools"),
    thinking: page.getByLabel("Thinking level"),
  }
  // Every directed pair, including the handoff from a menu to model search.
  for (const name of ["model", "tools", "thinking", "model", "thinking", "tools", "model"] as const) {
    await pickers[name].click()
    const panel = page.locator(`[data-composer-popup="${name}"]`)
    await expect(panel).toBeVisible()
    await expect(page.locator("[data-composer-popup]")).toHaveCount(1)
    if (name === "model") await expect(panel.getByRole("combobox")).toBeFocused()
    if (name === "tools") await expect(panel.getByRole("textbox")).toBeFocused()
    const menu = await panel.boundingBox()
    const draft = (await input.boundingBox())!
    const button = (await pickers[name].boundingBox())!
    const menuBottom = menu!.y + menu!.height
    expect(menuBottom).toBeGreaterThan(draft.y + draft.height / 2)
    expect(menuBottom).toBeLessThanOrEqual(button.y)
    expect(menu!.x).toBeLessThan(draft.x + draft.width)
    expect(menu!.x + menu!.width).toBeGreaterThan(draft.x)
    expect(await surface()).toEqual(idle)
    await expect(input).toHaveValue("Keep this draft")
  }
  await page.keyboard.press("Escape")
  await expect(pickers.model).toBeFocused()
  await pickers.thinking.click()
  await input.click({position: {x: 8, y: 8}})
  await expect(page.locator("[data-composer-popup]")).toHaveCount(0)
  await expect(input).toBeFocused()
  await pickers.tools.click()
  await pickers.tools.click()
  await expect(page.locator("[data-composer-popup]")).toHaveCount(0)
})

test("home has one input surface and the folded title bar shares its background", async ({ page }) => {
  await page.goto("/")
  const input = composer(page)
  await input.click()
  const frame = page.locator("[data-composer-box]")
  const toolbar = frame.locator(".composer-toolbar")
  expect(await toolbar.evaluate(el => getComputedStyle(el).borderTopWidth)).toBe("0px")
  const dock = await page.getByTestId("composer-dock").boundingBox()
  const box = await frame.boundingBox()
  expect(dock!.x).toBeCloseTo(box!.x, 0)
  expect(dock!.width).toBeCloseTo(box!.width, 0)
  const sidebar = page.getByTestId("conversation-list")
  expect(await sidebar.evaluate(el => getComputedStyle(el).borderRightWidth)).toBe("0px")
  const handle = sidebar.getByRole("separator")
  await handle.hover()
  expect(await handle.evaluate(el => getComputedStyle(el, "::after").width)).toBe("1px")
  expect((await handle.boundingBox())!.width).toBeGreaterThanOrEqual(8)
  const brand = sidebar.locator(".wb-brand")
  const newChat = sidebar.getByRole("button", { name: "New conversation", exact: true })
  await expect(brand).toBeVisible()
  const rect = await brand.boundingBox()
  expect(rect!.y + rect!.height).toBeLessThanOrEqual((await newChat.boundingBox())!.y)
  await page.getByRole("button", { name: "Hide conversations" }).click()
  const leading = page.getByTestId("titlebar-leading")
  await expect(leading).toHaveAttribute("data-expanded", "false")
  const colors = await leading.evaluate(el => [el, el.closest("header")!].map(n => getComputedStyle(n).backgroundColor))
  expect(colors[0]).toBe(colors[1])
  await expect(brand.locator("svg")).toBeVisible()
  const icon = (await brand.boundingBox())!
  expect(icon.y + icon.height).toBeLessThanOrEqual((await newChat.boundingBox())!.y)
})
