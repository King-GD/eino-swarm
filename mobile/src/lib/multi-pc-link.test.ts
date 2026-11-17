import { afterEach, expect, it, vi } from "vitest"
import { DeviceLink } from "./client"
import { bytesToHex, utf8 } from "./bytes"
import { generateIdentity, respond, type Session } from "./crypto"
import { marshalFrame, TYPE_DATA, TYPE_HANDSHAKE, unmarshalFrame } from "./frame"
import { OpMeta, PROTOCOL_V } from "./rpc"

/** Matches the dependency Hub's pubkey → socket registration, including its
 * replacement behavior. Requests cross the actual client crypto/RPC boundary. */
class RelaySocket {
  readyState = 0
  binaryType = "arraybuffer"
  onopen: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  readonly sessions = new Map<string, Session>()
  constructor(readonly hosts: Map<string, ReturnType<typeof generateIdentity>>) { queueMicrotask(() => { this.readyState = 1; this.onopen?.(new Event("open")) }) }
  send(data: Uint8Array) {
    const frame = unmarshalFrame(data)
    const host = this.hosts.get(bytesToHex(frame.dst))
    if (!host) return
    const key = bytesToHex(host.pub) + bytesToHex(frame.sessionID)
    if (frame.type === TYPE_HANDSHAKE) {
      const { msg, sess } = respond(host, frame.src, frame.payload)
      this.sessions.set(key, sess)
      queueMicrotask(() => this.deliver(marshalFrame({ type: TYPE_HANDSHAKE, src: host.pub, dst: frame.src, sessionID: frame.sessionID, payload: msg })))
    } else if (frame.type === TYPE_DATA) {
      const session = this.sessions.get(key)!
      const request = JSON.parse(new TextDecoder().decode(session.open(frame.payload)))
      const payload = session.seal(utf8(JSON.stringify({ v: PROTOCOL_V, id: request.id, ok: true, host: bytesToHex(host.pub), startup: "ready" })))
      queueMicrotask(() => this.deliver(marshalFrame({ type: TYPE_DATA, src: host.pub, dst: frame.src, sessionID: frame.sessionID, payload })))
    }
  }
  deliver(data: Uint8Array) { this.onmessage?.({ data: data.slice().buffer } as MessageEvent) }
  close() { if (this.readyState === 3) return; this.readyState = 3; this.onclose?.({ code: 1006, reason: "" } as CloseEvent) }
}

afterEach(() => vi.useRealTimers())
it("keeps all PCs connected on one Hub socket across repeated 15-second heartbeats", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] })
  const phone = generateIdentity(), first = generateIdentity(), second = generateIdentity()
  const hosts = new Map([first, second].map(host => [bytesToHex(host.pub), host]))
  const sockets: RelaySocket[] = []
  const open = vi.fn(() => {
    sockets.at(-1)?.close() // A second physical socket would evict the first PC.
    const socket = new RelaySocket(hosts); sockets.push(socket)
    return socket as unknown as WebSocket
  })
  const a = new DeviceLink(phone, first.pub, new Uint8Array(16).fill(1))
  const b = new DeviceLink(phone, second.pub, new Uint8Array(16).fill(2))
  const disconnected = vi.fn(); a.onDisconnect = disconnected; b.onDisconnect = disconnected
  try {
    await a.connect("https://hub.example.test", "ticket-a", open)
    await b.connect("https://hub.example.test", "ticket-b", open)
    for (let i = 0; i < 4; i++) {
      await vi.advanceTimersByTimeAsync(15_000)
      const [ra, rb] = await Promise.all([a.rpc({ op: OpMeta }), b.rpc({ op: OpMeta })])
      expect(ra.host).toBe(bytesToHex(first.pub)); expect(rb.host).toBe(bytesToHex(second.pub))
    }
    expect(open).toHaveBeenCalledOnce()
    expect(disconnected).not.toHaveBeenCalled()
    b.close()
    expect(a.alive()).toBe(true)
    expect((await a.rpc({ op: OpMeta })).ok).toBe(true)
    expect(sockets[0].readyState).toBe(1)
    a.close()
    expect(sockets[0].readyState).toBe(3)
  } finally { a.close(); b.close() }
})

it("isolates logical RPC timeouts and replaces the transport only after a real socket loss", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] })
  const phone = generateIdentity(), first = generateIdentity(), second = generateIdentity()
  const hosts = new Map([first, second].map(host => [bytesToHex(host.pub), host]))
  const sockets: RelaySocket[] = []
  const open = () => { const socket = new RelaySocket(hosts); sockets.push(socket); return socket as unknown as WebSocket }
  const a = new DeviceLink(phone, first.pub, new Uint8Array(16).fill(1), { keepAliveMs: 0 })
  const b = new DeviceLink(phone, second.pub, new Uint8Array(16).fill(2), { keepAliveMs: 0 })
  const disconnected = vi.fn(); a.onDisconnect = disconnected
  const resumed = new DeviceLink(phone, first.pub, new Uint8Array(16).fill(1), { keepAliveMs: 0 })
  try {
    await a.connect("https://hub.example.test", "a", open)
    await b.connect("https://hub.example.test", "b", open)
    hosts.delete(bytesToHex(second.pub))
    const timedOut = b.rpc({ op: OpMeta }, { timeoutMs: 100 }).catch(error => error)
    await vi.advanceTimersByTimeAsync(100)
    expect(await timedOut).toMatchObject({ message: "rpc timeout" })
    expect(a.alive()).toBe(true)
    expect(disconnected).not.toHaveBeenCalled()
    sockets[0].close()
    expect(a.alive()).toBe(false)
    expect(disconnected).toHaveBeenCalledOnce()
    await resumed.connect("https://hub.example.test", "a", open)
    expect(sockets).toHaveLength(2)
    expect((await resumed.rpc({ op: OpMeta })).ok).toBe(true)
  } finally { a.close(); b.close(); resumed.close() }
})

it("shares normalized Hub endpoints without mixing different Hub base paths", async () => {
  const phone = generateIdentity(), first = generateIdentity(), second = generateIdentity()
  const hosts = new Map([first, second].map(host => [bytesToHex(host.pub), host]))
  const registrations = new Map<string, RelaySocket>()
  const open = vi.fn((url: string) => {
    registrations.get(url)?.close()
    const socket = new RelaySocket(hosts); registrations.set(url, socket)
    return socket as unknown as WebSocket
  })
  const a = new DeviceLink(phone, first.pub, new Uint8Array(16).fill(1), { keepAliveMs: 0 })
  const b = new DeviceLink(phone, second.pub, new Uint8Array(16).fill(2), { keepAliveMs: 0 })
  const other = new DeviceLink(phone, first.pub, new Uint8Array(16).fill(1), { keepAliveMs: 0 })
  try {
    await a.connect("https://hub.example.test/team/", "a", open)
    await b.connect("https://hub.example.test/team//", "b", open)
    expect(open).toHaveBeenCalledOnce()
    await other.connect("https://hub.example.test/other", "other", open)
    expect(open).toHaveBeenCalledTimes(2)
    expect((await a.rpc({ op: OpMeta })).ok).toBe(true)
    expect((await other.rpc({ op: OpMeta })).ok).toBe(true)
  } finally { a.close(); b.close(); other.close() }
})
