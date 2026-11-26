import { expect, test, type Page, type TestInfo } from "@playwright/test"
import { writeFile } from "node:fs/promises"
import { firstCertificate, firstDomain, laterDomain, nextDomain, rootedEasmNodeIds, savedEasmGraph, updatedEasmGraph } from "../../frontend/e2e/easm-graph-fixture"

type GraphStage = HTMLDivElement & { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

test.use({ screenshot: "only-on-failure", trace: "retain-on-failure" })

async function control(page: Page, kind: "show" | "patch" | "snapshot" | "remove", panelId = "mobile-map", state?: unknown) {
  await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("zwai.mock.task-panel", { detail })), { kind, panel_id: panelId, state })
}
async function capture(page: Page, info: TestInfo, name: string) {
  await writeFile(info.outputPath(name), await page.screenshot(), { mode: 0o600 })
}
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("zwai.phone.locale", "en"))
  await page.setViewportSize({ width: 390, height: 844 })
})

test("paired EASM shows the first certificate source and hides detached clues after reopening", async ({ page }) => {
  test.setTimeout(60_000)
  await page.goto("/?mock=1&tick=0&panels=easm")
  await expect(page.getByTestId("transcript")).toBeVisible()
  await control(page, "snapshot", "mobile-easm", savedEasmGraph)
  const sheet = page.getByRole("dialog", { name: "Task panels" })
  const panel = sheet.getByTestId("easm-task-panel")
  const stage = panel.getByTestId("easm-graph")
  const card = panel.getByTestId("easm-node-card")
  async function expectRootedGraph(includesNextBatch: boolean) {
    await expect(stage).toHaveAttribute("data-ready", "true")
    const ids = updatedEasmGraph.nodes.map(({ id }) => id)
    await expect.poll(() => stage.evaluate((element, nodes) => nodes.filter((id) => (element as GraphStage).__easmNodePoint?.(id)), ids))
      .toEqual(includesNextBatch ? [...rootedEasmNodeIds, nextDomain.id] : rootedEasmNodeIds)
  }
  async function openDomain(domain: typeof firstDomain) {
    const point = await stage.evaluate((element, id) => (element as GraphStage).__easmNodePoint?.(id), domain.id)
    if (!point) throw new Error(`Missing canvas position for ${domain.id}`)
    await stage.click({ position: point })
    await expect(card.getByRole("heading", { name: domain.label, exact: true })).toBeVisible()
  }

  await expect(panel).toHaveAttribute("data-revision", "1")
  await expectRootedGraph(false)
  await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
  await openDomain(laterDomain)
  await expect(card.getByText("No saved effective features")).toBeVisible()
  await expect(card.getByRole("button", { name: /certificate/i })).toHaveCount(0)
  await card.getByRole("button", { name: "Close node card" }).click()

  // Opening the certificate zooms the graph, so inspect its source last.
  await openDomain(firstDomain)
  await card.getByRole("button", { name: firstCertificate.label, exact: true }).click()
  await expect(card.getByRole("heading", { name: firstCertificate.label, exact: true })).toBeVisible()
  await expect(card.getByText(firstCertificate.detail.serial, { exact: true })).toBeVisible()
  await card.getByRole("button", { name: "Close node card" }).click()

  await control(page, "snapshot", "mobile-easm", updatedEasmGraph)
  await expect(panel).toHaveAttribute("data-revision", "2")
  await expectRootedGraph(true)

  await sheet.getByRole("button", { name: "Close", exact: true }).click()
  await expect(sheet).toHaveCount(0)
  await page.getByRole("button", { name: "Task panels", exact: true }).click()
  await expect(panel).toHaveAttribute("data-revision", "2")
  await expectRootedGraph(true)
})

test("paired EASM hides successful empty metrics and explains failed collection", async ({ page }) => {
  await page.goto("/?mock=1&tick=0&panels=easm")
  await expect(page.getByTestId("transcript")).toBeVisible()
  await control(page, "snapshot", "mobile-easm", {
    ...savedEasmGraph,
    collection: { status: "done", steps: [{ id: "heat", status: "done" }, { id: "components", status: "done" }] },
  })
  const panel = page.getByRole("dialog", { name: "Task panels" }).getByTestId("easm-task-panel")
  const heatRegion = { name: "Asset attention trend", exact: true }
  const supplyRegion = { name: "Supply chain / third-party components", exact: true }
  await expect(panel).toHaveAttribute("data-revision", "1")
  await expect(panel.getByText("Asset attention trend", { exact: false })).toHaveCount(0)
  await expect(panel.getByText("Supply chain / third-party components", { exact: false })).toHaveCount(0)
  await expect(panel.getByText(/cloud provider/i)).toHaveCount(0)

  await control(page, "snapshot", "mobile-easm", {
    ...savedEasmGraph,
    collection: { status: "done", steps: [{ id: "heat", status: "failed" }, { id: "components", status: "done" }, { id: "clouds", status: "unavailable" }] },
    components: [{ name: "Fixture component", count: 3 }],
    clouds: [],
  })
  await expect(panel).toHaveAttribute("data-revision", "2")
  await expect(panel.getByRole("region", heatRegion)).toHaveCount(0)
  await expect(panel.getByText("Asset attention trend · Collection failed", { exact: true })).toBeVisible()
  await expect(panel.getByRole("region", supplyRegion).getByText("Fixture component", { exact: true })).toBeVisible()
  await expect(panel.getByRole("heading", { name: "Cloud providers", exact: true })).toHaveCount(0)
  await expect(panel.getByText("Cloud providers · Source unavailable", { exact: true })).toBeVisible()
})

test("a paired task opens its local map once and preserves touch zoom through patch, hidden snapshot and rotation", async ({ page }, info) => {
  const htmlRequests: string[] = []
  page.on("request", (request) => { if (request.url().includes("/task-panels/")) htmlRequests.push(request.url()) })
  await page.goto("/?mock=1&tick=0&panels=delayed")
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible()
  await expect(page.getByRole("button", { name: "Task panels", exact: true })).toHaveCount(0)
  await control(page, "show")
  const sheet = page.getByRole("dialog", { name: "Task panels" })
  await expect(sheet).toBeVisible()
  const iframe = sheet.getByTestId("task-panel-frame")
  const frame = page.frameLocator('[data-testid="phone-task-panels"] iframe')
  const map = frame.locator("#map"), scene = frame.locator('[data-testid="map-scene"]')
  await expect(frame.locator("body")).toHaveAttribute("data-revision", "1")
  await expect(frame.locator("#phase")).toContainText("3 collected")
  await expect(frame.locator(".countries path")).toHaveCount(177)
  await expect(frame.locator(".points circle")).toHaveCount(0)
  expect(await frame.locator('[data-country="CN"]').getAttribute("fill")).not.toBe(await frame.locator('[data-country="US"]').getAttribute("fill"))
  await iframe.evaluate((node) => { (window as unknown as { savedPanel: Element }).savedPanel = node })
  await scene.evaluate((node) => { (window as unknown as { savedScene: Element }).savedScene = node })
  const box = await map.boundingBox()
  expect(box).not.toBeNull()
  const x = box!.x + box!.width * .5, y = box!.y + Math.min(100, box!.height * .5)
  await page.touchscreen.tap(x, y)
  await page.touchscreen.tap(x, y)
  await expect(map).toHaveAttribute("data-zoom", /scale\(2\)/)
  const zoom = await map.getAttribute("data-zoom")
  await control(page, "patch")
  await expect(frame.locator("body")).toHaveAttribute("data-revision", "2")
  await expect(frame.locator(".points circle")).toHaveCount(1)
  await expect(frame.locator("#progress-label")).toContainText("1 / 3")
  await expect(map).toHaveAttribute("data-zoom", zoom!)
  expect(await scene.evaluate((node) => node === (window as unknown as { savedScene: Element }).savedScene)).toBe(true)
  await capture(page, info, "map-touch-patch-portrait.png")
  await sheet.getByRole("button", { name: "Close", exact: true }).click()
  await expect(sheet).toHaveCount(0)
  await control(page, "snapshot")
  // Cross the hidden-manifest poll; it must neither reopen nor render a queue.
  await page.waitForTimeout(1_700)
  await expect(page.getByRole("dialog", { name: "Task panels" })).toHaveCount(0)
  await page.getByRole("button", { name: "Task panels", exact: true }).click()
  await expect(frame.locator("body")).toHaveAttribute("data-revision", "3")
  await expect(frame.locator(".points circle")).toHaveCount(3)
  await expect(frame.locator("#progress-label")).toContainText("3 / 3")
  expect(await iframe.evaluate((node) => node === (window as unknown as { savedPanel: Element }).savedPanel)).toBe(true)
  await expect(map).toHaveAttribute("data-zoom", zoom!)
  await page.setViewportSize({ width: 844, height: 390 })
  await expect.poll(async () => (await iframe.boundingBox())?.width).toBeGreaterThan(800)
  await expect(map).toHaveAttribute("data-zoom", zoom!)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await capture(page, info, "map-snapshot-landscape.png")
  expect(htmlRequests).toHaveLength(1)
  expect(new URL(htmlRequests[0]).origin).toBe(new URL(page.url()).origin)
})

test("multiple panels retain their frames and Back returns to the conversation", async ({ page }, info) => {
  await page.goto("/?mock=1&tick=0&panels=multi")
  const sheet = page.getByRole("dialog", { name: "Task panels" })
  await expect(sheet.getByRole("combobox")).toBeVisible()
  await expect(sheet.locator("iframe")).toHaveCount(2)
  await sheet.getByRole("combobox").selectOption("mobile-map-second")
  await expect(sheet.locator("iframe:visible")).toHaveCount(1)
  await control(page, "snapshot", "mobile-map-second")
  await expect(page.frameLocator('[data-testid="phone-task-panels"] iframe:visible').locator("body")).toHaveAttribute("data-revision", "2")
  await capture(page, info, "multiple-panel-selected.png")
  await sheet.getByRole("button", { name: "Back", exact: true }).click()
  await expect(sheet).toHaveCount(0)
  await expect(page.getByTestId("transcript")).toBeVisible()
  await page.getByRole("button", { name: "Task panels", exact: true }).click()
  await expect(sheet.getByRole("combobox")).toHaveValue("mobile-map-second")
  await expect(sheet.locator("iframe")).toHaveCount(2)
})

test("a bundled version mismatch reports a local failure without loading plugin HTML", async ({ page }, info) => {
  const htmlRequests: string[] = []
  page.on("request", (request) => { if (request.url().includes("/task-panels/")) htmlRequests.push(request.url()) })
  await page.goto("/?mock=1&tick=0&panels=incompatible")
  const sheet = page.getByRole("dialog", { name: "Task panels" })
  await expect(sheet.getByRole("alert")).toContainText("incompatible")
  await expect(sheet.locator("iframe")).toHaveCount(0)
  expect(htmlRequests).toHaveLength(0)
  await capture(page, info, "version-refusal.png")
})

test("a failed initial body retains an entry and Retry can display the panel", async ({ page }) => {
  await page.goto("/?mock=1&tick=0&panels=failed")
  const entry = page.getByRole("button", { name: "Task panels", exact: true })
  const sheet = page.getByRole("dialog", { name: "Task panels" })
  await expect(entry).toBeVisible()
  // The next manifest may recover and auto-open the sheet before this click.
  await Promise.any([entry.click({ timeout: 5_000 }), sheet.waitFor()])
  await sheet.getByRole("button", { name: "Retry", exact: true }).click()
  await expect(page.frameLocator('[data-testid="phone-task-panels"] iframe').locator("body")).toHaveAttribute("data-revision", "1")
})

test("a complete multi-chunk panel renders from the paired RPC body", async ({ page }) => {
  await page.goto("/?mock=1&tick=0&panels=large")
  const frame = page.frameLocator('[data-testid="phone-task-panels"] iframe')
  await expect(frame.locator("body")).toHaveAttribute("data-revision", "1")
  await expect(frame.locator("#phase")).toContainText("3 collected")
  await expect(frame.locator(".countries path")).toHaveCount(177)
})

test("the phone closes the selected instance while keeping its paired conversation", async ({ page }) => {
  await page.goto("/?mock=1&tick=0&panels=delayed")
  await expect(page.getByTestId("transcript")).toBeVisible()
  await control(page, "show")
  const sheet = page.getByRole("dialog", { name: "Task panels" })
  const entry = page.getByRole("button", { name: "Task panels", exact: true })
  await expect(sheet).toBeVisible()
  await sheet.getByRole("button", { name: "Close", exact: true }).click()
  await expect(entry).toBeVisible()
  await entry.click()
  await sheet.getByRole("button", { name: "Close panel instance", exact: true }).click()
  await expect(sheet).toHaveCount(0)
  await expect(entry).toHaveCount(0)
  await expect(page.getByTestId("transcript")).toBeVisible()
})
