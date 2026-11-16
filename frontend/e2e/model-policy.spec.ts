import { expect, test } from "@playwright/test"

test("functional model overrides save and restore without switching product mode", async ({ page, request }) => {
  const { settings } = await (await request.get("/api/settings")).json()
  const provider = settings.models.providers[0]
  const { models } = await (await request.get("/api/models")).json()
  const initial = models.find((row: { provider_id: string }) => row.provider_id === provider.id).model
  expect((await request.put("/api/settings", { data: { models: { bindings: { title: { provider_id: provider.id, model: initial } } } } })).ok()).toBeTruthy()
  try {
    await page.goto("/")
    await page.getByRole("button", { name: "Settings", exact: true }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByRole("tab", { name: "Models", exact: true }).click()
    await expect(dialog.getByLabel("Conversation title Model", { exact: true })).toHaveValue(initial)
    await dialog.getByLabel("Conversation title Model", { exact: true }).fill("role-choice")
    await dialog.getByLabel("Conversation title Model", { exact: true }).press("Tab")
    await expect.poll(async () => (await (await request.get("/api/settings")).json()).settings.models.bindings.title.model).toBe("role-choice")
    await dialog.getByRole("button", { name: "Use default", exact: true }).click()
    await expect.poll(async () => (await (await request.get("/api/settings")).json()).settings.models.bindings ?? {}).toEqual({})
    await expect(dialog.getByLabel("Conversation title Model", { exact: true })).toHaveCount(0)
    await expect(dialog.getByRole("combobox", { name: /^(Product|Execution) mode$/i })).toHaveCount(0)
  } finally {
    await request.put("/api/settings", { data: { models: { bindings: settings.models.bindings ?? {} } } })
  }
})

test("managed policy removes model settings and composer selection", async ({ page }) => {
  // This fixture exercises the UI projection. Direct mutation rejection is
  // covered separately through the real Go HTTP and phone RPC handlers.
  const policy = { mode: "gateway", configurable: false, ready: true, scope: "gateway:managed", defaults: { conversation: { provider_id: "gateway:managed", model: "account" } } }
  await page.route("**/api/meta", async route => {
    const data = await (await route.fetch()).json()
    await route.fulfill({ json: { ...data, model_policy: policy } })
  })
  await page.route("**/api/settings", async route => {
    const data = await (await route.fetch()).json()
    await route.fulfill({ json: { ...data, settings: { ...data.settings, model_policy: policy } } })
  })
  await page.goto("/")
  await expect(page.getByLabel("Model", { exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Settings", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("tab", { name: "Models", exact: true })).toHaveCount(0)
  await expect(dialog.getByText("Functional models", { exact: true })).toHaveCount(0)
})
