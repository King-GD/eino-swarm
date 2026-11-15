import { expect, test } from "@playwright/test"
import { writeFile } from "node:fs/promises"

for (const language of [
  { locale: "en", mode: "Goal mode", pursuing: "Pursuing", back: "Back", open: "Open", files: "Outputs", agents: "Agents (1)" },
  { locale: "zh", mode: "目标模式", pursuing: "进行中", back: "返回", open: "打开", files: "产出物", agents: "子 Agent (1)" },
]) {
  test(`${language.locale}: goal stays visible while navigating the conversation`, async ({ page }, info) => {
    await page.addInitScript((locale) => localStorage.setItem("zwai.phone.locale", locale), language.locale)
    await page.goto("/?mock=1&tick=0&history=goal-active")
    await page.getByRole("button", { name: language.back, exact: true }).click()
    await page.getByRole("button", { name: `${language.open} Trim the layout pass`, exact: true }).first().click()
    const banner = page.getByTestId("goal-banner")
    const check = async (name: string) => {
      await expect(banner).toBeInViewport()
      await expect(banner).toContainText(language.mode)
      await expect(banner).toContainText(language.pursuing)
      await expect(banner).toContainText("Verify the required artifact.")
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      await writeFile(info.outputPath(`${name}.png`), await page.screenshot({ animations: "disabled" }), { mode: 0o600 })
    }
    await check("chat")
    await page.getByRole("button", { name: language.files, exact: true }).click()
    await expect(page.getByTestId("phone-file-browser")).toBeVisible()
    await check("files")
    await page.getByRole("button", { name: language.back, exact: true }).click()
    await page.getByRole("button", { name: language.agents, exact: true }).click()
    await check("agents")
    await page.getByTestId("agent-roster").getByRole("button").click()
    await check("worker")
  })
}
