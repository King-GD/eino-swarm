import type { GatewayHost } from "./gateway-account"
import type { SavedLink } from "./store"

/** Keep an online selection; otherwise use the first available account PC. */
export function autoConnectTarget(
  hosts: GatewayHost[],
  saved: SavedLink[],
  activeFingerprint: string,
): GatewayHost | undefined {
  const online = hosts.filter((host) => host.online && host.enabled)
  const active = saved.find(link => link.fingerprint === activeFingerprint)
  if (active && !active.gatewayHostID) return undefined
  return online.find(host => host.id === active?.gatewayHostID) ?? online[0]
}
