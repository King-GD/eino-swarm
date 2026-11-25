import type { KnowledgeGraph, KnowledgeGraphEdge, KnowledgeGraphNode } from "./knowledge"

// Saved page IDs are slugs. The synthetic nodes use a separate namespace.
export const KNOWLEDGE_PROJECT_ROOT = "project:root"
export const KNOWLEDGE_UNLINKED_GROUP = "project:unlinked"
const UNLINKED_GROUP_THRESHOLD = 100

export interface KnowledgeLayoutNode {
  id: string
  title: string
  kind: "page" | "root" | "group"
}

export interface KnowledgeLayoutEdge extends KnowledgeGraphEdge {
  membership?: true
}

export interface KnowledgeLayoutGraph {
  nodes: KnowledgeLayoutNode[]
  edges: KnowledgeLayoutEdge[]
}

interface RootedKnowledgeGraph extends KnowledgeLayoutGraph {
  groupedPages: KnowledgeGraphNode[]
  groupedIDs: ReadonlySet<string>
}

/** Give each component a path to the project without inventing page relations. */
export function rootKnowledgeGraph(
  graph: KnowledgeGraph, projectTitle: string, unlinkedTitle: (count: number) => string,
): RootedKnowledgeGraph {
  const nodes: KnowledgeLayoutNode[] = [
    { id: KNOWLEDGE_PROJECT_ROOT, title: projectTitle, kind: "root" },
    ...graph.nodes.map(({ id, title }) => ({ id, title, kind: "page" as const })),
  ]
  const neighbors = new Map(graph.nodes.map(({ id }): [string, string[]] => [id, []]))
  const edges: KnowledgeLayoutEdge[] = []
  for (const edge of graph.edges) {
    const from = neighbors.get(edge.source)
    const to = neighbors.get(edge.target)
    if (!from || !to) continue
    edges.push(edge)
    if (edge.source !== edge.target) {
      from.push(edge.target)
      to.push(edge.source)
    }
  }

  const visited = new Set<string>()
  const unlinked: KnowledgeGraphNode[] = []
  for (const page of graph.nodes) {
    if (visited.has(page.id)) continue
    if (!neighbors.get(page.id)!.length) {
      unlinked.push(page)
      continue
    }
    // One entry point per component. Ties use the stable page ID, so a
    // refresh or title edit cannot move a branch to a different page.
    let anchor = page.id
    const queue = [page.id]
    visited.add(page.id)
    for (let i = 0; i < queue.length; i++) {
      const id = queue[i]
      const adjacent = neighbors.get(id)!
      const anchorDegree = neighbors.get(anchor)!.length
      if (adjacent.length > anchorDegree || (adjacent.length === anchorDegree && id < anchor)) anchor = id
      for (const neighbor of adjacent) {
        if (visited.has(neighbor)) continue
        visited.add(neighbor)
        queue.push(neighbor)
      }
    }
    edges.push({ source: KNOWLEDGE_PROJECT_ROOT, target: anchor, membership: true })
  }

  const groupedPages = unlinked.length > UNLINKED_GROUP_THRESHOLD ? unlinked : []
  let parent = KNOWLEDGE_PROJECT_ROOT
  if (groupedPages.length) {
    parent = KNOWLEDGE_UNLINKED_GROUP
    nodes.push({ id: parent, title: unlinkedTitle(groupedPages.length), kind: "group" })
    edges.push({ source: KNOWLEDGE_PROJECT_ROOT, target: parent, membership: true })
  }
  for (const { id } of unlinked) edges.push({ source: parent, target: id, membership: true })
  return { nodes, edges, groupedPages, groupedIDs: new Set(groupedPages.map(({ id }) => id)) }
}

/** A large unlinked branch is browsable without running a simulation on every leaf. */
export function visibleKnowledgeGraph(graph: RootedKnowledgeGraph, selected?: string): KnowledgeLayoutGraph {
  if (!graph.groupedPages.length) return graph
  const visible = (id: string) => id === selected || !graph.groupedIDs.has(id)
  return {
    nodes: graph.nodes.filter(({ id }) => visible(id)),
    edges: graph.edges.filter(({ source, target }) => visible(source) && visible(target)),
  }
}
