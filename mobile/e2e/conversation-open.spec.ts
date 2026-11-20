import { expect, test, type Page } from "@playwright/test"

test("jumps between three turns and reads deferred work only when expanded", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("zwai.phone.locale", "en"))
  await page.goto("/?mock=1&tick=0&history=compact")
  await page.getByRole("button", { name: "Back", exact: true }).click()
  await page.getByRole("button", { name: "Open Trim the layout pass", exact: true }).click()
  const transcript = page.getByTestId("transcript")
  const turns = page.getByRole("navigation", { name: "Conversation turns" })
  await expect(turns.getByRole("button")).toHaveCount(3)
  await expect(turns.getByRole("button", { name: "Jump to turn 1: First prompt" })).toBeVisible()
  await expect(turns.getByRole("button", { name: "Jump to turn 3: Third prompt" })).toHaveAttribute("aria-current", "location")
  await expect(transcript.getByText("Response 2", { exact: true })).toHaveCount(0)
  await expect(transcript.getByText("Deferred thought 2.1", { exact: true })).toHaveCount(0)

  await page.getByRole("button", { name: "Earlier", exact: true }).click()
  await expect(transcript.getByText("Response 2", { exact: true })).toBeInViewport()
  await expect(transcript.getByText("Deferred thought 2.1", { exact: true })).toHaveCount(0)
  const second = transcript.locator('[data-phone-turn="compact-2"][data-phone-prompt]')
  const fold = second.locator("xpath=following-sibling::*[1]").getByRole("button", { name: "Thinking and tools" })
  await fold.click()
  await expect(transcript.getByText("Deferred thought 2.1", { exact: true })).toBeVisible()
  await fold.click()
  await expect(transcript.getByText("Deferred thought 2.1", { exact: true })).toHaveCount(0)

  await turns.getByRole("button", { name: /^Jump to turn 1:/ }).click()
  await expect(transcript.getByText("First prompt", { exact: true })).toBeInViewport()
  await expect(turns.getByRole("button", { name: "Jump to turn 1: First prompt" })).toHaveAttribute("aria-current", "location")
  await turns.getByRole("button", { name: /^Jump to turn 3:/ }).click()
  await expect(transcript.getByText("Third prompt", { exact: true })).toBeInViewport()
})

test("shows older prompt previews supplied by legacy history before jumping", async ({ page }, info) => {
  await page.addInitScript(() => localStorage.setItem("zwai.phone.locale", "en"))
  await page.goto("/?mock=1&tick=0&history=compact&legacy-turn-previews=1")
  await page.getByRole("button", { name: "Back", exact: true }).tap()
  await page.getByTestId("project-stack").getByRole("button", { name: "Open Trim the layout pass", exact: true }).tap()
  const rail = page.getByRole("navigation", { name: "Conversation turns" })
  const transcript = page.getByTestId("transcript")
  const first = rail.getByRole("button", { name: "Jump to turn 1: First prompt" })
  const second = rail.getByRole("button", { name: "Jump to turn 2: Second prompt" })
  await expect(first).toContainText("First prompt")
  await expect(second).toContainText("Second prompt")
  await expect(rail).not.toContainText("Response 1")
  await expect(transcript.getByText("First prompt", { exact: true })).toHaveCount(0)
  await expect(rail.getByRole("button", { name: "Jump to turn 3: Third prompt" })).toHaveAttribute("aria-current", "location")

  await rail.evaluate((strip) => { strip.scrollLeft = 0 })
  await expect(first).toBeInViewport()
  await page.screenshot({ path: info.outputPath("legacy-turn-previews-before-jump.png") })
  await first.tap()
  await expect(transcript.getByText("First prompt", { exact: true })).toBeInViewport()
  await expect(first).toHaveAttribute("aria-current", "location")
})

test("turn six leaves turn five fully tappable without moving the strip", async ({ page }, info) => {
  await page.setViewportSize({ width: 320, height: 812 })
  await page.addInitScript(() => localStorage.setItem("zwai.phone.locale", "en"))
  await page.goto("/?mock=1&tick=0&history=compact&many-turns=1")
  await page.getByRole("button", { name: "Back", exact: true }).tap()
  await page.getByTestId("project-stack").getByRole("button", { name: "Open Trim the layout pass", exact: true }).tap()
  const rail = page.getByRole("navigation", { name: "Conversation turns" })
  const transcript = page.getByTestId("transcript")
  const seventh = rail.getByRole("button", { name: "Jump to turn 7: Seventh prompt" })
  const sixth = rail.getByRole("button", { name: "Jump to turn 6: Sixth prompt" })
  const fifth = rail.getByRole("button", { name: "Jump to turn 5: Fifth prompt" })
  await expect(seventh).toHaveAttribute("aria-current", "location")
  await expect(sixth).toBeInViewport({ ratio: 0.99 })
  const sixthBox = (await sixth.boundingBox())!
  await page.touchscreen.tap(sixthBox.x + sixthBox.width / 2, sixthBox.y + sixthBox.height / 2)
  await expect(sixth).toHaveAttribute("aria-current", "location")
  await expect(fifth).toBeInViewport({ ratio: 0.99 })
  await expect(transcript.getByText("Fifth prompt", { exact: true })).toHaveCount(0)
  await page.screenshot({ path: info.outputPath("turn-six-with-tappable-turn-five.png") })
  const fifthBox = (await fifth.boundingBox())!
  await page.touchscreen.tap(fifthBox.x + fifthBox.width / 2, fifthBox.y + fifthBox.height / 2)
  await expect(transcript.getByText("Fifth prompt", { exact: true })).toBeInViewport()
  await expect(fifth).toHaveAttribute("aria-current", "location")
})

for (const earlierFirst of [false, true]) {
  test(`a turn jump finds its prompt across a split history page${earlierFirst ? " after Earlier loaded the reply" : ""}`, async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("zwai.phone.locale", "en"))
    await page.goto("/?mock=1&tick=0&history=compact&many-turns=1&split-history=1")
    await page.getByRole("button", { name: "Back", exact: true }).tap()
    await page.getByTestId("project-stack").getByRole("button", { name: "Open Trim the layout pass", exact: true }).tap()
    const rail = page.getByRole("navigation", { name: "Conversation turns" })
    const transcript = page.getByTestId("transcript")
    const sixth = rail.getByRole("button", { name: "Jump to turn 6: Sixth prompt" })
    const fifth = rail.getByRole("button", { name: "Jump to turn 5: Fifth prompt" })
    await expect(rail.getByRole("button", { name: /^Jump to turn 7:/ })).toHaveAttribute("aria-current", "location")
    await sixth.tap()
    await expect(sixth).toHaveAttribute("aria-current", "location")

    if (earlierFirst) {
      await page.getByRole("button", { name: "Earlier", exact: true }).tap()
      await expect(transcript.getByText("Response 5", { exact: true })).toBeInViewport()
    }
    const prompt = transcript.getByText("Fifth prompt", { exact: true })
    await expect(prompt).toHaveCount(0)
    await fifth.tap()
    await expect(prompt).toBeInViewport()
    await expect(fifth).toHaveAttribute("aria-current", "location")
  })
}

test("browsing the turn strip survives rerenders at the same reading position", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("zwai.phone.locale", "en")
    localStorage.setItem("zwai.phone.providers", JSON.stringify([{
      id: "offline", label: "Offline", baseURL: "http://127.0.0.1:1", api: "chat", model: "fixture",
    }]))
    localStorage.setItem("zwai.phone.directThreads", JSON.stringify([{
      id: "rail", title: "Turn strip", providerId: "offline", model: "fixture", updatedAt: 1,
      messages: Array.from({ length: 6 }, (_, i) => [
        {
          id: `prompt-${i}`, role: "user",
          text: i === 1 ? "" : `Message ${i + 1} about the navigation layout`,
          ...(i === 1 ? { attachments: [{ name: "notes.txt", kind: "file" }] } : {}),
        },
        { id: `reply-${i}`, role: "assistant", text: "Offline answer.\n\n".repeat(50) },
      ]).flat(),
    }]))
  })
  await page.goto("/")
  await page.getByRole("button", { name: "Open Turn strip", exact: true }).tap()
  const rail = page.getByRole("navigation", { name: "Conversation turns" })
  const first = rail.getByRole("button", { name: /^Jump to turn 1:/ })
  const last = rail.getByRole("button", { name: /^Jump to turn 6:/ })
  await last.tap()
  await expect(last).toHaveAttribute("aria-current", "location")
  await rail.evaluate((strip) => {
    strip.dispatchEvent(new WheelEvent("wheel", { deltaX: -300, bubbles: true }))
    strip.scrollLeft = 0
  })
  await expect(first).toBeInViewport()
  await expect(rail.getByRole("button", { name: "Jump to turn 2: Attachments (1)" })).toBeInViewport()

  await page.getByRole("textbox", { name: "Message", exact: true }).tap()
  await page.getByRole("button", { name: "Thinking level", exact: true }).tap()
  await page.getByRole("radio", { name: "High thinking", exact: true }).tap()
  await expect(last).toHaveAttribute("aria-current", "location")
  await expect.poll(() => rail.evaluate((strip) => strip.scrollLeft)).toBe(0)

  const transcript = page.getByTestId("transcript")
  await transcript.evaluate((el) => {
    el.dispatchEvent(new WheelEvent("wheel", { deltaY: -600, bubbles: true }))
    const row = el.querySelector('[data-phone-turn="prompt-4"]')!
    el.scrollTop += row.getBoundingClientRect().top - el.getBoundingClientRect().top + 64
  })
  const fifth = rail.getByRole("button", { name: /^Jump to turn 5:/ })
  await expect(fifth).toHaveAttribute("aria-current", "location")
  await expect(fifth).toBeInViewport()
})

test("scrolls Chat and PC tabs in one strip", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("zwai.phone.locale", "en")
    localStorage.setItem("zwai.phone.providers", JSON.stringify([{
      id: "offline", label: "Offline", baseURL: "http://127.0.0.1:1", api: "chat", model: "fixture",
    }]))
    localStorage.setItem("zwai.remote.links", JSON.stringify(Array.from({ length: 6 }, (_, i) => ({
      hubURL: "http://127.0.0.1:1", ticket: "fixture", hostPub: "fixture",
      sessionID: `session-${i}`, fingerprint: `fixture-${i}`, label: `Fixture workstation ${i + 1}`,
    }))))
  })
  await page.goto("/?mock=1&tick=0&history=compact")
  await page.getByRole("button", { name: "Back", exact: true }).click()
  const strip = page.getByRole("tablist", { name: "Navigation" })
  const chat = strip.getByRole("tab", { name: "Chat", exact: true })
  await expect(strip.getByRole("tab")).toHaveCount(8)
  await expect(strip.getByRole("tab", { name: "Walkthrough PC" })).toBeInViewport()
  await strip.evaluate((el) => { el.scrollLeft = 0 })
  await expect(chat).toBeInViewport()
  await strip.evaluate((el) => { el.scrollLeft = el.scrollWidth })
  await expect(chat).not.toBeInViewport()
  await expect(strip.getByRole("tab", { name: "Walkthrough PC" })).toBeInViewport()
})

test("direct Chat shows jump controls starting at the third prompt", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("zwai.phone.locale", "en")
    localStorage.setItem("zwai.phone.providers", JSON.stringify([{
      id: "offline", label: "Offline", baseURL: "http://127.0.0.1:1", api: "chat", model: "fixture",
    }]))
    localStorage.setItem("zwai.phone.directThreads", JSON.stringify([2, 3].map((count) => ({
      id: `direct-${count}`, title: `Direct with ${count}`, providerId: "offline", model: "fixture", updatedAt: count,
      messages: Array.from({ length: count }, (_, index) => [
        { id: `prompt-${index}`, role: "user", text: `Direct prompt ${index + 1}` },
        { id: `response-${index}`, role: "assistant", text: "Long assistant paragraph.\n\n".repeat(45) },
      ]).flat(),
    }))))
  })
  await page.goto("/")
  const turns = page.getByRole("navigation", { name: "Conversation turns" })
  await page.getByRole("button", { name: "Open Direct with 2", exact: true }).click()
  await expect(turns).toHaveCount(0)
  await page.getByRole("button", { name: "Back", exact: true }).click()
  await page.getByRole("button", { name: "Open Direct with 3", exact: true }).click()
  await expect(turns.getByRole("button")).toHaveCount(3)
  await turns.getByRole("button", { name: /^Jump to turn 3:/ }).click()
  const transcript = page.getByTestId("transcript")
  await expect(transcript.getByText("Direct prompt 3", { exact: true })).toBeInViewport()
  await expect(transcript.getByText("Direct prompt 1", { exact: true })).not.toBeInViewport()
  await expect(turns.getByRole("button", { name: "Jump to turn 3: Direct prompt 3" })).toHaveAttribute("aria-current", "location")
  await transcript.evaluate((el) => {
    const prompt = el.querySelector('[data-phone-turn="prompt-1"]')!
    el.scrollTop += prompt.getBoundingClientRect().top - el.getBoundingClientRect().top + 120
  })
  await expect(turns.getByRole("button", { name: "Jump to turn 2: Direct prompt 2" })).toHaveAttribute("aria-current", "location")
  await transcript.evaluate((el) => { el.scrollTop = 0 })
  await expect(turns.getByRole("button", { name: "Jump to turn 1: Direct prompt 1" })).toHaveAttribute("aria-current", "location")
})

for (const language of [
  { locale: "en", back: "Back", open: "Open Trim the layout pass", stop: "Stop" },
  { locale: "zh", back: "返回", open: "打开 Trim the layout pass", stop: "停止" },
]) {
  const reopen = async (page: Page) => {
    await page.getByRole("button", { name: language.back, exact: true }).click()
    // The same live thread also appears in In progress. Select its project row.
    const sameThread = page.getByTestId("project-stack").getByRole("button", { name: language.open, exact: true })
    await expect(sameThread).toHaveCount(1)
    await sameThread.click()
  }

  test(`opening backfills the latest answer without Earlier on both visits (${language.locale})`, async ({ page }, info) => {
    await page.addInitScript((locale) => localStorage.setItem("zwai.phone.locale", locale), language.locale)
    await page.goto("/?mock=1&tick=0&history=hidden&pause=open")
    const transcript = page.getByTestId("transcript")
    for (let visit = 0; visit < 2; visit++) {
      await expect(transcript.getByText("Earlier answer", { exact: true })).toHaveCount(1)
      await expect(transcript.getByText("Earlier answer", { exact: true })).toBeVisible()
      await expect(transcript.getByText("Earlier answer", { exact: true })).toBeInViewport()
      await expect(transcript).not.toContainText("No visible messages in this turn yet")
      await expect(transcript).not.toContainText("这一轮暂无可显示消息")
      await expect(page.locator("form").getByRole("button", { name: language.stop, exact: true })).toBeVisible()
      await expect(page.locator(".screen-push")).toHaveCSS("opacity", "1")
      await page.screenshot({ path: info.outputPath(`conversation-open-${language.locale}-${visit}.png`) })
      if (visit === 0) await reopen(page)
    }
  })

  test(`live manager updates survive Back and reopening (${language.locale})`, async ({ page }, info) => {
    await page.addInitScript((locale) => localStorage.setItem("zwai.phone.locale", locale), language.locale)
    await page.goto("/?mock=1&tick=150&history=streamed-agents")
    const transcript = page.getByTestId("transcript")
    await expect(transcript).toContainText("Manager update 0")
    await expect(transcript).toContainText("Manager update 2")
    for (let visit = 0; visit < 2; visit++) {
      for (let update = 0; update < 3; update++) {
        await expect(transcript.getByText(`Manager update ${update}`, { exact: true })).toHaveCount(1)
      }
      await expect(page.locator("form").getByRole("button", { name: language.stop, exact: true })).toBeVisible()
      await expect(page.locator(".screen-push")).toHaveCSS("opacity", "1")
      await page.screenshot({ path: info.outputPath(`conversation-live-${language.locale}-${visit}.png`) })
      if (visit === 0) await reopen(page)
    }
  })

  test(`waiting tasks show the previous answer through a boundary on both visits (${language.locale})`, async ({ page }, info) => {
    await page.addInitScript((locale) => localStorage.setItem("zwai.phone.locale", locale), language.locale)
    await page.goto("/?mock=1&tick=0&history=hidden&waiting=1")
    const transcript = page.getByTestId("transcript")
    for (let visit = 0; visit < 2; visit++) {
      await expect(transcript.getByText("Earlier answer", { exact: true })).toBeInViewport()
      await expect(transcript.getByText("Earlier answer", { exact: true })).toHaveCount(1)
      await expect(transcript.getByTestId("turn-boundary")).toHaveCount(1)
      await expect(page.locator(".screen-push")).toHaveCSS("opacity", "1")
      await expect(transcript).not.toContainText("这一轮暂无可显示消息")
      await expect(transcript).not.toContainText("No visible messages in this turn yet")
      await page.screenshot({ path: info.outputPath(`waiting-open-${language.locale}-${visit}.png`) })
      if (visit === 0) await reopen(page)
    }
  })
}
