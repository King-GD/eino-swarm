import { afterEach, describe, expect, it } from "vitest"

import { knowledgeStack, useKnowledge } from "./knowledge"

afterEach(() => useKnowledge.setState({ projectId: undefined, stack: [], refreshByProject: {} }))

describe("Project-owned knowledge panel state", () => {
  it("keeps page selections from other projects out of the return stack", () => {
    const { openPage, back } = useKnowledge.getState()
    openPage("project-one", "first")
    openPage("project-one", "second")
    back("project-two")
    expect(knowledgeStack(useKnowledge.getState(), "project-one")).toEqual(["first", "second"])
    expect(knowledgeStack(useKnowledge.getState(), "project-two")).toEqual([])
    openPage("project-two", "third")
    back("project-two")
    expect(knowledgeStack(useKnowledge.getState(), "project-two")).toEqual([])
  })

  it("refreshes only the invalidated project", () => {
    const { invalidate } = useKnowledge.getState()
    invalidate("project-one")
    invalidate("project-two")
    invalidate("project-one")
    expect(useKnowledge.getState().refreshByProject).toEqual({ "project-one": 2, "project-two": 1 })
  })
})
