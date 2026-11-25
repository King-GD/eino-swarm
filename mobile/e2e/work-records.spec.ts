import { expect, test } from "@playwright/test"
import { writeFile } from "node:fs/promises"

test("paired phones open the saved work path and honor the PC setting", async ({ page }, info) => {
  await page.addInitScript(() => localStorage.setItem("zwai.phone.locale", "en"))
  await page.goto("/?mock=1&tick=0&work-records=on")
  const entry = page.getByRole("button", { name: "Work records", exact: true })
  await entry.click()
  const graph = page.getByRole("region", { name: "Shared work collaboration graph" })
  await expect(page.getByRole("button", { name: "This conversation" })).toHaveAttribute("aria-pressed", "true")
  await expect(graph.locator("[data-node-id]")).toHaveCount(3)
  expect(await graph.getByTestId("nuwa-edge").evaluateAll((edges) =>
    edges.map((edge) => [edge.getAttribute("data-from"), edge.getAttribute("data-to")]))
  ).toEqual([["task", "fact"], ["fact", "result"]])
  await expect(graph.getByRole("button", { name: /Inputs were verified/ })).toHaveText(/Project prerequisite/)
  await graph.getByRole("button", { name: /Review notes/ }).click()
  await expect(page.getByRole("region", { name: "Details and evidence" })).toContainText("Saved for this conversation.")
  await expect(page.getByRole("region", { name: "Details and evidence" })).toContainText("Saved review receipt")
  await writeFile(info.outputPath("phone-work-records.png"), await page.screenshot(), { mode: 0o600 })

  await page.getByRole("button", { name: "Entire project" }).click()
  await expect(graph.locator("[data-node-id]")).toHaveCount(2)
  await expect(graph.getByRole("button", { name: /Review notes/ })).toHaveCount(0)
  await page.getByRole("button", { name: "Back", exact: true }).click()
  await expect(page.getByTestId("transcript")).toBeVisible()
  await entry.click()
  await expect(graph.locator("[data-node-id]")).toHaveCount(3)

  await page.evaluate(() => {
    const url = new URL(location.href)
    url.searchParams.set("work-records", "off")
    history.replaceState(null, "", url)
  })
  await expect(graph).toHaveCount(0)
  await expect(entry).toHaveCount(0)
  await expect(page.getByTestId("transcript")).toBeVisible()
})
