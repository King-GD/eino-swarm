import { expect, it, vi } from "vitest"
import { RetainedPhoneLinks } from "./retained-phone-links"
import type { SavedLink } from "./store"
import type { RemoteLink } from "./link"

const saved: SavedLink = { fingerprint: "pc", hubURL: "https://hub.invalid", ticket: "ticket", hostPub: "pub", sessionID: "session" }
const link = () => ({ alive: () => true, close: vi.fn() }) as unknown as RemoteLink

it("does not dial after cleanup wins before a queued background open", async () => {
  const pool = new RetainedPhoneLinks(), open = vi.fn(async () => link())
  const pending = pool.connect(saved, open, () => true)
  const refused = expect(pending).rejects.toThrow("offline")
  pool.close()
  await refused
  expect(open).not.toHaveBeenCalled()
})

it("shares one opening between background warming and foreground selection", async () => {
  const pool = new RetainedPhoneLinks(), connected = link()
  let finish!: (link: RemoteLink) => void
  const open = vi.fn(() => new Promise<RemoteLink>(resolve => { finish = resolve }))
  const background = pool.connect(saved, open, () => true)
  const foreground = pool.connect(saved, open, () => true)
  expect(foreground).toBe(background)
  await Promise.resolve()
  finish(connected)
  expect(await foreground).toBe(connected)
  expect(await pool.connect(saved, open, () => true)).toBe(connected)
  expect(open).toHaveBeenCalledOnce()
  expect(pool.take(saved)).toBe(connected)
  connected.close()
})

it("closes a late background connection after page cleanup", async () => {
  const pool = new RetainedPhoneLinks(), connected = link()
  let finish!: (link: RemoteLink) => void
  const pending = pool.connect(saved, () => new Promise(resolve => { finish = resolve }), () => true)
  const refused = expect(pending).rejects.toThrow("offline")
  await Promise.resolve()
  pool.close()
  finish(connected)
  await refused
  expect(connected.close).toHaveBeenCalledOnce()
  expect(pool.take(saved)).toBeUndefined()
})

it("refuses a pending connection whose authority was removed", async () => {
  const pool = new RetainedPhoneLinks(), connected = link()
  let finish!: (link: RemoteLink) => void
  const pending = pool.connect(saved, () => new Promise(resolve => { finish = resolve }), () => true)
  const refused = expect(pending).rejects.toThrow("offline")
  await Promise.resolve()
  pool.reconcile([])
  finish(connected)
  await refused
  expect(connected.close).toHaveBeenCalledOnce()
})
