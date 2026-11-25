import { describe, expect, it } from "vitest"

import {
  backKnowledgePage,
  filterKnowledgeIssues,
  filterKnowledgeTree,
  groupKnowledgeConnections,
  knowledgeSourceURL,
  matchKnowledgeGraphNodes,
  pushKnowledgePage,
  type KnowledgeNode,
} from "./knowledge"
import { isKnowledgePageID, knowledgePageFromHref, splitKnowledgeLinks } from "./knowledge-link-ref"

const shared: KnowledgeNode = { id: "shared", label: "Shared page", kind: "page", aliases: ["Alternate title"], cycle: true }

describe("Knowledge navigation", () => {
  it("finds graph nodes by their titles, aliases and stable IDs", () => {
    const main = { id: "stable-slug", title: "Renamed page", type: "entry", aliases: ["Old name"] }
    const isolated = { id: "another", title: "Unlinked page", type: "entry" }
    const nodes = [main, isolated]
    expect(matchKnowledgeGraphNodes(nodes, "  renamed ")).toEqual([main])
    expect(matchKnowledgeGraphNodes(nodes, "OLD NAME")).toEqual([main])
    expect(matchKnowledgeGraphNodes(nodes, "stable-slug")).toEqual([main])
    expect(matchKnowledgeGraphNodes(nodes, "unlinked")).toEqual([isolated])
    expect(matchKnowledgeGraphNodes(nodes, "absent")).toEqual([])
  })

  it("filters without dropping ancestors or coalescing copies under different parents", () => {
    const first: KnowledgeNode = { id: "one", label: "One", kind: "page", children: [shared] }
    const second: KnowledgeNode = { id: "two", label: "Two", kind: "page", children: [shared] }
    const unrelated: KnowledgeNode = { id: "other", label: "Other", kind: "page" }
    const all = [first, second, unrelated]
    expect(filterKnowledgeTree(all, "  ALTERNATE ")).toEqual([first, second])
    expect(filterKnowledgeTree(all, "one")).toEqual([first])
    expect(filterKnowledgeTree(all, "absent")).toEqual([])
    expect(filterKnowledgeTree(all, "")).toBe(all)
  })

  it("retains the traversal order even when a link leads back to an earlier page", () => {
    let stack: readonly string[] = []
    expect(backKnowledgePage(stack)).toBe(stack)
    stack = pushKnowledgePage(stack, "one")
    expect(pushKnowledgePage(stack, "one")).toBe(stack)
    stack = pushKnowledgePage(pushKnowledgePage(stack, "two"), "one")
    expect(stack).toEqual(["one", "two", "one"])
    expect(backKnowledgePage(stack)).toEqual(["one", "two"])
  })

  it("filters the pending queue by the details readers can see", () => {
    const issues = [
      { kind: "broken_link", target: "another" },
      { kind: "invalid_page", path: "pages/example.md" },
    ] as const
    expect(filterKnowledgeIssues([...issues], "EXAMPLE")).toEqual([issues[1]])
    expect(filterKnowledgeIssues([...issues], "unknown")).toEqual([])
  })

  it("groups relation targets by stable relation even when two display labels match", () => {
    const first = { relation: "one", label: "Reference", target: "second", resolved: true }
    const second = { relation: "two", label: "Reference", target: "third", resolved: false }
    const third = { relation: "one", label: "Reference", target: "fourth", resolved: true }
    expect([...groupKnowledgeConnections([first, second, third])]).toEqual([
      ["one", { label: "Reference", links: [first, third] }],
      ["two", { label: "Reference", links: [second] }],
    ])
  })
})

describe("Wiki references and source links", () => {
  it("uses the stable page ID and keeps labels as plain display text", () => {
    expect(splitKnowledgeLinks("See [[first|report.md]], then [[second]].")).toEqual([
      { type: "text", value: "See " },
      { type: "page", id: "first", value: "report.md" },
      { type: "text", value: ", then " },
      { type: "page", id: "second", value: "second" },
      { type: "text", value: "." },
    ])
    expect(knowledgePageFromHref("#zwai-knowledge:first")).toBe("first")
    expect(knowledgePageFromHref("https://example.invalid/#zwai-knowledge:first")).toBeUndefined()
  })

  it("leaves traversal, reserved basenames and invalid references as text", () => {
    for (const id of ["..", "a/b", "a:b", "a--b", "con", "lpt1", "a\n", "a".repeat(101)]) {
      expect(isKnowledgePageID(id)).toBe(false)
      expect(knowledgePageFromHref(`#zwai-knowledge:${id}`)).toBeUndefined()
      expect(splitKnowledgeLinks(`[[${id}]]`)).toEqual([{ type: "text", value: `[[${id}]]` }])
    }
  })

  it("opens HTTP sources and displays other stored source URIs as text", () => {
    expect(knowledgeSourceURL("https://example.invalid/source")).toBe("https://example.invalid/source")
    expect(knowledgeSourceURL("urn:fixture:source")).toBeUndefined()
    expect(knowledgeSourceURL("javascript:void(0)")).toBeUndefined()
    expect(knowledgeSourceURL("//example.invalid")).toBeUndefined()
  })
})
