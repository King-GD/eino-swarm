import { expect, test, type Locator, type Page } from "@playwright/test"
import { readFileSync } from "node:fs"
import type { EasmResult } from "../src/lib/easm/types"
import { firstCertificate, firstDomain, laterDomain, nextDomain, rootedEasmNodeIds, savedEasmGraph, updatedEasmGraph } from "./easm-graph-fixture"

type GraphStage = HTMLDivElement & { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

const definition = JSON.parse(readFileSync(new URL("../src/task-panels/catalog.json", import.meta.url), "utf8"))
  .find((item: { id: string }) => item.id === "easm-asset-portrait")

/** Route the panel list and let the test push patches over the real SSE
 *  listener, so the renderer is exercised exactly as the producer feeds it. */
async function servePanel(page: Page, thread: string, state: EasmResult, sourceStatus = "running") {
  const saved = { id: "easm-e2e", thread_id: thread, plugin_id: definition.id, plugin_version: definition.version, definition, revision: 1, state, source_status: sourceStatus }
  await page.addInitScript(() => {
    const Native = window.EventSource
    const active = new Set<EventSource>()
    class TrackedSource extends Native {
      constructor(url: string | URL, options?: EventSourceInit) { super(url, options); active.add(this) }
      close() { active.delete(this); super.close() }
    }
    window.EventSource = TrackedSource
    Object.assign(window, {
      emitPanelUpdate: (data: { thread_id: string }) => {
        for (const source of active) if (new URL(source.url).pathname === `/api/threads/${data.thread_id}/events`)
          source.dispatchEvent(new MessageEvent("panel_update", { data: JSON.stringify(data) }))
      },
    })
  })
  await page.route(`**/api/threads/${thread}/panels`, (route) => route.fulfill({ json: { panels: [saved], cursor: saved.revision } }))
  return saved
}

async function emit(page: Page, update: object) {
  await page.evaluate((data) => (window as unknown as { emitPanelUpdate: (value: unknown) => void }).emitPanelUpdate(data), update)
}

async function nodePoint(stage: Locator, id: string) {
  await expect.poll(() => stage.evaluate((element, nodeId) => Boolean((element as GraphStage).__easmNodePoint?.(nodeId)), id)).toBe(true)
  return (await stage.evaluate((element, nodeId) => (element as GraphStage).__easmNodePoint?.(nodeId), id))!
}

const portrait: EasmResult = {
  schema_version: 1, title: "Fixture Org", query: 'org="Fixture Org"',
  collection: { status: "running", steps: [{ id: "organizations", status: "done" }, { id: "summary", status: "done" }, { id: "heat", status: "running" }, { id: "components", status: "pending" }] },
  nodes: [
    { id: "root", kind: "root", label: "Fixture Org", detail: { organization_id: "41", description: "A sourced introduction" } },
    { id: "company:org:42", kind: "company", label: "Peer Org", detail: { basis: [{ source: "organizations" }] } },
    { id: "domain:fixture.test", kind: "domain", label: "fixture.test", detail: { basis: [{ source: "organizations", via: "Fixture Org" }, { source: "filings", via: "FILING-1", date: "2026-01-02T00:00:00Z" }] } },
  ],
  edges: [
    { source: "root", target: "company:org:42", confirmed: false },
    { source: "root", target: "domain:fixture.test", confirmed: true },
  ],
  summary: { clues: 2, fofa_assets: 120, fofa_ips: 9 },
  heat: [], components: [],
}

test("EASM shows the first certificate source and hides detached clues after reopening", async ({ page }) => {
  const title = `EASM saved graph ${Date.now()}`
  const response = await page.request.post("/api/threads", { data: { title } })
  await expect(response).toBeOK()
  const created = await response.json()
  const saved = await servePanel(page, created.thread.id, savedEasmGraph, "finished")
  const panel = page.getByTestId("easm-task-panel")

  async function openConversation() {
    await page.getByTestId("dest-chats").click()
    await page.getByRole("button", { name: title, exact: true }).click()
    await expect(panel).toHaveAttribute("data-revision", String(saved.revision))
  }

  const stage = panel.getByTestId("easm-graph")
  async function expectRootedGraph(includesNextBatch: boolean) {
    await expect(stage).toHaveAttribute("data-ready", "true")
    const ids = updatedEasmGraph.nodes.map(({ id }) => id)
    await expect.poll(() => stage.evaluate((element, nodes) => nodes.filter((id) => (element as GraphStage).__easmNodePoint?.(id)), ids))
      .toEqual(includesNextBatch ? [...rootedEasmNodeIds, nextDomain.id] : rootedEasmNodeIds)
  }

  await page.goto("/")
  await openConversation()
  await expectRootedGraph(false)
  await expect(panel.getByText("Asset attention trend", { exact: false })).toHaveCount(0)
  await expect(panel.getByText("Supply chain / third-party components", { exact: false })).toHaveCount(0)
  await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
  const card = panel.getByTestId("easm-node-card")
  await stage.click({ position: await nodePoint(stage, laterDomain.id) })
  await expect(card.getByRole("heading", { name: laterDomain.label, exact: true })).toBeVisible()
  await expect(card.getByText("No saved effective features")).toBeVisible()
  await expect(card.getByRole("button", { name: /certificate/i })).toHaveCount(0)
  await card.getByRole("button", { name: "Close node card" }).click()

  // Open the first source last, since opening the certificate zooms the graph.
  await stage.click({ position: await nodePoint(stage, firstDomain.id) })
  await expect(card.getByRole("heading", { name: firstDomain.label, exact: true })).toBeVisible()
  await card.getByRole("button", { name: firstCertificate.label, exact: true }).click()
  await expect(card.getByRole("heading", { name: firstCertificate.label, exact: true })).toBeVisible()
  await expect(card.getByText(firstCertificate.detail.serial, { exact: true })).toBeVisible()
  await card.getByRole("button", { name: "Close node card" }).click()

  saved.revision++
  saved.state = updatedEasmGraph
  await emit(page, {
    cursor: saved.revision, thread_id: saved.thread_id, panel_id: saved.id, revision: saved.revision, base_revision: saved.revision - 1,
    kind: "snapshot", state: saved.state, source_status: saved.source_status,
  })
  await expect(panel).toHaveAttribute("data-revision", "2")
  await expectRootedGraph(true)

  await page.reload()
  await openConversation()
  await expectRootedGraph(true)
})

// The producer stores structured evidence and step status only. The user must
// see matching loading indicators before the first data, then the graph and
// evidence worded in their language as the producer publishes results.
test("EASM renders the producer state, words evidence and follows step status", async ({ page }, info) => {
  const title = `EASM portrait ${Date.now()}`
  const created = await (await page.request.post("/api/threads", { data: { title } })).json()
  const thread = created.thread.id as string
  await servePanel(page, thread, {
    ...portrait, nodes: [], edges: [], summary: undefined,
    collection: { status: "running", steps: portrait.collection.steps.map((step) => ({ ...step, status: "pending" })) },
  })
  await page.goto("/")
  await page.getByTestId("dest-chats").click()
  await page.getByRole("button", { name: title, exact: true }).click()
  const panel = page.getByTestId("easm-task-panel")
  await expect(panel).toHaveAttribute("data-revision", "1")
  await expect(panel.getByRole("searchbox", { name: "Search assets or features" })).toHaveAttribute("placeholder", "Organization, domain or certificate")
  await expect(panel.getByRole("progressbar", { name: "Loading asset data", exact: true })).toBeVisible()
  await expect(panel.getByRole("progressbar", { name: "Loading asset attention trend" })).toBeVisible()
  await expect(panel.getByRole("progressbar", { name: "Loading supply chain components" })).toBeVisible()
  await expect(panel.getByText("Waiting for asset data")).toHaveCount(0)
  await panel.screenshot({ path: info.outputPath("easm-loading.png") })
  await panel.getByRole("button", { name: "Expand EASM", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("progressbar", { name: "Loading asset data", exact: true })).toBeVisible()
  await expect(dialog.getByRole("progressbar", { name: "Loading asset attention trend" })).toBeVisible()
  await expect(dialog.getByRole("progressbar", { name: "Loading supply chain components" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()

  await emit(page, { cursor: 2, thread_id: thread, panel_id: "easm-e2e", revision: 2, base_revision: 1, kind: "snapshot", state: portrait, source_status: "running" })
  await expect(panel).toHaveAttribute("data-revision", "2")
  await expect(panel.getByRole("progressbar", { name: "Loading asset data", exact: true })).toHaveCount(0)
  await expect(panel.getByText("120", { exact: true })).toBeVisible()
  await expect(panel.getByText("FOFA assets")).toBeVisible()
  await expect(panel.getByRole("progressbar", { name: "Loading asset attention trend" })).toBeVisible()
  await expect(panel.getByRole("progressbar", { name: "Loading supply chain components" })).toBeVisible()

  const stage = panel.getByTestId("easm-graph")
  await expect(stage).toHaveAttribute("data-ready", "true")
  await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
  const point = await nodePoint(stage, "domain:fixture.test")
  await stage.click({ position: point })
  const card = panel.getByTestId("easm-node-card")
  await expect(card.getByRole("heading", { name: "fixture.test" })).toBeVisible()
  await expect(card.getByText("From organization lookup: Fixture Org")).toBeVisible()
  await expect(card.getByText("From ICP filing: FILING-1 (2026-01-02)")).toBeVisible()
  await card.getByRole("button", { name: "Close node card" }).click()

  await emit(page, {
    cursor: 3, thread_id: thread, panel_id: "easm-e2e", revision: 3, base_revision: 2, kind: "patch", source_status: "finished",
    operations: [
      { op: "replace", path: "/collection/status", value: "done" },
      { op: "replace", path: "/collection/steps/2/status", value: "done" },
      { op: "replace", path: "/collection/steps/3/status", value: "done" },
      { op: "replace", path: "/heat", value: [{ date: "2026-01-05", value: 0 }, { date: "2026-01-12", value: 0 }] },
    ],
  })
  await expect(panel).toHaveAttribute("data-revision", "3")
  await expect(panel.getByRole("progressbar")).toHaveCount(0)
  await expect(panel.getByTestId("easm-heat")).toBeVisible()
  const heatRegion = { name: "Asset attention trend", exact: true }
  const supplyRegion = { name: "Supply chain / third-party components", exact: true }
  await expect(panel.getByRole("region", supplyRegion)).toHaveCount(0)
  await expect(panel.getByText("Supply chain / third-party components", { exact: false })).toHaveCount(0)

  await panel.getByRole("button", { name: "Expand EASM", exact: true }).click()
  await expect(dialog.getByTestId("easm-heat")).toBeVisible()
  await expect(dialog.getByRole("region", supplyRegion)).toHaveCount(0)
  await expect(dialog.getByText("Supply chain / third-party components", { exact: false })).toHaveCount(0)

  // A later query can return components even when trend collection fails.
  await emit(page, {
    cursor: 4, thread_id: thread, panel_id: "easm-e2e", revision: 4, base_revision: 3, kind: "snapshot", source_status: "finished",
    state: {
      ...portrait,
      query: 'domain="fixture.test"',
      collection: { status: "done", steps: [{ id: "heat", status: "failed" }, { id: "components", status: "done" }] },
      components: [{ name: "Fixture component", count: 3 }],
    },
  })
  await expect(panel).toHaveAttribute("data-revision", "4")
  await expect(dialog.getByRole("region", heatRegion)).toHaveCount(0)
  await expect(dialog.getByText("Asset attention trend · Collection failed", { exact: true })).toBeVisible()
  await expect(dialog.getByRole("region", supplyRegion).getByText("Fixture component", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()
  await expect(panel.getByRole("region", heatRegion)).toHaveCount(0)
  await expect(panel.getByText("Asset attention trend · Collection failed", { exact: true })).toBeVisible()
  await expect(panel.getByRole("region", supplyRegion).getByText("Fixture component", { exact: true })).toBeVisible()
})

// An interrupted run must explain the missing metrics without reserving room
// for empty cards or presenting the result as a completed empty collection.
test("EASM explains an interrupted collection and gives the expanded graph the full width", async ({ page }) => {
  const title = `EASM interrupted ${Date.now()}`
  const created = await (await page.request.post("/api/threads", { data: { title } })).json()
  const thread = created.thread.id as string
  await servePanel(page, thread, {
    ...portrait, nodes: [], edges: [], summary: undefined,
    collection: { status: "interrupted", steps: portrait.collection.steps.map((step) => ({ ...step, status: "interrupted" })) },
  }, "interrupted")
  await page.goto("/")
  await page.getByTestId("dest-chats").click()
  await page.getByRole("button", { name: title, exact: true }).click()
  const panel = page.getByTestId("easm-task-panel")
  await expect(panel.getByText("No asset data", { exact: true })).toBeVisible()
  await expect(panel.getByRole("progressbar")).toHaveCount(0)
  await expect(panel.getByText("Asset attention trend · Collection interrupted", { exact: true })).toBeVisible()
  await expect(panel.getByText("Supply chain / third-party components · Collection interrupted", { exact: true })).toBeVisible()
  await expect(panel.getByRole("region", { name: "Asset attention trend", exact: true })).toHaveCount(0)
  await expect(panel.getByRole("region", { name: "Supply chain / third-party components", exact: true })).toHaveCount(0)

  await panel.getByRole("button", { name: "Expand EASM", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("Asset attention trend · Collection interrupted", { exact: true })).toBeVisible()
  await expect(dialog.getByText("Supply chain / third-party components · Collection interrupted", { exact: true })).toBeVisible()
  await expect(dialog.getByRole("region", { name: "Asset attention trend", exact: true })).toHaveCount(0)
  await expect(dialog.getByRole("region", { name: "Supply chain / third-party components", exact: true })).toHaveCount(0)
  const graph = dialog.getByRole("region", { name: "Asset relationship graph", exact: true })
  await expect(graph).toBeVisible()
  await expect.poll(async () => {
    const [graphBox, dialogBox] = await Promise.all([graph.boundingBox(), dialog.boundingBox()])
    return graphBox && dialogBox ? graphBox.width / dialogBox.width : 0
  }).toBeGreaterThan(0.9)
})

// An engine restart interrupts a source in the middle of a collection. The
// state it published can still say a step is running. The host's receipt ends
// that wait without overwriting a different step's recorded unavailability.
test("EASM stops spinning when the host reports the source interrupted mid-collection", async ({ page }) => {
  const title = `EASM restarted ${Date.now()}`
  const created = await (await page.request.post("/api/threads", { data: { title } })).json()
  const thread = created.thread.id as string
  await servePanel(page, thread, {
    ...portrait, nodes: [], edges: [], summary: undefined,
    collection: { status: "running", steps: [{ id: "heat", status: "running" }, { id: "components", status: "unavailable" }] },
  }, "interrupted")
  await page.goto("/")
  await page.getByTestId("dest-chats").click()
  await page.getByRole("button", { name: title, exact: true }).click()
  const panel = page.getByTestId("easm-task-panel")
  await expect(panel.getByText("No asset data", { exact: true })).toBeVisible()
  await expect(panel.getByRole("progressbar")).toHaveCount(0)
  await expect(panel.getByText("Asset attention trend · Collection interrupted", { exact: true })).toBeVisible()
  await expect(panel.getByText("Supply chain / third-party components · Source unavailable", { exact: true })).toBeVisible()
  await expect(panel.getByRole("region", { name: "Asset attention trend", exact: true })).toHaveCount(0)
  await expect(panel.getByRole("region", { name: "Supply chain / third-party components", exact: true })).toHaveCount(0)
})
