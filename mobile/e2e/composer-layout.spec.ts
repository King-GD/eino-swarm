import { writeFileSync } from "node:fs"
import { expect, test, type Page } from "@playwright/test"

test("inbox search keeps its header, results and controls in the keyboard viewport", async ({ page }) => {
  await page.goto("/?mock=1&tick=0&history=compact")
  await page.getByRole("button", { name: "返回", exact: true }).click()
  const search = page.getByRole("searchbox", { name: "搜索对话" })
  const menu = page.getByRole("button", { name: "菜单", exact: true })
  const newChat = page.getByTestId("new-chat")
  const initialTop = (await menu.boundingBox())!.y
  const initialBottom = await search.evaluate((node) => node.getBoundingClientRect().bottom)
  expect.soft(await search.evaluate((node) => parseFloat(getComputedStyle(node).fontSize))).toBeGreaterThanOrEqual(16)
  await search.tap()
  await expect(search).toBeFocused()
  // Mobile WebKit's keyboard reduces the visual viewport without changing the
  // layout viewport. Real keyboard geometry is verified in the packaged app.
  const { keyboardTop, pageTop } = await page.evaluate(() => {
    const viewport = window.visualViewport!
    const height = viewport.height - 280
    const pageTop = 120
    Object.defineProperty(viewport, "height", { configurable: true, value: height })
    Object.defineProperty(viewport, "pageTop", { configurable: true, value: pageTop })
    viewport.dispatchEvent(new Event("resize"))
    viewport.dispatchEvent(new Event("scroll"))
    return { keyboardTop: pageTop + height, pageTop }
  })
  await search.pressSequentially("Trim")
  const match = page.getByRole("button", { name: "打开 Trim the layout pass", exact: true })
  await expect(match).toBeVisible()
  expect((await search.boundingBox())!.y).toBeGreaterThan((await match.boundingBox())!.y)
  for (const control of [search, newChat, match, page.getByTestId("inbox-scroller")]) {
    await expect.soft.poll(() => control.evaluate((node) => node.getBoundingClientRect().bottom)).toBeLessThanOrEqual(keyboardTop)
  }
  expect((await menu.boundingBox())!.y - pageTop).toBe(initialTop)

  await page.evaluate(() => {
    const viewport = window.visualViewport!
    Reflect.deleteProperty(viewport, "height")
    Reflect.deleteProperty(viewport, "pageTop")
    viewport.dispatchEvent(new Event("resize"))
  })
  await search.blur()
  await expect.poll(() => search.evaluate((node) => node.getBoundingClientRect().bottom)).toBe(initialBottom)
  await expect(search).toHaveValue("Trim")
  await expect(match).toBeVisible()
})

// Bounds must include the submission itself: a visible editor or a page with
// overflow hidden can still hide the button on its right.
async function controlsFit(page: Page, label: string) {
  const editor = page.getByLabel(label, { exact: true })
  const submit = page.locator('form button[type="submit"]')
  await editor.fill("long message ".repeat(80))
  await expect(submit).toHaveCount(1)
  for (const width of [320, 375, 402]) {
    await page.setViewportSize({ width, height: 812 })
    await editor.focus()
    const add = page.getByRole("button", { name: "添加", exact: true })
    await expect(add).toHaveCount(1)
    for (const item of [editor, submit, add, ...await page.getByRole("button", { name: "插入", exact: true }).all()]) {
      const box = await item.boundingBox()
      expect(box).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width).toBeLessThanOrEqual(width)
      await expect(item).toBeInViewport()
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
  }
  await editor.blur()
  await expect(submit).toBeEnabled()
  await submit.click()
  await expect(page.locator("form textarea")).toHaveValue("")
}

test("PC follow-up and new chat keep submission inside the phone", async ({ page }) => {
  await page.goto("/?mock=1&tick=0")
  await expect(page.locator("form").getByRole("button", { name: "停止", exact: true })).toBeEnabled()
  await expect(page.getByRole("button", { name: "插入", exact: true })).toHaveCount(0)
  await controlsFit(page, "消息")
  await page.getByRole("button", { name: "返回" }).click()
  await page.getByTestId("new-chat").click()
  await page.locator(".screen-push").evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)))
  await controlsFit(page, "新消息")
})

test("the live composer offers send options beside Follow-up without a persistent hint", async ({ page }, info) => {
  await page.goto("/?mock=1&tick=0")
  const form = page.locator("form")
  const editor = page.getByLabel("消息", { exact: true })
  const options = form.getByRole("button", { name: "发送方式", exact: true })
  const followUp = form.getByRole("button", { name: "跟进", exact: true })
  const hint = page.getByText(/^(排在本轮之后|Queued after this turn)$/)
  await expect(form.getByRole("button", { name: "停止", exact: true })).toBeEnabled()
  await expect(hint).toHaveCount(0)
  await editor.fill("Check the current result")
  await expect(followUp).toBeEnabled()
  await expect(options).toBeEnabled()
  await expect(hint).toHaveCount(0)
  writeFileSync(info.outputPath("composer-live-without-queue-hint.png"), await page.screenshot(), { mode: 0o600 })

  // Exercise both real composer callbacks: hiding a hint must not turn Steer
  // into a queued send or remove Follow-up from an active conversation.
  await options.click()
  await page.getByRole("menuitem", { name: "插入本轮", exact: true }).click()
  await expect(page.getByTestId("transcript").getByText("Check the current result", { exact: true })).toBeVisible()
  await expect(editor).toHaveValue("")
  await expect(form.getByRole("button", { name: "停止", exact: true })).toBeEnabled()
  await editor.fill("Continue with the next item")
  await expect(followUp).toBeEnabled()
  await followUp.click()
  await expect(page.getByTestId("transcript").getByText("Continue with the next item", { exact: true })).toBeVisible()
  await expect(editor).toHaveValue("")
  await expect(form.getByRole("button", { name: "发送", exact: true })).toBeDisabled()
  await expect(options).toHaveCount(0)
  await expect(hint).toHaveCount(0)
  writeFileSync(info.outputPath("composer-idle-without-steer.png"), await page.screenshot(), { mode: 0o600 })
})

test("composer reveals settings on focus and docks Agents and Outputs next to the message box", async ({ page }, info) => {
  await page.setViewportSize({ width: 320, height: 812 })
  await page.goto("/?mock=1&tick=0&history=streamed-agents&speech=local")
  const form = page.getByTestId("message-composer")
  const editor = form.getByRole("textbox", { name: "消息", exact: true })
  const model = form.getByRole("button", { name: "模型", exact: true })
  const thinking = form.getByRole("button", { name: "思考强度", exact: true })
  const add = form.getByRole("button", { name: "添加", exact: true })
  const mic = form.getByRole("button", { name: "开始语音输入", exact: true })
  const stop = form.getByRole("button", { name: "停止", exact: true })
  const links = page.getByTestId("thread-composer-links")
  const agents = links.getByRole("button", { name: "子 Agent (3)", exact: true })
  const outputs = links.getByRole("button", { name: "产出物", exact: true })

  await expect(agents).toBeVisible()
  await expect(outputs).toBeVisible()
  await expect(mic).toBeVisible()
  await expect(model).toHaveCount(0)
  await expect(thinking).toHaveCount(0)
  await expect(page.locator("header").getByRole("button", { name: /停止|子 Agent|产出物/ })).toHaveCount(0)
  const compact = (await form.boundingBox())!
  const addIcon = (await add.locator("svg").boundingBox())!
  const microphone = (await mic.boundingBox())!
  expect((await editor.boundingBox())!.x - (addIcon.x + addIcon.width)).toBeLessThanOrEqual(12)
  expect((await stop.boundingBox())!.x - (microphone.x + microphone.width)).toBeGreaterThanOrEqual(12)
  expect((await links.boundingBox())!.y).toBeLessThan(compact.y)
  writeFileSync(info.outputPath("composer-compact.png"), await page.screenshot(), { mode: 0o600 })

  await editor.tap()
  await expect(model).toBeVisible()
  await expect(thinking).toBeVisible()
  const modelName = model.getByText("scripted", { exact: true })
  await expect(model).toHaveAccessibleDescription("scripted")
  await expect.poll(() => modelName.evaluate((node) => node.getBoundingClientRect().width)).toBeGreaterThan(16)
  await expect(thinking).toHaveAccessibleDescription("默认思考")
  expect(await thinking.getByText("默认思考", { exact: true }).evaluate((node) => node.getBoundingClientRect().width)).toBeLessThanOrEqual(1)
  for (const control of [add, model, thinking, mic]) {
    expect((await control.boundingBox())!.y).toBeCloseTo((await stop.boundingBox())!.y, 0)
  }
  expect((await form.boundingBox())!.height - compact.height).toBeGreaterThanOrEqual(36)
  expect((await model.boundingBox())!.y).toBeGreaterThanOrEqual((await editor.boundingBox())!.y + (await editor.boundingBox())!.height)
  writeFileSync(info.outputPath("composer-expanded.png"), await page.screenshot(), { mode: 0o600 })

  await editor.fill("Keep this draft")
  await editor.evaluate((field: HTMLTextAreaElement) => field.setSelectionRange(4, 4))
  await thinking.tap()
  const thinkingSheet = page.getByRole("dialog", { name: "思考强度", exact: true })
  await thinkingSheet.getByRole("button", { name: "关闭", exact: true }).tap()
  await expect(editor).toBeFocused()
  await expect(thinking).toBeVisible()
  await thinking.tap()
  await thinkingSheet.getByRole("radio", { name: "高思考", exact: true }).tap()
  await expect(thinkingSheet).toHaveCount(0)
  await expect(editor).toBeFocused()
  await expect(thinking).toHaveAccessibleDescription("高思考")
  expect(await thinking.getByText("高思考", { exact: true }).evaluate((node) => node.getBoundingClientRect().width)).toBeLessThanOrEqual(1)
  await model.tap()
  const modelSheet = page.getByRole("dialog", { name: "模型", exact: true })
  await expect(modelSheet.getByRole("radio", { name: "scripted", exact: true })).toBeVisible()
  await modelSheet.getByRole("button", { name: "关闭", exact: true }).tap()
  await expect(editor).toBeFocused()
  await expect(model).toBeVisible()
  await page.keyboard.insertText("!")
  await expect(editor).toHaveValue("Keep! this draft")
  const actions = await Promise.all([add, model, thinking, mic, form.getByRole("button", { name: "发送方式" }), form.getByRole("button", { name: "跟进" }), stop]
    .map((control) => control.boundingBox()))
  const actionLine = actions[0]!.y
  const composerBounds = (await form.boundingBox())!
  for (const action of actions) {
    expect(action).not.toBeNull()
    expect(action!.y).toBe(actionLine)
    expect(action!.x + action!.width).toBeLessThanOrEqual(composerBounds.x + composerBounds.width)
  }

  await agents.tap()
  await expect(page.getByTestId("agent-roster").getByRole("button")).toHaveCount(3)
  await page.getByRole("button", { name: "返回", exact: true }).tap()
  await outputs.tap()
  await expect(page.getByRole("heading", { name: "产出物", exact: true })).toBeVisible()
})

test("closing a picker keeps the whole composer above the software keyboard", async ({ page }) => {
  await page.goto("/?mock=1&tick=0&history=streamed-agents")
  const form = page.getByTestId("message-composer")
  const editor = form.getByRole("textbox", { name: "消息", exact: true })
  await editor.tap()
  await form.getByRole("button", { name: "思考强度", exact: true }).tap()
  await page.getByRole("dialog", { name: "思考强度", exact: true }).getByRole("button", { name: "关闭", exact: true }).tap()
  await expect(editor).toBeFocused()

  const setVisibleViewport = (top: number, height: number, event: "resize" | "scroll", nativePan = 0) => page.evaluate(({ top, height, event, nativePan }) => {
    const viewport = window.visualViewport!
    Object.defineProperty(viewport, "height", { configurable: true, value: height })
    Object.defineProperty(viewport, "offsetTop", { configurable: true, value: top + nativePan })
    Object.defineProperty(viewport, "pageTop", { configurable: true, value: top + nativePan })
    Object.defineProperty(window, "scrollY", { configurable: true, value: nativePan })
    viewport.dispatchEvent(new Event(event))
  }, { top, height, event, nativePan })
  const formBottom = () => form.evaluate((node) => node.getBoundingClientRect().bottom)
  const withoutKeyboard = await formBottom()
  const keyboardViewport = Math.round(withoutKeyboard * 0.55)
  await setVisibleViewport(0, keyboardViewport, "resize")
  await expect.poll(formBottom).toBeLessThanOrEqual(keyboardViewport)
  await expect.poll(formBottom).toBeGreaterThan(keyboardViewport - 24)
  await expect.poll(() => editor.evaluate((node) => node.getBoundingClientRect().top)).toBeGreaterThan(0)

  await setVisibleViewport(80, keyboardViewport, "scroll")
  await expect.poll(formBottom).toBeGreaterThan(keyboardViewport + 56)
  await expect.poll(formBottom).toBeLessThanOrEqual(keyboardViewport + 80)

  // WebKit can report the same native pan as both pageTop and window scroll.
  await setVisibleViewport(0, keyboardViewport, "scroll", 80)
  await expect.poll(formBottom).toBeLessThanOrEqual(keyboardViewport)

  // With a short viewport, the header and task links can exhaust the flex space.
  await setVisibleViewport(0, 180, "resize")
  await expect.poll(formBottom).toBeGreaterThan(156)
  await expect.poll(formBottom).toBeLessThanOrEqual(180)

  await page.evaluate(() => {
    const viewport = window.visualViewport!
    Reflect.deleteProperty(viewport, "height")
    Reflect.deleteProperty(viewport, "offsetTop")
    Reflect.deleteProperty(viewport, "pageTop")
    Reflect.deleteProperty(window, "scrollY")
    viewport.dispatchEvent(new Event("resize"))
  })
  await expect.poll(formBottom).toBe(withoutKeyboard)
})

test("a live composer keeps its single Stop beside Follow-up and retains the unsent draft on stop", async ({ page }) => {
  await page.goto("/?mock=1&tick=0")
  const form = page.getByTestId("message-composer")
  const stop = form.getByRole("button", { name: "停止", exact: true })
  await expect(stop).toBeEnabled()
  await expect(stop.locator("svg")).toHaveClass(/lucide-square/)
  await expect(page.getByRole("button", { name: "停止", exact: true })).toHaveCount(1)
  const editor = form.getByRole("textbox", { name: "消息", exact: true })
  await editor.fill("follow-up")
  await expect(stop).toBeEnabled()
  await expect(form.getByRole("button", { name: "跟进", exact: true })).toBeEnabled()
  await stop.tap()
  await expect(stop).toHaveCount(0)
  await expect(form.getByRole("button", { name: "发送", exact: true })).toBeEnabled()
  await expect(editor).toHaveValue("follow-up")
  await expect(page.getByTestId("transcript")).not.toContainText("follow-up")
})

test("Send now on a waiting row starts it after Stop while leaving the other row queued", async ({ page }, testInfo) => {
  await page.goto("/?mock=1&tick=400&queue=1")
  const form = page.locator("form")
  const queue = page.getByTestId("followup-queue")
  await expect(queue).toContainText("first waiting message")
  await expect(queue).toContainText("second waiting message")
  await form.getByRole("button", { name: "停止", exact: true }).click()
  await expect(page.getByRole("button", { name: "停止", exact: true })).toHaveCount(0)
  await expect(form.getByRole("button", { name: "发送", exact: true })).toBeDisabled()
  await expect(queue.locator("li")).toHaveCount(2)
  await page.screenshot({ path: testInfo.outputPath("queued-steer-after-stop-idle.png") })

  await queue.locator("li").filter({ hasText: "first waiting message" })
    .getByRole("button", { name: "立即发送", exact: true }).click()
  await expect(queue.locator("li")).toHaveCount(1)
  await expect(queue).not.toContainText("first waiting message")
  await expect(queue).toContainText("second waiting message")
  await expect(page.getByTestId("transcript")).toContainText("first waiting message")
  await expect(form.getByRole("button", { name: "停止", exact: true })).toBeEnabled()
  await page.screenshot({ path: testInfo.outputPath("queued-steer-after-stop-running.png") })

  await form.getByRole("button", { name: "停止", exact: true }).click()
  await expect(page.getByRole("button", { name: "停止", exact: true })).toHaveCount(0)
  await expect(form.getByRole("button", { name: "发送", exact: true })).toBeDisabled()
  await expect(queue.locator("li")).toHaveCount(1)
  await expect(queue).toContainText("second waiting message")
  await expect(page.getByRole("alert")).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath("queued-steer-after-stop-stopped-again.png") })
})

test("direct model chat keeps submission inside the phone", async ({ page }) => {
  await page.addInitScript(() => {
    const model = "long-model-name-".repeat(12)
    localStorage.setItem("zwai.phone.providers", JSON.stringify([{
      id: "layout", label: "Demo", baseURL: "https://endpoint.invalid/v1", apiKey: "", api: "chat", model,
      catalog: [model], timeoutSeconds: 300,
    }]))
  })
  await page.route("https://endpoint.invalid/v1/**", (route) => route.fulfill({
    contentType: "text/event-stream",
    body: 'data: {"choices":[{"delta":{"content":"offline reply"}}]}\n\ndata: [DONE]\n\n',
  }))
  await page.goto("/")
  await page.getByRole("textbox", { name: "消息", exact: true }).focus()
  const model = "long-model-name-".repeat(12)
  const modelName = page.getByRole("button", { name: "模型", exact: true }).getByText(model, { exact: true })
  await expect(modelName).toBeVisible()
  await expect.poll(() => modelName.evaluate(node => node.getBoundingClientRect().width)).toBeGreaterThan(16)
  await controlsFit(page, "消息")
  await expect(page.getByText("offline reply", { exact: true })).toBeVisible()
  await controlsFit(page, "消息")
})

test("direct chat cancels a pending model request from the primary Stop button", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("zwai.phone.providers", JSON.stringify([{
    id: "offline", label: "Demo", baseURL: "https://endpoint.invalid/v1", apiKey: "", api: "chat",
    model: "scripted", catalog: ["scripted"], timeoutSeconds: 300,
  }])))
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  let requested = false
  await page.route("https://endpoint.invalid/v1/**", async (route) => {
    requested = true
    await gate
    await route.abort()
  })
  try {
    await page.goto("/")
    await page.getByLabel("消息").fill("start a reply")
    await page.locator("form").getByRole("button", { name: "发送", exact: true }).click()
    await expect.poll(() => requested).toBe(true)
    const form = page.locator("form")
    const stop = form.getByRole("button", { name: "停止", exact: true })
    await expect(stop).toBeEnabled()
    await stop.click()
    await expect(page.getByRole("button", { name: "停止", exact: true })).toHaveCount(0)
    await expect(page.getByLabel("消息")).toBeEnabled()
    await expect(form.getByRole("button", { name: "发送", exact: true })).toBeDisabled()
    await expect(page.getByRole("alert")).toHaveCount(0)
  } finally {
    release()
  }
})

test("selected answer keeps Add to chat near the highlight in WebKit", async ({ page }) => {
  await page.goto("/?mock=1&tick=0")
  await page.getByLabel("消息").fill("show result")
  await page.getByRole("button", { name: "跟进" }).click()
  const source = page.getByTestId("transcript").getByText("Here is what changed:")
  await expect(source).toBeVisible()
  const select = () => source.evaluate((element) => {
    element.scrollIntoView({ block: "center" })
    const range = document.createRange()
    range.selectNodeContents(element)
    window.getSelection()?.removeAllRanges()
    window.getSelection()?.addRange(range)
    document.dispatchEvent(new Event("selectionchange"))
  })
  await select()
  const bottom = await page.evaluate(() => window.getSelection()?.getRangeAt(0).getBoundingClientRect().bottom)
  const action = page.getByRole("button", { name: "加入对话" })
  await expect(action).toBeVisible()
  const box = await action.boundingBox()
  expect(box).not.toBeNull()
  expect(bottom).toBeDefined()
  expect(box!.y - bottom!).toBeGreaterThanOrEqual(56)
  expect(box!.y - bottom!).toBeLessThanOrEqual(80)
  await page.getByRole("textbox", { name: "消息", exact: true }).click()
  await expect(action).toHaveCount(0)
  await select()
  await action.click()
  await expect(page.getByTestId("quote-draft")).toHaveValue("Here is what changed:")
})

test("older PC client tasks stay hidden from the phone inbox", async ({ page }) => {
  await page.goto("/?mock=1&tick=0")
  await page.getByRole("button", { name: "返回" }).click()
  const inbox = page.getByTestId("inbox-scroller")
  const conversation = inbox.getByRole("button", { name: /^打开 Trim the layout pass$/ }).first()
  await expect(conversation).toBeVisible()
  await expect(inbox.getByTestId("client-groups")).toHaveCount(0)
  await expect(inbox.getByRole("button", { name: "open session" })).toHaveCount(0)
  await conversation.click()
  await expect(page.getByTestId("transcript")).toBeVisible()
})
