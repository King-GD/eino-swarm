import { expect, test } from "@playwright/test"

test("Thinking off sends an explicit request and reports a model that still reasons", async ({ page }) => {
  let calls = 0
  await page.addInitScript(() => {
    localStorage.setItem("zwai.phone.locale", "en")
    localStorage.setItem("zwai.phone.providers", JSON.stringify([{
      id: "fixture", label: "Fixture", baseURL: "http://127.0.0.1:9/v1", api: "chat", model: "fixture-model",
    }]))
  })
  await page.route("http://127.0.0.1:9/v1/**", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ json: { data: [{ id: "fixture-model" }] } })
      return
    }
    calls++
    expect(route.request().postDataJSON()).toMatchObject({ reasoning_effort: "none", stream: true })
    const delta = calls === 1 ? { content: "Completed without thinking." } : { reasoning_content: "Still thinking." }
    await route.fulfill({
      contentType: "text/event-stream",
      body: `data: ${JSON.stringify({ choices: [{ delta, finish_reason: "stop" }] })}\n\ndata: [DONE]\n\n`,
    })
  })
  await page.goto("/")
  await page.getByLabel("Message", { exact: true }).tap()
  await page.getByRole("button", { name: "Thinking level", exact: true }).tap()
  await page.getByRole("radio", { name: "Thinking off", exact: true }).tap()
  await page.getByLabel("Message", { exact: true }).fill("Check the setting.")
  await page.getByRole("button", { name: "Send", exact: true }).tap()
  await expect(page.getByText("Completed without thinking.", { exact: true })).toBeVisible()
  await expect(page.getByTestId("phone-thought-toggle")).toHaveCount(0)

  await page.reload()
  await page.getByRole("button", { name: "Open Check the setting.", exact: true }).tap()
  await page.getByLabel("Message", { exact: true }).tap()
  await expect(page.getByRole("button", { name: "Thinking level", exact: true })).toContainText("Thinking off")
  await page.getByLabel("Message", { exact: true }).fill("Check the next request.")
  await page.getByRole("button", { name: "Send", exact: true }).tap()
  await expect(page.getByText(/still generated reasoning with Thinking off/)).toBeVisible()
  expect(calls).toBe(2)
})

// Exercise the actual phone composer, HTTP reader and persisted transcript.
// The endpoint is intercepted locally; these checks never use a real model.
for (const field of ["reasoning_content", "reasoning"] as const) {
  for (const wire of ["SSE", "JSON"] as const) {
    test(`${field} from ${wire} retains its own collapsed control after reopening a direct chat`, async ({ page }) => {
      const pieces = ["检查当前证据 before answering.\n", "Compare both paths before concluding."]
      const thought = pieces.join("")
      const answer = "Verified answer."
      const prompt = "Inspect the current input."
      let requests = 0
      await page.route("http://127.0.0.1:9/**", async (route) => {
        const url = route.request().url()
        if (url.endsWith("/models")) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ data: [{ id: "fixture-model" }] }),
          })
          return
        }
        expect(url).toBe("http://127.0.0.1:9/v1/chat/completions")
        requests++
        const sent = route.request().postDataJSON() as { messages: { role: string; content: string }[] }
        expect(sent.messages).toEqual([{ role: "user", content: prompt }])
        const message = { role: "assistant", content: answer, [field]: thought }
        await route.fulfill({
          status: 200,
          contentType: wire === "JSON" ? "application/json" : "text/event-stream",
          body: wire === "JSON"
            ? JSON.stringify({ choices: [{ message, finish_reason: "stop" }] })
            : [
              ...pieces.map((text) => ({ choices: [{ delta: { role: "assistant", [field]: text } }] })),
              { choices: [{ delta: { content: answer }, finish_reason: "stop" }] },
            ].map((frame) => `data: ${JSON.stringify(frame)}\n\n`).join("") + "data: [DONE]\n\n",
        })
      })

      await page.goto("/")
      await page.getByRole("button", { name: "其他方式", exact: true }).click()
      await page.getByRole("button", { name: "连接模型" }).click()
      await page.getByLabel("接口地址").fill("http://127.0.0.1:9/v1")
      await page.getByLabel("接口类型").selectOption("chat")
      await page.getByRole("button", { name: "拉取模型" }).click()
      await expect(page.getByLabel("默认模型")).toHaveValue("fixture-model")
      await page.getByRole("button", { name: "保存" }).click()
      await page.getByLabel("消息").fill(prompt)
      await page.getByRole("button", { name: "发送" }).click()

      await expect(page.getByText(answer, { exact: true })).toBeVisible()
      await expect(page.getByTestId("work-fold")).toHaveCount(0)
      await expect(page.getByTestId("phone-thought-toggle")).toHaveAttribute("aria-expanded", "false")
      await expect(page.getByTestId("phone-thought")).not.toBeVisible()
      await page.getByTestId("phone-thought-toggle").click()
      await expect(page.getByTestId("phone-thought")).toHaveJSProperty("textContent", thought)
      expect(requests).toBe(1)

      await page.reload()
      await page.getByRole("button", { name: `打开 ${prompt}`, exact: true }).click()
      await expect(page.getByText(answer, { exact: true })).toBeVisible()
      await expect(page.getByTestId("work-fold")).toHaveCount(0)
      await expect(page.getByTestId("phone-thought-toggle")).toHaveAttribute("aria-expanded", "false")
      await expect(page.getByTestId("phone-thought")).not.toBeVisible()
      await page.getByTestId("phone-thought-toggle").click()
      await expect(page.getByTestId("phone-thought")).toHaveJSProperty("textContent", thought)
      expect(requests).toBe(1)
    })
  }
}
