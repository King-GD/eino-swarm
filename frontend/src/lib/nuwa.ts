/** Shared work is a projection of durable project records, never inferred from
 * prose or worker completion. Reported facts remain reported until revised. */
export interface NuwaEvidence {
  thread_id: string
  turn_id?: string
  seq: number
  label?: string
}

export interface NuwaModelBinding {
  ref?: string
  provider_id: string
  model: string
  reasoning_effort?: string
}

export interface NuwaNode {
  id: string
  project_id?: string
  scope: "thread" | "project"
  kind: "task" | "fact" | "artifact" | string
  title: string
  summary: string
  detail?: string
  status: string
  version: number
  depends_on: string[] | null
  thread_id: string
  turn_id: string
  agent_id?: string
  generation?: number
  provider_id?: string
  model?: string
  reasoning_effort?: string
  model_binding?: NuwaModelBinding
  artifact_path?: string
  evidence?: NuwaEvidence[]
  missing_dependencies?: string[]
  unavailable_evidence?: NuwaEvidence[]
  stale_dependencies?: string[]
  incomplete_basis?: boolean
  dependency_versions?: Record<string, number>
  updated_by?: string
  updated_thread_id?: string
  updated_turn_id?: string
  updated_generation?: number
  created_at: string
  updated_at: string
}

export interface NuwaWorker {
  agent_id: string
  role: string
  status: string
  thread_id: string
  turn_id: string
  generation?: number
  parent_id?: string
  model_binding?: NuwaModelBinding
}

export interface NuwaSnapshot {
  project_id: string
  revision: number
  nodes: NuwaNode[]
  workers: NuwaWorker[]
}

export interface WorkVersion {
  thread_id: string
  project_id: string
  thread_revision: number
  project_revision: number
  data_generation: number
}

export interface ThreadWorkSnapshot extends WorkVersion {
  has_own_work?: boolean
  nodes: NuwaNode[]
  workers: NuwaWorker[]
}

export function parseWorkVersion(raw: string, threadId: string): WorkVersion | undefined {
  try {
    const value = JSON.parse(raw) as WorkVersion
    if (value.thread_id === threadId && typeof value.project_id === "string" &&
        [value.thread_revision, value.project_revision, value.data_generation].every(
          (revision) => Number.isSafeInteger(revision) && revision >= 0,
        )) return value
  } catch { /* A malformed notification cannot advance the view. */ }
}

/** Project counters are comparable only while membership and data generation match. */
export function workAtLeast(next: WorkVersion, before: WorkVersion): boolean {
  if (next.data_generation !== before.data_generation) return next.data_generation > before.data_generation
  if (next.project_id !== before.project_id) return next.thread_revision > before.thread_revision
  return next.thread_revision >= before.thread_revision && next.project_revision >= before.project_revision
}

export function isInactiveNuwaNode({ kind, status }: Pick<NuwaNode, "kind" | "status">) {
  if (kind === "task") return status === "cancelled"
  return (kind === "fact" || kind === "artifact") && (status === "superseded" || status === "retracted")
}

/** Keep the prerequisites of visible active work, including a chain of inactive prerequisites. */
export function pruneNuwaNodes(nodes: NuwaNode[]) {
  const active = nodes.filter((node) => !isInactiveNuwaNode(node))
  const index = new Map(nodes.map((node) => [node.id, node]))
  const keep = new Set<string>()
  const pending = [...active]
  while (pending.length) {
    const node = pending.pop()!
    if (keep.has(node.id)) continue
    keep.add(node.id)
    for (const id of node.depends_on ?? []) {
      const dependency = index.get(id)
      if (dependency && !keep.has(id)) pending.push(dependency)
    }
  }
  return {
    active,
    shown: nodes.filter((node) => keep.has(node.id)),
    collapsed: nodes.filter((node) => !keep.has(node.id)),
  }
}

/** Add only the shared prerequisites that the conversation graph refers to. */
export function includeNuwaProjectPrerequisites(nodes: NuwaNode[], available: NuwaNode[]) {
  const shared = new Map(available.filter((node) => (node.scope ?? "project") === "project").map((node) => [node.id, node]))
  const graph = [...nodes]
  const seen = new Set(graph.map((node) => node.id))
  for (const node of graph) {
    for (const id of node.depends_on ?? []) {
      const prerequisite = shared.get(id)
      if (prerequisite && !seen.has(id)) {
        graph.push(prerequisite)
        seen.add(id)
      }
    }
  }
  return graph
}

type NuwaPathNode = Pick<NuwaNode, "id" | "created_at"> & { depends_on: string[] | null }

function compareNuwaCreation(a: NuwaPathNode, b: NuwaPathNode) {
  return a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id)
}

interface NuwaPosition<T> {
  node: T
  column: number
  row: number
}

/** Kahn layers keep real dependencies visible without trusting the stored
 * graph to be acyclic. Cyclic records are still inspectable, never dropped. */
export function layoutNuwa<T extends NuwaPathNode>(input: T[]): {
  positions: NuwaPosition<T>[]
  edges: { from: string; to: string }[]
  missing: string[]
  cyclic: boolean
} {
  const ordered = [...input].sort(compareNuwaCreation)
  const known = new Set(ordered.map((node) => node.id))
  const remaining = new Map(ordered.map((node) => [node.id, node]))
  const columns = new Map<string, number>()
  const missing = new Set<string>()
  const edges: { from: string; to: string }[] = []
  for (const node of ordered)
    for (const id of new Set(node.depends_on ?? [])) {
      if (known.has(id)) edges.push({ from: id, to: node.id })
      else missing.add(id)
    }
  while (remaining.size) {
    const ready = [...remaining.values()].filter((node) =>
      (node.depends_on ?? []).every((id) => !known.has(id) || columns.has(id))
    )
    if (!ready.length) break
    for (const node of ready) {
      const parents = (node.depends_on ?? []).filter((id) => columns.has(id))
      columns.set(
        node.id,
        parents.length ? Math.max(...parents.map((id) => columns.get(id)!)) + 1 : 0
      )
      remaining.delete(node.id)
    }
  }
  const cyclic = remaining.size > 0
  const last = Math.max(0, ...columns.values())
  for (const node of remaining.values()) columns.set(node.id, last + 1)
  const rows = new Map<number, number>()
  const positions = ordered
    .sort((a, b) => columns.get(a.id)! - columns.get(b.id)!)
    .map((node) => {
      const column = columns.get(node.id)!
      const row = rows.get(column) ?? 0
      rows.set(column, row + 1)
      return { node, column, row }
    })
  return { positions, edges, missing: [...missing].sort(), cyclic }
}

export type CollaborationNode = NuwaPathNode & { depends_on: string[] } & (
  | { type: "work"; record: NuwaNode }
  | { type: "loose"; kind: string; records: NuwaNode[] }
)

const looseKindOrder = ["task", "fact", "artifact"]

/** One row per kind for records that neither depend on nor support another record. */
export function nuwaLooseGroupID(kind: string) {
  return `__nuwa_loose_${kind}__`
}

/** Status counts for a collapsed row, most common first. */
export function nuwaStatusTally(records: Pick<NuwaNode, "status">[]) {
  const counts = new Map<string, number>()
  for (const record of records) counts.set(record.status, (counts.get(record.status) ?? 0) + 1)
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([status, count]) => ({ status, count }))
}

/** Newest recorded work first, so a long result list opens on the latest entry. */
export function compareNuwaRecent(a: Pick<NuwaNode, "id" | "title" | "updated_at">, b: Pick<NuwaNode, "id" | "title" | "updated_at">) {
  return b.updated_at.localeCompare(a.updated_at) || a.title.localeCompare(b.title) || a.id.localeCompare(b.id)
}

function looseKindRank(kind: string) {
  const index = looseKindOrder.indexOf(kind)
  return index === -1 ? `1-${kind}` : `0-${String(index).padStart(2, "0")}`
}

/** Records that share a saved dependency stay on the path. Everything else is
 * grouped by kind so an unconnected result does not become its own card. */
export function partitionNuwaCollaboration(nodes: NuwaNode[]) {
  const known = new Set(nodes.map((node) => node.id))
  const linked = new Set<string>()
  const missing = new Set<string>()
  for (const node of nodes) {
    for (const id of new Set(node.depends_on ?? [])) {
      if (known.has(id)) {
        linked.add(node.id)
        linked.add(id)
      } else {
        missing.add(id)
      }
    }
  }
  const loose = new Map<string, NuwaNode[]>()
  const path = nodes.filter((node) => linked.has(node.id))
  for (const node of nodes) {
    if (linked.has(node.id)) continue
    loose.set(node.kind, [...(loose.get(node.kind) ?? []), node])
  }
  const groups: { kind: string; id: string; records: NuwaNode[] }[] = []
  for (const kind of [...loose.keys()].sort((a, b) => looseKindRank(a).localeCompare(looseKindRank(b)) || a.localeCompare(b))) {
    const records = loose.get(kind)!.sort(compareNuwaCreation)
    // One unconnected record is still the thing to read. A crowd of the same
    // kind collapses so the dependency path stays visible.
    if (records.length === 1) path.push(records[0])
    else groups.push({ kind, id: nuwaLooseGroupID(kind), records })
  }
  return { path, groups, missing: [...missing].sort() }
}

/** The upstream and downstream records of a selection. Null means nothing is
 * selected, so the whole path stays visible. An unconnected record focuses its
 * kind group instead of a dependency chain. */
export function nuwaPathFocus(nodes: NuwaNode[], selected?: string) {
  if (!selected) return null
  const { path, groups } = partitionNuwaCollaboration(nodes)
  const group = groups.find((entry) => entry.id === selected || entry.records.some((node) => node.id === selected))
  const byID = new Map(path.map((node) => [node.id, node]))
  if (!byID.has(selected)) return group ? new Set([group.id, selected]) : null
  const upstream = new Set<string>()
  const visitUpstream = (id: string) => {
    if (upstream.has(id) || !byID.has(id)) return
    upstream.add(id)
    for (const parent of byID.get(id)!.depends_on ?? []) visitUpstream(parent)
  }
  visitUpstream(selected)
  const children = new Map<string, string[]>()
  for (const node of path) {
    for (const parent of node.depends_on ?? []) {
      if (!byID.has(parent)) continue
      children.set(parent, [...(children.get(parent) ?? []), node.id])
    }
  }
  const downstream = new Set<string>()
  const visitDownstream = (id: string) => {
    if (downstream.has(id)) return
    downstream.add(id)
    for (const child of children.get(id) ?? []) visitDownstream(child)
  }
  visitDownstream(selected)
  return new Set([...upstream, ...downstream])
}

/** Draw saved dependencies and group unlinked records by kind. */
export function layoutNuwaCollaboration(nodes: NuwaNode[]) {
  const partitioned = partitionNuwaCollaboration(nodes)
  const entries: CollaborationNode[] = partitioned.path.map((record) => ({
    type: "work", id: record.id, record, created_at: record.created_at,
    depends_on: record.depends_on ?? [],
  }))
  partitioned.groups.forEach((group, index) => {
    entries.push({
      type: "loose", id: group.id, kind: group.kind, records: group.records,
      // Rank sorts the rows without pretending the group was recorded then.
      created_at: `0001-01-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
      depends_on: [],
    })
  })
  const laid = layoutNuwa(entries)
  return { ...laid, missing: [...new Set([...laid.missing, ...partitioned.missing])].sort() }
}
