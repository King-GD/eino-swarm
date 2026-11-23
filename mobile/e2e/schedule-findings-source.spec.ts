import { expect, test } from "@playwright/test"

test("the paired phone identifies a scheduled result and opens its source conversation", async ({ page }, info) => {
  await page.addInitScript(() => localStorage.setItem("zwai.phone.locale", "en"))
  await page.goto("/?mock=1&tick=0&history=schedule-findings-source")
  const back = page.getByRole("button", { name: "Back", exact: true })
  await back.click()

  const attention = page.getByTestId("completion-attention")
  const project = page.getByTestId("project-stack")
  const source = (within: typeof project) => within.getByRole("button", { name: "Source · Summarise this week" })
  for (const section of [attention, project]) {
    await expect(section.getByText("Scheduled result · sch_project_monitor")).toBeVisible()
    await expect(source(section)).toBeVisible()
  }
  await page.screenshot({ path: info.outputPath("scheduled-result-phone.png"), animations: "disabled" })
  await source(project).click()
  await expect(back).toBeVisible()
  await expect(page.getByRole("heading", { name: "Summarise this week" })).toBeVisible()
})
