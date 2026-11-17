import { capabilityAllows } from "../../../frontend/src/lib/capabilities"
import { defaultSpeechSettings, type SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"

export type GatewayVoiceModels = Record<string, Partial<Record<"transcription" | "speech", string>>>
const roles = ["transcription", "speech"] as const
const name = (value: unknown): value is string => typeof value === "string" && value.trim().length > 0 && value.length <= 256 && !/[\u0000-\u001f\u007f]/.test(value)

function sources(bootstrap: GatewayBootstrap) {
  return ([{ kind: "preset" as const, descriptor: bootstrap.models }, { kind: "gateway" as const, descriptor: bootstrap.gateway_models }])
    .filter(source => source.descriptor !== undefined)
}
function defaultModel(bootstrap: GatewayBootstrap, kind: "preset" | "gateway", role: typeof roles[number]): string | undefined {
  const descriptor = kind === "preset" ? bootstrap.models : bootstrap.gateway_models
  const selected = descriptor?.defaults?.[role]
  if (name(selected)) return selected
  // Fixed public role aliases come only from the advertised enabled role list.
  if (kind === "preset" && bootstrap.capabilities?.sources.preset?.capabilities[role]?.configurable === false && bootstrap.models.roles?.some(row => row.id === role && row.enabled)) return role
}

/** Only an authorized typed catalog can supply a missing default. Names and
 * credentials are never inferred from a PC or a model's spelling. */
export async function discoverGatewayVoiceModels(bootstrap: GatewayBootstrap, read: (url: string) => Promise<unknown>): Promise<GatewayVoiceModels> {
  const result: GatewayVoiceModels = {}
  await Promise.all(sources(bootstrap).map(async ({ kind, descriptor }) => {
    if (!descriptor) return
    const capability = bootstrap.capabilities?.sources[kind]
    const selected: GatewayVoiceModels[string] = {}
    for (const role of roles) if (capabilityAllows(capability, role)) {
      const model = defaultModel(bootstrap, kind, role)
      if (model) selected[role] = model
    }
    const missing = roles.filter(role => capabilityAllows(capability, role) && !selected[role])
    if (missing.length) {
      try {
        const data = await read(kind === "preset" ? bootstrap.models.client_catalog_url ?? descriptor.catalog_url : descriptor.catalog_url)
        if (!Array.isArray(data)) throw new Error("invalid_gateway_catalog")
        for (const role of missing) {
          const row = data.slice(0, 1000).find(row => row && row.model_type === role && name(row.id))
          if (row) selected[role] = row.id
        }
      } catch (error) {
        const status = error && typeof error === "object" && "status" in error ? error.status : undefined
        const code = error && typeof error === "object" && "code" in error ? error.code : undefined
        if (status === 401) throw error
        if (status === 403 || code === "invalid_gateway_response" || error instanceof SyntaxError || error instanceof Error && error.message === "invalid_gateway_catalog") {
          result[descriptor.source_id] = selected
          return
        }
        for (const role of missing) {
          const previous = bootstrap.voice_models?.[descriptor.source_id]?.[role]
          if (name(previous)) selected[role] = previous
        }
      }
    }
    result[descriptor.source_id] = selected
  }))
  return result
}

/** Gateway voice is a phone capability; PC feature switches do not gate it. */
export function gatewayVoiceSnapshot(session: GatewaySession, bootstrap: GatewayBootstrap): SpeechSnapshot {
  const defaults = defaultSpeechSettings()
  const snapshot: SpeechSnapshot = {
    execution: "gateway", gateway_origin: session.origin, limits: defaults.limits,
    input: { ...defaults.input, enabled: false, ready: false, label: "" },
    output: { ...defaults.output, enabled: false, ready: false, label: "" },
  }
  for (const role of roles) {
    for (const { kind, descriptor } of sources(bootstrap)) {
      if (!descriptor || !capabilityAllows(bootstrap.capabilities?.sources[kind], role)) continue
      const model = defaultModel(bootstrap, kind, role) ?? bootstrap.voice_models?.[descriptor.source_id]?.[role]
      if (!name(model)) continue
      const slot = role === "transcription" ? snapshot.input : snapshot.output
      Object.assign(slot, { model, label: model, enabled: true, ready: true, execution: "gateway", gateway_origin: session.origin, source_id: descriptor.source_id })
      break
    }
  }
  return snapshot
}
