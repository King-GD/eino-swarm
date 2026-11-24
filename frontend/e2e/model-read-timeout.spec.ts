import { expect, test } from "@playwright/test"

test.afterEach(async ({ request }) => {
  await request.put("/api/settings", { data: { ui: { locale: "system" } } })
})

test("localizes a streamed model read timeout live and after reload", async ({ page, request }) => {
  expect((await request.put("/api/settings", { data: { ui: { locale: "zh" } } })).ok()).toBe(true)
  const title = "Stream receive timeout"
  const created = await request.post("/api/threads", { data: { title } })
  expect(created.ok()).toBe(true)
  const { thread } = await created.json()
  const base = { thread_id: thread.id, turn_id: "offline-timeout-turn", agent_id: "manager", created_at: "2026-01-01T00:00:00Z" }
  const reason = "model response read timed out"
  const events = [
    { ...base, seq: 1, kind: "user_message", text: "Review the available findings." },
    { ...base, seq: 2, kind: "reasoning", text: "Reviewing the available findings.", message_id: "partial" },
    { ...base, seq: 3, kind: "generation_interrupted", message_id: "partial", text: JSON.stringify({ reason: "model_error", recovery: "none", detail: reason }) },
    { ...base, seq: 4, kind: "model_retry", text: JSON.stringify({ attempt: 1, cap: 2, reason: "model_read_timeout" }) },
    { ...base, seq: 5, kind: "error", err: reason, text: "" },
  ]
  let replay = false
  await page.route(new RegExp(`/api/threads/${thread.id}/log(?:\\?|$)`), (route) =>
    route.fulfill({ json: { events: replay ? events : [], roster: [], has_more: false } }))
  await page.route(new RegExp(`/api/threads/${thread.id}/events(?:\\?|$)`), (route) =>
    replay ? route.fulfill({ status: 204 }) : route.fulfill({
      contentType: "text/event-stream",
      body: "retry: 3600000\n\n" + events.map((ev) => `id: ${ev.seq}\nevent: ${ev.kind}\ndata: ${JSON.stringify(ev)}\n\n`).join(""),
    }))
  const open = async () => {
    await page.getByTestId("dest-chats").click()
    await page.locator(`[data-testid="thread-row"][data-id="${thread.id}"]`).getByTestId("row-label").click()
  }
  await page.goto("/")
  for (const stage of ["live", "replayed"]) {
    await open()
    const transcript = page.getByTestId("transcript")
    await expect(transcript.getByTestId("generation-notice")).toContainText("接收模型响应超时。")
    await expect(transcript.getByText("接收模型响应超时，正在重试。", { exact: true })).toBeVisible()
    const error = transcript.locator("p.stream-text").and(transcript.getByText("接收模型响应超时。", { exact: true }))
    await expect(error).toBeVisible()
    await expect(transcript).not.toContainText(reason)
    if (stage === "live") {
      replay = true
      await page.reload()
    }
  }
})
