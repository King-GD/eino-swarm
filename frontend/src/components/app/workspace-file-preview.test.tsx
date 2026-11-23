import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { FilesTab } from "./files-tab"
import { MemoMarkdown } from "./markdown"
import { WorkspaceFilePreview } from "./workspace-file-preview"
import { api } from "@/lib/api"
import { readFilePreview } from "@/lib/file-preview"
import type { FileEntry } from "@/lib/types"
import { useApp } from "@/store/app"

vi.mock("@/lib/file-preview", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/file-preview")>()
  return { ...actual, readFilePreview: vi.fn() }
})

function file(path: string, dir = false): FileEntry {
  const slash = path.lastIndexOf("/")
  return {
    path,
    name: slash < 0 ? path : path.slice(slash + 1),
    size: dir ? 0 : 12,
    dir,
    modified: "2026-01-01T00:00:00Z",
  }
}

beforeEach(() => {
  useApp.setState({
    activeId: "th_1",
    locale: "en",
    fileScope: "task",
    fileView: undefined,
    files: [],
    meta: undefined,
  })
  vi.mocked(readFilePreview).mockReset().mockResolvedValue({ kind: "text", text: "# Notes\n" })
  vi.spyOn(api, "searchFiles").mockResolvedValue({
    workspace: "/tmp/task",
    files: [file("work/notes/a.md")],
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  useApp.setState({ activeId: undefined, files: [], fileView: undefined, fileScope: "task", meta: undefined })
})

describe("answer file links open the workspace preview", () => {
  it("opens the task file when the answer used a work-relative path", async () => {
    render(
      <WorkspaceFilePreview>
        <MemoMarkdown text="Saved notes/a.md." />
      </WorkspaceFilePreview>,
    )
    fireEvent.click(screen.getByRole("button", { name: "Preview notes/a.md" }))
    expect(await screen.findByRole("heading", { name: "Notes" })).toBeInTheDocument()
    expect(screen.getByText("work/notes/a.md")).toBeInTheDocument()
    expect(api.searchFiles).toHaveBeenCalledWith("th_1", "notes/a.md", undefined)
    expect(readFilePreview).toHaveBeenCalledWith("th_1", "work/notes/a.md", expect.any(AbortSignal))
  })

  it("keeps a project-folder root off a task path lookup", async () => {
    useApp.setState({
      fileScope: "project",
      fileView: { root: "project-root", isolated: false, has_project: true },
    })
    render(
      <WorkspaceFilePreview>
        <MemoMarkdown text="Saved notes/a.md." />
      </WorkspaceFilePreview>,
    )
    fireEvent.click(screen.getByRole("button", { name: "Preview notes/a.md" }))
    await screen.findByRole("heading", { name: "Notes" })
    expect(api.searchFiles).toHaveBeenCalledWith("th_1", "notes/a.md", undefined)
  })

  it("opens a file-tree row without searching for another path", async () => {
    useApp.setState({
      fileScope: "task",
      fileView: { root: "task-root", isolated: true, has_project: false },
    })
    render(
      <WorkspaceFilePreview>
        <FilesTab
          files={[file("readme.md")]}
          workspace="/tmp/task"
          threadId="th_1"
          fileScope="task"
          fileView={{ root: "task-root", isolated: true, has_project: false }}
          canReveal={false}
          onUpload={vi.fn()}
          onDelete={vi.fn()}
          onRefresh={vi.fn()}
          onReveal={vi.fn()}
        />
      </WorkspaceFilePreview>,
    )
    fireEvent.click(screen.getByRole("treeitem", { name: "readme.md" }))
    expect(await screen.findByRole("heading", { name: "Notes" })).toBeInTheDocument()
    expect(api.searchFiles).not.toHaveBeenCalled()
    expect(readFilePreview).toHaveBeenCalledWith(
      "th_1",
      "readme.md",
      expect.any(AbortSignal),
      { scope: "task", root: "task-root" },
    )
  })

  it("lists each matching file when the answer names one that exists in more than one place", async () => {
    vi.mocked(api.searchFiles).mockResolvedValue({
      workspace: "/tmp/task",
      files: [file("report/list/a.md"), file("recon/notes/a.md"), file("recon/notes/a.md")],
    })
    render(
      <WorkspaceFilePreview>
        <MemoMarkdown text="See a.md." />
      </WorkspaceFilePreview>,
    )
    fireEvent.click(screen.getByRole("button", { name: "Preview a.md" }))
    expect(await screen.findByRole("heading", { name: "a.md" })).toBeInTheDocument()
    expect(screen.getByText("Choose which file to preview")).toBeInTheDocument()
    expect(readFilePreview).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "Preview report/list/a.md" }))
    expect(await screen.findByRole("heading", { name: "Notes" })).toBeInTheDocument()
    expect(screen.getByText("report/list/a.md")).toBeInTheDocument()
    expect(readFilePreview).toHaveBeenCalledTimes(1)
    expect(readFilePreview).toHaveBeenCalledWith("th_1", "report/list/a.md", expect.any(AbortSignal))
  })

  it("closes the preview when the conversation changes", async () => {
    render(
      <WorkspaceFilePreview>
        <MemoMarkdown text="Saved notes/a.md." />
      </WorkspaceFilePreview>,
    )
    fireEvent.click(screen.getByRole("button", { name: "Preview notes/a.md" }))
    await screen.findByRole("heading", { name: "Notes" })
    useApp.setState({ activeId: "th_2" })
    await waitFor(() => {
      expect(screen.queryByTestId("file-preview")).not.toBeInTheDocument()
    })
  })
})
