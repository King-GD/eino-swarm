import { readFileSync } from "node:fs"
import { expect, test, type Page } from "@playwright/test"

import { composer, waitForDraftSaved } from "./session"

const definition = JSON.parse(readFileSync(new URL("../src/task-panels/catalog.json", import.meta.url), "utf8"))[0]

async function instrumentStreams(page: Page) {
  await page.addInitScript(() => {
    const Native = window.EventSource
    const active = new Set<EventSource>(), constructed: string[] = []
    class TrackedSource extends Native {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options)
        active.add(this); constructed.push(String(url))
      }
      close() { active.delete(this); super.close() }
    }
    window.EventSource = TrackedSource
    Object.assign(window, {
      panelStreamInventory: () => ({ active: [...active].map((source) => source.url), constructed }),
      emitPanelUpdate: (data: { thread_id: string }) => {
        for (const source of active) if (new URL(source.url).pathname === `/api/threads/${data.thread_id}/events`)
          source.dispatchEvent(new MessageEvent("panel_update", { data: JSON.stringify(data) }))
      },
    })
  })
}

test("HTML map plugin applies patches without replacing its frame, zoom or existing points", async ({ page }, info) => {
  const title = `Map protocol ${Date.now()}`
  const created = await (await page.request.post("/api/threads", { data: { title } })).json()
  const thread = created.thread.id as string
  const emptyTitle = `${title} without a panel`
  expect((await page.request.post("/api/threads", { data: { title: emptyTitle } })).ok()).toBe(true)
  const asset = { id: "a", ip: "192.0.2.1", port: 443, protocol: "https", host: "one.example", country: "CN", region: "", city: "", longitude: 116.4, latitude: 39.9, url: "https://one.example/", status: "running" }
  let saved = {
    id: "map-protocol", thread_id: thread, plugin_id: definition.id, plugin_version: definition.version, definition, revision: 1,
    state: { collection: { phase: "collecting", received: 1, discovered: 1, completeness: "unknown" }, assets_by_id: { a: asset }, probe: { pending: 0, running: 1, responded: 0, no_response: 0, skipped: 0, interrupted: 0 }, results_by_id: {} },
  }
  await instrumentStreams(page)
  await page.route(`**/api/threads/${thread}/panels`, (route) => route.fulfill({ json: { panels: [saved], cursor: saved.revision } }))
  await page.goto("/")
  await page.getByTestId("dest-chats").click()
  await page.getByRole("button", { name: title, exact: true }).click()
  await expect(page.getByRole("tab", { name: "Task panels", exact: true })).toHaveAttribute("aria-selected", "true")
  const iframe = page.getByTestId("task-panel-frame")
  const map = page.frameLocator('[data-testid="task-panel-frame"]')
  await expect(map.locator("body")).toHaveAttribute("data-revision", "1")
  await expect(map.locator(".countries path")).toHaveCount(177)
  await expect(map.locator(".points circle")).toHaveCount(1)
  const countryBefore = await map.locator('[data-country="US"]').getAttribute("fill")
  const child = await (await iframe.elementHandle())!.contentFrame()
  expect(child).toBeTruthy()
  await child!.evaluate(() => Object.assign(window, { originalPoint: document.querySelector(".points circle"), originalScene: document.querySelector("#map>g") }))
  await map.locator("#map").hover()
  await page.mouse.wheel(0, -120)
  await expect(map.locator("#map")).toHaveAttribute("data-zoom", /scale\(1\./)
  const zoomBefore = await map.locator("#map").getAttribute("data-zoom")
  const second = { ...asset, id: "b", ip: "198.51.100.2", host: "two.example", country: "US", longitude: -74, latitude: 40.7, status: "responded" }
  const result = { status: "responded", status_code: 403, elapsed_ms: 15, finished_at: new Date().toISOString() }
  const operations = [
    { op: "replace", path: "/assets_by_id/a/status", value: "responded" },
    { op: "add", path: "/assets_by_id/b", value: second },
    { op: "add", path: "/results_by_id/a", value: result },
    { op: "add", path: "/results_by_id/b", value: result },
    { op: "replace", path: "/probe/running", value: 0 },
    { op: "replace", path: "/probe/responded", value: 2 },
    { op: "replace", path: "/collection/discovered", value: 2 },
  ]
  await page.evaluate((update) => (window as unknown as { emitPanelUpdate: (data: unknown) => void }).emitPanelUpdate(update), { cursor: 2, thread_id: thread, panel_id: saved.id, revision: 2, base_revision: 1, kind: "patch", operations })
  await expect(map.locator("body")).toHaveAttribute("data-revision", "2")
  await expect(map.locator(".points circle")).toHaveCount(2)
  expect(await map.locator('[data-country="US"]').getAttribute("fill")).not.toBe(countryBefore)
  expect(await map.locator("#map").getAttribute("data-zoom")).toBe(zoomBefore)
  expect(await child!.evaluate(() => {
    const stored = window as unknown as { originalPoint: Element; originalScene: Element }
    return stored.originalPoint === document.querySelector('.points circle[data-point="116.4,39.9"]') && stored.originalScene === document.querySelector("#map>g")
  })).toBe(true)
  await expect(map.locator("#details")).toContainText("HTTP 403")
  // A full-state recovery uses the same plugin instance and does not reset zoom.
  saved = { ...saved, revision: 3, state: { ...saved.state, collection: { ...saved.state.collection, phase: "finished" }, assets_by_id: { a: { ...asset, status: "responded" } }, probe: { ...saved.state.probe, running: 0, responded: 1 }, results_by_id: { a: result } } }
  await page.getByRole("tab", { name: "Agents", exact: true }).click()
  await page.evaluate((update) => (window as unknown as { emitPanelUpdate: (data: unknown) => void }).emitPanelUpdate(update), { cursor: 3, thread_id: thread, panel_id: saved.id, revision: 3, kind: "snapshot", state: saved.state })
  await expect(page.getByRole("tab", { name: "Agents", exact: true })).toHaveAttribute("aria-selected", "true")
  await page.getByRole("tab", { name: "Task panels", exact: true }).click()
  await expect(map.locator("body")).toHaveAttribute("data-revision", "3")
  await expect(map.locator(".points circle")).toHaveCount(1)
  expect(await map.locator("#map").getAttribute("data-zoom")).toBe(zoomBefore)
  expect((await (await iframe.elementHandle())!.contentFrame()) === child).toBe(true)
  await page.screenshot({ path: info.outputPath("fofa-map-incremental.png") })
  const stopped = { ...saved.state, assets_by_id: { a: asset }, probe: { ...saved.state.probe, responded: 0, running: 1 } }
  await page.evaluate((update) => (window as unknown as { emitPanelUpdate: (data: unknown) => void }).emitPanelUpdate(update), { cursor: 4, thread_id: thread, panel_id: saved.id, revision: 4, kind: "snapshot", source_status: "interrupted", state: stopped })
  await expect(map.locator("body")).toHaveAttribute("data-revision", "4")
  await expect(map.locator("#counters")).toContainText("1 interrupted")
  await expect(map.locator(".points circle")).toHaveClass("interrupted")
  expect(await map.locator("#map").getAttribute("data-zoom")).toBe(zoomBefore)

  saved = { ...saved, revision: 4, state: stopped }
  await page.getByRole("button", { name: emptyTitle, exact: true }).click()
  await expect(page.getByRole("tab", { name: "Agents", exact: true })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("tab", { name: "Task panels", exact: true })).toHaveCount(0)
  await expect(iframe).toHaveCount(0)

  await page.getByRole("button", { name: title, exact: true }).click()
  await expect(page.getByRole("tab", { name: "Task panels", exact: true })).toHaveAttribute("aria-selected", "true")
  await expect(map.locator("body")).toHaveAttribute("data-revision", "4")
})

test("two map windows leave HTTP connections available for durable draft saves", async ({ page, context, request }) => {
  const other = await context.newPage()
  try {
    for (const [index, target] of [page, other].entries()) {
      const title = `Panel connection budget ${index}-${Date.now()}`
      const { thread } = await (await request.post("/api/threads", { data: { title } })).json()
      await instrumentStreams(target)
      const panel = { id: `panel-${thread.id}`, thread_id: thread.id, plugin_id: definition.id, plugin_version: definition.version, definition, revision: 1, state: definition.initial_state }
      await target.route(`**/api/threads/${thread.id}/panels`, (route) => route.fulfill({ json: { panels: [panel], cursor: 1 } }))
      await target.goto("/")
      await target.getByTestId("dest-chats").click()
      await target.getByRole("button", { name: title, exact: true }).click()
      await expect(target.getByRole("tab", { name: "Task panels", exact: true })).toBeVisible()
    }
    // Both tabs hold their presence and chat SSE while the writes below reach
    // real HTTP and SQLite; no request route fulfills or bypasses draft saves.
    await composer(page).fill("First window persists while maps are subscribed.")
    await composer(other).fill("Second window persists while maps are subscribed.")
    await Promise.all([waitForDraftSaved(page), waitForDraftSaved(other)])
    for (const target of [page, other]) {
      const inventory = await target.evaluate(() => (window as unknown as { panelStreamInventory: () => { active: string[]; constructed: string[] } }).panelStreamInventory())
      expect(inventory.active.length).toBeLessThanOrEqual(2)
      expect(inventory.constructed.some((url) => url.includes("/panels/events"))).toBe(false)
      expect(inventory.active.some((url) => url.includes("panels=1"))).toBe(true)
      await expect(target.frameLocator('[data-testid="task-panel-frame"]').locator(".countries path")).toHaveCount(177)
    }
  } finally { await other.close() }
})
