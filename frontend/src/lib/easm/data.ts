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
    if (!edge || !ids.has(edge.source) || !ids.has(edge.target) ||
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
  return true
}

/** A section without data waits while its step can still deliver. The host's
 *  source_status is the only proof a producer is collecting right now: the
 *  snapshot's own "running" survives an engine restart that interrupted the
 *  source, and trusting it alone left the panel spinning forever after reload.
 *  While the owning task is active the agent may run the source again, so that
 *  keeps the wait even before the next call starts. */
export function sectionLoading(result: EasmResult, step: EasmStepId, taskActive: boolean, sourceStatus?: string): boolean {
  const status = result.collection.steps.find((item) => item.id === step)?.status
  if (sourceStatus === "running" && (status === "running" || status === "pending")) return true
  return taskActive && status !== "done"
}

/** The graph is filled by whole batches the agent pushes, so no single step owns
 *  it: it waits while a source call is collecting or the owning task can still
 *  push another batch, and stops once neither can. */
export function graphSectionLoading(taskActive: boolean, sourceStatus?: string): boolean {
  return taskActive || sourceStatus === "running"
}
