import { useCallback, useEffect, useRef, type RefObject } from "react"
import { openLink, type RemoteLink } from "./link"
import { gatewayUpgradePending } from "./gateway-link-upgrade"
import { sameLinkAuthority } from "./link-authority"
import { loadSavedLink, loadSavedLinks, type SavedLink } from "./store"
import { RetainedPhoneLinks } from "./retained-phone-links"

type Options = {
  allowsLink?: (link: SavedLink) => boolean
  onlineLink?: (link: SavedLink) => boolean | undefined
  link: RefObject<RemoteLink | null>; authority: RefObject<SavedLink | null>
  generation: RefObject<number>; unlinking: RefObject<boolean>; retryTimer: RefObject<number>
  recoverRef: RefObject<(force?: boolean) => Promise<void>>
  install: (next: RemoteLink | null) => void
  attach: (next: RemoteLink) => void
  restore: (next: RemoteLink) => Promise<void>
  firstList: (next: RemoteLink) => Promise<void>
  busy: (busy: boolean) => void; reconnecting: (busy: boolean) => void
  error: (error?: string) => void; message: (error: unknown) => string
}

/** One selected transport, retained inactive PCs and generation-scoped opening. */
export function usePhoneLinkLifecycle(options: Options) {
  const latest = useRef(options)
  latest.current = options
  const opening = useRef<{ generation: number } | null>(null)
  const attempts = useRef(0)
  const retained = useRef(new RetainedPhoneLinks())
  const permitted = useCallback((saved: SavedLink) => latest.current.allowsLink?.(saved) !== false
    && !gatewayUpgradePending(saved) && loadSavedLinks().some(row => sameLinkAuthority(row, saved)), [])
  const detach = useCallback(() => {
    const o = latest.current
    const previous = o.link.current, saved = o.authority.current
    o.link.current = null
    o.authority.current = null
    o.install(null)
    if (!previous) return
    if (saved && o.allowsLink?.(saved) !== false && !gatewayUpgradePending(saved)
      && loadSavedLinks().some(row => sameLinkAuthority(row, saved))) retained.current.retain(saved, previous)
    else previous.close()
  }, [])
  const close = useCallback(() => {
    const o = latest.current
    const previous = o.link.current
    o.link.current = null
    o.authority.current = null
    o.install(null)
    previous?.close()
  }, [])

  const run = useCallback(async (boot: boolean, force = false) => {
    const o = latest.current
    if (o.unlinking.current || opening.current?.generation === o.generation.current) return
    const saved = loadSavedLink()
    if (!saved || o.allowsLink?.(saved) === false || gatewayUpgradePending(saved)) { o.busy(false); return }
    if (!boot && !force && o.link.current?.alive() && sameLinkAuthority(saved, o.authority.current)) return
    const operation = { generation: o.generation.current }
    opening.current = operation
    const valid = () => latest.current.allowsLink?.(saved) !== false && operation.generation === o.generation.current && sameLinkAuthority(saved, loadSavedLink())
    if (boot) o.busy(true)
    else o.reconnecting(true)
    // Keep other PCs connected. A forced reconnect or changed credential still
    // retires that PC's old socket before dialing its replacement.
    if (!boot && force) close()
    else detach()
    o.authority.current = saved
    try {
      const next = await retained.current.connect(saved, openLink, () => permitted(saved))
      if (!valid()) return
      retained.current.take(saved)
      o.authority.current = saved
      o.link.current = next
      o.attach(next)
      o.install(next)
      attempts.current = 0
      o.error(undefined)
      if (boot) await o.firstList(next)
      else await o.restore(next)
    } catch (error) {
      if (!valid()) return
      if (!o.link.current) o.authority.current = null
      o.error(o.message(error))
      if (!gatewayUpgradePending(saved)) {
        const delay = boot ? 1000 : Math.min(15_000, 1000 * 2 ** Math.min(attempts.current++, 4))
        window.clearTimeout(o.retryTimer.current)
        o.retryTimer.current = window.setTimeout(() => { void o.recoverRef.current() }, delay)
      }
    } finally {
      if (opening.current === operation) {
        opening.current = null
        if (operation.generation === o.generation.current) {
          o.busy(false); o.reconnecting(false)
          // onDisconnect can arrive while firstList/restore still holds this
          // operation. Replay that recovery after the RPC phase has settled.
          if (valid() && o.link.current && !o.link.current.alive() && !gatewayUpgradePending(saved)) {
            window.clearTimeout(o.retryTimer.current)
            o.retryTimer.current = window.setTimeout(() => { void o.recoverRef.current() }, 1000)
          }
        }
      }
    }
  }, [close, detach, permitted])
  const boot = useCallback(() => run(true), [run])
  const recover = useCallback((force = false) => run(false, force), [run])
  options.recoverRef.current = recover
  useEffect(() => { retained.current.reconcile(loadSavedLinks(), latest.current.allowsLink) })
  useEffect(() => {
    let stopped = false
    const warm = () => {
      if (stopped || document.hidden) return
      for (const saved of loadSavedLinks()) {
        if (!permitted(saved) || latest.current.onlineLink?.(saved) === false || sameLinkAuthority(saved, latest.current.authority.current)) continue
        void retained.current.connect(saved, openLink, () => !stopped && permitted(saved)).catch(() => undefined)
      }
    }
    warm()
    const visible = () => { if (!document.hidden) warm() }
    window.addEventListener("zwai.gateway.account", warm)
    document.addEventListener("visibilitychange", visible)
    const timer = window.setInterval(warm, 15_000)
    return () => {
      stopped = true
      window.clearInterval(timer)
      window.removeEventListener("zwai.gateway.account", warm)
      document.removeEventListener("visibilitychange", visible)
    }
  }, [permitted])
  useEffect(() => {
    void boot()
    return () => {
      const o = latest.current
      o.generation.current++
      window.clearTimeout(o.retryTimer.current)
      const previous = o.link.current
      o.link.current = null
      o.authority.current = null
      previous?.close()
      retained.current.close()
    }
  }, [boot])
  return { boot, recover, close, detach }
}
