import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { NuwaPanel } from "./nuwa-panel"
import { NuwaGraph } from "./nuwa-graph"
import { useApp } from "@/store/app"
import { useTraceFocus } from "@/store/trace-focus"
import { nuwaAPI, subscribeNuwa } from "@/lib/nuwa-api"
import { nuwaLooseGroupID, NUWA_GOAL_ID, type NuwaNode, type NuwaSnapshot } from "@/lib/nuwa"
import type { Project } from "@/lib/types"
import { publishWorkEvent } from "@/lib/nuwa-events"

vi.mock("@/lib/nuwa-api", () => ({ nuwaAPI: { snapshot: vi.fn(), threadSnapshot: vi.fn(), promote: vi.fn() }, subscribeNuwa: vi.fn() }))
const project: Project = {
  id: "p", name: "Shared project", system_prompt: "", workdir: "", resolved_workdir: "",
  memory_enabled: false, memory_dir: "", nuwa_enabled: true, created_at: "", updated_at: "",
}
const node = (id: string, patch: Partial<NuwaNode> = {}): NuwaNode => ({
  id, title: `Node ${id}`, scope: "project", kind: "task", summary: `Summary ${id}`, status: "pending", version: 1,
  depends_on: [], thread_id: "a", turn_id: "turn1", created_at: "", updated_at: "", ...patch,
})
const snapshot: NuwaSnapshot = {
  project_id: "p", revision: 1,
  nodes: [
    node("plan", { status: "done" }),
    node("finding", { kind: "fact", status: "reported", depends_on: ["plan"], detail: "Full source evidence", evidence: [{ thread_id: "a", turn_id: "turn1", seq: 17, label: "Measured result" }] }),
    node("output", { kind: "artifact", status: "reported", depends_on: ["finding"], thread_id: "b", artifact_path: "results/report.md" }),
  ],
  workers: [{ agent_id: "researcher-1", role: "researcher", status: "running", thread_id: "a", turn_id: "turn1", parent_id: "coordinator-1", model_binding: { ref: "analysis", provider_id: "endpoint", model: "chosen-model", reasoning_effort: "high" } }],
}
function mockNuwaSnapshot(next: NuwaSnapshot) {
  vi.mocked(nuwaAPI.snapshot).mockResolvedValue(next)
  vi.mocked(nuwaAPI.threadSnapshot).mockImplementation(async (thread_id) => ({
    view: {
      thread_id, project_id: next.project_id, project_revision: next.revision, thread_revision: 0, data_generation: 0,
      nodes: next.nodes.filter((record) => record.scope === "project" || record.thread_id === thread_id),
      workers: next.workers.filter((worker) => worker.thread_id === thread_id),
    },
    tag: '"tag"',
  }))
}
beforeEach(() => {
  vi.clearAllMocks()
  mockNuwaSnapshot(snapshot)
  vi.mocked(subscribeNuwa).mockReturnValue(vi.fn())
  useApp.setState({ activeId: "a", loadUntilTurn: vi.fn().mockResolvedValue(true) })
  useTraceFocus.getState().clear()
})

describe("Nuwa shared work", () => {
  it("shows the project executor without inventing one for an older project", async () => {
    const { rerender } = render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    expect(await screen.findByText("Executor model is not saved yet.")).toBeInTheDocument()
    rerender(<NuwaPanel project={{ ...project, nuwa_executor_model: "executor-model" }} threadId="a" onSelectAgent={vi.fn()} />)
    expect(screen.getByText("Executor model: executor-model")).toBeInTheDocument()
  })

  it("shows recorder and model on each record and converges isolated records to the goal", async () => {
    mockNuwaSnapshot({
      ...snapshot,
      nodes: [
        node("left", { kind: "artifact", agent_id: "manager", generation: 0, depends_on: null, created_at: "2026-01-01" }),
        node("right", { kind: "artifact", agent_id: "researcher-1", generation: 1, created_at: "2026-01-02" }),
        node("prior", { kind: "artifact", agent_id: "researcher-1", generation: 1, turn_id: "other-turn", created_at: "2026-01-03" }),
        node("legacy", { kind: "artifact" }),
      ],
      workers: snapshot.workers.map((worker) => ({ ...worker, generation: 1 })),
    })
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    const path = await screen.findByRole("region", { name: "Collaboration paths" })
    const graph = within(path).getByRole("region", { name: "Shared work collaboration graph" })
    // Four isolated results share one row and one dashed arrow, not a card each.
    expect(within(graph).queryByTestId("nuwa-origin")).toBeNull()
    expect(within(graph).queryByTestId("nuwa-edge")).toBeNull()
    expect(within(graph).queryByRole("button", { name: /Node right/ })).toBeNull()
    const group = within(graph).getByTestId("nuwa-loose-group")
    expect(group).toHaveAttribute("data-kind", "artifact")
    expect(group).toHaveAttribute("data-count", "4")
    expect(group).toHaveTextContent("4 Pending")
    expect(within(graph).getByTestId("nuwa-goal")).toBeInTheDocument()
    const goalEdges = within(graph).getAllByTestId("nuwa-goal-edge")
    expect(goalEdges.map((edge) => edge.getAttribute("data-from"))).toEqual([nuwaLooseGroupID("artifact")])
    expect(goalEdges.every((edge) => edge.getAttribute("data-to") === NUWA_GOAL_ID)).toBe(true)
    fireEvent.click(group)
    const rightCard = within(path).getByRole("button", { name: /Node right/ })
    expect(rightCard).toHaveAccessibleDescription("Recorded by researcher in turn turn1")
    fireEvent.click(rightCard)
    const detail = screen.getByTestId("nuwa-node-detail")
    expect(within(detail).getByText("chosen-model · high")).toBeInTheDocument()
    expect(within(detail).getByText("Recorded by researcher in turn turn1")).toBeInTheDocument()
    fireEvent.click(within(path).getByRole("button", { name: /Node left/ }))
    expect(screen.queryByRole("list", { name: "Shared work nodes" })).toBeNull()
    expect(within(screen.getByTestId("nuwa-node-detail")).getByText("Recorded by Manager in turn turn1")).toBeInTheDocument()
  })

  it("keeps a direct turn visible when the collaboration graph is empty", async () => {
    mockNuwaSnapshot({ ...snapshot, nodes: [], workers: [] })
    const onOpenFiles = vi.fn()
    const props = { project, threadId: "a", onSelectAgent: vi.fn() }
    const activity = { id: "direct-turn", status: "running", onOpenFiles }
    const { rerender } = render(<NuwaPanel {...props} activity={activity} />)
    await screen.findByText("No work records in this conversation yet.")
    const section = screen.getByRole("region", { name: "Latest turn in this conversation" })
    expect(within(section).getByRole("status")).toHaveTextContent("Running")
    expect(screen.queryByRole("region", { name: "Shared work collaboration graph" })).toBeNull()

    rerender(<NuwaPanel {...props} activity={{ ...activity, status: "cancelled" }} />)
    expect(within(section).getByRole("status")).toHaveTextContent("Stopped")
    fireEvent.click(within(section).getByRole("button", { name: "Files" }))
    expect(onOpenFiles).toHaveBeenCalledOnce()
    fireEvent.click(within(section).getByRole("button", { name: "Open turn Trace" }))
    await waitFor(() => expect(useTraceFocus.getState().turnId).toBe(activity.id))

    rerender(<NuwaPanel {...props} threadId="b" />)
    expect(screen.queryByRole("region", { name: "Latest turn in this conversation" })).toBeNull()
  })

  it("paints facts and results on different backgrounds", async () => {
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    const graph = await screen.findByRole("region", { name: "Shared work collaboration graph" })
    const card = (name: RegExp) => within(graph).getByRole("button", { name }).className
    // A shared fill made a reported fact look like a finished result.
    expect(card(/Node finding/)).toContain("bg-nuwa-fact-bg")
    expect(card(/Node output/)).toContain("bg-nuwa-artifact-bg")
    expect(card(/Node plan/)).toContain("bg-nuwa-task-bg")
    expect(card(/Node finding/)).not.toContain("bg-nuwa-artifact-bg")
    const tally = (kind: string) =>
      screen.getByTestId("nuwa-panel").querySelector(`div[data-kind="${kind}"]`)
    expect(tally("fact")?.className).toContain("bg-nuwa-fact-bg")
    expect(tally("artifact")?.className).toContain("bg-nuwa-artifact-bg")
    expect(tally("fact")?.className).not.toEqual(tally("artifact")?.className)
    fireEvent.click(within(graph).getByRole("button", { name: /Node finding/ }))
    expect(screen.getByTestId("nuwa-node-detail").className).toContain("bg-nuwa-fact-bg")
    fireEvent.click(within(graph).getByRole("button", { name: /Node output/ }))
    expect(screen.getByTestId("nuwa-node-detail").className).toContain("bg-nuwa-artifact-bg")
  })

  it("shows reported facts separately from completed work and opens source evidence on demand", async () => {
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    const graph = await screen.findByRole("region", { name: "Shared work collaboration graph" })
    expect(within(graph).getAllByTestId("nuwa-edge")).toHaveLength(2)
    expect(screen.getByText("chosen-model · high")).toBeInTheDocument()
    fireEvent.click(within(graph).getByRole("button", { name: /Node finding/ }))
    const detail = screen.getByTestId("nuwa-node-detail")
    expect(within(detail).getByText("Reported")).toBeInTheDocument()
    expect(screen.queryByText("Full source evidence")).toBeNull()
    fireEvent.click(within(detail).getByRole("button", { name: "Details and evidence" }))
    expect(await screen.findByText("Full source evidence")).toBeInTheDocument()
    fireEvent.click(within(detail).getByRole("button", { name: /Measured result/ }))
    await waitFor(() => expect(useTraceFocus.getState().turnId).toBe("turn1"))
    expect(useApp.getState().loadUntilTurn).toHaveBeenCalledWith("turn1")
  })

  it("filters a thread without discarding the other thread's prerequisite", async () => {
    render(<NuwaPanel project={project} threadId="b" onSelectAgent={vi.fn()} />)
    await screen.findByRole("region", { name: "Shared work collaboration graph" })
    fireEvent.click(screen.getByRole("button", { name: "This conversation" }))
    const graph = screen.getByRole("region", { name: "Shared work collaboration graph" })
    expect(within(graph).getAllByRole("button")).toHaveLength(3)
    expect(screen.queryByText("chosen-model · high")).toBeNull()
    fireEvent.click(within(graph).getByRole("button", { name: /Node output/ }))
    expect(screen.getByRole("link", { name: "Open result file" })).toHaveAttribute("href", "/api/threads/b/download/results/report.md")
  })

  it("updates a selected node when a newer project revision arrives", async () => {
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    const graph = await screen.findByRole("region", { name: "Shared work collaboration graph" })
    fireEvent.click(within(graph).getByRole("button", { name: /Node plan/ }))
    mockNuwaSnapshot({ ...snapshot, revision: 2, nodes: snapshot.nodes.map((n) => n.id === "plan" ? { ...n, status: "blocked", summary: "Needs new evidence", version: 2 } : n) })
    act(() => publishWorkEvent("a", { kind: "revision", version: {
      thread_id: "a", project_id: "p", project_revision: 2, thread_revision: 0, data_generation: 0,
    } }))
    await waitFor(() => expect(within(screen.getByTestId("nuwa-node-detail")).getByText("Blocked")).toBeInTheDocument())
    expect(within(screen.getByTestId("nuwa-node-detail")).getByText("Needs new evidence")).toBeInTheDocument()
  })

  it("opens the actual worker without inferring its model from its role", async () => {
    const select = vi.fn()
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={select} />)
    const worker = await screen.findByRole("button", { name: /Open agent.*researcher/ })
    expect(worker).toHaveAccessibleName(/chosen-model/)
    expect(worker).toHaveAccessibleName(/high/)
    expect(worker).toHaveAccessibleName(/Running/)
    expect(within(worker).getByText("Parent agent: a / coordinator-1")).toBeInTheDocument()
    fireEvent.click(worker)
    expect(select).toHaveBeenCalledWith("researcher-1")
  })

  it("announces an unrecorded model and the worker status without inferring a model from its role", async () => {
    mockNuwaSnapshot({
      ...snapshot,
      workers: snapshot.workers.map((worker) => ({ ...worker, role: "unconfigured-model-role", model_binding: undefined, status: "blocked" })),
    })
    const select = vi.fn()
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={select} />)
    const worker = await screen.findByRole("button", { name: /Open agent.*unconfigured-model-role/ })
    expect(worker).toHaveAccessibleName(/Model not recorded/)
    expect(worker).toHaveAccessibleName(/Blocked/)
    expect(worker).not.toHaveAccessibleName(/chosen-model|high/)
    fireEvent.click(worker)
    expect(select).toHaveBeenCalledWith("researcher-1")
  })

  it("keeps a deleted source explicit instead of offering a broken evidence jump", async () => {
    mockNuwaSnapshot({ ...snapshot, nodes: snapshot.nodes.map((n) => n.id === "finding" ? { ...n, unavailable_evidence: n.evidence } : n) })
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    const graph = await screen.findByRole("region", { name: "Shared work collaboration graph" })
    fireEvent.click(within(graph).getByRole("button", { name: /Node finding/ }))
    fireEvent.click(within(screen.getByTestId("nuwa-node-detail")).getByRole("button", { name: "Details and evidence" }))
    expect(screen.getByRole("button", { name: /Measured result/ })).toBeDisabled()
    expect(screen.getByText("Source unavailable")).toBeInTheDocument()
  })

  it("draws a dense report as its dependency path and fades records outside the selection", () => {
    const loose = [
      ...Array.from({ length: 16 }, (_, index) => node(`artifact-${index}`, { kind: "artifact" })),
      ...Array.from({ length: 8 }, (_, index) => node(`fact-${index}`, { kind: "fact" })),
      node("loose-task"),
    ]
    const chain = [
      node("audit"),
      node("cve", { kind: "fact", depends_on: ["audit"] }),
      node("verify", { kind: "artifact", depends_on: ["cve"] }),
      node("matrix", { kind: "fact" }),
      node("poc", { kind: "artifact", depends_on: ["matrix"] }),
    ]
    render(<NuwaGraph nodes={[...loose, ...chain]} onSelect={vi.fn()} selected="poc" />)
    const graph = screen.getByRole("region", { name: "Shared work collaboration graph" })
    expect(within(graph).getAllByTestId("nuwa-loose-group")).toHaveLength(2)
    expect(within(graph).getByRole("button", { name: /loose-task/ })).toHaveAttribute("data-node-id", "loose-task")
    const artifactGroup = within(graph).getAllByTestId("nuwa-loose-group").find((group) => group.getAttribute("data-kind") === "artifact")
    const factGroup = within(graph).getAllByTestId("nuwa-loose-group").find((group) => group.getAttribute("data-kind") === "fact")
    expect(artifactGroup).toHaveAttribute("data-count", "16")
    expect(artifactGroup?.className).toContain("bg-nuwa-artifact-bg")
    expect(factGroup?.className).toContain("bg-nuwa-fact-bg")
    expect(within(graph).queryByRole("button", { name: /artifact-0/ })).toBeNull()
    expect(within(graph).getAllByTestId("nuwa-edge")).toHaveLength(3)
    expect(within(graph).getAllByTestId("nuwa-goal-edge")).toHaveLength(5)
    expect(within(graph).getByRole("button", { name: /poc/ })).toHaveAttribute("data-emphasis", "active")
    expect(within(graph).getByRole("button", { name: /matrix/ })).toHaveAttribute("data-emphasis", "active")
    expect(within(graph).getByRole("button", { name: /audit/ })).toHaveAttribute("data-emphasis", "dim")
    expect(within(graph).getByRole("button", { name: /verify/ })).toHaveAttribute("data-emphasis", "dim")
    const pocEdge = within(graph).getAllByTestId("nuwa-edge").find((edge) => edge.getAttribute("data-to") === "poc")
    expect(pocEdge).toHaveAttribute("data-emphasis", "active")
    const verifyEdge = within(graph).getAllByTestId("nuwa-goal-edge").find((edge) => edge.getAttribute("data-from") === "verify")
    expect(verifyEdge).toHaveAttribute("data-emphasis", "dim")
  })

  it("filters a long unlinked list by title or summary", () => {
    const records = Array.from({ length: 9 }, (_, index) => node(`artifact-${index}`, {
      kind: "artifact", summary: index === 3 ? "unique-summary" : "shared",
    }))
    render(<NuwaGraph nodes={records} onSelect={vi.fn()} />)
    const path = screen.getByRole("region", { name: "Collaboration paths" })
    fireEvent.click(within(path).getByTestId("nuwa-loose-group"))
    const filter = screen.getByRole("textbox", { name: "Filter Results" })
    expect(within(path).getAllByRole("button", { name: /artifact-/ })).toHaveLength(9)
    expect(within(path).getByRole("button", { name: /artifact-3/ }).className).toContain("bg-nuwa-artifact-bg")
    fireEvent.change(filter, { target: { value: "unique-summary" } })
    expect(within(path).getByRole("button", { name: /artifact-3/ })).toBeInTheDocument()
    expect(within(path).queryByRole("button", { name: /artifact-0/ })).toBeNull()
    fireEvent.change(filter, { target: { value: "missing" } })
    expect(screen.getByText("No matching records")).toBeInTheDocument()
  })

  it("leaves an unknown collaboration kind on the ordinary card", () => {
    render(<NuwaGraph nodes={[node("note", { kind: "note" })]} onSelect={vi.fn()} />)
    const card = screen.getByRole("button", { name: /Node note/ })
    expect(card.className).toContain("bg-card")
    expect(card.className).not.toMatch(/bg-nuwa-(task|fact|artifact)-bg/)
  })

  it("keeps unknown states visible and exposes every cyclic graph node", () => {
    render(<NuwaGraph nodes={[node("a", { depends_on: ["b"], status: "future-status" }), node("b", { depends_on: ["a"] })]} onSelect={vi.fn()} />)
    expect(screen.getByText("future-status")).toBeInTheDocument()
    expect(screen.getByRole("status")).toHaveTextContent("cycle")
    expect(screen.getAllByRole("button")).toHaveLength(2)
  })

  it("marks a result for review when its dependency has been revised", async () => {
    mockNuwaSnapshot({ ...snapshot, nodes: snapshot.nodes.map((n) => n.id === "output" ? { ...n, stale_dependencies: ["finding"], dependency_versions: { finding: 1 } } : n) })
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    const graph = await screen.findByRole("region", { name: "Shared work collaboration graph" })
    const result = within(graph).getByRole("button", { name: /Node output/ })
    expect(within(result).getByText("Needs review")).toBeInTheDocument()
    fireEvent.click(result)
    expect(within(screen.getByTestId("nuwa-node-detail")).getByText("Source changed; review this result.")).toBeInTheDocument()
    expect(within(screen.getByTestId("nuwa-node-detail")).getByText("Reported")).toBeInTheDocument()
  })

  it("opens an expanded graph with selectable node details", async () => {
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    fireEvent.click(await screen.findByRole("button", { name: "Expand graph" }))
    const dialog = screen.getByRole("dialog", { name: "Collaboration paths" })
    const graph = within(dialog).getByRole("region", { name: "Shared work collaboration graph" })
    fireEvent.click(within(graph).getByRole("button", { name: /Node output/ }))
    expect(within(dialog).getByTestId("nuwa-node-detail")).toContainHTML("Summary output")
    expect(within(graph).getAllByTestId("nuwa-edge")).toHaveLength(2)
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })
})

describe("Conversation work controls", () => {
  it("defaults to the visible conversation, filters shared work and merges with the displayed tag", async () => {
    const privateFact = node("private", { kind: "fact", scope: "thread", status: "reported" })
    mockNuwaSnapshot({ ...snapshot, nodes: [...snapshot.nodes, privateFact] })
    vi.mocked(nuwaAPI.promote).mockImplementation(async () => {
      mockNuwaSnapshot({ ...snapshot, revision: 2, nodes: [...snapshot.nodes, { ...privateFact, scope: "project", version: 2 }] })
      return { thread_id: "a", project_id: "p", project_revision: 2, thread_revision: 0, data_generation: 0 }
    })
    render(<NuwaPanel project={project} threadId="a" onSelectAgent={vi.fn()} />)
    const graph = await screen.findByRole("region", { name: "Shared work collaboration graph" })
    expect(screen.getByRole("button", { name: "This conversation" })).toHaveAttribute("aria-pressed", "true")
    fireEvent.click(screen.getByRole("button", { name: "Entire project" }))
    expect(within(graph).queryByRole("button", { name: /Node private/ })).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "This conversation" }))
    fireEvent.click(within(graph).getByRole("button", { name: /Node private/ }))
    const detail = screen.getByTestId("nuwa-node-detail")
    expect(detail).toHaveTextContent("Conversation record")
    fireEvent.click(within(detail).getByRole("button", { name: "Merge into project" }))
    expect(nuwaAPI.promote).toHaveBeenCalledWith("a", ["private"], '"tag"')
    await waitFor(() => expect(detail).toHaveTextContent("Project record"))
    expect(within(detail).queryByRole("button", { name: "Merge into project" })).toBeNull()
  })

  it("has no project action in a standalone conversation and restores a pruned record on a new revision", async () => {
    const fact = node("private", { kind: "fact", scope: "thread", status: "retracted" })
    mockNuwaSnapshot({ ...snapshot, project_id: "", revision: 0, nodes: [fact], workers: [] })
    render(<NuwaPanel threadId="a" onSelectAgent={vi.fn()} />)
    expect(screen.getByRole("button", { name: "Entire project" })).toBeDisabled()
    const group = await screen.findByRole("button", { name: "Invalidated: 1" })
    expect(screen.queryByRole("region", { name: "Shared work collaboration graph" })).toBeNull()
    expect(screen.getByText("Pruned 1")).toBeInTheDocument()
    fireEvent.click(group)
    const list = screen.getByRole("list", { name: "Invalidated records" })
    expect(list).toHaveTextContent("Refuted")
    fireEvent.click(within(list).getByRole("button", { name: /Node private/ }))
    expect(within(screen.getByTestId("nuwa-node-detail")).queryByRole("button", { name: "Merge into project" })).toBeNull()

    const toggle = screen.getByRole("switch", { name: "Show pruned records" })
    fireEvent.click(toggle)
    expect(screen.getByRole("region", { name: "Shared work collaboration graph" })).toHaveTextContent("Node private")
    expect(screen.queryByRole("button", { name: "Invalidated: 1" })).toBeNull()
    fireEvent.click(toggle)
    expect(screen.queryByRole("region", { name: "Shared work collaboration graph" })).toBeNull()

    const version = { thread_id: "a", project_id: "", project_revision: 0, thread_revision: 1, data_generation: 0 }
    vi.mocked(nuwaAPI.threadSnapshot).mockResolvedValue({
      view: { ...version, nodes: [{ ...fact, status: "reported", version: 2 }], workers: [] }, tag: '"new"',
    })
    act(() => publishWorkEvent("a", { kind: "revision", version }))
    const graph = await screen.findByRole("region", { name: "Shared work collaboration graph" })
    expect(graph).toHaveTextContent("Node private")
    expect(graph).toHaveTextContent("Reported")
    expect(screen.queryByRole("button", { name: "Invalidated: 1" })).toBeNull()
  })
})
