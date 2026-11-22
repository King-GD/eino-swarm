import { expect, test, type Locator, type Page } from "@playwright/test"
import { readFileSync } from "node:fs"
import type { EasmResult } from "../src/lib/easm/types"

type GraphStage = HTMLDivElement & { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

const definition = JSON.parse(readFileSync(new URL("../src/task-panels/catalog.json", import.meta.url), "utf8"))
  .find((item: { id: string }) => item.id === "easm-asset-portrait")

/** Route the panel list and let the test push patches over the real SSE
 *  listener, so the renderer is exercised exactly as the producer feeds it. */
async function servePanel(page: Page, thread: string, state: EasmResult) {
  const saved = { id: "easm-e2e", thread_id: thread, plugin_id: definition.id, plugin_version: definition.version, definition, revision: 1, state }
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
  await page.route(`**/api/threads/${thread}/panels`, (route) => route.fulfill({ json: { panels: [saved], cursor: 1 } }))
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

// The producer stores structured evidence and step status only. The user must
// see the graph, evidence worded in their language, and a heat spinner that
// follows the saved step rather than a transient signal.
test("EASM renders the producer state, words evidence and follows step status", async ({ page }) => {
  const title = `EASM portrait ${Date.now()}`
  const created = await (await page.request.post("/api/threads", { data: { title } })).json()
  const thread = created.thread.id as string
  await servePanel(page, thread, portrait)
  await page.goto("/")
  await page.getByTestId("dest-chats").click()
  await page.getByRole("button", { name: title, exact: true }).click()
  const panel = page.getByTestId("easm-task-panel")
  await expect(panel).toHaveAttribute("data-revision", "1")
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
    cursor: 2, thread_id: thread, panel_id: "easm-e2e", revision: 2, base_revision: 1, kind: "patch",
    operations: [
      { op: "replace", path: "/collection/status", value: "done" },
      { op: "replace", path: "/collection/steps/2/status", value: "done" },
      { op: "replace", path: "/collection/steps/3/status", value: "done" },
      { op: "replace", path: "/heat", value: [{ date: "2026-01-05", value: 3 }, { date: "2026-01-12", value: 8 }] },
    ],
  })
  await expect(panel).toHaveAttribute("data-revision", "2")
  await expect(panel.getByRole("progressbar")).toHaveCount(0)
  await expect(panel.getByTestId("easm-heat")).toBeVisible()
  await expect(panel.getByText("No component data")).toBeVisible()
})

// An interrupted run (Stop, restart) must not leave a spinner after reload.
test("EASM shows no heat spinner for an interrupted collection", async ({ page }) => {
  const title = `EASM interrupted ${Date.now()}`
  const created = await (await page.request.post("/api/threads", { data: { title } })).json()
  const thread = created.thread.id as string
  await servePanel(page, thread, { ...portrait, collection: { status: "interrupted", steps: [{ id: "heat", status: "interrupted" }] } })
  await page.goto("/")
  await page.getByTestId("dest-chats").click()
  await page.getByRole("button", { name: title, exact: true }).click()
  const panel = page.getByTestId("easm-task-panel")
  await expect(panel.getByText("No attention trend data")).toBeVisible()
  await expect(panel.getByRole("progressbar")).toHaveCount(0)
})
