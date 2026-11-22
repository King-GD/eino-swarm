import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { expect, test } from "@playwright/test"

test("old settings and a saved Clients tab cannot reopen local agent tasks", async ({ page, request }) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "zwai-e2e-clients-"))
  try {
    const dir = path.join(root, "projects", "work")
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(path.join(dir, "local.jsonl"), JSON.stringify({
      type: "user", message: { content: "Private local task" },
    }) + "\n")
    const settings = await request.put("/api/settings", {
      data: { clients: { enabled: true, claude_dir: root } },
    })
    expect(settings.ok()).toBeTruthy()

    await page.addInitScript(() => localStorage.setItem("zwai.desk.dest", "clients"))
    const clientsRequests: string[] = []
    page.on("request", req => {
      if (new URL(req.url()).pathname.startsWith("/api/clients")) clientsRequests.push(req.url())
    })
    await page.goto("/")
    await expect(page.getByTestId("dest-projects")).toHaveAttribute("aria-current", "page")
    await expect(page.getByTestId("dest-clients")).toHaveCount(0)
    await expect(page.getByTestId("clients-list")).toHaveCount(0)
    await page.getByTestId("dest-chats").click()
    await expect(page.getByTestId("dest-chats")).toHaveAttribute("aria-current", "page")
    expect(clientsRequests).toEqual([])

    const catalog = await request.get("/api/clients")
    expect(catalog.ok()).toBeTruthy()
    expect(await catalog.json()).toEqual({ enabled: false, tools: [] })
    expect((await request.get("/api/clients/task", { params: { id: "claude:local" } })).status()).toBe(404)
  } finally {
    fs.rmSync(root, { recursive: true, force: true })
  }
})
