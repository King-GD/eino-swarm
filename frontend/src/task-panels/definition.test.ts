import { expect, it } from "vitest"
import catalog from "./catalog.json"
import { panelDefinitionCompatible } from "./definition"
import type { TaskPanel } from "./types"

interface EasmSchema {
  required: string[]
  properties: Record<string, { type?: string; required?: string[]; properties?: Record<string, unknown> }>
  $defs: Record<string, { required: string[]; properties: Record<string, unknown> }>
}

function savedEasmPanel(): TaskPanel & { definition: { schema: EasmSchema } } {
  const definition = JSON.parse(JSON.stringify(catalog.find((item) => item.id === "easm-asset-portrait")))
  return {
    id: "p", thread_id: "t", plugin_id: definition.id, plugin_version: definition.version, revision: 1, definition,
    state: {
      schema_version: 1, title: "Existing portrait",
      nodes: [{ id: "root", kind: "root", label: "Existing entity" }, { id: "domain", kind: "domain", label: "example.test" }],
      edges: [{ source: "root", target: "domain", confirmed: true }], heat: [], components: [],
    },
  }
}

it("requires the pinned plugin identity and schema without depending on object key order", () => {
  const definition = catalog[0], panel: TaskPanel = { id: "p", thread_id: "t", plugin_id: definition.id, plugin_version: definition.version, revision: 1, state: definition.initial_state, definition }
  expect(panelDefinitionCompatible(panel)).toBe(true)
  expect(panelDefinitionCompatible({ ...panel, plugin_id: "unregistered" })).toBe(false)
  expect(panelDefinitionCompatible({ ...panel, plugin_version: "old" })).toBe(false)
  expect(panelDefinitionCompatible({ ...panel, definition: { ...definition, schema: {} } })).toBe(false)
  expect(panelDefinitionCompatible({ ...panel, definition: { ...definition, entry: "task-panels/wrong.html" } })).toBe(false)
  const schema = Object.fromEntries(Object.entries(definition.schema).reverse())
  expect(panelDefinitionCompatible({ ...panel, definition: { ...definition, schema } })).toBe(true)
})

it("reads an EASM portrait pinned before optional query and certificate metadata without changing it", () => {
  const panel = savedEasmPanel()
  delete panel.definition.schema.properties.query_scope
  delete panel.definition.schema.$defs.detail.properties.is_valid
  delete panel.definition.schema.$defs.detail.properties.is_match
  delete panel.definition.schema.$defs.detail.properties.observations
  const saved = JSON.stringify(panel), bundled = JSON.stringify(catalog)
  expect(panelDefinitionCompatible(panel)).toBe(true)
  expect(JSON.stringify(panel)).toBe(saved)
  expect(JSON.stringify(catalog)).toBe(bundled)
})

it("reads a pinned EASM query scope without the later optional component server identity", () => {
  const panel = savedEasmPanel()
  delete panel.definition.schema.properties.query_scope.properties!.components_server_id
  const saved = JSON.stringify(panel)
  expect(panelDefinitionCompatible(panel)).toBe(true)
  expect(JSON.stringify(panel)).toBe(saved)
})

it("reads an EASM portrait pinned before per-host observations without changing its stored definition or state", () => {
  const panel = savedEasmPanel()
  delete panel.definition.schema.$defs.detail.properties.observations
  const saved = JSON.stringify(panel), bundled = JSON.stringify(catalog)
  expect(panelDefinitionCompatible(panel)).toBe(true)
  expect(JSON.stringify(panel)).toBe(saved)
  expect(JSON.stringify(catalog)).toBe(bundled)
})

it("rejects changed observation types and requirements instead of treating them as absent", () => {
  const wrongType = savedEasmPanel()
  wrongType.definition.schema.$defs.detail.properties.observations = { type: "object" }
  expect(panelDefinitionCompatible(wrongType)).toBe(false)

  const extraRequired = savedEasmPanel()
  const observation = extraRequired.definition.schema.$defs.detail.properties.observations as { items: { required: string[] } }
  observation.items.required.push("is_valid")
  expect(panelDefinitionCompatible(extraRequired)).toBe(false)

  const optionalHost = savedEasmPanel()
  const optionalObservation = optionalHost.definition.schema.$defs.detail.properties.observations as { items: { required: string[] } }
  optionalObservation.items.required = []
  expect(panelDefinitionCompatible(optionalHost)).toBe(false)

  const wrongFlag = savedEasmPanel()
  const flagObservation = wrongFlag.definition.schema.$defs.detail.properties.observations as { items: { properties: Record<string, unknown> } }
  flagObservation.items.properties.is_valid = { type: "string" }
  expect(panelDefinitionCompatible(wrongFlag)).toBe(false)
})

it.each(["is_valid", "is_match"])("rejects a changed type for an existing %s flag", (flag) => {
  const panel = savedEasmPanel()
  delete panel.definition.schema.properties.query_scope
  panel.definition.schema.$defs.detail.properties[flag] = { type: "string" }
  expect(panelDefinitionCompatible(panel)).toBe(false)
})

it("rejects changed EASM requirements and other schema differences", () => {
  const rootRequired = savedEasmPanel()
  delete rootRequired.definition.schema.properties.query_scope
  rootRequired.definition.schema.required.push("query_scope")
  expect(panelDefinitionCompatible(rootRequired)).toBe(false)

  const detailRequired = savedEasmPanel()
  detailRequired.definition.schema.$defs.detail.required.push("is_valid")
  expect(panelDefinitionCompatible(detailRequired)).toBe(false)

  const scopeRequired = savedEasmPanel()
  delete scopeRequired.definition.schema.properties.query_scope.properties!.components_server_id
  scopeRequired.definition.schema.properties.query_scope.required!.push("components_server_id")
  expect(panelDefinitionCompatible(scopeRequired)).toBe(false)

  const scopeType = savedEasmPanel()
  scopeType.definition.schema.properties.query_scope = { type: "string" }
  expect(panelDefinitionCompatible(scopeType)).toBe(false)

  const unrelatedField = savedEasmPanel()
  delete unrelatedField.definition.schema.$defs.detail.properties.serial
  expect(panelDefinitionCompatible(unrelatedField)).toBe(false)
})
