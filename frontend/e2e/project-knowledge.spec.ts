import { appendFile, mkdir, writeFile } from "node:fs/promises"
import { join } from "node:path"
import { expect, test, type Page } from "@playwright/test"
import { freshConversation, send, waitForIdle } from "./session"

async function openProjectConversation(page: Page, name: string, memoryEnabled = false) {
  const projectResponse = await page.request.post("/api/projects", { data: { name, memory_enabled: memoryEnabled } })
  expect(projectResponse.status()).toBe(201)
  const { project } = await projectResponse.json() as { project: { id: string } }
  const title = "Original conversation"
  const threadResponse = await page.request.post("/api/threads", { data: { project_id: project.id, title } })
  expect(threadResponse.status()).toBe(201)
  const { thread } = await threadResponse.json() as { thread: { id: string } }
  await page.goto("/")
  const row = page.locator(`[data-testid="thread-row"][data-id="${thread.id}"]`)
  if (!await row.isVisible()) await page.getByRole("button", { name, exact: true }).click()
  await row.getByTestId("row-label").click()
  await expect(page.getByTestId("thread-title")).toHaveText(title)
  return { projectId: project.id, threadId: thread.id, title }
}

const knowledgeTab = (page: Page) => page.getByRole("tab", { name: "Knowledge", exact: true })

test("a conversation outside projects keeps its shared library and explains knowledge admission", async ({ page }) => {
  await freshConversation(page)
  const task = page.getByRole("group", { name: "Task", exact: true })
  const shared = page.getByRole("group", { name: "Shared library", exact: true })
  await expect(task.getByRole("tab")).toHaveText(["Agents", "Files", "Trace", "Work records"])
  await expect(shared.getByRole("tab", { name: "Memory", exact: true })).toBeVisible()
  await shared.getByRole("tab", { name: "Knowledge", exact: true }).click()
  await expect(page.getByTestId("knowledge-panel")).toContainText("Move this conversation into a project")
  await expect(page.getByTestId("knowledge-enable")).toHaveCount(0)
  await expect(page.getByTestId("knowledge-distill")).toHaveCount(0)
})

test("enable, browse, follow links, and return through the project wiki", async ({ page }, info) => {
  const name = `Wiki navigation ${Date.now()}`
  const { projectId, threadId } = await openProjectConversation(page, name)
  const kb = `/api/projects/${projectId}/knowledge`
  const projectGroup = page.getByRole("group", { name, exact: true })
  await expect(projectGroup.getByText(name)).toHaveAttribute("title", name)
  await expect(projectGroup.getByRole("tab", { name: "Memory", exact: true })).toBeVisible()
  await projectGroup.getByRole("tab", { name: "Knowledge", exact: true }).click()
  const enabled = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === kb)
  await page.getByTestId("knowledge-enable").click()
  expect((await enabled).status()).toBe(200)
  const { data_dir: dataDir } = await (await page.request.get("/api/meta")).json() as { data_dir: string }
  const knowledgeDir = join(dataDir, "projects", projectId, "knowledge")
  const dir = join(knowledgeDir, "pages", "entries")
  await mkdir(dir, { recursive: true })
  await appendFile(join(knowledgeDir, "schema.yaml"), [
    "\nviews:", "  - id: linked", "    root_type: entry", "    follow: [related]", "    depth: 2", "",
  ].join("\n"))
  await writeFile(join(dir, "entry-one.md"), [
    "---", "id: entry-one", "type: entry", "title: First entry", "links:", "  - rel: related", "    to: entry-two",
    "sources:", "  - kind: event", "    thread_id: th_missing", "    seq: 1", "    classification: primary", "---",
    "Read [[entry-two|the next entry]] and [[entry-missing]].", "",
  ].join("\n"))
  await writeFile(join(dir, "entry-two.md"), [
    "---", "id: entry-two", "type: entry", "title: Second entry", "---", "Second entry body.", "",
  ].join("\n"))
  // A tab switch reads the files again, including edits made outside the app.
  await page.getByRole("tab", { name: "Agents", exact: true }).click()
  await knowledgeTab(page).click()
  const tree = page.getByTestId("knowledge-tree")
  await expect(tree.getByRole("button", { name: "First entry" })).toBeVisible()
  await page.getByTestId("knowledge-filter").fill("second")
  await expect(tree.getByRole("button", { name: "First entry" })).toHaveCount(0)
  await expect(tree.getByRole("button", { name: "Second entry" })).toBeVisible()
  await page.getByTestId("knowledge-filter").clear()
  await page.getByTestId("knowledge-view").click()
  await page.getByRole("option", { name: "linked", exact: true }).click()
  await expect(tree.getByText("related", { exact: true })).toBeVisible()
  await expect(tree.getByRole("button", { name: "Second entry", exact: true })).toHaveCount(2)
  await page.getByTestId("knowledge-view").click()
  await page.getByRole("option", { name: "Pending", exact: true }).click()
  await expect(tree).toContainText("entry-missing")
  await tree.getByRole("button", { name: "entry-one" }).click()
  const detail = page.getByTestId("knowledge-page")
  await expect(detail.getByRole("heading", { name: "First entry" })).toBeVisible()
  await expect(detail.getByText("Primary material", { exact: true })).toBeVisible()
  await expect(detail.getByText("Event #1 · Source unavailable", { exact: true })).toBeVisible()
  await detail.getByRole("button", { name: "the next entry" }).click()
  await expect(detail.getByRole("heading", { name: "Second entry" })).toBeVisible()
  await expect(detail.getByRole("heading", { name: "Backlinks" })).toBeVisible()
  await detail.getByRole("button", { name: "Expand preview" }).click()
  await expect(page.getByTestId("knowledge-preview")).toContainText("Second entry body.")
  const screenshot = info.outputPath("knowledge-preview.png")
  await page.screenshot({ path: screenshot, animations: "disabled" })
  await info.attach("knowledge-preview", { path: screenshot, contentType: "image/png" })
  await page.keyboard.press("Escape")
  await expect(page.getByTestId("knowledge-preview")).toHaveCount(0)
  await page.getByTestId("knowledge-back").click()
  await expect(detail.getByRole("heading", { name: "First entry" })).toBeVisible()
  await page.getByTestId("knowledge-back").click()
  await expect(tree).toBeVisible()

  await page.getByRole("tab", { name: "Agents", exact: true }).click()
  const started = page.waitForResponse((response) => response.request().method() === "POST" &&
    new URL(response.url()).pathname === `/api/threads/${threadId}/deliver`)
  await send(page, "Refer to [[entry-two|the wiki reference]].")
  const { turn } = await (await started).json() as { turn: { id: string } }
  await waitForIdle(page)
  await writeFile(join(dir, "entry-two.md"), [
    "---", "id: entry-two", "type: entry", "title: Second entry", `updated_turn: ${turn.id}`,
    `updated_thread: ${threadId}`, "updated_at: 2026-01-01T00:00:00Z", "---", "Second entry body.", "",
  ].join("\n"))
  await page.getByTestId("transcript").getByTestId("knowledge-wikilink").filter({ hasText: "the wiki reference" }).first().click()
  await expect(knowledgeTab(page)).toHaveAttribute("aria-selected", "true")
  await expect(detail.getByRole("heading", { name: "Second entry" })).toBeVisible()
  await detail.getByRole("button", { name: "Open Trace", exact: true }).click()
  await expect(page.getByRole("tab", { name: "Trace", exact: true })).toHaveAttribute("aria-selected", "true")
  expect((await page.request.delete(`/api/projects/${projectId}`)).status()).toBe(204)
})


for (const [scope, value, tab] of [
  ["Latest turn", "latest_turn", "Memory"],
  ["Current task", "thread", "Knowledge"],
  ["Current project", "project", "Knowledge"],
] as const) {
  test(`Distill ${scope.toLowerCase()} in place and share its receipt across panels`, async ({ page }) => {
    const { projectId, threadId, title } = await openProjectConversation(page, `Distillation ${value} ${Date.now()}`, true)
    await send(page, "Keep the observation and retain the reusable procedure.")
    await waitForIdle(page)
    const endpoint = `/api/projects/${projectId}/distill`
    await page.getByRole("tab", { name: tab === "Memory" ? /^Memory/ : "Knowledge" }).click()
    const admitted = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === endpoint)
    await page.getByTestId("knowledge-distill").click()
    const enable = page.getByRole("menuitem", { name: `Enable knowledge and distill · ${scope}`, exact: true })
    await expect(enable).toBeVisible()
    await enable.click()
    const reply = await admitted
    expect(reply.status()).toBe(202)
    expect(reply.request().postDataJSON()).toEqual({
      scope: value,
      ...(value === "project" ? {} : { thread_id: threadId }),
      enable_knowledge: true,
    })
    const job = await reply.json()
    const status = page.getByTestId("project-distill-status")
    await expect(status).toContainText("Distillation finished.", { timeout: 45_000 })
    expect(await (await page.request.get(endpoint)).json()).toMatchObject({
      review_id: job.review_id, state: "completed", thread_id: threadId,
      knowledge: [expect.objectContaining({ action: "write_page" })],
    })
    await expect(page.getByTestId("thread-title")).toHaveText(title)
    await page.getByRole("tab", { name: tab === "Memory" ? "Knowledge" : /^Memory/ }).click()
    await expect(status).toContainText("1 knowledge changes")
    await status.getByRole("button", { name: "Open Trace" }).click()
    await expect(page.getByRole("tab", { name: "Trace", exact: true })).toHaveAttribute("aria-selected", "true")
    expect((await page.request.delete(`/api/projects/${projectId}`)).status()).toBe(204)
  })
}

test("automatic review updates the open knowledge panel and the original Trace", async ({ page }) => {
  const { settings } = await (await page.request.get("/api/settings")).json()
  const { projectId, threadId } = await openProjectConversation(page, `Automatic knowledge ${Date.now()}`, true)
  try {
    expect((await page.request.put("/api/settings", { data: {
      memory: { ...settings.memory, enabled: true, auto_review: true, notifications: "on" },
    } })).ok()).toBe(true)
    await knowledgeTab(page).click()
    await page.getByTestId("knowledge-enable").click()
    const tree = page.getByTestId("knowledge-tree")
    await expect(tree).toContainText("No pages yet.")
    const observed = "Record this observation with its verification procedure."
    const delivery = page.waitForResponse((response) => response.request().method() === "POST" &&
      new URL(response.url()).pathname === `/api/threads/${threadId}/deliver`)
    await send(page, observed)
    const { turn } = await (await delivery).json() as { turn: { id: string } }
    // Observe live delivery and file refresh before a history or Trace query can hide a missing event.
    await expect(page.getByTestId("memory-notice")).toContainText("Knowledge updated", { timeout: 30_000 })
    await expect(tree.getByRole("button", { name: observed, exact: true })).toBeVisible()
    const receipt = await (await page.request.get(`/api/trace/${turn.id}`)).json()
    const reviews = receipt.events.filter((event: { kind: string }) => event.kind === "memory_review")
    expect(reviews).toHaveLength(1)
    expect(JSON.parse(reviews[0].text)).toMatchObject({
      project_id: projectId, changed: true, notes: { add: 1 },
      skills: [expect.objectContaining({ action: "create" })],
      knowledge: [expect.objectContaining({ action: "write_page", title: observed })],
    })
  } finally {
    await page.request.put("/api/settings", { data: { memory: settings.memory } })
    await page.request.delete(`/api/projects/${projectId}`)
  }
})
