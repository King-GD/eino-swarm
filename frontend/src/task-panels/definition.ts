import catalog from "./catalog.json"
import type { TaskPanel } from "./types"

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`
  if (value !== null && typeof value === "object") return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonical((value as Record<string, unknown>)[key])}`).join(",")}}`
  return JSON.stringify(value)
}

interface EasmSchema {
  properties?: Record<string, { properties?: Record<string, unknown> }>
  $defs?: Record<string, { properties?: Record<string, unknown> }>
}

function schemaForPinnedEasm(current: unknown, pinned: unknown): unknown {
  const schema = JSON.parse(JSON.stringify(current)) as EasmSchema
  const saved = pinned as EasmSchema | null
  const properties = saved?.properties ?? {}
  if (!Object.hasOwn(properties, "query_scope")) {
    delete schema.properties?.query_scope
  } else {
    const scope = schema.properties?.query_scope?.properties
    if (scope && !Object.hasOwn(properties.query_scope?.properties ?? {}, "components_server_id")) delete scope.components_server_id
  }
  const detail = schema.$defs?.detail?.properties
  const savedDetail = saved?.$defs?.detail?.properties ?? {}
  for (const field of ["is_valid", "is_match", "observations"]) {
    if (detail && !Object.hasOwn(savedDetail, field)) delete detail[field]
  }
  return schema
}

/** Stored data may only be executed by its matching bundled definition. */
export function panelDefinitionCompatible(panel: TaskPanel): boolean {
  const current = catalog.find((definition) => definition.id === panel.plugin_id)
  if (!current || current.version !== panel.plugin_version || panel.definition.id !== current.id ||
    panel.definition.version !== current.version || panel.definition.entry !== current.entry) return false
  const schema = current.id === "easm-asset-portrait" ? schemaForPinnedEasm(current.schema, panel.definition.schema) : current.schema
  return canonical(panel.definition.schema) === canonical(schema)
}
