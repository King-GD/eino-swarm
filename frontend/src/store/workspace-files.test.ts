import { afterEach, expect, test, vi } from "vitest"

import { api } from "@/lib/api"
import { useApp } from "@/store/app"

afterEach(() => {
  vi.restoreAllMocks()
  useApp.setState(useApp.getInitialState())
})

test("a late project file listing cannot replace the active task listing", async () => {
  type Listed = Awaited<ReturnType<typeof api.files>>
  const response = (root: string, file: string): Listed => ({
    workspace: root,
    file_view: { root, isolated: true, has_project: true },
    files: [{ path: file, name: file, size: 0, dir: false, modified: "" }],
  })
  const task = response("task-root", "task-result.md")
  const shared = response("project-root", "project-note.md")
  let release: (result: Listed) => void = () => undefined
  const pending = new Promise<Listed>((resolve) => { release = resolve })
  vi.spyOn(api, "files").mockImplementation((_id, _dir, scope) =>
    scope?.scope === "project" ? pending : Promise.resolve(task))
  useApp.setState({ activeId: "task", fileView: task.file_view })
  useApp.getState().setFileScope("project")
  useApp.getState().setFileScope("task")
  await useApp.getState().refreshFiles()
  release(shared)
  await pending
  await Promise.resolve()
  expect(useApp.getState().workspace).toBe(task.workspace)
  expect(useApp.getState().files.map(({ path }) => path)).toEqual(["task-result.md"])
})
