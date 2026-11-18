import { expect, test, type Locator } from "@playwright/test"

import { easmSnapshots, startEasmFixture } from "./easm-fixture"
import { composer, freshConversation, reloadOpenConversation, waitForIdle } from "./session"

type GraphStage = HTMLDivElement & { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

async function nodePoint(stage: Locator, id: string) {
  const point = await stage.evaluate((element, nodeId) => (element as GraphStage).__easmNodePoint?.(nodeId), id)
  expect(point).toBeTruthy()
  return point!
}

async function waitForViewport(stage: Locator, id: string) {
  await stage.evaluate((element, nodeId) => new Promise<void>((resolve) => {
    let previous = ""
    let settledFrames = 0
    const sample = () => {
      const graph = element as GraphStage
      const current = JSON.stringify([graph.dataset.zoom, graph.__easmNodePoint?.(nodeId)])
      settledFrames = current === previous ? settledFrames + 1 : 0
      previous = current
      if (settledFrames === 3) resolve()
      else requestAnimationFrame(sample)
    }
    requestAnimationFrame(sample)
  }), id)
}

test("three cumulative tool snapshots update one portrait and restore the latest data after reload", async ({ page, request }, info) => {
  test.skip(process.env.ZWAI_E2E_EASM !== "1", "requires the keyless loopback model fixture")
  test.setTimeout(120_000)
  const { settings } = await (await request.get("/api/settings")).json()
  const fixture = await startEasmFixture()
  let threadId = ""
  try {
    expect((await request.put("/api/settings", { data: {
      models: { default: "easm-fixture", providers: [{ id: "easm-fixture", label: "EASM fixture", base_url: fixture.url, model: "easm-fixture", api_key: "" }] },
      swarm: { ...settings.swarm, auto_title: false, composer_predictions: false },
      memory: { ...settings.memory, auto_review: false },
    } })).ok()).toBe(true)
    await freshConversation(page)
    threadId = (await page.locator('[data-testid="thread-row"][aria-current="true"]').getAttribute("data-id")) ?? ""
    expect(threadId).toBeTruthy()
    await composer(page).fill("Publish the available portrait results as they arrive.")
    await composer(page).press("Enter")

    const panel = page.getByTestId("easm-task-panel")
    await expect(page.getByRole("tab", { name: "Task panels", exact: true })).toHaveAttribute("aria-selected", "true")
    await expect(panel).toHaveAttribute("data-revision", /\d+/)
    await expect(page.getByTestId("task-panel-frame")).toHaveCount(0)
    await expect.poll(() => fixture.state().receipts["easm-read"]?.revision).toBeGreaterThan(0)
    const initialRevision = fixture.state().receipts["easm-read"].revision
    const initial = await (await request.get(`/api/threads/${threadId}/panels`)).json()
    expect(initial.panels).toHaveLength(1)
    const panelId = initial.panels[0].id as string
    expect(initial.panels[0]).toMatchObject({ plugin_id: "easm-asset-portrait", state: { nodes: [], edges: [] } })

    fixture.releaseBatch(1)
    const stage = panel.getByTestId("easm-graph").first()
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 1))
    await expect(stage).toHaveAttribute("data-ready", "true")
    await expect(stage).toHaveAttribute("data-selected-id", "root|sample")
    await expect(panel.getByText("示例集团", { exact: true })).toBeVisible()
    await stage.locator("canvas").first().evaluate((canvas) => Object.assign(window, { originalEasmCanvas: canvas }))
    fixture.releaseBatch(2)
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 2))
    await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
    await expect.poll(() => stage.evaluate((element) => Boolean((element as GraphStage).__easmNodePoint?.("domain|example.test")))).toBe(true)
    await panel.getByRole("button", { name: "适配视图", exact: true }).first().click()
    await waitForViewport(stage, "domain|example.test")
    await stage.click({ position: await nodePoint(stage, "domain|example.test") })
    const card = panel.getByTestId("easm-node-card")
    await expect(card).toContainText("example.test")
    await expect(stage).toHaveAttribute("data-selected-id", "domain|example.test")
    const beforeZoom = await stage.getAttribute("data-zoom")
    const expectedZoom = Math.min(1.5, Number(beforeZoom) + 0.3)
    await panel.getByRole("button", { name: "放大关联网络", exact: true }).first().click()
    await expect.poll(async () => Number(await stage.getAttribute("data-zoom"))).toBe(expectedZoom)
    const zoom = await stage.getAttribute("data-zoom")
    expect(Number(zoom)).toBeGreaterThan(0)

    fixture.releaseBatch(3)
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 3))
    await expect(card).toContainText("第三批已复核的域名归属依据")
    await expect(stage).toHaveAttribute("data-selected-id", "domain|example.test")
    await expect(stage).toHaveAttribute("data-zoom", zoom!)
    await expect.poll(() => stage.evaluate((element) => Boolean((element as GraphStage).__easmNodePoint?.("feature|certificate")))).toBe(true)
    expect(await stage.locator("canvas").first().evaluate((canvas) => canvas === (window as unknown as { originalEasmCanvas: HTMLCanvasElement }).originalEasmCanvas)).toBe(true)
    await waitForIdle(page)

    const saved = await (await request.get(`/api/threads/${threadId}/panels/${panelId}`)).json()
    expect(saved).toMatchObject({ id: panelId, revision: initialRevision + 3, state: easmSnapshots[2] })
    expect(Object.keys(fixture.state().receipts).filter((id) => id.startsWith("easm-batch-"))).toHaveLength(3)
    expect(fixture.errors).toEqual([])
    await page.screenshot({ path: info.outputPath("easm-third-snapshot.png") })

    await card.getByRole("button", { name: "关闭节点卡片" }).click()
    const heat = panel.getByTestId("easm-heat")
    await expect(panel.getByRole("img", { name: "资产热力趋势曲线" })).toBeVisible()
    await heat.hover()
    await expect(heat.locator(".recharts-tooltip-wrapper")).toHaveText(/^\d{4}-\d{2}-\d{2}$/)
    await expect(panel.getByTestId("easm-supply").getByRole("row")).toHaveCount(3)
    await panel.getByRole("button", { name: "查看NGINX标签依据" }).click()
    const evidence = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "NGINX", exact: true }) })
    await expect(evidence).toContainText("近 30 天有 6 天上榜")
    await expect(evidence).toContainText("已列入 KEV")
    await evidence.getByRole("button", { name: "Close", exact: true }).click()
    await panel.getByRole("button", { name: "展开资产图", exact: true }).first().click()
    const expanded = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "示例集团 · 资产图谱" }) })
    await expect(expanded.getByTestId("easm-graph")).toBeVisible()
    await expect(expanded.getByTestId("easm-heat")).toBeVisible()
    await expect(expanded.getByTestId("easm-supply").getByRole("row")).toHaveCount(3)
    await expanded.getByRole("button", { name: "证书", exact: true }).click()
    await expect(expanded.getByRole("button", { name: "证书", exact: true })).toHaveAttribute("aria-pressed", "false")

    await reloadOpenConversation(page)
    await page.getByRole("tab", { name: "Task panels", exact: true }).click()
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 3))
    await expect(panel.getByText("示例集团", { exact: true })).toBeVisible()
    await expect(panel.getByTestId("easm-supply").getByRole("row")).toHaveCount(3)
    await expect.poll(() => stage.evaluate((element) => Boolean((element as GraphStage).__easmNodePoint?.("feature|certificate")))).toBe(true)
    const recovered = await (await request.get(`/api/threads/${threadId}/panels/${panelId}`)).json()
    expect(recovered).toMatchObject({ id: panelId, revision: initialRevision + 3, state: easmSnapshots[2] })
    expect(fixture.state().requests).toBe(6)
    await page.screenshot({ path: info.outputPath("easm-recovered-after-reload.png") })
  } finally {
    await info.attach("easm-publication-receipts", { body: JSON.stringify({ ...fixture.state(), errors: fixture.errors }, null, 2), contentType: "application/json" })
    if (threadId) await request.post(`/api/threads/${threadId}/interrupt`, { timeout: 5000 }).catch(() => undefined)
    await request.put("/api/settings", { data: { models: settings.models, swarm: settings.swarm, memory: settings.memory } }).catch(() => undefined)
    await fixture.close()
  }
})
