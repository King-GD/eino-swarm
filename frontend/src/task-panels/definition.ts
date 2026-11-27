import catalog from "./catalog.json"
import easmV1 from "./easm-v1.json"
import easmV2 from "./easm-v2.json"
import type { TaskPanel } from "./types"

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`
  if (value !== null && typeof value === "object") return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonical((value as Record<string, unknown>)[key])}`).join(",")}}`
  return JSON.stringify(value)
}

/** Stored data may only be executed by its matching bundled definition. */
export function panelDefinitionCompatible(panel: TaskPanel): boolean {
  // A saved panel keeps rendering under the contract it was written for; the
  // host upgrades it at startup, so this only covers a client that arrived
  // before that upgrade.
  const published = panel.plugin_id === easmV1.id ? [easmV1, easmV2] : []
  const pinned = published.find((definition) => definition.version === panel.plugin_version)
  const current = pinned ?? catalog.find((definition) => definition.id === panel.plugin_id)
  const renderer = (definition: object) => "renderer" in definition ? definition.renderer : undefined
  return Boolean(current && current.version === panel.plugin_version && panel.definition.id === current.id && panel.definition.version === current.version && panel.definition.entry === current.entry && renderer(panel.definition) === renderer(current) && canonical(panel.definition.schema) === canonical(current.schema))
}
