import type { EasmResult, EasmStepId } from "./types"

/** The host validates field types against the catalog Schema. These checks
 *  cover relationships between records before a snapshot reaches the graph. */
export function isEasmResult(value: unknown): value is EasmResult {
  const result = value as Partial<EasmResult> | null
  if (!result || result.schema_version !== 1 || typeof result.title !== "string" || !result.title ||
    !Array.isArray(result.nodes) || !Array.isArray(result.edges) ||
    !Array.isArray(result.heat) || !Array.isArray(result.components) ||
    !result.collection || !Array.isArray(result.collection.steps)) return false

  const ids = new Set<string>()
  let root: string | undefined
  for (const node of result.nodes) {
    if (!node || typeof node.id !== "string" || !node.id || ids.has(node.id)) return false
    ids.add(node.id)
    if (node.kind === "root") {
      if (root !== undefined) return false
      root = node.id
    }
  }
  if (result.nodes.length && root === undefined) return false
  const targets = new Set<string>()
  for (const edge of result.edges) {
    // An older snapshot may retain a clue whose first source is missing.
    // The rooted projection skips that relationship without losing the clue.
    if (!edge || typeof edge.source !== "string" || !edge.source || !ids.has(edge.target) ||
      edge.source === edge.target || edge.target === root || targets.has(edge.target)) return false
    targets.add(edge.target)
  }
  for (const node of result.nodes) {
    const features = node.detail?.features
    if (features !== undefined && (!Array.isArray(features) || features.some(
      (feature) => !feature || (feature.node_id !== undefined && !ids.has(feature.node_id)),
    ))) return false
  }

  let previousDate = ""
  for (const point of result.heat) {
    if (!point || typeof point.date !== "string" || point.date <= previousDate) return false
    const date = new Date(`${point.date}T00:00:00Z`)
    if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, 10) !== point.date) return false
    previousDate = point.date
  }
  for (const component of result.components) {
    if (!component || (component.tags !== undefined && !Array.isArray(component.tags))) return false
    const types = new Set<string>()
    for (const tag of component.tags ?? []) {
      if (!tag || types.has(tag.type)) return false
      types.add(tag.type)
    }
  }
  // A panel stored before the cloud table existed has no clouds field at all.
  if (result.clouds !== undefined) {
    if (!Array.isArray(result.clouds)) return false
    for (const cloud of result.clouds) {
      if (!cloud || typeof cloud.name !== "string" || !cloud.name || !Array.isArray(cloud.services)) return false
    }
  }
  return true
}

export type EasmSectionStatus = "loading" | "failed" | "unavailable" | "interrupted"

/** A saved failure or interruption is meaningful even when the step has no
 *  data. The host receipt settles stale pending/running steps after a restart
 *  or Stop. An active task can still supply its first query. */
export function sectionStatus(result: EasmResult, step: EasmStepId, taskActive: boolean, sourceStatus?: string): EasmSectionStatus | undefined {
  const status = result.collection.steps.find((item) => item.id === step)?.status
  if (status === "failed" || status === "unavailable" || status === "interrupted") return status
  if (status === "pending" || status === "running") {
    if (sourceStatus === "interrupted") return "interrupted"
    if (sourceStatus === "running") return "loading"
  }
  if (taskActive && status !== "done") return "loading"
}

/** The graph is filled by whole batches the agent pushes, so no single step owns
 *  it: it waits while a source call is collecting or the owning task can still
 *  push another batch, and stops once neither can. */
export function graphSectionLoading(taskActive: boolean, sourceStatus?: string): boolean {
  return taskActive || sourceStatus === "running"
}
