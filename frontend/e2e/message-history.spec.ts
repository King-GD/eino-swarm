import { execFileSync } from "node:child_process"
import { join } from "node:path"
import { expect, test, type Page } from "@playwright/test"

function longMessage(label: string) {
  return [label, ...Array.from({ length: 55 }, (_, i) => `Paragraph ${i + 1} of ${label.toLowerCase()}.`)].join("\n\n")
}

async function seedHistory(page: Page, title: string) {
  const response = await page.request.post("/api/threads", { data: { title } })
  expect(response.ok()).toBe(true)
  const { thread } = await response.json()
  const rows: { kind: string; text: string; turn: string; agent: string; call: string; err: string }[] = []
  let turn = "first-turn"
  function add(kind: string, text: string, call = "", agent = "manager", err = "") {
    rows.push({ kind, text, turn, agent, call, err })
  }
  add("user_message", "Earliest prompt")
  add("agent_message", longMessage("Earlier answer"))
  add("reasoning", "Stored historical reasoning")
  add("spawned", "Evidence worker", "", "worker-1")
  add("tool_call", "exec(worker fixture argument)", "worker-tool", "worker-1")
  add("tool_result", "Worker evidence from its own log", "worker-tool", "worker-1")
  add("tool_call", 'wait_agents({"ids":["worker-1"]})', "wait")
  add("tool_result", "Stored result of the wait", "wait")
  for (let i = 0; i < 103; i++) {
    add("tool_call", `exec({"command":"printf archived-${i}"})`, `call-${i}`)
    add("tool_result", `{"stdout":"Archived result ${i}","exit_code":0}`, `call-${i}`)
  }
  add("compacted", JSON.stringify({ auto: true, tokens_before: 78000, tokens_after: 23000 }))
  add("reasoning", "Thought after the context compaction")
  add("plan_updated", "# Stored plan")
  add("tool_call", 'ask_user({"questions":[{"id":"format","prompt":"Which format?","options":[{"id":"text","label":"Text"},{"id":"table","label":"Table"}]}]})', "ask")
  add("tool_result", '{"answers":{"format":{"answers":["Text"]}}}', "ask")
  add("agent_message", " \t\u2003")
  add("agent_message", longMessage("Second answer in the same turn"))
  add("error", "", "", "manager", "Recorded historical error")
  add("finished", "Evidence worker finished", "", "worker-1")
  turn = "latest-turn"
  add("user_message", longMessage("Latest user prompt"))
  add("reasoning", "Latest reasoning is already local")
  add("tool_call", 'exec({"command":"printf latest"})', "latest")
  add("tool_result", '{"stdout":"Latest result","exit_code":0}', "latest")
  add("agent_message", longMessage("Latest answer"))
  add("done", "Latest answer")

  // Populate the private Playwright DB so both browser and HTTP use the real
  // history reader. No provider or production conversation owns these rows.
  const db = join(process.env.ZWAI_E2E_DATA_DIR ?? ".e2e-data", "zwai.db")
  execFileSync("python3", ["-c", `
import json, sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    db.executemany("""
        INSERT INTO events (thread_id, seq, turn_id, agent_id, kind, text, tool_call_id, err, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, '2026-01-01T00:00:00Z')
    """, [(sys.argv[2], i, r["turn"], r["agent"], r["kind"], r["text"], r["call"], r["err"])
          for i, r in enumerate(json.load(sys.stdin), 1)])
`, db, thread.id], { input: JSON.stringify(rows) })
  return thread.id as string
}

for (const mode of ["user", "developer"] as const) {
  test(`older history moves one visible message at a time in ${mode} mode`, async ({ page, request }, info) => {
    const settings = (await (await request.get("/api/settings")).json()).settings.ui
    const saved = await request.put("/api/settings", { data: { ui: { ...settings, locale: "en", transcript_mode: mode } } })
    expect(saved.ok()).toBe(true)
    try {
      const title = `Message history in ${mode} mode`
      const id = await seedHistory(page, title)
      const path = `/api/threads/${id}/log`
      const pages: { url: URL; body: string }[] = []
      let expansionRequests = 0
      let workerRequests = 0
      let failExpansion = true
      await page.route(`**${path}?*`, async (route) => {
        const url = new URL(route.request().url())
        if (url.searchParams.has("work_from")) {
          expansionRequests++
          if (failExpansion) {
            failExpansion = false
            return route.fulfill({ status: 500, json: { error: "Fixture failure" } })
          }
          return route.continue()
        }
        const response = await route.fetch()
        pages.push({ url, body: await response.text() })
        return route.fulfill({ response })
      })
      await page.route(`**/api/threads/${id}/agents/worker-1/log`, (route) => {
        workerRequests++
        return route.continue()
      })
      await page.goto("/")
      await page.getByTestId("dest-chats").click()
      await page.getByRole("button", { name: title, exact: true }).click()
      const transcript = page.getByTestId("transcript")
      await expect(transcript.getByText("Latest answer", { exact: true })).toBeAttached()
      expect(pages).toHaveLength(1)
      expect(pages[0].body).toContain("Latest reasoning is already local")
      expect(pages[0].body).not.toContain("worker fixture argument")
      await page.getByRole("tab", { name: "Agents" }).click()
      const worker = page.getByTestId("agent-roster").locator('[data-agent-id="worker-1"]')
      await expect(worker).toBeVisible()
      expect(workerRequests).toBe(0)

      expect(expansionRequests).toBe(0)

      // Mouse wheel scrolls to the top. A second wheel at the top still
      // requests history even though scrollTop need not change.
      await transcript.hover()
      const up = async (expected: string, pageCount: number) => {
        await page.mouse.wheel(0, -10000)
        await expect(transcript.getByText(new RegExp(`^${expected}`))).toBeInViewport()
        expect(pages).toHaveLength(pageCount)
        expect(pages[pageCount - 1].url.searchParams.get("view")).toBe("messages")
      }
      await up("Latest user prompt", 2)
      await expect(transcript.getByText("Second answer in the same turn", { exact: true })).toHaveCount(0)
      await up("Second answer in the same turn", 3)
      await expect(transcript.getByText("Earlier answer", { exact: true })).toHaveCount(0)
      await up("Earlier answer", 4)
      await expect(transcript.getByText("Earliest prompt", { exact: true })).toHaveCount(0)
      const older = pages[3].body
      expect(older).toContain("history_work")
      expect(older).not.toContain("Stored historical reasoning")
      expect(older).not.toContain("printf archived-")
      expect(older).not.toContain("Archived result")
      expect(older).not.toContain("worker fixture argument")
      await expect(worker).toBeVisible()
      expect(workerRequests).toBe(0)

      const summary = transcript.getByTestId("work-fold").filter({ hasText: /Thought.*104 tools/ })
      const compact = transcript.getByText(/Context compressed.*78000.*23000/)
      await expect(summary).toHaveCount(1)
      await expect(compact).toBeAttached()
      await expect(transcript.getByText("Plan updated.")).toBeAttached()
      await expect(transcript.getByTestId("ask-card").getByText("Which format?")).toBeAttached()
      await expect(transcript.getByText("Recorded historical error")).toBeAttached()
      const order = await transcript.evaluate((el) => {
        const text = el.textContent ?? ""
        return ["Earlier answer", "Thought · 104 tools", "Context compressed", "Plan updated.",
          "Which format?", "Second answer in the same turn", "Recorded historical error"].map((label) => text.indexOf(label))
      })
      expect(order[0]).toBeGreaterThanOrEqual(0)
      expect(order.slice(1).every((at, i) => at > order[i])).toBe(true)

      // Click the latest local fold after paging, when Playwright scrolling
      // it into view cannot consume the scroll gestures under test.
      const local = transcript.getByTestId("work-fold").filter({ hasText: "Thought · 1 tool" })
      if (mode === "user") {
        await local.click()
        await expect(transcript.getByTestId("thought-toggle")).toBeVisible()
        await local.click()
      } else {
        await expect(local).toHaveCount(0)
        await expect(transcript.getByTestId("thought-toggle")).toHaveCount(1)
      }
      expect(expansionRequests).toBe(0)

      // A failed expansion can retry the same range and render the existing
      // thought and tool rows when the detail arrives.
      await summary.click()
      await expect(transcript.getByRole("alert").filter({ hasText: "Could not load work details." })).toBeVisible()
      await transcript.getByRole("button", { name: "Retry", exact: true }).click()
      const tool = transcript.getByRole("button", { name: /exec.*archived-102/ })
      await expect(tool).toBeAttached()
      if (await tool.getAttribute("aria-expanded") === "false") await tool.click()
      await expect(transcript.getByText("Archived result 102", { exact: true })).toBeVisible()
      expect(expansionRequests).toBeGreaterThan(3)
      await expect(transcript.getByText("worker fixture argument", { exact: true })).toHaveCount(0)
      await summary.click()
      await expect(tool).toHaveCount(0)
      await expect(summary).toHaveAttribute("aria-expanded", "false")

      await worker.click()
      await expect(page.getByTestId("agent-scroller").getByRole("button", { name: /exec.*worker fixture argument/ })).toBeVisible()
      expect(workerRequests).toBe(1)
      await page.screenshot({ path: info.outputPath(`message-history-${mode}.png`) })
    } finally {
      await request.put("/api/settings", { data: { ui: settings } })
    }
  })
}
