import { createHash } from "node:crypto"
import { expect, test } from "@playwright/test"

import { mockOutputFiles } from "../src/lib/mock-files"

test("PC attachments load only after tapping View image, including after reopening", async ({ page }, info) => {
  await page.goto("/?mock=1&tick=0&history=input-images")
  const back = page.getByRole("button", { name: "返回", exact: true })
  await back.click()
  const thread = page.getByTestId("project-stack").getByRole("button", { name: "打开 Sweep the unused exports", exact: true })
  const files = mockOutputFiles("?file-preview=tall-image")
  for (const visit of ["first-open", "reopen"]) {
    await thread.click()
    const images = page.getByTestId("transcript-image")
    await expect(images).toHaveCount(3)
    await expect(page.getByText("Screenshots from the PC", { exact: true })).toBeVisible()
    const buttons = images.getByRole("button", { name: "查看图片", exact: true })
    const pictures = images.getByRole("img", { name: "图片", exact: true })
    await expect(buttons).toHaveCount(3)
    await images.first().scrollIntoViewIfNeeded()
    await images.last().scrollIntoViewIfNeeded()
    await expect(pictures).toHaveCount(0)
    await expect(buttons).toHaveCount(3)
    await page.screenshot({ path: info.outputPath(visit + "-before-view.png"), animations: "disabled" })
    for (const [i, width, height, path] of [
      [0, 1200, 800, "outputs/landscape.png"],
      [1, 512, 4096, "outputs/tall.png"],
      [2, 1200, 800, "outputs/landscape.png"],
    ] as const) {
      const frame = images.nth(i)
      await frame.scrollIntoViewIfNeeded()
      await frame.getByRole("button", { name: "查看图片", exact: true }).tap()
      const picture = frame.getByRole("img", { name: "图片" })
      await picture.scrollIntoViewIfNeeded()
      await expect(picture).toBeInViewport({ ratio: 0.9 })
      await expect.poll(() => picture.evaluate((img: HTMLImageElement) => [img.naturalWidth, img.naturalHeight])).toEqual([width, height])
      const { mime, bytes } = await picture.evaluate(async (img: HTMLImageElement) => {
        const response = await fetch(img.currentSrc)
        return { mime: response.headers.get("Content-Type"), bytes: Array.from(new Uint8Array(await response.arrayBuffer())) }
      })
      expect(mime).toBe("image/png")
      expect(createHash("sha256").update(Buffer.from(bytes)).digest("hex"))
        .toBe(createHash("sha256").update(files[path].bytes).digest("hex"))
      await expect(pictures).toHaveCount(i + 1)
      await expect(buttons).toHaveCount(2 - i)
    }
    await page.screenshot({ path: info.outputPath(visit + ".png"), animations: "disabled" })
    await back.click()
  }
})
