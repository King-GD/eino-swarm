export interface PanelDefinition {
  id: string
  version: string
  name: string
  description: string
  entry: string
  schema: unknown
  initial_state?: unknown
}

export interface TaskPanel {
  id: string
  thread_id: string
  plugin_id: string
  plugin_version: string
  revision: number
  source_status?: string
  state: unknown
  definition: PanelDefinition
}

export interface PanelChange {
  cursor: number
  thread_id: string
  panel_id: string
  revision: number
  base_revision?: number
  kind: "snapshot" | "patch" | "deleted" | "removed"
  state?: unknown
  operations?: JsonPatchOperation[]
  source_status?: string
}

export interface JsonPatchOperation {
  op: "add" | "remove" | "replace" | "move" | "copy" | "test"
  path: string
  from?: string
  value?: unknown
}

export interface HostContext {
  panel_id: string
  thread_id: string
  revision: number
  theme: "light" | "dark"
  locale: string
  width: number
  height: number
  visible: boolean
  source_status?: string
}
