import { gatewayImageModel } from "./gateway-image-models"
import { applyGatewayAudio, gatewayAudioView } from "./gateway-audio-parameters"
import { speechRoute, type SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { gatewayVoiceSnapshot, type GatewayVoiceModels } from "./gateway-speech"
import { capabilityAllows, type CapabilityRole } from "../../../frontend/src/lib/capabilities"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"

export const GATEWAY_CAPABILITY_REFRESH_EVENT = "zwai.gateway.capabilities.refresh"
export const GATEWAY_CAPABILITIES_EVENT = "zwai.gateway.capabilities"
let current: { session: GatewaySession; bootstrap: GatewayBootstrap } | undefined
let modelPolicyRevision = 0
export function gatewayModelPolicyRevision(): number { return modelPolicyRevision }
function policyKey(value: typeof current): string {
  if (!value) return ""
  const { session, bootstrap } = value
  return JSON.stringify([session.origin, session.token, session.user.id, session.device.id, bootstrap.capabilities,
    bootstrap.models.source_id, bootstrap.gateway_models?.source_id, bootstrap.models.defaults, bootstrap.gateway_models?.defaults, bootstrap.image_models])
}
export function publishGatewayCapabilities(session: GatewaySession, bootstrap: GatewayBootstrap): void {
  const policyChanged = policyKey(current) !== policyKey({ session, bootstrap })
  if (policyChanged) modelPolicyRevision++
  const changed = policyChanged || JSON.stringify([current?.bootstrap.voice_models, current?.bootstrap.voice_parameters]) !== JSON.stringify([bootstrap.voice_models, bootstrap.voice_parameters])
  current = { session, bootstrap }
  if (changed) window.dispatchEvent(new Event(GATEWAY_CAPABILITIES_EVENT))
}
/** A phone uses its own device grant, even for a PC's inherited voice model. */
export function gatewayCapabilityAllows(session: GatewaySession, sourceID: string | undefined, role: CapabilityRole): boolean {
  if (!current || current.session.token !== session.token || current.session.origin !== session.origin || current.session.user.id !== session.user.id || current.session.device.id !== session.device.id) return false
  const boot = current.bootstrap
  const kind = sourceID === boot.models.source_id ? "preset" : sourceID && sourceID === boot.gateway_models?.source_id ? "gateway" : undefined
  return Boolean(kind && capabilityAllows(boot.capabilities?.sources[kind], role))
}

export function invalidateGatewayCapabilities(session: GatewaySession): void {
  if (current?.session.token !== session.token || current.session.origin !== session.origin || !current.bootstrap.capabilities) return
  current = { session, bootstrap: { ...current.bootstrap, capabilities: undefined, capability_status: "unavailable" } }
  modelPolicyRevision++
  window.dispatchEvent(new Event(GATEWAY_CAPABILITIES_EVENT))
}

export function gatewayCapabilityTarget(session: GatewaySession, sourceID: string | undefined, role: CapabilityRole): string | undefined {
  if (!gatewayCapabilityAllows(session, sourceID, role) || !current) return
  return sourceID === current.bootstrap.models.source_id ? current.bootstrap.models.api_base_url : current.bootstrap.gateway_models?.api_base_url
}

function bootstrapFor(session: GatewaySession): GatewayBootstrap | undefined {
  if (current?.session.token === session.token && current.session.origin === session.origin && current.session.user.id === session.user.id && current.session.device.id === session.device.id) return current.bootstrap
}
export function currentGatewayVoice(session: GatewaySession) {
  const bootstrap = bootstrapFor(session)
  return bootstrap && applyGatewayAudio(gatewayVoiceSnapshot(session, bootstrap), session, bootstrap)
}
export function knownGatewayVoiceModels(session: GatewaySession): GatewayVoiceModels | undefined {
  return bootstrapFor(session)?.voice_models
}

/** Revalidate a PC selection against the phone's own catalog and device authority. */
export function authoritativeGatewayAudio(session: GatewaySession, snapshot: SpeechSnapshot): SpeechSnapshot | undefined {
 const bootstrap = bootstrapFor(session)
 return bootstrap && applyGatewayAudio(snapshot, session, bootstrap)
}
export function currentGatewayAudioView(session: GatewaySession, snapshot: SpeechSnapshot, kind: "input" | "output") {
 const bootstrap = bootstrapFor(session), route = speechRoute(snapshot,kind)
 if (!bootstrap || route?.execution !== "gateway" || route.gateway_origin !== session.origin) return
 return gatewayAudioView(session,bootstrap,route.source_id,snapshot[kind].model,kind === "input" ? "transcription" : "speech",snapshot[kind].audio?.overrides)
}

export function knownGatewayAudioParameters(session: GatewaySession) {return bootstrapFor(session)?.voice_parameters}

export function currentGatewayImageModel(session: GatewaySession, sourceID: string) {
  const bootstrap = bootstrapFor(session)
  return bootstrap && gatewayImageModel(bootstrap, sourceID)
}

export function knownGatewayImageModels(session: GatewaySession) { return bootstrapFor(session)?.image_models }

export function invalidateGatewayImageCapability(session: GatewaySession, sourceID: string): void {
  const bootstrap = bootstrapFor(session)
  const capabilities = bootstrap?.capabilities
  const kind = sourceID === bootstrap?.models.source_id ? "preset"
    : sourceID === bootstrap?.gateway_models?.source_id ? "gateway" : undefined
  if (!bootstrap || !capabilities || !kind) return
  const source = capabilities.sources[kind]
  const image = source?.capabilities.image
  if (!source || !image) return
  publishGatewayCapabilities(session, {
    ...bootstrap,
    capabilities: {
      ...capabilities,
      sources: {
        ...capabilities.sources,
        [kind]: { ...source, capabilities: { ...source.capabilities, image: { ...image, available: false, endpoints: [] } } },
      },
    },
  })
}
