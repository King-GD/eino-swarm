import { GATEWAY_ACCOUNT_EVENT, clearGatewaySession, loadGatewaySession, type GatewaySession } from "./gateway-account"

export const GATEWAY_REFUSAL_EVENT = "zwai.gateway.refusal"
export type GatewayRefusal = { session: GatewaySession; status: 401 | 403 }

/** Authority comparisons deliberately include the credential and device. A late
 * response from a previous login must never expire its replacement. */
export function sameGatewayAuthority(left: GatewaySession, right: GatewaySession | null): boolean {
  return Boolean(right && left.origin === right.origin && left.token === right.token && left.user.id === right.user.id && left.device.id === right.device.id)
}

export function notifyGatewayRefusal(session: GatewaySession, status: number): void {
  if ((status !== 401 && status !== 403) || !sameGatewayAuthority(session, loadGatewaySession())) return
  if (status === 401) clearGatewaySession()
  window.dispatchEvent(new CustomEvent<GatewayRefusal>(GATEWAY_REFUSAL_EVENT, { detail: { session, status } }))
}

export function watchGatewayRefusal(listener: (refusal: GatewayRefusal) => void): () => void {
  const receive = (event: Event) => listener((event as CustomEvent<GatewayRefusal>).detail)
  window.addEventListener(GATEWAY_REFUSAL_EVENT, receive)
  return () => window.removeEventListener(GATEWAY_REFUSAL_EVENT, receive)
}

export function gatewayAuthorityCurrent(session: GatewaySession): boolean {
  const current = loadGatewaySession()
  return sameGatewayAuthority(session, current) && current?.user.enabled !== false && current?.user.approval_status === "approved" && Date.parse(current.device.expires_at) > Date.now()
}

/** Every request using this authority is cancelled on a refusal as well as a
 * logout, replacement or expiry. A 403 does not destroy the account credential. */
export function watchGatewayAuthority(session: GatewaySession, invalidate: () => void): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined
  let closed = false
  const cleanup = () => {
    if (closed) return
    closed = true
    unwatchRefusal()
    window.removeEventListener(GATEWAY_ACCOUNT_EVENT, changed)
    window.removeEventListener("storage", changed)
    clearTimeout(timer)
  }
  const refuse = () => { if (!closed) { cleanup(); invalidate() } }
  const changed = () => {
    if (closed) return
    clearTimeout(timer)
    timer = undefined
    if (!gatewayAuthorityCurrent(session)) { refuse(); return }
    const current = loadGatewaySession()!
    const remaining = Math.max(0, Date.parse(current.device.expires_at) - Date.now())
    // Same-authority refreshes may extend or shorten a live request's lease.
    // Recheck saved authority at expiry before trusting any older timer.
    // Browsers clamp long timers; later account/storage events re-arm those.
    if (remaining <= 2_147_483_647) timer = setTimeout(changed, remaining)
  }
  const unwatchRefusal = watchGatewayRefusal((refusal) => { if (sameGatewayAuthority(session, refusal.session)) refuse() })
  window.addEventListener(GATEWAY_ACCOUNT_EVENT, changed)
  window.addEventListener("storage", changed)
  changed()
  return cleanup
}
