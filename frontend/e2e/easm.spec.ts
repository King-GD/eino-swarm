import { expect, test, type Locator, type Page } from "@playwright/test"

import { freshConversation } from "./session"

// The tab draws one fixed local file. Real enterprise results never enter the
// repository, so the case answers that request with its own fixture.
const dataset = {
  schema_version: 1,
  title: "示例集团资产画像",
  summary: { clues: 5, fofa_assets: 1200, fofa_ips: 120 },
  nodes: [
    { id: "root|x", kind: "root", label: "示例集团", ip_count: 1200, detail: { website: "https://example.test", region: "北京", industry: ["软件"] } },
    { id: "company|b", kind: "company", label: "示例科技", detail: { website: "https://tech.example.test" } },
    { id: "domain|example.test", kind: "domain", label: "example.test" },
    { id: "feature|cert", kind: "feature", featureKind: "cert", label: "*.example.test", detail: { serial: "0A1B2C3D4E5F", issuer: "示例 CA", not_before: "2026-01-01T00:00:00Z", not_after: "2027-01-01T00:00:00Z" } },
    { id: "feature|icp", kind: "feature", featureKind: "icp", label: "京ICP备12345678号", detail: { subject_name: "示例集团" } },
    { id: "feature|org", kind: "feature", featureKind: "org", label: "示例证书组织" },
  ],
  edges: [
    { source: "root|x", target: "company|b", confirmed: true },
    { source: "company|b", target: "domain|example.test", confirmed: true },
    { source: "domain|example.test", target: "feature|cert" },
    { source: "root|x", target: "feature|icp" },
    { source: "root|x", target: "feature|org" },
  ],
  heat: [{ date: "2026-11-10", value: 3 }, { date: "2026-11-17", value: 8 }],
  components: [
    { name: "nginx", display_name: "NGINX", count: 42, hot_days: 6, tags: [{ type: "hot", evidence: [{ title: "示例情报", url: "https://example.test/intel", date: "2026-11-12" }] }, { type: "kev", summary: "已列入 KEV" }] },
    { name: "openssl", count: 7 },
  ],
}

type Frame = { ready: string | null; visible: boolean }

/** Samples the graph once per animation frame, so a painted frame that is not
 *  fitted yet is caught: the stage stays transparent until its first fit. */
async function watchFrames(page: Page) {
  await page.evaluate(() => {
    const frames: Frame[] = []
    ;(window as unknown as { __easmFrames: Frame[] }).__easmFrames = frames
    const sample = () => {
      const stage = document.querySelector('[data-testid="easm-graph"]')
      if (stage) frames.push({ ready: stage.getAttribute("data-ready"), visible: getComputedStyle(stage).opacity !== "0" })
      requestAnimationFrame(sample)
    }
    requestAnimationFrame(sample)
  })
}

async function openEasm(page: Page) {
  await page.route("**/easm-data.json", (route) => route.fulfill({ json: dataset }))
  await freshConversation(page)
  await watchFrames(page)
  await page.getByRole("tab", { name: "示例集团资产画像" }).click()
  const stage = page.getByTestId("easm-graph")
  await expect(stage).toBeVisible()
  await expect(stage).toHaveAttribute("data-ready", "true")
  await expect(stage).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
  return stage
}

type GraphStage = HTMLDivElement & { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

/** A node's page coordinates, read from the drawn graph. */
async function nodePoint(stage: Locator, id: string) {
  const point = await stage.evaluate((element, id) => {
    const rect = element.getBoundingClientRect()
    const at = (element as GraphStage).__easmNodePoint?.(id)
    return at && { x: rect.left + at.x, y: rect.top + at.y }
  }, id)
  expect(point).toBeTruthy()
  return point!
}

/** Clicking a node opens its card without rebuilding the graph: a rebuild
 *  restarts the layout and the canvas briefly goes blank under the card. */
async function clickWithoutRebuild(page: Page, stage: Locator, id: string) {
  const layouts = await stage.getAttribute("data-layouts")
  const point = await nodePoint(stage, id)
  await page.mouse.move(point.x, point.y)
  await expect(stage).toHaveAttribute("data-visible-labels", "1")
  await page.mouse.click(point.x, point.y)
  await expect(page.getByTestId("easm-node-card")).toBeVisible()
  await expect(stage).toHaveAttribute("data-ready", "true")
  expect(await stage.getAttribute("data-layouts")).toBe(layouts)
}

test("draws the fixed local result and opens a node's details", async ({ page }) => {
  const stage = await openEasm(page)
  const heat = page.getByTestId("easm-heat")
  await expect(page.getByRole("img", { name: "资产热力趋势曲线" })).toBeVisible()
  // Hovering names the date; the daily figure never appears.
  await heat.hover()
  await expect(heat.locator(".recharts-tooltip-wrapper")).toHaveText(/^\d{4}-\d{2}-\d{2}$/)
  await expect(page.getByTestId("easm-supply").getByRole("row")).toHaveCount(3)
  // Names stay off the canvas until the pointer asks for one.
  await expect(stage).toHaveAttribute("data-visible-labels", "0")
  // The tab opens on the whole graph, every node inside the canvas.
  const box = (await stage.boundingBox())!
  for (const id of dataset.nodes.map((node) => node.id)) {
    const point = await nodePoint(stage, id)
    expect(point.x).toBeGreaterThan(box.x)
    expect(point.x).toBeLessThan(box.x + box.width)
    expect(point.y).toBeGreaterThan(box.y)
    expect(point.y).toBeLessThan(box.y + box.height)
  }

  // The tab never shows an unfitted graph: an unpainted stage waits until the
  // whole layout is fitted, so no frame shows a lone node near the origin.
  const frames = await page.evaluate(() => (window as unknown as { __easmFrames: Frame[] }).__easmFrames)
  const painted = frames.filter((frame) => frame.visible)
  expect(painted.length).toBeGreaterThan(0)
  for (const frame of painted) expect(frame.ready).toBe("true")

  // The root starts selected, so a different node is what changes the selection.
  await clickWithoutRebuild(page, stage, "domain|example.test")
  const card = page.getByTestId("easm-node-card")
  await expect(card).toContainText("主域名")
  await card.getByRole("button", { name: "关闭节点卡片" }).click()
  await expect(card).toHaveCount(0)

  const root = await nodePoint(stage, "root|x")
  await page.mouse.click(root.x, root.y)
  await expect(card).toBeVisible()
  await expect(card).toContainText("示例集团")
  await expect(card).toContainText("目标企业")
  await expect(card.getByRole("link", { name: /example\.test/ })).toHaveAttribute("href", "https://example.test")
  await card.getByRole("button", { name: "关闭节点卡片" }).click()
  await expect(card).toHaveCount(0)
})

test("the enlarged view keeps the graph, heat and supply side by side", async ({ page }) => {
  await openEasm(page)
  await page.getByRole("button", { name: "展开资产图" }).first().click()
  const dialog = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "示例集团 · 资产图谱" }) })
  await expect(dialog).toBeVisible()
  const graph = dialog.getByTestId("easm-graph")
  await expect(graph).toBeVisible()
  await expect(dialog.getByTestId("easm-heat")).toBeVisible()
  await expect(dialog.getByTestId("easm-supply").getByRole("row")).toHaveCount(3)

  await expect(graph).toHaveAttribute("data-ready", "true")
  await expect(graph).toHaveAttribute("data-layout-moving", "false", { timeout: 20_000 })
  await clickWithoutRebuild(page, graph, "company|b")
  await expect(dialog.getByTestId("easm-node-card")).toContainText("示例科技")
  await dialog.getByRole("button", { name: "关闭节点卡片" }).click()

  // Legend filters hide a kind and say so; the summary keeps the source counts.
  const cert = dialog.getByRole("button", { name: "证书", exact: true })
  await cert.click()
  await expect(cert).toHaveAttribute("aria-pressed", "false")
  await expect(dialog.getByText("5")).toBeVisible()
})

test("a component row explains its tag evidence", async ({ page }) => {
  await openEasm(page)
  await page.getByRole("button", { name: "查看NGINX标签依据" }).click()
  const evidence = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "NGINX" }) })
  await expect(evidence).toContainText("近 30 天有 6 天上榜")
  await expect(evidence).toContainText("已列入 KEV")
})
