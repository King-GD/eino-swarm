import { appendFile, mkdir, writeFile } from "node:fs/promises"
import { join } from "node:path"
import { expect, test, type Page } from "@playwright/test"
import { freshConversation, send, waitForIdle } from "./session"

async function openProjectConversation(page: Page, name: string) {
  const projectResponse = await page.request.post("/api/projects", { data: { name, memory_enabled: false } })
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
  await expect(task.getByRole("tab")).toHaveText(["Agents", "Files", "Trace"])
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

for (const scope of ["Current task", "Current project"] as const) {
  test(`Distill ${scope.toLowerCase()} enables the wiki and sends a scoped ordinary turn`, async ({ page }) => {
    const { projectId, threadId, title } = await openProjectConversation(page, `Distill ${scope} ${Date.now()}`)
    const kb = `/api/projects/${projectId}/knowledge`
    await knowledgeTab(page).click()
    const create = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/threads")
    const deliver = page.waitForResponse((response) => response.request().method() === "POST" && /\/api\/threads\/[^/]+\/steer$/.test(new URL(response.url()).pathname))
    await page.getByTestId("knowledge-distill").click()
    await page.getByRole("menuitem", { name: scope, exact: true }).click()
    const created = await create
    expect(created.status()).toBe(201)
    const { thread } = await created.json() as { thread: { id: string; title: string; project_id: string } }
    expect(thread.id).not.toBe(threadId)
    expect(thread.project_id).toBe(projectId)
    expect(thread.title).toBe(scope === "Current task" ? `Distill task knowledge: ${title}` : "Distill project knowledge")
    const sent = await deliver
    expect(new URL(sent.url()).pathname).toBe(`/api/threads/${thread.id}/steer`)
    expect(sent.status()).toBe(202)
    expect(await sent.json()).toMatchObject({ steered: false, turn: { thread_id: thread.id } })
    expect(sent.request().postDataJSON()).toMatchObject({
      text: scope === "Current task"
        ? `Distill knowledge from the history of this conversation only. Conversation ID: ${threadId}. Conversation title: ${JSON.stringify(title)}.`
        : "Distill knowledge from the history of all conversations in the current project.",
    })
    expect(sent.request().postDataJSON()).not.toHaveProperty("history_report")
    expect(await (await page.request.get(kb)).json()).toMatchObject({ enabled: true })
    await expect(page.getByTestId("thread-title")).toHaveText(thread.title)
    await waitForIdle(page)
    expect((await page.request.delete(`/api/projects/${projectId}`)).status()).toBe(204)
  })
}
