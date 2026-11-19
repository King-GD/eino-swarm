import { readFileSync } from "node:fs"
import { expect, test, type Locator } from "@playwright/test"
import type { EasmResult } from "../src/lib/easm/types"

import { easmCompletion, easmPrompt, easmSnapshots, startEasmFixture } from "./easm-fixture"
import { freshConversation, reloadOpenConversation, send, statusBadge, waitForIdle } from "./session"

type GraphStage = HTMLDivElement & { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

async function nodePoint(stage: Locator, id: string) {
  const point = await stage.evaluate((element, nodeId) => (element as GraphStage).__easmNodePoint?.(nodeId), id)
  expect(point).toBeTruthy()
  return point!
}

async function expectNodePainted(stage: Locator, id: string) {
  await expect(stage).toHaveCSS("opacity", "1")
  await expect.poll(() => stage.evaluate((element, nodeId) => {
    const point = (element as GraphStage).__easmNodePoint?.(nodeId)
    const canvas = element.querySelector("canvas")
    if (!point || !canvas || point.x < 10 || point.y < 10 ||
      point.x > element.clientWidth - 10 || point.y > element.clientHeight - 10) return false
    const scale = canvas.width / element.clientWidth
    const pixels = canvas.getContext("2d")!.getImageData(
      Math.floor((point.x - 10) * scale), Math.floor((point.y - 10) * scale),
      Math.ceil(20 * scale), Math.ceil(20 * scale),
    ).data
    // A node needs colored pixels at its visible point, beyond the pale links and text.
    for (let offset = 0; offset < pixels.length; offset += 4) {
      const channels = [pixels[offset], pixels[offset + 1], pixels[offset + 2]]
      if (pixels[offset + 3] > 0 && Math.max(...channels) - Math.min(...channels) > 40) return true
    }
    return false
  }, id), { message: `${id} is painted inside the visible graph` }).toBe(true)
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

test("the Tsinghua exposure inventory opens EASM and shows each batch before the conversation finishes", async ({ page, request }, info) => {
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
    const panel = page.getByTestId("easm-task-panel")
    await expect(panel).toHaveCount(0)
    await send(page, easmPrompt)
    await expect(page.getByRole("tab", { name: "Task panels", exact: true })).toHaveAttribute("aria-selected", "true")
    await expect(panel).toHaveAttribute("data-revision", /\d+/)
    await expect(panel.getByText("等待资产数据")).toBeVisible()
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
    await expect(panel.getByText("清华大学", { exact: true })).toBeVisible()
    await expect(statusBadge(page)).toContainText("Working")
    await expectNodePainted(stage, "root|sample")
    await page.screenshot({ path: info.outputPath("easm-root-before-followup.png") })
    await stage.locator("canvas").first().evaluate((canvas) => Object.assign(window, { originalEasmCanvas: canvas }))
    fixture.releaseBatch(2)
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 2))
    await expect(statusBadge(page)).toContainText("Working")
    await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
    await expect.poll(() => stage.evaluate((element) => Boolean((element as GraphStage).__easmNodePoint?.("domain|example.test")))).toBe(true)
    await expectNodePainted(stage, "domain|example.test")
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
    await expect(card.getByRole("heading", { name: "资产判断详情", exact: true })).toBeVisible()
    await expect(card).toContainText("第三批已复核的域名归属依据")
    await expect(stage).toHaveAttribute("data-selected-id", "domain|example.test")
    await expect(stage).toHaveAttribute("data-zoom", zoom!)
    await expect.poll(() => stage.evaluate((element) => Boolean((element as GraphStage).__easmNodePoint?.("feature|certificate")))).toBe(true)
    expect(await stage.locator("canvas").first().evaluate((canvas) => canvas === (window as unknown as { originalEasmCanvas: HTMLCanvasElement }).originalEasmCanvas)).toBe(true)
    await expect(statusBadge(page)).toContainText("Working")
    await expect(page.getByText(easmCompletion, { exact: true })).toHaveCount(0)
    await page.screenshot({ path: info.outputPath("easm-third-snapshot.png") })
    fixture.finish()
    await waitForIdle(page)
    await expect(page.getByText(easmCompletion, { exact: true })).toBeVisible()
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 3))

    const saved = await (await request.get(`/api/threads/${threadId}/panels/${panelId}`)).json()
    expect(saved).toMatchObject({ id: panelId, revision: initialRevision + 3, state: easmSnapshots[2] })
    expect(Object.keys(fixture.state().receipts).filter((id) => id.startsWith("easm-batch-"))).toHaveLength(3)
    expect(fixture.errors).toEqual([])

    await card.getByRole("button", { name: "*.example.test", exact: true }).click()
    await expect(card.getByRole("heading", { name: "*.example.test", exact: true })).toBeVisible()
    await card.getByRole("button", { name: "关闭节点卡片" }).click()
    await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
    const search = panel.getByRole("searchbox", { name: "搜索资产或特征", exact: true })
    await search.fill("*.example.test")
    await search.press("Enter")
    await waitForViewport(stage, "feature|certificate")
    const certificatePoint = await nodePoint(stage, "feature|certificate")
    // ForceGraph refreshes its hit canvas after viewport changes.
    await stage.hover({ position: certificatePoint })
    await expect(stage).toHaveCSS("cursor", "pointer")
    await stage.click({ position: certificatePoint })
    await expect(card.getByRole("heading", { name: "*.example.test", exact: true })).toBeVisible()
    await expect(card.getByRole("heading", { name: "资产判断详情", exact: true })).toBeVisible()
    await expect(card.getByRole("heading", { name: "有效特征", exact: true })).toBeVisible()
    await expect(card.getByText("sample-certificate", { exact: true })).toBeVisible()
    await expect(card.getByText("示例 CA", { exact: true })).toBeVisible()
    await expect(card).toContainText("2099-01-01")
    await expect(card.getByRole("button", { name: "example.test", exact: true })).toBeVisible()
    await expect(card.getByRole("table", { name: "服务观测", exact: true })).toHaveCount(0)
    await expect(card.getByText("未通过", { exact: true })).toHaveCount(0)
    await expect(card.getByText("未匹配", { exact: true })).toHaveCount(0)
    await expect(card.getByText("校验状态", { exact: true })).toHaveCount(0)
    await expect(card.getByText("匹配状态", { exact: true })).toHaveCount(0)
    await page.screenshot({ path: info.outputPath("easm-certificate-card.png") })
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
    const expanded = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "清华大学 · 资产图谱" }) })
    await expect(expanded.getByTestId("easm-graph")).toBeVisible()
    await expect(expanded.getByTestId("easm-heat")).toBeVisible()
    await expect(expanded.getByTestId("easm-supply").getByRole("row")).toHaveCount(3)
    const graphCard = expanded.getByRole("region", { name: "资产关联知识库" })
    const canvas = graphCard.locator("canvas").first()
    const cardBackground = () => graphCard.evaluate((card) => getComputedStyle(card).backgroundColor)
    await page.evaluate(() => document.documentElement.classList.remove("dark"))
    const lightBackground = await cardBackground()
    await expect(canvas).toHaveCSS("background-color", "rgba(0, 0, 0, 0)")
    await page.evaluate(() => document.documentElement.classList.add("dark"))
    await expect.poll(cardBackground).not.toBe(lightBackground)
    await expect(canvas).toHaveCSS("background-color", "rgba(0, 0, 0, 0)")
    const certificateFilter = expanded.getByRole("button", { name: "域名证书", exact: true })
    await certificateFilter.focus()
    await expect(page.getByRole("tooltip", { name: "域名证书" })).toBeVisible()
    await certificateFilter.press("Space")
    await expect(certificateFilter).toHaveAttribute("aria-pressed", "false")

    await reloadOpenConversation(page)
    await page.getByRole("tab", { name: "Task panels", exact: true }).click()
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 3))
    await expect(panel.getByText("清华大学", { exact: true })).toBeVisible()
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

test("EASM paints its subject root before any collection snapshot arrives", async ({ page, request }, info) => {
  test.skip(process.env.ZWAI_E2E_EASM !== "1", "requires the keyless loopback model fixture")
  const subject = easmSnapshots[0].nodes[0].label
  const fixture = await startEasmFixture([], subject)
  const { settings } = await (await request.get("/api/settings")).json()
  let threadId = ""
  try {
    expect((await request.put("/api/settings", { data: {
      models: { default: "easm-fixture", providers: [{ id: "easm-fixture", label: "EASM fixture", base_url: fixture.url, model: "easm-fixture", api_key: "" }] },
      swarm: { ...settings.swarm, auto_title: false, composer_predictions: false },
      memory: { ...settings.memory, auto_review: false },
    } })).ok()).toBe(true)
    await freshConversation(page)
    await send(page, easmPrompt)
    threadId = (await page.locator('[data-testid="thread-row"][aria-current="true"]').getAttribute("data-id")) ?? ""
    expect(threadId).toBeTruthy()
    const panel = page.getByTestId("easm-task-panel")
    await expect(panel).toBeVisible()
    await expect.poll(() => fixture.state().receipts["easm-read"]?.revision).toBeGreaterThan(0)
    const initial = await (await request.get(`/api/threads/${threadId}/panels`)).json()
    expect(initial.panels).toHaveLength(1)
    const saved = initial.panels[0]
    expect(saved.state.nodes).toHaveLength(1)
    expect(saved.state.nodes[0]).toMatchObject({ kind: "root", label: subject })
    expect(saved.state.edges).toEqual([])
    await expect(panel).toHaveAttribute("data-revision", String(saved.revision))
    await expectNodePainted(panel.getByTestId("easm-graph"), saved.state.nodes[0].id)
    await expect(panel.getByText("等待资产数据")).toHaveCount(0)
    await expect(statusBadge(page)).toContainText("Working")
    expect(Object.keys(fixture.state().receipts).filter((id) => id.startsWith("easm-batch-"))).toEqual([])
    expect(fixture.errors).toEqual([])
    await page.screenshot({ path: info.outputPath("easm-subject-root-before-collection.png") })
  } finally {
    if (threadId) await request.post(`/api/threads/${threadId}/interrupt`, { timeout: 5000 }).catch(() => undefined)
    await request.put("/api/settings", { data: { models: settings.models, swarm: settings.swarm, memory: settings.memory } }).catch(() => undefined)
    await fixture.close()
  }
})

test("EASM shows the root overview and lets asset cards navigate their ICP chain", async ({ page, request }, info) => {
  test.skip(process.env.ZWAI_E2E_EASM !== "1", "requires the keyless loopback model fixture")
  const company = { id: "company|icp-owner", kind: "company" as const, label: "示例企业", detail: { features: [
    { kind: "domain" as const, value: "example.test", node_id: "domain|icp-domain" },
  ] } }
  const icp = { id: "feature|icp", kind: "feature" as const, featureKind: "icp" as const, label: "京ICP备12345678号", detail: { subject_name: company.label } }
  const certOrg = { id: "feature|cert-org", kind: "feature" as const, featureKind: "org" as const, label: "Example Operations" }
  const logoBytes = readFileSync(new URL("./fixtures/preview-landscape.png", import.meta.url))
  const logo = `data:image/png;base64,${logoBytes.toString("base64")}`
  const introduction = "示例企业提供数据整理和协作服务。公开资料记录了它的产品团队和合作机构。"
  const root = { ...easmSnapshots[0].nodes[0], detail: {
    ...easmSnapshots[0].nodes[0].detail,
    logo, description: introduction,
    organizations: [{ name: company.label }], reason: "根节点历史判断文字", industry: ["历史行业字段"],
  } }
  const domain = { id: "domain|icp-domain", kind: "domain" as const, label: "example.test", detail: { features: [
    { kind: "domain" as const, value: "EXAMPLE.TEST.", node_id: "domain|icp-domain" },
    { kind: "icp" as const, value: icp.label, node_id: icp.id },
  ] } }
  const snapshot: EasmResult = {
    schema_version: 1, title: "EASM", nodes: [root, company, icp, domain, certOrg],
    edges: [
      { source: root.id, target: company.id, confirmed: false },
      { source: company.id, target: icp.id, confirmed: false },
      { source: icp.id, target: domain.id, confirmed: false },
      { source: root.id, target: certOrg.id, confirmed: false },
    ],
    summary: { clues: 5, fofa_assets: 42, fofa_ips: 6 },
    heat: [], components: [],
  }
  const fixture = await startEasmFixture([snapshot])
  const { settings } = await (await request.get("/api/settings")).json()
  let threadId = ""
  try {
    expect((await request.put("/api/settings", { data: {
      models: { default: "easm-fixture", providers: [{ id: "easm-fixture", label: "EASM fixture", base_url: fixture.url, model: "easm-fixture", api_key: "" }] },
      swarm: { ...settings.swarm, auto_title: false, composer_predictions: false },
      memory: { ...settings.memory, auto_review: false },
    } })).ok()).toBe(true)
    await freshConversation(page)
    await send(page, easmPrompt)
    threadId = (await page.locator('[data-testid="thread-row"][aria-current="true"]').getAttribute("data-id")) ?? ""
    expect(threadId).toBeTruthy()
    const panel = page.getByTestId("easm-task-panel")
    await expect.poll(() => fixture.state().receipts["easm-read"]?.revision).toBeGreaterThan(0)
    fixture.releaseBatch(1)
    const stage = panel.getByTestId("easm-graph")
    await expect(panel).toHaveAttribute("data-revision", String(fixture.state().receipts["easm-read"].revision + 1))
    await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
    for (const node of [root, company, icp, domain, certOrg]) await expectNodePainted(stage, node.id)
    const orgFilter = panel.getByRole("button", { name: "证书组织", exact: true })
    await expect(orgFilter.locator("span[aria-hidden]")).toHaveCSS("background-color", "rgb(226, 145, 18)")
    await orgFilter.hover()
    await expect(page.getByRole("tooltip", { name: "证书组织", exact: true })).toBeVisible()
    await orgFilter.press("Escape")
    await stage.click({ position: await nodePoint(stage, root.id) })
    const card = panel.getByTestId("easm-node-card")
    await expect(card.getByRole("heading", { name: root.label, exact: true })).toBeVisible()
    const expectRootProfile = async () => {
      const name = `${root.label}标识`
      const headerLogo = card.locator("header").getByRole("img", { name, exact: true })
      await expect(headerLogo).toBeVisible()
      await expect(headerLogo).toHaveAttribute("src", logo)
      await expect.poll(() => headerLogo.evaluate((element) => {
        const image = element as HTMLImageElement
        return [image.naturalWidth, image.naturalHeight]
      })).toEqual([1200, 800])
      await expect(card.locator('img[src^="data:image/png;base64,"]')).toHaveCount(1)
      await expect(card.getByRole("heading", { name: "简介", exact: true })).toBeVisible()
      await expect(card.getByText(introduction, { exact: true })).toBeVisible()
    }
    await expectRootProfile()
    await expect(card.locator("summary").filter({ hasText: "组织列表" })).toBeVisible()
    await expect(card.getByRole("button", { name: company.label, exact: true })).toBeVisible()
    await expect(card.getByRole("heading", { name: "有效特征", exact: true })).toBeVisible()
    const orgLink = card.getByRole("button", { name: certOrg.label, exact: true })
    await expect(orgLink).toBeVisible()
    await expect(orgLink.locator("span[aria-hidden]")).toHaveCSS("background-color", "rgb(226, 145, 18)")
    expect(await orgLink.locator("img").getAttribute("src")).toBe(await orgFilter.locator("img").getAttribute("src"))
    await expect(card.getByText("节点信息", { exact: true })).toHaveCount(0)
    await expect(card.getByText("FOFA 资产数", { exact: true })).toHaveCount(0)
    await expect(card.getByText("资产判断详情", { exact: true })).toHaveCount(0)
    await expect(card.getByText("根节点历史判断文字", { exact: true })).toHaveCount(0)
    await expect(card.getByText("历史行业字段", { exact: true })).toHaveCount(0)
    await page.screenshot({ path: info.outputPath("easm-root-card.png") })
    await card.getByRole("button", { name: "关闭节点卡片" }).click()
    await stage.click({ position: await nodePoint(stage, domain.id) })
    await expect(card.getByRole("heading", { name: domain.label, exact: true })).toBeVisible()
    await expect(card.getByRole("heading", { name: "资产判断详情", exact: true })).toBeVisible()
    await expect(card.getByRole("heading", { name: "有效特征", exact: true })).toBeVisible()
    await expect(card.getByRole("heading", { name: "简介", exact: true })).toHaveCount(0)
    await expect(card.locator("summary").filter({ hasText: "组织列表" })).toHaveCount(0)
    await expect(card.getByText("暂无已保存的判断依据", { exact: true })).toBeVisible()
    await expect(card.getByText("关联的 ICP 备案", { exact: true })).toHaveCount(0)
    await expect(card.locator("summary").filter({ hasText: "域名" })).toHaveCount(0)
    await card.getByRole("button", { name: icp.label, exact: true }).click()
    await expect(stage).toHaveAttribute("data-selected-id", icp.id)
    await expect(card.getByText("备案主体", { exact: true })).toBeVisible()
    await expect(card.getByRole("button", { name: domain.label, exact: true })).toBeVisible()
    await card.getByRole("button", { name: company.label, exact: true }).click()
    await expect(stage).toHaveAttribute("data-selected-id", company.id)
    await card.getByRole("button", { name: domain.label, exact: true }).click()
    await expect(stage).toHaveAttribute("data-selected-id", domain.id)
    await expect(card.locator("summary").filter({ hasText: "域名" })).toHaveCount(0)
    await card.getByRole("button", { name: icp.label, exact: true }).click()
    await expect(card.getByRole("heading", { name: icp.label, exact: true })).toBeVisible()
    const filter = panel.getByRole("button", { name: "ICP 备案", exact: true })
    const controls = panel.getByRole("group", { name: "关联网络操作" })
    await expect(filter).toHaveText("")
    await expect(panel.getByRole("button", { name: "只看当前关联" })).toHaveCount(0)
    const [filterRect, graphRect, controlsRect] = await Promise.all([filter.boundingBox(), stage.boundingBox(), controls.boundingBox()])
    expect(filterRect!.x).toBeGreaterThan(controlsRect!.x + controlsRect!.width)
    expect(filterRect!.y - graphRect!.y).toBeLessThan(20)
    await filter.hover()
    await expect(page.getByRole("tooltip", { name: "ICP 备案" })).toBeVisible()
    await filter.press("Escape")
    await expect(page.getByRole("tooltip", { name: "ICP 备案" })).toHaveCount(0)
    await filter.click()
    await expect(filter).toHaveAttribute("aria-pressed", "false")
    await expect(card).toHaveCount(0)
    await filter.click()
    await expect(filter).toHaveAttribute("aria-pressed", "true")
    await panel.getByRole("button", { name: "适配视图", exact: true }).click()
    await expectNodePainted(stage, icp.id)
    const saved = await (await request.get(`/api/threads/${threadId}/panels`)).json()
    expect(saved.panels[0].state).toEqual(snapshot)
    expect(fixture.errors).toEqual([])
    await page.screenshot({ path: info.outputPath("easm-domain-icp-details.png") })
    fixture.finish()
    await waitForIdle(page)
    await reloadOpenConversation(page)
    await page.getByRole("tab", { name: "Task panels", exact: true }).click()
    await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
    await expectNodePainted(stage, root.id)
    await stage.click({ position: await nodePoint(stage, root.id) })
    await expectRootProfile()
    await page.screenshot({ path: info.outputPath("easm-root-profile-after-reload.png") })
  } finally {
    if (threadId) await request.post(`/api/threads/${threadId}/interrupt`, { timeout: 5000 }).catch(() => undefined)
    await request.put("/api/settings", { data: { models: settings.models, swarm: settings.swarm, memory: settings.memory } }).catch(() => undefined)
    await fixture.close()
  }
})

test("EASM paints the sidebar after snapshots arrive while its tab is hidden", async ({ page, request }) => {
  test.skip(process.env.ZWAI_E2E_EASM !== "1", "requires the keyless loopback model fixture")
  const batch = easmSnapshots[1]
  const subject = batch.nodes.find((node) => node.kind === "root")!.label
  const fixture = await startEasmFixture([batch, { ...batch, summary: { ...batch.summary, fofa_assets: 36 } }])
  const { settings } = await (await request.get("/api/settings")).json()
  try {
    expect((await request.put("/api/settings", { data: {
      models: { default: "easm-fixture", providers: [{ id: "easm-fixture", label: "EASM fixture", base_url: fixture.url, model: "easm-fixture", api_key: "" }] },
      swarm: { ...settings.swarm, auto_title: false, composer_predictions: false },
      memory: { ...settings.memory, auto_review: false },
    } })).ok()).toBe(true)
    await freshConversation(page)
    await send(page, easmPrompt)
    const taskTab = page.getByRole("tab", { name: "Task panels", exact: true })
    const panel = page.getByTestId("easm-task-panel")
    await expect(taskTab).toHaveAttribute("aria-selected", "true")
    await expect(panel.getByText("等待资产数据")).toBeVisible()
    const initialRevision = Number(await panel.getAttribute("data-revision"))
    await page.getByRole("tab", { name: "Agents", exact: true }).click()
    await expect(panel).toBeHidden()
    fixture.releaseBatch(1)
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 1))
    fixture.releaseBatch(2)
    await expect(panel).toHaveAttribute("data-revision", String(initialRevision + 2))
    fixture.finish()
    await waitForIdle(page)

    await taskTab.click()
    const stage = panel.getByTestId("easm-graph")
    const expectPainted = async () => {
      await expect(stage).toHaveCSS("opacity", "1", { timeout: 3000 })
      await expect.poll(() => stage.evaluate((element) => {
        const canvas = element.querySelector("canvas")!
        const { width, height } = canvas
        return canvas.getContext("2d")!.getImageData(0, 0, width, height).data.some((channel) => channel > 0)
      })).toBe(true)
    }
    await expectPainted()
    await panel.getByRole("button", { name: "展开 EASM" }).click()
    const expanded = page.getByRole("dialog", { name: `${subject} · 资产图谱` })
    await expect(expanded.getByTestId("easm-graph")).toHaveCSS("opacity", "1")
    await expanded.getByRole("button", { name: "Close", exact: true }).click()
    await expectPainted()
    expect(fixture.errors).toEqual([])
  } finally {
    await request.put("/api/settings", { data: { models: settings.models, swarm: settings.swarm, memory: settings.memory } }).catch(() => undefined)
    await fixture.close()
  }
})
