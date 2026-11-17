import { useEffect, useRef } from "react"
import type { useGatewayAccount } from "./use-gateway-account"
import { loadActiveFingerprint, loadSavedLinks } from "./store"
import { sameLinkAuthority } from "./link-authority"
import { autoConnectTarget } from "./gateway-auto-connect"

/** Provision every online account PC without moving the user's current view. */
export function useAutoGatewayPCs(account: ReturnType<typeof useGatewayAccount>) {
  const latest = useRef(account)
  latest.current = account
  const selected = useRef(false)
  const authority = `${account.session?.origin}:${account.session?.user.id}:${account.session?.device.id}`
  useEffect(() => {
    selected.current = false
    const select = () => { selected.current = true }
    window.addEventListener("zwai.gateway.connect", select)
    return () => window.removeEventListener("zwai.gateway.connect", select)
  }, [authority])
  useEffect(() => {
    if (account.status !== "authorized" || !account.session || !account.bootstrap) return
    let stopped = false
    const connect = async () => {
      await Promise.allSettled(account.hosts.filter(host => host.online && host.enabled).map(async host => {
        const before = loadSavedLinks()
        const saved = await latest.current.connectHost(host.id, { select: false })
        if (stopped) return
        if (!before.some(link => sameLinkAuthority(link, saved))) window.dispatchEvent(new Event("zwai.gateway.account"))
        if (selected.current) return
        const links = loadSavedLinks()
        const active = links.find(link => link.fingerprint === loadActiveFingerprint())
        if (active && active.gatewayOrigin && active.gatewayOrigin !== account.session?.origin) return
        const owned = links.filter(link => link.gatewayOrigin === account.session?.origin
          && link.gatewayOwnerID === account.session?.user.id && link.gatewayMobileID === account.session?.device.id)
        const readyHosts = account.hosts.filter(host => owned.some(link => link.gatewayHostID === host.id))
        const targetHost = autoConnectTarget(readyHosts, links, loadActiveFingerprint())
        const target = owned.find(link => link.gatewayHostID === targetHost?.id)
        if (!target) return
        selected.current = true
        window.dispatchEvent(new CustomEvent("zwai.gateway.connect", { detail: target.fingerprint }))
      }))
    }
    void connect()
    return () => { stopped = true }
  }, [authority, account.status, account.bootstrap, account.hosts])
}
