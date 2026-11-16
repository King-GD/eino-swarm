/** The gateway supplies permission and presets; clients never infer a login channel. */
export type ModelRef = { provider_id: string; model: string }
export type ModelRole = "conversation" | "title" | "compact" | "embedding" | "transcription" | "speech" | "ocr" | "decisions"
export type ModelPolicy = {
  mode: "local" | "gateway"
  configurable: boolean
  ready: boolean
  scope: string
  defaults?: Partial<Record<ModelRole, ModelRef>>
  bindings?: Partial<Record<ModelRole, ModelRef>>
  roles?: { id: string; enabled: boolean; label: string }[]
}

export const modelRoles: ModelRole[] = ["conversation", "title", "compact", "embedding", "transcription", "speech", "ocr", "decisions"]

/** Roles a person can assign. Decisions stays a background call. */
export const configurableModelRoles = modelRoles.filter((role): role is Exclude<ModelRole, "decisions"> => role !== "decisions")
