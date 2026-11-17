import { expect, it } from "vitest"
import catalog from "./catalog.json"
import { panelDefinitionCompatible } from "./definition"
import type { TaskPanel } from "./types"

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
