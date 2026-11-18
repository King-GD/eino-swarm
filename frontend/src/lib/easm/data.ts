import type { EasmResult } from "./types"

/** The host validates field types against the catalog Schema. These checks
 *  cover relationships between records before a snapshot reaches the graph. */
export function isEasmResult(value: unknown): value is EasmResult {
  const result = value as Partial<EasmResult> | null
  if (!result || result.schema_version !== 1 || typeof result.title !== "string" || !result.title ||
    !Array.isArray(result.nodes) || !Array.isArray(result.edges) ||
    !Array.isArray(result.heat) || !Array.isArray(result.components)) return false

  const ids = new Set<string>()
  let roots = 0
  for (const node of result.nodes) {
    if (!node || typeof node.id !== "string" || !node.id || ids.has(node.id)) return false
    ids.add(node.id)
    if (node.kind === "root") roots++
  }
  if (roots !== (result.nodes.length ? 1 : 0)) return false
  for (const edge of result.edges) {
    if (!edge || !ids.has(edge.source) || !ids.has(edge.target)) return false
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
