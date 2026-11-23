import { describe, expect, it } from "vitest"
import { builtinInspectorScopes, groupInspectorTabs, projectScopeLabel } from "./inspector-scopes"

describe("inspector scope", () => {
  it("groups registered and built-in tabs without changing their identities", () => {
    const builtins = Object.entries(builtinInspectorScopes).map(([id, scope]) => ({ id, scope }))
    const taskPanel = { id: "task-panels", scope: "thread" } as const
    const collaboration = { id: "nuwa", scope: "project" } as const
    const groups = groupInspectorTabs([taskPanel, collaboration, ...builtins])
    expect(groups.thread.map(({ id }) => id)).toEqual(["task-panels", "agents", "files", "trace"])
    expect(groups.project.map(({ id }) => id)).toEqual(["nuwa", "memory", "knowledge"])
    expect(groups.thread[0]).toBe(taskPanel)
  })

  it("labels the project group as a shared library for a conversation outside projects", () => {
    expect(projectScopeLabel(undefined, "Shared library")).toBe("Shared library")
    expect(projectScopeLabel("A long project name", "Shared library")).toBe("A long project name")
  })
})
