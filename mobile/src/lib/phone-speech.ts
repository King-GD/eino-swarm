import { useEffect, useSyncExternalStore } from "react"
import type { SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { speechPlayback } from "../../../frontend/src/lib/speech-media"
import { GATEWAY_ACCOUNT_EVENT, loadGatewaySession } from "./gateway-account"
import { gatewayAuthorityCurrent, sameGatewayAuthority, GATEWAY_REFUSAL_EVENT } from "./gateway-authority"
import type { RemoteLink } from "./link"
import { OpSpeechConfig } from "./rpc"
import { forgetSpeechCache, loadSpeechCache, pruneSpeechCaches, saveSpeechCache, speechBinding, type SyncedSpeech } from "./speech-cache"
import { gatewaySpeechCall, localSpeechCall, SpeechCallError } from "./speech-transport"
import type { SavedLink } from "./store"

export type PhoneSpeechState = {
  epoch: number
  snapshot?: SpeechSnapshot
  syncedAt?: number
  host?: string
  connected: boolean
  inputReady: boolean
  outputReady: boolean
}
let state: PhoneSpeechState = { epoch: 0, connected: false, inputReady: false, outputReady: false }
let source: { pc: SavedLink; link: RemoteLink | null; cached?: SyncedSpeech } | undefined
const listeners = new Set<() => void>()
const requests = new Set<AbortController>()
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }
const snapshot = () => state

export function cancelPhoneSpeech(): void {
  for (const request of requests) request.abort()
  speechPlayback.stop()
  window.dispatchEvent(new Event("zwai.phone.speech.cancel"))
}

function publish(changed: boolean) {
  if (changed) cancelPhoneSpeech()
  const speech = source?.cached?.snapshot
  const session = loadGatewaySession()
  const connected = Boolean(source?.link?.alive())
  const allowed = speech?.execution === "pc_local" ? connected : Boolean(speech && session &&
    speech.gateway_origin === session.origin && gatewayAuthorityCurrent(session) &&
    (!source?.pc.gatewayOwnerID || source.pc.gatewayOwnerID === session.user.id) &&
    (!source?.pc.gatewayMobileID || source.pc.gatewayMobileID === session.device.id))
  state = { epoch: state.epoch + (changed ? 1 : 0), snapshot: speech, syncedAt: source?.cached?.syncedAt,
    host: source?.pc.label || source?.pc.fingerprint, connected,
    inputReady: Boolean(allowed && speech?.input.enabled && speech.input.ready), outputReady: Boolean(allowed && speech?.output.enabled && speech.output.ready) }
  for (const listener of listeners) listener()
}

/** Only the explicitly selected PC supplies speech configuration. The gateway
 * cache can outlive its socket; local execution always requires that socket. */
export function setPhoneSpeechSource(pc: SavedLink | undefined, link: RemoteLink | null): void {
  if (pc && source && speechBinding(pc) === speechBinding(source.pc)) {
    // A PC socket is not the owner of a direct Gateway recording or request.
    // Keep the source object stable so its outstanding result is still valid.
    const localChanged = source.cached?.snapshot.execution !== "gateway" &&
      (source.link !== link || state.connected !== Boolean(link?.alive()))
    source.pc = pc
    source.link = link
    publish(localChanged)
    return
  }
  source = pc ? { pc, link, cached: loadSpeechCache(pc) } : undefined
  publish(true)
}

export function acceptPhoneSpeechConfig(pc: SavedLink, raw: unknown): boolean {
  if (!source || speechBinding(source.pc) !== speechBinding(pc)) return false
  const cached = saveSpeechCache(pc, raw)
  if (!cached) {
    forgetSpeechCache(pc)
    source.cached = undefined
    publish(true)
    return false
  }
  const changed = JSON.stringify(cached.snapshot) !== JSON.stringify(source.cached?.snapshot)
  source.cached = cached
  publish(changed)
  return true
}

export function usePhoneSpeechSync(link: RemoteLink | null, pc: SavedLink | undefined, hosts: SavedLink[]): void {
  const binding = pc ? speechBinding(pc) : ""
  useEffect(() => { pruneSpeechCaches(hosts) }, [hosts])
  // Retire the authority on PC/grant changes or unmount, independently from
  // reconnecting its transport. Link-only cleanup must leave Gateway work up.
  useEffect(() => () => {
    if (source && speechBinding(source.pc) === binding) { source = undefined; publish(true) }
  }, [binding])
  useEffect(() => {
    setPhoneSpeechSource(pc, link)
    let alive = true
    let busy = false
    const refresh = async () => {
      if (!pc || !link?.alive() || busy || document.hidden) { publish(false); return }
      busy = true
      try {
        const response = await link.rpc({ op: OpSpeechConfig }, { dropOnTimeout: false, timeoutMs: 15000 })
        if (!alive || !source || speechBinding(source.pc) !== binding || source.link !== link) return
        if (response.ok && response.speech) acceptPhoneSpeechConfig(pc, response.speech)
        else if (response.ok || response.code === "unknown_op" || response.code === "audio_config_limit") {
          // A live old host cannot authorize cached features it no longer declares.
          source.cached = undefined
          forgetSpeechCache(pc)
          publish(true)
        }
      } catch { if (alive) publish(false) }
      finally { busy = false }
    }
    let authority = loadGatewaySession()
    let authorityAllowed = Boolean(authority && gatewayAuthorityCurrent(authority))
    const authorityChanged = () => {
      const next = loadGatewaySession()
      const nextAllowed = Boolean(next && gatewayAuthorityCurrent(next))
      // Health refreshes also publish account events. A new avatar or renewed
      // expiry is not a new credential and must not interrupt dictation.
      const changed = Boolean(authority) !== Boolean(next) ||
        Boolean(authority && next && !sameGatewayAuthority(authority, next)) || authorityAllowed !== nextAllowed
      authority = next
      authorityAllowed = nextAllowed
      publish(changed)
    }
    const authorityRefused = () => publish(true)
    const visibility = () => { if (document.hidden) cancelPhoneSpeech(); else void refresh() }
    const timer = setInterval(() => { void refresh() }, 15000)
    window.addEventListener("focus", refresh)
    document.addEventListener("visibilitychange", visibility)
    window.addEventListener(GATEWAY_ACCOUNT_EVENT, authorityChanged)
    window.addEventListener(GATEWAY_REFUSAL_EVENT, authorityRefused)
    void refresh()
    return () => {
      alive = false; clearInterval(timer)
      window.removeEventListener("focus", refresh)
      document.removeEventListener("visibilitychange", visibility)
      window.removeEventListener(GATEWAY_ACCOUNT_EVENT, authorityChanged)
      window.removeEventListener(GATEWAY_REFUSAL_EVENT, authorityRefused)
    }
    // The complete binding captures every pairing authority field. Host labels
    // are presentation only, and the cached snapshot owns its own timestamp.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [link, binding])
}

export function usePhoneSpeech(): PhoneSpeechState { return useSyncExternalStore(subscribe, snapshot, snapshot) }

async function call(operation: "transcribe" | "speak", input: Blob | string, signal: AbortSignal): Promise<string | Blob> {
  const target = source
  const speech = target?.cached?.snapshot
  if (!target || !speech || !(operation === "transcribe" ? state.inputReady : state.outputReady)) throw new SpeechCallError("unavailable")
  const controller = new AbortController()
  const abort = () => controller.abort()
  requests.add(controller)
  if (signal.aborted) abort(); else signal.addEventListener("abort", abort, { once: true })
  try {
    let result: string | Blob
    if (speech.execution === "gateway") {
      const session = loadGatewaySession()
      if (!session) throw new SpeechCallError("unavailable")
      result = await gatewaySpeechCall(speech, session, operation, input, controller.signal)
    } else {
      if (!target.link?.alive()) throw new SpeechCallError("unavailable")
      result = await localSpeechCall(speech, target.link, operation, input, controller.signal)
    }
    if (controller.signal.aborted || source !== target) throw new DOMException("Aborted", "AbortError")
    return result
  } finally { signal.removeEventListener("abort", abort); requests.delete(controller) }
}

export async function transcribePhoneSpeech(blob: Blob, signal: AbortSignal): Promise<string> { return await call("transcribe", blob, signal) as string }
export async function synthesizePhoneSpeech(text: string, signal: AbortSignal): Promise<Blob> { return await call("speak", text, signal) as Blob }
