import { capabilityAllows, type CapabilityRole } from "../../../frontend/src/lib/capabilities"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"

export const GATEWAY_CAPABILITY_REFRESH_EVENT = "zwai.gateway.capabilities.refresh"
export const GATEWAY_CAPABILITIES_EVENT = "zwai.gateway.capabilities"
let current: { session: GatewaySession; bootstrap: GatewayBootstrap } | undefined
export function publishGatewayCapabilities(session: GatewaySession, bootstrap: GatewayBootstrap): void {
  const changed = !current || current.session.token !== session.token || current.session.origin !== session.origin ||
    JSON.stringify([current.bootstrap.capabilities, current.bootstrap.models.source_id, current.bootstrap.gateway_models?.source_id]) !== JSON.stringify([bootstrap.capabilities, bootstrap.models.source_id, bootstrap.gateway_models?.source_id])
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
  window.dispatchEvent(new Event(GATEWAY_CAPABILITIES_EVENT))
}

export function gatewayCapabilityTarget(session: GatewaySession, sourceID: string | undefined, role: CapabilityRole): string | undefined {
  if (!gatewayCapabilityAllows(session, sourceID, role) || !current) return
  return sourceID === current.bootstrap.models.source_id ? current.bootstrap.models.api_base_url : current.bootstrap.gateway_models?.api_base_url
}
