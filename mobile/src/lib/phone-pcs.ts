import type { GatewayHost } from "./gateway-account"
import { gatewayHostDisplayName } from "./gateway-names"
import { linkLabel, type SavedLink } from "./store"

export function onlineFirst<T extends { online?: boolean }>(hosts: T[]): T[] {
  return [...hosts].sort((a, b) => Number(Boolean(b.online)) - Number(Boolean(a.online)))
}

export function orderSavedPCs(saved: SavedLink[], hosts: GatewayHost[] = [], origin?: string): SavedLink[] {
  return onlineFirst(saved.map(link => ({ link, online: hosts.find(host => host.id === link.gatewayHostID && (!origin || link.gatewayOrigin === origin))?.online }))).map(row => row.link)
}

/** Display entries never invent saved pairing credentials for offline PCs. */
export function phonePCChoices(saved: SavedLink[], hosts: GatewayHost[] = [], origin?: string) {
  const choices = saved.map(link => {
    const host = hosts.find(row => row.id === link.gatewayHostID && (!origin || link.gatewayOrigin === origin))
    return { id: link.fingerprint, fingerprint: link.fingerprint, hostID: link.gatewayHostID,
      label: linkLabel(link, saved), online: host?.online, enabled: host?.enabled !== false }
  })
  for (const host of hosts) {
    if (saved.some(link => link.gatewayHostID === host.id && (!origin || link.gatewayOrigin === origin))) continue
    choices.push({ id: "account:" + host.id, fingerprint: "", hostID: host.id,
      label: gatewayHostDisplayName(host) || "", online: host.online, enabled: host.enabled })
  }
  return onlineFirst(choices)
}
