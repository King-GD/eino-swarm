/** Public capability identifiers; this list describes implemented adapters. */
export const roles = ["conversation", "title", "compact", "translation", "embedding", "ocr", "decisions", "transcription", "speech", "image"] as const
export type CapabilityRole = typeof roles[number]
export type RoleCapability = { audio_parameters_version?: number; supported: boolean; available: boolean; configurable: boolean; endpoints: string[]; reason?: string }
export type SourceCapabilities = { authorized: boolean; available: boolean; configurable: boolean; capabilities: Partial<Record<CapabilityRole, RoleCapability>> }
export type ClientCapabilities = { version: 1; model_roles_version: 3; sources: { preset?: SourceCapabilities; gateway?: SourceCapabilities } }
const endpoints: Record<CapabilityRole, string[]> = {
  conversation: ["chat_completions", "responses"], title: ["chat_completions", "responses"], compact: ["chat_completions", "responses"], translation: ["chat_completions", "responses"],
  ocr: ["chat_completions", "responses"], embedding: ["embeddings"], decisions: ["decisions"], transcription: ["audio_transcriptions"], speech: ["audio_speech"], image: [],
}
export function capabilityAllows(source: SourceCapabilities | undefined, role: CapabilityRole): boolean {
  const c = source?.capabilities?.[role]
  return Boolean(source?.authorized && source.available && c?.supported && c.available && c.endpoints?.some(e => endpoints[role].includes(e)))
}
export function capabilityConfigurable(source: SourceCapabilities | undefined, role: CapabilityRole): boolean {
  return capabilityAllows(source, role) && source?.capabilities[role]?.configurable === true
}
const record = (x: unknown): x is Record<string, unknown> => Boolean(x && typeof x === "object" && !Array.isArray(x))
export function parseCapabilities(data: unknown): ClientCapabilities | undefined {
  if (!record(data) || data.version !== 1 || data.model_roles_version !== 3 || !record(data.sources)) return
  const sources: ClientCapabilities["sources"] = {}
  for (const kind of ["preset", "gateway"] as const) {
    const value = data.sources[kind]
    if (!record(value) || !record(value.capabilities) || [value.authorized, value.available, value.configurable].some(v => typeof v !== "boolean")) return
    const capabilities: SourceCapabilities["capabilities"] = {}
    for (const role of roles) {
      const c = value.capabilities[role]
      if (c === undefined) continue
      if (!record(c) || [c.supported, c.available, c.configurable].some(v => typeof v !== "boolean") || !Array.isArray(c.endpoints) || c.endpoints.length > 16 || c.endpoints.some(e => typeof e !== "string" || e.length > 64) || c.reason !== undefined && (typeof c.reason !== "string" || c.reason.length > 256)) return
      capabilities[role] = { ...(typeof c.audio_parameters_version === "number" ? { audio_parameters_version: c.audio_parameters_version } : {}), supported: c.supported as boolean, available: c.available as boolean, configurable: c.configurable as boolean, endpoints: c.endpoints as string[], ...(typeof c.reason === "string" ? { reason: c.reason } : {}) }
    }
    sources[kind] = { authorized: value.authorized as boolean, available: value.available as boolean, configurable: value.configurable as boolean, capabilities }
  }
  return { version: 1, model_roles_version: 3, sources }
}
