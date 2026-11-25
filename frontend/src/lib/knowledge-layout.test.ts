import { describe, expect, it } from "vitest"
import type { KnowledgeGraph, KnowledgeGraphNode } from "./knowledge"
import {
  KNOWLEDGE_PROJECT_ROOT, KNOWLEDGE_UNLINKED_GROUP, rootKnowledgeGraph, visibleKnowledgeGraph,
  type KnowledgeLayoutGraph,
} from "./knowledge-layout"

const entry = (id: string): KnowledgeGraphNode => ({ id, title: id, type: "entry" })
const groupTitle = (count: number) => `Unlinked pages (${count})`

function reachable(graph: KnowledgeLayoutGraph) {
  const neighbors = new Map(graph.nodes.map(({ id }): [string, string[]] => [id, []]))
  for (const { source, target } of graph.edges) {
    neighbors.get(source)!.push(target)
    neighbors.get(target)!.push(source)
  }
  const seen = new Set([KNOWLEDGE_PROJECT_ROOT])
  for (const id of seen) for (const next of neighbors.get(id)!) seen.add(next)
  return seen
}

describe("project knowledge graph layout", () => {
  it("joins separate components, single pages and self-links to one project root", () => {
    const graph: KnowledgeGraph = {
      nodes: ["c", "b", "a", "y", "x", "root", "loop"].map(entry),
      edges: [
        { source: "a", target: "b", relations: ["related"] },
        { source: "a", target: "c", wikilink: true },
        { source: "b", target: "c", wikilink: true },
        { source: "x", target: "y", relations: ["related"], wikilink: true },
        { source: "loop", target: "loop", wikilink: true },
      ],
    }
    const rooted = rootKnowledgeGraph(graph, "Working project", groupTitle)
    expect(rooted.nodes.filter(({ kind }) => kind === "root")).toEqual([
      { id: KNOWLEDGE_PROJECT_ROOT, title: "Working project", kind: "root" },
    ])
    expect(rooted.edges.filter(({ membership }) => !membership)).toEqual(graph.edges)
    expect(rooted.edges.filter(({ membership }) => membership).map(({ target }) => target).sort())
      .toEqual(["a", "loop", "root", "x"])
    expect(reachable(rooted).size).toBe(graph.nodes.length + 1)

    const reordered = rootKnowledgeGraph({
      nodes: [...graph.nodes].reverse(), edges: [...graph.edges].reverse(),
    }, "Working project", groupTitle)
    expect(reordered.edges.filter(({ membership }) => membership).map(({ target }) => target).sort())
      .toEqual(["a", "loop", "root", "x"])
    expect(reachable(visibleKnowledgeGraph(reordered)).size).toBe(graph.nodes.length + 1)
  })

  it("keeps every page reachable and expands a selected leaf in a large sparse graph", () => {
    const graph: KnowledgeGraph = {
      nodes: Array.from({ length: 6_270 }, (_, index) => entry(`note-${index}`)),
      edges: Array.from({ length: 316 }, (_, index) => ({
        source: `note-${2 * index}`, target: `note-${2 * index + 1}`, wikilink: true,
      })),
    }
    const rooted = rootKnowledgeGraph(graph, "Large project", groupTitle)
    expect(rooted.groupedPages).toHaveLength(5_638)
    expect(reachable(rooted).size).toBe(6_272)

    const overview = visibleKnowledgeGraph(rooted)
    expect(overview.nodes).toHaveLength(634)
    expect(reachable(overview).size).toBe(overview.nodes.length)
    expect(overview.nodes.find(({ id }) => id === KNOWLEDGE_UNLINKED_GROUP)?.title).toBe("Unlinked pages (5638)")

    const selection = visibleKnowledgeGraph(rooted, "note-6269")
    expect(selection.nodes).toHaveLength(635)
    expect(selection.edges).toContainEqual({
      source: KNOWLEDGE_UNLINKED_GROUP, target: "note-6269", membership: true,
    })
    expect(reachable(selection).size).toBe(selection.nodes.length)
    expect(selection.edges.filter(({ membership }) => !membership)).toEqual(graph.edges)
  })
})
