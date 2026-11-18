import { act, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { RightPanel } from "./panel"
import { emptyTranscript } from "@/lib/transcript"
import type { Project } from "@/lib/types"
import { useTaskPanels } from "@/store/task-panels"
import type { TaskPanel } from "@/task-panels/types"

vi.mock("./nuwa-panel", () => ({
  NuwaPanel: ({ project }: { project: Project }) => <p>Recorded project work: {project.id}</p>,
}))
vi.mock("./task-panel-frame", () => ({
  TaskPanelFrame: ({ panel, visible }: { panel: TaskPanel; visible: boolean }) => (
    <output data-testid="registered-frame" data-visible={visible}>{panel.id}</output>
  ),
}))
const project: Project = {
  id: "p", name: "P", system_prompt: "", workdir: "", resolved_workdir: "",
  memory_enabled: false, memory_dir: "", created_at: "", updated_at: "",
}
const definition = {
  id: "progress", version: "1", name: "Progress", description: "", entry: "", schema: {},
}
const progress: TaskPanel = {
  id: "progress-first", thread_id: "first", plugin_id: definition.id,
  plugin_version: definition.version, revision: 1, state: {}, definition,
}
const initialTaskPanels = useTaskPanels.getInitialState()
afterEach(() => act(() => useTaskPanels.setState(initialTaskPanels, true)))
const props = {
  tab: "nuwa" as const, transcript: emptyTranscript(), files: [], workspace: "", turns: [],
  onTabChange: vi.fn(), onSelectAgent: vi.fn(), onUpload: async () => {},
  onDeleteFile: vi.fn(), onRefreshFiles: vi.fn(), onReveal: vi.fn(),
}

describe("Project collaboration mode", () => {
  it("keeps ordinary projects on the existing Agents view", () => {
    render(<RightPanel {...props} nuwaProject={project} />)
    expect(screen.queryByRole("tab", { name: "Collaboration" })).toBeNull()
    expect(screen.getByRole("tab", { name: "Agents" })).toHaveAttribute("data-state", "active")
  })

  it("keeps recorded collaboration reachable after the mode is turned off", () => {
    render(<RightPanel {...props} nuwaProject={{ ...project, nuwa_enabled: false, nuwa_revision: 2 }} />)
    expect(screen.getByRole("tab", { name: "Collaboration" })).toHaveAttribute("data-state", "active")
    expect(screen.getByText("Recorded project work: p")).toBeInTheDocument()
  })

  it("keeps the current task frame mounted beside collaboration and drops it when changing conversation", () => {
    const foreign = { ...progress, id: "foreign-progress", thread_id: "other" }
    act(() => useTaskPanels.setState({ threadId: "first", panels: [progress, foreign], connected: true }))
    const onTabChange = vi.fn()
    const enabled = { ...project, nuwa_enabled: true }
    const view = render(<RightPanel {...props} onTabChange={onTabChange} tab="task-panels" threadId="first" nuwaProject={enabled} />)
    expect(screen.getAllByRole("tab").slice(0, 2).map((item) => item.textContent)).toEqual(["Task panels", "Collaboration"])
    const frame = screen.getByTestId("registered-frame")
    expect(frame).toHaveTextContent(progress.id)
    expect(screen.queryByText(foreign.id)).toBeNull()
    expect(frame).toHaveAttribute("data-visible", "true")
    expect(screen.queryByText(/Recorded project work/)).toBeNull()

    view.rerender(<RightPanel {...props} onTabChange={onTabChange} tab="nuwa" threadId="first" nuwaProject={enabled} />)
    expect(screen.getByTestId("registered-frame")).toBe(frame)
    expect(frame).toHaveAttribute("data-visible", "false")
    expect(screen.getByText("Recorded project work: p")).toBeVisible()

    view.rerender(<RightPanel {...props} onTabChange={onTabChange} tab="task-panels" threadId="second" nuwaProject={{ ...enabled, id: "other" }} />)
    expect(screen.queryByRole("tab", { name: "Task panels" })).toBeNull()
    expect(screen.queryByTestId("registered-frame")).toBeNull()
    expect(screen.getByRole("tab", { name: "Agents" })).toHaveAttribute("aria-selected", "true")
    expect(onTabChange).not.toHaveBeenCalled()

    act(() => useTaskPanels.setState({ threadId: "second", panels: [{ ...progress, id: "progress-second", thread_id: "second" }] }))
    expect(screen.getByRole("tab", { name: "Task panels" })).toHaveAttribute("aria-selected", "true")
    expect(screen.getByTestId("registered-frame")).toHaveTextContent("progress-second")
  })

  it("keeps project collaboration on the manually selected tab after enable and renders only the current project", () => {
    const onTabChange = vi.fn()
    const view = render(<RightPanel {...props} onTabChange={onTabChange} tab="agents" nuwaProject={project} />)
    view.rerender(<RightPanel {...props} onTabChange={onTabChange} tab="agents" nuwaProject={{ ...project, nuwa_enabled: true }} />)
    expect(screen.getByRole("tab", { name: "Collaboration" })).toBeVisible()
    expect(screen.getByRole("tab", { name: "Agents" })).toHaveAttribute("aria-selected", "true")
    expect(screen.queryByText(/Recorded project work/)).toBeNull()
    expect(onTabChange).not.toHaveBeenCalled()

    view.rerender(<RightPanel {...props} onTabChange={onTabChange} nuwaProject={{ ...project, id: "other", nuwa_revision: 2 }} />)
    expect(screen.getByText("Recorded project work: other")).toBeVisible()
    view.rerender(<RightPanel {...props} onTabChange={onTabChange} threadId="standalone" />)
    expect(screen.queryByRole("tab", { name: "Collaboration" })).toBeNull()
    expect(screen.queryByText(/Recorded project work/)).toBeNull()
  })
})
