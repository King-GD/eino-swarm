import { bytesToHex, equalBytes } from "./bytes"
import { unmarshalFrame } from "./frame"

/** Each logical channel owns a distinct encrypted session. */
export type HubSocket = {
  readonly readyState: number
  onopen: ((event: Event) => void) | null
  onmessage: ((event: MessageEvent) => void) | null
  onerror: ((event: Event) => void) | null
  onclose: ((event: CloseEvent) => void) | null
  send(data: Uint8Array): void
  close(): void
}
export type SocketOpener = (url: string, protocol: string) => WebSocket
export const openBrowserSocket: SocketOpener = (url, protocol) => new WebSocket(url, [protocol])

type Relay = { socket: WebSocket; channels: Map<string, HubSocket>; finish: (event: CloseEvent) => void }
// Factories isolate injected transports; production uses one stable factory.
const pools = new WeakMap<SocketOpener, Map<string, Relay>>()

export function openHubSocket(url: string, protocol: string, device: Uint8Array, host: Uint8Array, session: Uint8Array, open: SocketOpener = openBrowserSocket, bindingScoped = false): HubSocket {
  let pool = pools.get(open)
  if (!pool) { pool = new Map(); pools.set(open, pool) }
  const peer = `${bytesToHex(host)}\t${bytesToHex(session)}`
  // Account relay sockets authenticate a single pairing ticket. Legacy QR
  // hubs may still replace a socket when the same device key connects twice.
  const key = JSON.stringify([url, bytesToHex(device), bindingScoped ? peer : null, bindingScoped ? protocol : null])
  let relay = pool.get(key)
  if (!relay || relay.socket.readyState > 1) {
    const socket = open(url, protocol)
    socket.binaryType = "arraybuffer"
    relay = { socket, channels: new Map(), finish: () => undefined }
    const owner = relay
    const retire = () => { if (pool!.get(key) === owner) pool!.delete(key) }
    owner.finish = event => {
      retire()
      const channels = [...owner.channels.values()]
      owner.channels.clear()
      for (const channel of channels) channel.onclose?.(event)
    }
    socket.onopen = event => { for (const channel of [...owner.channels.values()]) channel.onopen?.(event) }
    socket.onerror = event => { for (const channel of [...owner.channels.values()]) channel.onerror?.(event) }
    socket.onclose = event => owner.finish(event)
    socket.onmessage = event => {
      try {
        const bytes = event.data instanceof Uint8Array ? event.data : new Uint8Array(event.data as ArrayBuffer)
        const frame = unmarshalFrame(bytes)
        if (!equalBytes(frame.dst, device)) return
        const channel = owner.channels.get(`${bytesToHex(frame.src)}\t${bytesToHex(frame.sessionID)}`)
        channel?.onmessage?.(event)
      } catch { /* Invalid relay frames cannot address a session. */ }
    }
    pool.set(key, owner)
  }
  if (relay.channels.has(peer)) throw new Error("pc_pairing_busy")
  const owner = relay
  let closed = false
  const channel: HubSocket = {
    get readyState() { return closed ? 3 : owner.socket.readyState },
    onopen: null, onmessage: null, onerror: null, onclose: null,
    send(data) {
      if (closed || owner.socket.readyState !== 1) throw new Error("offline")
      owner.socket.send(data)
    },
    close() {
      if (closed) return
      closed = true
      if (owner.channels.get(peer) === channel) owner.channels.delete(peer)
      // Settle a connect/handshake waiter even when other PCs still own the Hub.
      channel.onclose?.({ code: 1000, reason: "" } as CloseEvent)
      if (owner.channels.size === 0) {
        if (pool!.get(key) === owner) pool!.delete(key)
        owner.socket.close()
      }
    },
  }
  owner.channels.set(peer, channel)
  if (owner.socket.readyState === 1) queueMicrotask(() => { if (!closed) channel.onopen?.(new Event("open")) })
  return channel
}
