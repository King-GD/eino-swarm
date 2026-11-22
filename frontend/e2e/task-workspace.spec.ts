import { writeFile } from "node:fs/promises"
import { join } from "node:path"
import { expect, test } from "@playwright/test"

test("opens task results first and reads shared project files only after switching", async ({ page, request }) => {
  const created = await request.post("/api/projects", { data: { name: "Workspace scope acceptance" } })
  expect(created.ok()).toBe(true)
  const { project } = await created.json()
  try {
    const createTask = async (title: string) => {
      const response = await request.post("/api/threads", { data: { title, project_id: project.id } })
      expect(response.ok()).toBe(true)
      const { thread } = await response.json()
      return `/api/threads/${thread.id}`
    }
    const first = await createTask("First workspace task")
    const second = await createTask("Second workspace task")
    const firstFiles = await (await request.get(`${first}/files`)).json()
    const secondFiles = await (await request.get(`${second}/files`)).json()
    const projectFiles = await (await request.get(`${first}/files?scope=project`)).json()
    expect(firstFiles.workspace).not.toBe(secondFiles.workspace)
    await writeFile(join(firstFiles.workspace, "results", "report.md"), "# First task deliverable")
    await writeFile(join(firstFiles.workspace, "work", "draft.txt"), "first intermediate file")
    await writeFile(join(secondFiles.workspace, "results", "report.md"), "# Second task deliverable")
    await writeFile(join(projectFiles.workspace, "report.md"), "# Shared project document")
    await page.goto("/")
    const projectRow = page.getByRole("button", { name: "Workspace scope acceptance", exact: true })
    if ((await projectRow.getAttribute("aria-expanded")) !== "true") await projectRow.click()
    await page.locator(`[data-testid="thread-row"][data-id="${first.split("/").at(-1)}"]`).getByTestId("row-label").click()
    await page.getByRole("tab", { name: "Files", exact: true }).click()
    const tree = page.getByTestId("file-tree")
    await expect(tree.getByRole("treeitem", { name: "Results", exact: true })).toHaveAttribute("aria-expanded", "true")
    await expect(tree.getByRole("treeitem", { name: "Working files", exact: true })).toHaveAttribute("aria-expanded", "false")
    await expect(tree.getByRole("treeitem", { name: "draft.txt", exact: true })).toHaveCount(0)
    await tree.getByRole("treeitem", { name: "report.md", exact: true }).click()
    const preview = page.getByTestId("file-preview")
    await expect(preview.getByRole("heading", { name: "First task deliverable" })).toBeVisible()
    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: "Project folder", exact: true }).click()
    await expect(tree.getByRole("treeitem", { name: "Task workspaces", exact: true })).toBeVisible()
    const shared = tree.getByRole("treeitem", { name: "report.md", exact: true })
    await expect(shared.getByTitle("Delete")).toHaveCount(0)
    await shared.click()
    await expect(preview.getByRole("heading", { name: "Shared project document" })).toBeVisible()
    await page.keyboard.press("Escape")
    const rejected = await request.delete(`${first}/download/report.md?scope=project`)
    expect(rejected.status()).toBe(400)
    await page.locator(`[data-testid="thread-row"][data-id="${second.split("/").at(-1)}"]`).getByTestId("row-label").click()
    await expect(page.getByRole("button", { name: "Task files", exact: true })).toHaveAttribute("aria-pressed", "true")
    await tree.getByRole("treeitem", { name: "report.md", exact: true }).click()
    await expect(preview.getByRole("heading", { name: "Second task deliverable" })).toBeVisible()
  } finally {
    await request.delete(`/api/projects/${project.id}`).catch(() => undefined)
  }
})
