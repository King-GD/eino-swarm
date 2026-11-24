import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test"
import { writeFile } from "node:fs/promises"
import { nuwaLooseGroupID, NUWA_GOAL_ID, type NuwaSnapshot, type ThreadWorkSnapshot } from "../src/lib/nuwa"
import { startNuwaFixture } from "./nuwa-fixture"
import { composer, freshConversation, waitForDraftSaved } from "./session"

const collaborationGraph = "Shared work collaboration graph"
const collaborationPaths = "Collaboration paths"

async function openThread(page: Page, projectName: string, id: string) {
  const project = page.getByRole("button", { name: projectName, exact: true })
  if ((await project.getAttribute("aria-expanded")) !== "true") await project.click()
  await page.locator(`[data-testid="thread-row"][data-id="${id}"]`).getByTestId("row-label").click()
}

async function createThread(request: APIRequestContext, project: string, title: string) {
  const response = await request.post("/api/threads", { data: { project_id: project, title } })
  expect(response.ok()).toBe(true)
  return (await response.json()).thread.id as string
}

async function configureNuwaFixture(request: APIRequestContext, url: string) {
  const { settings } = await (await request.get("/api/settings")).json()
  expect(
    (
      await request.put("/api/settings", {
        data: {
          models: {
            default: "nuwa-fixture",
            providers: [
              {
                id: "nuwa-fixture",
                label: "Offline shared-work fixture",
                base_url: url,
                model: "nuwa-fixture",
                api_key: "",
                context_window: 128000,
              },
              {
                id: "nuwa-worker-endpoint",
                label: "Independent worker fixture",
                base_url: url,
                model: "nuwa-worker",
                api_key: "",
                context_window: 128000,
              },
            ],
          },
          swarm: { ...settings.swarm, auto_title: false, composer_predictions: false },
          memory: { ...settings.memory, auto_review: false },
        },
      })
    ).ok()
  ).toBe(true)
}

test("Nuwa creation shares live evidence paths and preserves them after disabling", async ({
  page,
  request,
}, info) => {
  test.skip(process.env.ZWAI_E2E_NUWA !== "1", "requires the real keyless Nuwa model fixture")
  const fixture = await startNuwaFixture()
  const projectName = `Shared work ${Date.now()}`
  let firstThread = ""
  try {
    await configureNuwaFixture(request, fixture.url)
    await page.goto("/")
    await page.getByRole("button", { name: "New project", exact: true }).click()
    await page.getByLabel("Name", { exact: true }).fill(projectName)
    await page
      .getByLabel("Instruction", { exact: true })
      .fill("Compare supplied evidence, preserve uncertainty, and prepare a reviewable result.")
    await page.getByRole("switch", { name: "Nuwa collaboration", exact: true }).click()
    const created = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        new URL(response.url()).pathname === "/api/projects"
    )
    await page.getByRole("button", { name: "Create project", exact: true }).click()
    const { project } = await (await created).json()
    expect(project.nuwa_enabled).toBe(true)
    expect(project.nuwa_planner_provider_id).toBe("nuwa-fixture")
    expect(project.nuwa_planner_model).toBe("nuwa-fixture")
    expect(project.nuwa_executor_provider_id).toBe("nuwa-worker-endpoint")
    expect(project.nuwa_executor_model).toBe("nuwa-worker")
    expect(project.system_prompt).toContain("preserve uncertainty")
    await page
      .getByRole("button", { name: `New conversation in ${projectName}`, exact: true })
      .click()
    firstThread =
      (await page
        .locator('[data-testid="thread-row"][aria-current="true"]')
        .getAttribute("data-id")) ?? ""
    expect(firstThread).not.toBe("")
    const collaboration = page.getByRole("tab", { name: "Work records", exact: true })
    await expect(collaboration).toHaveAttribute("aria-selected", "false")
    await expect(page.getByTestId("nuwa-panel")).toHaveCount(0)
    await collaboration.click()
    await expect(page.getByTestId("nuwa-panel")).toContainText("No work records in this conversation yet.")
    const started = await request.post(`/api/threads/${firstThread}/turns`, {
      data: { text: "Compare the supplied evidence and prepare review notes." },
    })
    expect(started.status()).toBe(202)
    const { turn } = await started.json()
    fixture.source(firstThread, turn.id)
    const graph = page.getByRole("region", { name: collaborationGraph, exact: true })
    await expect(graph.getByRole("button", { name: /Compare the evidence/ })).toContainText(
      "Running"
    )
    fixture.releaseTask()
    await expect(graph.getByRole("button", { name: /Review notes/ })).toBeVisible()
    await expect(graph.getByTestId("nuwa-edge")).toHaveCount(2)
    await expect(graph.getByTestId("nuwa-goal-edge")).toHaveCount(1)
    const resultRow = page
      .getByRole("list", { name: "Shared work nodes", exact: true })
      .getByRole("button")
      .filter({ has: page.getByText("Review notes", { exact: true }) })
    const statusBounds = await resultRow.getByText("Reported", { exact: true }).boundingBox()
    const panelBounds = await page.getByTestId("nuwa-panel").boundingBox()
    expect(statusBounds).not.toBeNull()
    expect(panelBounds).not.toBeNull()
    await writeFile(
      info.outputPath("nuwa-node-status-bounds.png"),
      await page.screenshot({ animations: "disabled" }),
      { mode: 0o600 }
    )
    expect(statusBounds!.x + statusBounds!.width).toBeLessThanOrEqual(
      panelBounds!.x + panelBounds!.width
    )
    await page.getByRole("button", { name: "Expand graph", exact: true }).click()
    const expanded = page.getByRole("dialog", { name: collaborationPaths, exact: true })
    const largeGraph = expanded.getByRole("region", {
      name: collaborationGraph,
      exact: true,
    })
    await expect(largeGraph.getByRole("button", { name: /Review notes/ })).toBeInViewport()
    await largeGraph.getByRole("button", { name: /Review notes/ }).click()
    await expect(expanded.getByTestId("nuwa-node-detail")).toContainText(
      "A result ready for independent review"
    )
    await writeFile(
      info.outputPath("nuwa-expanded-graph.png"),
      await page.screenshot({ animations: "disabled" }),
      { mode: 0o600 }
    )
    await expanded.getByRole("button", { name: "Close", exact: true }).click()
    await graph.getByRole("button", { name: /Review notes/ }).click()
    const output = await request.get(
      (await page
        .getByRole("link", { name: "Open result file", exact: true })
        .getAttribute("href")) ?? ""
    )
    expect(output.ok()).toBe(true)
    expect(await output.text()).toBe(
      "# Review notes\n\nThe conclusion is limited to the measured input.\n"
    )
    await graph.getByRole("button", { name: /Measured finding/ }).click()
    const detail = page.getByTestId("nuwa-node-detail")
    await expect(detail).toContainText("Reported")
    await expect(detail).not.toContainText("Detailed observation")
    await detail.getByRole("button", { name: "Details and evidence", exact: true }).click()
    await expect(detail).toContainText("Detailed observation with its original source.")
    await expect(detail.getByRole("button", { name: /Original task input/ })).toContainText("#1")
    await detail.getByRole("button", { name: /Original task input/ }).click()
    await expect(page.getByRole("tab", { name: "Trace", exact: true })).toHaveAttribute(
      "data-state",
      "active"
    )
    await expect
      .poll(async () => (await (await request.get(`/api/trace/${turn.id}`)).json()).turn.status)
      .toBe("done")
    const original = await (await request.get(`/api/projects/${project.id}/nuwa`)).json()
    expect(original.nodes).toHaveLength(3)
    expect(fixture.workerModels.length).toBeGreaterThan(0)
    expect(original.workers).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          role: "independent-review",
          model_binding: expect.objectContaining({
            provider_id: "nuwa-worker-endpoint",
            model: "nuwa-worker",
          }),
        }),
      ])
    )
    const finding = original.nodes.find((node: { kind: string }) => node.kind === "fact")
    expect(finding.evidence[0]).toMatchObject({ thread_id: firstThread, turn_id: turn.id, seq: 1 })
    await writeFile(
      info.outputPath("nuwa-original-snapshot.json"),
      JSON.stringify(original, null, 2),
      { mode: 0o600 }
    )
    const second = await createThread(request, project.id, "Independent review")
    await page.reload()
    await openThread(page, projectName, second)
    await page.getByRole("tab", { name: "Work records", exact: true }).click()
    await expect(graph.getByRole("button", { name: /Review notes/ })).toBeVisible()
    await expect(page.getByRole("button", { name: "This conversation", exact: true })).toHaveAttribute("aria-pressed", "true")
    await expect(graph.getByRole("button", { name: /Review notes/ })).toBeVisible()
    await page.getByRole("button", { name: "Entire project", exact: true }).click()
    fixture.revise({ id: finding.id, version: finding.version })
    const reviewed = await request.post(`/api/threads/${second}/turns`, {
      data: { text: "Review the recorded evidence." },
    })
    expect(reviewed.status()).toBe(202)
    const { turn: reviewTurn } = await reviewed.json()
    await expect(graph.getByRole("button", { name: /Measured finding/ })).toContainText(
      "Superseded"
    )
    await expect(graph.getByTestId("nuwa-edge")).toHaveCount(2)
    await expect(graph.getByRole("button", { name: /Review notes/ })).toContainText("Needs review")
    await graph.getByRole("button", { name: /Review notes/ }).click()
    await expect(detail).toContainText("Source changed; review this result.")
    await graph.getByRole("button", { name: /Measured finding/ }).click()
    await expect(detail).toContainText("A later check superseded the initial result")
    await detail.getByRole("button", { name: "Details and evidence", exact: true }).click()
    const sourceLink = detail.getByRole("button", { name: /Original task input/ })
    await expect(sourceLink).toContainText("#1")
    await expect
      .poll(async () => (await (await request.get(`/api/trace/${reviewTurn.id}`)).json()).turn.status)
      .toBe("done")
    const newProject = await request.post("/api/projects", {
      data: { name: "Moved evidence project", nuwa_enabled: true },
    })
    expect(newProject.ok()).toBe(true)
    const { project: destination } = await newProject.json()
    const moveSource = async (projectId: string) => {
      const moved = await request.patch(`/api/threads/${firstThread}`, {
        data: { project_id: projectId },
      })
      expect(moved.ok()).toBe(true)
    }
    await moveSource(destination.id)
    await expect(sourceLink).toBeDisabled()
    await expect(sourceLink).toContainText("Source unavailable")
    await moveSource(project.id)
    await expect(sourceLink).toBeEnabled()
    const replacement = await startNuwaFixture("discussion")
    try {
      await configureNuwaFixture(request, replacement.url)
      replacement.releaseTask()
      const resent = await request.post(`/api/threads/${firstThread}/turns`, {
        data: { text: "Discuss the replacement interpretation.", from_event_seq: 1 },
      })
      expect(resent.status()).toBe(202)
      const { turn: replacementTurn } = await resent.json()
      await expect
        .poll(async () => (await (await request.get(`/api/trace/${replacementTurn.id}`)).json()).turn.status)
        .toBe("done")
      await expect(sourceLink).toBeDisabled()
      await expect(sourceLink).toContainText("Source unavailable")
      expect(replacement.workerModels).toEqual([])
      expect(replacement.errors).toEqual([])
    } finally {
      replacement.releaseTask()
      await request.post(`/api/threads/${firstThread}/interrupt`).catch(() => undefined)
      await replacement.close()
    }
    const wrap = page
      .getByTestId("project-wrap")
      .filter({ has: page.getByRole("button", { name: projectName, exact: true }) })
    await wrap.hover()
    await wrap
      .getByRole("button", { name: `Project options for ${projectName}`, exact: true })
      .click()
    await page.getByRole("menuitem", { name: "Edit", exact: true }).click()
    await expect(
      page.getByRole("switch", { name: "Nuwa collaboration", exact: true })
    ).toBeChecked()
    await page.getByRole("switch", { name: "Nuwa collaboration", exact: true }).click()
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await page.reload()
    await openThread(page, projectName, second)
    await page.getByRole("tab", { name: "Work records", exact: true }).click()
    await expect(page.getByTestId("nuwa-panel")).toContainText("Nuwa collaboration is off")
    await expect(page.getByTestId("nuwa-panel")).toContainText("Current work continues")
    await expect(graph.getByRole("button", { name: /Measured finding/ })).toContainText(
      "Superseded"
    )
    expect(fixture.errors).toEqual([])
    await writeFile(
      info.outputPath("nuwa-final-snapshot.json"),
      JSON.stringify(await (await request.get(`/api/projects/${project.id}/nuwa`)).json(), null, 2),
      { mode: 0o600 }
    )
    await writeFile(
      info.outputPath("nuwa-shared-work.png"),
      await page.screenshot({ animations: "disabled" }),
      { mode: 0o600 }
    )
    await moveSource(destination.id)
    const deleted = await request.delete(`/api/threads/${firstThread}`)
    expect(deleted.status()).toBe(204)
    await expect(graph.getByRole("button", { name: /Measured finding/ })).toHaveCount(0)
    await expect(page.getByTestId("nuwa-panel")).toContainText("Nuwa collaboration is off")
  } finally {
    fixture.releaseTask()
    if (firstThread)
      await request.post(`/api/threads/${firstThread}/interrupt`).catch(() => undefined)
    await fixture.close()
  }
})

test("published artifacts retain their recorded turn and publishers after reload", async ({
  page,
  request,
}, info) => {
  test.skip(process.env.ZWAI_E2E_NUWA !== "1", "requires the real keyless Nuwa model fixture")
  const fixture = await startNuwaFixture("published")
  const projectName = `Published work ${Date.now()}`
  let thread = ""
  try {
    await configureNuwaFixture(request, fixture.url)
    const created = await request.post("/api/projects", {
      data: { name: projectName, nuwa_enabled: true },
    })
    expect(created.ok()).toBe(true)
    const { project } = await created.json()
    expect(project.nuwa_planner_model).toBe("nuwa-fixture")
    expect(project.nuwa_executor_model).toBe("nuwa-worker")
    thread = await createThread(request, project.id, "Publish independent artifacts")
    await page.goto("/")
    await openThread(page, projectName, thread)
    await page.getByRole("tab", { name: "Work records", exact: true }).click()
    fixture.releaseTask()
    const started = await request.post(`/api/threads/${thread}/turns`, {
      data: { text: "Publish the independently produced review artifacts." },
    })
    expect(started.status()).toBe(202)
    const { turn } = await started.json()
    await expect
      .poll(async () => (await (await request.get(`/api/trace/${turn.id}`)).json()).turn.status)
      .toBe("done")
    const persisted = await request.get(`/api/projects/${project.id}/nuwa`)
    expect(persisted.ok()).toBe(true)
    const snapshot = (await persisted.json()) as NuwaSnapshot
    expect(snapshot.nodes).toHaveLength(2)
    const managerResult = snapshot.nodes.find((node) => node.title === "Review notes")
    const workerResult = snapshot.nodes.find((node) => node.title === "Field observations")
    const worker = snapshot.workers.find((entry) => entry.role === "independent-review")
    if (!managerResult || !workerResult || !worker) throw new Error("Recorded publishers are missing")
    expect(managerResult.agent_id).toBe("manager")
    expect(workerResult).toMatchObject({ agent_id: worker.agent_id, generation: worker.generation })
    expect(worker.agent_id).not.toBe(managerResult.agent_id)
    for (const node of [managerResult, workerResult]) {
      expect(node).toMatchObject({ kind: "artifact", thread_id: thread, turn_id: turn.id })
      expect(node.depends_on ?? []).toEqual([])
    }

    const assertOrigins = async (graph: Locator) => {
      // Two independently published results share no dependency. They collapse
      // into one result row and one dashed arrow instead of a card each.
      await expect(graph.getByTestId("nuwa-edge")).toHaveCount(0)
      await expect(graph.getByTestId("nuwa-origin")).toHaveCount(0)
      await expect(graph.getByTestId("nuwa-goal")).toHaveCount(1)
      const group = graph.getByTestId("nuwa-loose-group")
      await expect(group).toHaveAttribute("data-kind", "artifact")
      await expect(group).toHaveAttribute("data-count", "2")
      const edges = graph.getByTestId("nuwa-goal-edge")
      await expect(edges).toHaveCount(1)
      const routed = await edges.evaluateAll((paths) => paths.map((path) => [
        path.getAttribute("data-from"),
        path.getAttribute("data-to"),
        getComputedStyle(path).strokeDasharray !== "none",
      ]))
      expect(routed).toEqual([[nuwaLooseGroupID("artifact"), NUWA_GOAL_ID, true]])
      await group.click()
      for (const [node, author] of [
        [managerResult, "Manager"],
        [workerResult, worker.role],
      ] as const) {
        const card = graph.getByRole("button", { name: new RegExp(node.title) })
        await expect(card).toHaveAttribute("data-node-id", node.id)
        await expect(card).toHaveAccessibleDescription(new RegExp(author))
        await expect(card).toHaveAccessibleDescription(new RegExp(`in turn ${turn.id}`))
      }
    }

    const sidebarPath = page.getByRole("region", { name: collaborationPaths, exact: true })
    await assertOrigins(sidebarPath)
    const group = sidebarPath.getByTestId("nuwa-loose-group")
    await expect(group).toBeInViewport({ ratio: 1 })
    for (const result of [managerResult, workerResult]) {
      const card = sidebarPath.getByRole("button", { name: new RegExp(result.title) })
      await expect(card).toBeInViewport({ ratio: 1 })
    }
    for (const [result, contents] of [
      [managerResult, "# Review notes\n\nThe conclusion is limited to the measured input.\n"],
      [workerResult, "# Field observations\n\nObserved facts were recorded.\n"],
    ] as const) {
      const card = sidebarPath.getByRole("button", { name: new RegExp(result.title) })
      await card.click()
      const output = await request.get(
        (await page
          .getByRole("link", { name: "Open result file", exact: true })
          .getAttribute("href")) ?? ""
      )
      expect(output.ok()).toBe(true)
      expect(await output.text()).toBe(contents)
    }
    await page.reload()
    await openThread(page, projectName, thread)
    await page.getByRole("tab", { name: "Work records", exact: true }).click()
    await page.getByRole("button", { name: "Expand graph", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: collaborationPaths, exact: true })
    await assertOrigins(dialog.getByRole("region", { name: collaborationPaths, exact: true }))
    await expect(dialog.getByText("Converges to goal", { exact: true })).toBeVisible()
    await writeFile(
      info.outputPath("nuwa-published-artifact-origins.png"),
      await page.screenshot({ animations: "disabled" }),
      { mode: 0o600 }
    )
    expect(fixture.workerModels.length).toBeGreaterThan(0)
    expect(fixture.errors).toEqual([])
  } finally {
    fixture.releaseTask()
    if (thread) await request.post(`/api/threads/${thread}/interrupt`).catch(() => undefined)
    await fixture.close()
  }
})

test("a Nuwa discussion stays visible when it records no shared work", async ({ page, request }, info) => {
  test.skip(process.env.ZWAI_E2E_NUWA !== "1", "requires the real keyless Nuwa model fixture")
  const fixture = await startNuwaFixture("discussion")
  const projectName = `Discussion ${Date.now()}`
  let thread = ""
  try {
    await configureNuwaFixture(request, fixture.url)
    await page.goto("/")
    await page.getByRole("button", { name: "New project", exact: true }).click()
    await page.getByLabel("Name", { exact: true }).fill(projectName)
    await page.getByRole("switch", { name: "Nuwa collaboration", exact: true }).click()
    await page.getByRole("button", { name: "Create project", exact: true }).click()
    await page.getByRole("button", { name: `New conversation in ${projectName}`, exact: true }).click()
    thread = (await page.locator('[data-testid="thread-row"][aria-current="true"]').getAttribute("data-id")) ?? ""
    expect(thread).not.toBe("")
    await page.getByRole("tab", { name: "Work records", exact: true }).click()

    const started = await request.post(`/api/threads/${thread}/turns`, {
      data: { text: "Discuss how the available evidence limits the conclusion." },
    })
    expect(started.status()).toBe(202)
    const { turn } = await started.json()
    const activity = page.getByRole("region", { name: "Latest turn in this conversation" })
    await expect(activity.getByRole("status")).toHaveText("Running")
    await expect(page.getByRole("region", { name: collaborationGraph })).toHaveCount(0)
    fixture.releaseTask()
    await expect(activity.getByRole("status")).toHaveText("Done")
    await writeFile(info.outputPath("nuwa-discussion-turn.png"), await page.screenshot({ animations: "disabled" }), { mode: 0o600 })

    await activity.getByRole("button", { name: "Open turn Trace" }).click()
    await expect(page.getByRole("tab", { name: "Trace", exact: true })).toHaveAttribute("data-state", "active")
    await expect(page.getByRole("tabpanel")).toContainText(turn.id)
    await page.getByRole("tab", { name: "Work records", exact: true }).click()
    await activity.getByRole("button", { name: "Files", exact: true }).click()
    await expect(page.getByRole("tab", { name: "Files", exact: true })).toHaveAttribute(
      "data-state",
      "active"
    )
    await expect(page.getByRole("tabpanel", { name: "Files" }).getByRole("status")).toHaveText("No deliverables yet")
    await expect(
      page.getByRole("treeitem", { name: "direct-result.txt", exact: true })
    ).toHaveCount(0)
    expect(fixture.workerModels).toEqual([])
    expect(fixture.errors).toEqual([])
  } finally {
    fixture.releaseTask()
    if (thread) await request.post(`/api/threads/${thread}/interrupt`).catch(() => undefined)
    await fixture.close()
  }
})


async function runWorkTurn(request: APIRequestContext, thread: string, text: string) {
  const response = await request.post(`/api/threads/${thread}/turns`, { data: { text } })
  expect(response.status()).toBe(202)
  const { turn } = await response.json()
  await expect.poll(async () => (await (await request.get(`/api/trace/${turn.id}`)).json()).turn.status).toBe("done")
}

async function workRecords(request: APIRequestContext, thread: string) {
  const response = await request.get(`/api/threads/${thread}/work`)
  expect(response.ok()).toBe(true)
  return (await response.json() as ThreadWorkSnapshot).nodes
}

test("ordinary conversations merge private records across two live windows and restore refuted work", async ({ page, context, request }) => {
  test.skip(process.env.ZWAI_E2E_NUWA !== "1", "requires the real keyless work record fixture")
  const fixture = await startNuwaFixture("ordinary")
  try {
    await configureNuwaFixture(request, fixture.url)
    const projectName = `Work records ${Date.now()}`
    const created = await request.post("/api/projects", { data: { name: projectName } })
    expect(created.ok()).toBe(true)
    const { project } = await created.json()
    const first = await createThread(request, project.id, "Make an observation")
    const second = await createThread(request, project.id, "Review the observation")
    await page.goto("/")
    await openThread(page, projectName, first)
    const tab = page.getByRole("tab", { name: "Work records", exact: true })
    await expect(tab).toHaveAttribute("aria-selected", "false")
    await tab.click()
    await runWorkTurn(request, first, "Record a bounded observation.")
    const graph = page.getByRole("region", { name: collaborationGraph, exact: true })
    const record = graph.getByRole("button", { name: /Bounded observation/ })
    await expect(record).toBeVisible()
    const records = await workRecords(request, first)
    expect(records).toHaveLength(1)
    const [initial] = records
    expect(initial.scope).toBe("thread")
    expect(await workRecords(request, second)).toEqual([])

    const peer = await context.newPage()
    try {
      await peer.goto("/")
      await openThread(peer, projectName, second)
      await peer.getByRole("tab", { name: "Work records", exact: true }).click()
      await expect(peer.getByTestId("nuwa-panel")).toContainText("No work records in this conversation yet.")
      // The browser needs an HTTP/1 slot for both of these writes while both
      // chat/presence streams and both work panels are open.
      await composer(peer).fill("Confirm draft save alongside the work stream.")
      await waitForDraftSaved(peer)
      await record.click()
      await page.getByRole("button", { name: "Merge into project", exact: true }).click()
      const peerGraph = peer.getByRole("region", { name: collaborationGraph, exact: true })
      const peerRecord = peerGraph.getByRole("button", { name: /Bounded observation/ })
      await expect(peerRecord).toBeVisible()
      await expect(page.getByTestId("nuwa-node-detail")).toContainText("Project record")
      const [shared] = await workRecords(request, second)
      expect(shared).toMatchObject({ id: initial.id, scope: "project", version: initial.version + 1 })

      fixture.revise({ id: shared.id, version: shared.version, status: "retracted" })
      await runWorkTurn(request, second, "Update the observation after contrary evidence.")
      await expect(peerRecord).toHaveCount(0)
      const collapsed = peer.getByRole("button", { name: "Invalidated: 1", exact: true })
      await expect(collapsed).toBeVisible()
      await collapsed.click()
      await expect(peer.getByRole("list", { name: "Invalidated records" })).toContainText("Refuted")

      const [refuted] = await workRecords(request, second)
      fixture.revise({ id: refuted.id, version: refuted.version, status: "reported" })
      await runWorkTurn(request, second, "Restore the same observation with the new evidence.")
      await expect(peerRecord).toContainText("Reported")
      await expect(collapsed).toHaveCount(0)
      const [restored] = await workRecords(request, second)
      expect(restored).toMatchObject({ id: initial.id, status: "reported", version: refuted.version + 1 })
    } finally {
      await peer.close()
    }
    expect(fixture.errors).toEqual([])
  } finally {
    await fixture.close()
  }
})

test("standalone conversations record work without a project destination", async ({ page, request }) => {
  test.skip(process.env.ZWAI_E2E_NUWA !== "1", "requires the real keyless work record fixture")
  const fixture = await startNuwaFixture("ordinary")
  try {
    await configureNuwaFixture(request, fixture.url)
    await freshConversation(page)
    const thread = (await page.locator('[data-testid="thread-row"][aria-current="true"]').getAttribute("data-id"))!
    const tab = page.getByRole("tab", { name: "Work records", exact: true })
    await expect(tab).toHaveAttribute("aria-selected", "false")
    await tab.click()
    await expect(page.getByRole("button", { name: "Entire project", exact: true })).toBeDisabled()
    await runWorkTurn(request, thread, "Record a standalone observation.")
    await page.getByRole("region", { name: collaborationGraph, exact: true })
      .getByRole("button", { name: /Bounded observation/ }).click()
    const detail = page.getByTestId("nuwa-node-detail")
    await expect(detail).toContainText("Conversation record")
    await expect(detail.getByRole("button", { name: "Merge into project", exact: true })).toHaveCount(0)
    expect(await workRecords(request, thread)).toEqual([expect.objectContaining({ scope: "thread" })])
    expect(fixture.errors).toEqual([])
  } finally {
    await fixture.close()
  }
})
