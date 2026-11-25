import { expect, test } from "@playwright/test"

test("phone browses the paired project wiki, follows a page, and opens its source", async ({ page }, info) => {
  await page.goto("/?mock=1&tick=0")
  await page.getByRole("button", { name: "返回", exact: true }).click()
  await page.getByRole("button", { name: "Platform的知识库", exact: true }).click()

  const wiki = page.getByRole("dialog", { name: "知识库" })
  await expect(wiki.getByRole("button", { name: /Design log/ })).toBeVisible()
  await wiki.getByRole("button", { name: "更多", exact: true }).click()
  await expect(wiki.getByRole("button", { name: /Project entry 32/ })).toBeVisible()

  const search = wiki.getByRole("searchbox", { name: "搜索页面" })
  await search.fill("no matching page")
  await expect(wiki.getByText("没有匹配的页面。")).toBeVisible()
  await search.fill("design")
  await expect(wiki.getByRole("button", { name: /Delivery notes/ })).toHaveCount(0)
  await wiki.getByRole("button", { name: /Design log/ }).click()
  await expect(wiki.getByRole("heading", { name: "Working notes" })).toBeVisible()
  await wiki.getByRole("button", { name: "delivery reference", exact: true }).click()
  await expect(wiki.getByText("The companion entry.")).toBeVisible()
  await wiki.getByRole("button", { name: "返回", exact: true }).click()

  await expect(wiki.getByRole("heading", { name: "Design log" })).toBeVisible()
  await expect(wiki.getByText("消息 #4 · 来源不可用")).toBeVisible()
  await wiki.getByText("原文摘录", { exact: true }).click()
  await expect(wiki.getByText("The reading view keeps the reference visible.")).toBeVisible()
  await page.setViewportSize({ width: 320, height: 720 })
  await page.screenshot({ path: info.outputPath("knowledge-source.png") })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320)

  await wiki.getByRole("button", { name: "事件 #12", exact: true }).click()
  await expect(wiki).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Trim the layout pass" })).toBeVisible()
  await page.getByRole("button", { name: "知识库", exact: true }).click()
  await expect(wiki.getByText("Platform", { exact: true })).toBeVisible()
  await expect(wiki.getByRole("button", { name: /Design log/ })).toBeVisible()
})

test("phone distinguishes a project without a wiki from an older PC", async ({ page }) => {
  await page.goto("/?mock=1&tick=0")
  await page.getByRole("button", { name: "返回", exact: true }).click()
  await page.getByRole("button", { name: "Field notes的知识库", exact: true }).click()
  await expect(page.getByText("此项目尚未启用知识库。")).toBeVisible()

  await page.goto("/?mock=1&tick=0&knowledge=legacy")
  await page.getByRole("button", { name: "返回", exact: true }).click()
  await page.getByRole("button", { name: "Platform的知识库", exact: true }).click()
  await expect(page.getByRole("alert")).toContainText("请更新配对电脑上的应用以浏览知识库。")
})
