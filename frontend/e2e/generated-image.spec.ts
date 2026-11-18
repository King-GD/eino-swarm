import { readFile } from "node:fs/promises"
import { expect, test } from "@playwright/test"

import { composer, freshConversation, reloadOpenConversation } from "./session"

test.skip(process.env.ZWAI_E2E_IMAGE_GENERATION !== "1", "requires the isolated image gateway fixture")

test("a command animates then displays the original image after reopening", async ({ page, request }, info) => {
  await freshConversation(page)
  await composer(page).fill("/im")
  await page.getByTestId("slash-command-image").click()
  await expect(composer(page)).toHaveValue("/image ")
  await composer(page).fill("/image An abstract field of color")
  await composer(page).press("Enter")

  const card = page.getByTestId("image-generation")
  await expect(card).toHaveAttribute("data-status", "running")
  await expect(card).toContainText("Generating image")
  await page.screenshot({ path: info.outputPath("generating.png"), animations: "disabled" })

  const thumbnail = card.getByAltText("generated-image.png")
  await expect(thumbnail).toBeVisible()
  await expect(card).toHaveCount(1)
  await expect.poll(() => thumbnail.evaluate((img: HTMLImageElement) => [img.naturalWidth, img.naturalHeight])).toEqual([168, 117])
  await card.getByRole("button", { name: /enlarge/i }).click()
  const preview = page.getByRole("dialog", { name: "generated-image.png" })
  await expect(preview.getByRole("img", { name: "generated-image.png" })).toBeVisible()
  await page.keyboard.press("Escape")
  const saving = page.waitForEvent("download")
  await card.getByRole("link", { name: "Save image" }).click()
  const download = await saving
  expect(download.suggestedFilename()).toBe("generated-image.png")
  const source = await thumbnail.getAttribute("src")
  expect(source).toContain("/generated-images/")
  const saved = await request.get(source!)
  expect(saved.headers()["content-type"]).toBe("image/png")
  expect(await readFile((await download.path())!)).toEqual(await saved.body())

  await reloadOpenConversation(page)
  await expect(card).toHaveCount(1)
  await expect(card).toHaveAttribute("data-status", "completed")
  await expect.poll(() => thumbnail.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(168)
  await page.screenshot({ path: info.outputPath("generated-reloaded.png") })
})
