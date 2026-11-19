/** Public gateway audio declarations; absence never invents configurable options. */
export type AudioRole = "speech" | "transcription"
export type AudioEnum = { values: string[]; default?: string; configurable: boolean }
export type AudioText = { max_characters: number; default?: string; configurable: boolean }
export type AudioNumber = { min: number; max: number; default: number; configurable: boolean }
export type AudioParameters = {
  voice?: AudioEnum; response_format?: AudioEnum; stream_format?: AudioEnum; language?: AudioEnum
  speed?: AudioNumber; instructions?: AudioText; prompt?: AudioText
  max_input_characters?: number; max_file_bytes?: number
}
export type AudioOverrides = Partial<Record<"voice" | "response_format" | "stream_format" | "language" | "instructions" | "prompt", string>> & { speed?: number }
export type AudioDescriptor = { version: number; revision?: string; status: string; parameters?: AudioParameters }
export type AudioParameterView = AudioDescriptor & { scope: string; overrides: AudioOverrides }
const record = (v: unknown): v is Record<string, unknown> => Boolean(v && typeof v === "object" && !Array.isArray(v))
const bytes = (value: string) => new TextEncoder().encode(value).length
const enumKeys = ["voice", "response_format", "stream_format", "language"] as const
const textKeys = ["instructions", "prompt"] as const
const unsupported = (): AudioDescriptor => ({ version: 0, status: "unsupported" })
export function parseAudioDescriptor(raw: unknown, role: AudioRole): AudioDescriptor {
  if (!record(raw) || raw.version !== 1 || typeof raw.revision !== "string" || !raw.revision || bytes(raw.revision) > 256) return unsupported()
  if (raw.status === "undeclared" || raw.status === "incompatible") return { version: 1, revision: raw.revision, status: raw.status }
  if (raw.status !== "configured" || !record(raw.parameters)) return unsupported()
  const parameters: AudioParameters = {}
  const p = raw.parameters
  for (const key of enumKeys) {
    if (p[key] === undefined) continue
    const v = p[key]
    if (!record(v) || typeof v.configurable !== "boolean" || !Array.isArray(v.values) || !v.values.length || v.values.length > 128 || v.values.some(x => typeof x !== "string" || !x || bytes(x) > 256) || new Set(v.values).size !== v.values.length || v.default !== undefined && !v.values.includes(v.default) || !v.configurable && v.default === undefined) return unsupported()
    parameters[key] = { values: [...v.values] as string[], configurable: v.configurable, ...(v.default === undefined ? {} : { default: v.default as string }) }
  }
  if (p.speed !== undefined) {
    const n = p.speed
    if (!record(n) || typeof n.configurable !== "boolean" || ![n.min, n.max, n.default].every(v => typeof v === "number" && Number.isFinite(v)) || (n.min as number) <= 0 || (n.max as number) < (n.min as number) || (n.default as number) < (n.min as number) || (n.default as number) > (n.max as number)) return unsupported()
    parameters.speed = { min: n.min as number, max: n.max as number, default: n.default as number, configurable: n.configurable }
  }
  for (const key of textKeys) {
    if (p[key] === undefined) continue
    const v = p[key]
    if (!record(v) || typeof v.configurable !== "boolean" || !Number.isInteger(v.max_characters) || (v.max_characters as number) < 1 || (v.max_characters as number) > 100000 || v.default !== undefined && (typeof v.default !== "string" || Array.from(v.default).length > (v.max_characters as number))) return unsupported()
    parameters[key] = { max_characters: v.max_characters as number, configurable: v.configurable, ...(v.default === undefined ? {} : { default: v.default as string }) }
  }
  for (const key of ["max_file_bytes", "max_input_characters"] as const) {
    if (p[key] !== undefined && (!Number.isSafeInteger(p[key]) || (p[key] as number) < 0)) return unsupported()
    if (p[key] !== undefined) parameters[key] = p[key] as number
  }
  if (role === "speech") { delete parameters.language; delete parameters.prompt; delete parameters.max_file_bytes }
  else { delete parameters.voice; delete parameters.speed; delete parameters.stream_format; delete parameters.instructions; delete parameters.max_input_characters }
  return { version: 1, revision: raw.revision, status: "configured", parameters }
}

export function resolveAudioParameters(descriptor: AudioDescriptor, overrides: AudioOverrides): { request: AudioOverrides; effective: AudioOverrides } {
  if (descriptor.version !== 1 || descriptor.status !== "configured" || !descriptor.parameters) throw new Error("audio_configuration_unavailable")
  const p = descriptor.parameters
  const request: AudioOverrides = {}, effective: AudioOverrides = {}
  for (const key of enumKeys) {
    const option = p[key], value = overrides[key]
    if (value !== undefined) {
      if (!option || typeof value !== "string" || !option.values.includes(value) || !option.configurable) throw new Error("invalid_audio_parameter")
      request[key] = value; effective[key] = value
    } else if (option?.default !== undefined) effective[key] = option.default
    else if (key === "voice" && option) throw new Error("invalid_audio_parameter")
  }
  const speed = overrides.speed, n = p.speed
  if (speed !== undefined) {
    if (!n || !Number.isFinite(speed) || speed < n.min || speed > n.max || !n.configurable) throw new Error("invalid_audio_parameter")
    request.speed = speed; effective.speed = speed
  } else if (n) effective.speed = n.default
  for (const key of textKeys) {
    const option = p[key], value = overrides[key]
    if (value !== undefined) {
      if (!option || typeof value !== "string" || Array.from(value).length > option.max_characters || !option.configurable) throw new Error("invalid_audio_parameter")
      request[key] = value; effective[key] = value
    } else if (option?.default !== undefined) effective[key] = option.default
  }
  return { request, effective }
}

/** Decoder limits are client capabilities, never inferred gateway choices. */
export function playableAudio(parameters: AudioOverrides, role: AudioRole): boolean {
  if (role === "transcription") return parameters.response_format === undefined || ["json", "verbose_json"].includes(parameters.response_format)
  return (parameters.response_format === undefined || ["mp3", "wav", "pcm"].includes(parameters.response_format)) && (parameters.stream_format === undefined || parameters.stream_format === "audio" || parameters.stream_format === "sse" && parameters.response_format === "pcm")
}

/** Explicit values with no visible control. A shown select, speed field, or text follow button already restores the gateway default. */
export function audioOverridesWithoutControl(parameters: AudioParameters | undefined, value: AudioOverrides): AudioOverrides {
  const stuck: AudioOverrides = {}
  for (const key of enumKeys) if (value[key] !== undefined && !parameters?.[key]?.configurable) stuck[key] = value[key]
  if (value.speed !== undefined && !parameters?.speed?.configurable) stuck.speed = value.speed
  for (const key of textKeys) if (value[key] !== undefined && !parameters?.[key]?.configurable) stuck[key] = value[key]
  return stuck
}

/** Copy preferences only; a paired PC never supplies the phone's authority. */
export function safeAudioOverrides(raw: unknown): AudioOverrides {
 const result:AudioOverrides={}
 if (!record(raw)) return result
 for (const key of [...enumKeys,...textKeys]) if (typeof raw[key] === "string" && new TextEncoder().encode(raw[key]).length <= 400000) result[key]=raw[key]
 if (typeof raw.speed === "number" && Number.isFinite(raw.speed) && raw.speed > 0) result.speed=raw.speed
 return result
}
