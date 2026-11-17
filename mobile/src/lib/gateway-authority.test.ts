import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { clearGatewaySession, loadGatewaySession, saveGatewaySession, type GatewaySession } from "./gateway-account"
import { notifyGatewayRefusal, watchGatewayAuthority } from "./gateway-authority"

vi.mock("@capacitor/browser", () => ({ Browser: { open: vi.fn(async () => undefined) } }))
vi.mock("@capacitor/core", () => ({ Capacitor: { getPlatform: () => "ios", isNativePlatform: () => false }, registerPlugin: () => ({}) }))

const started = Date.parse("2026-11-16T00:00:00Z")
const session = (expiresIn: number): GatewaySession => ({
  origin: "https://gateway.example.test", token: "agm_" + "a".repeat(64),
  user: { id: "user", username: "account", enabled: true, approval_status: "approved" },
  device: { id: "phone", expires_at: new Date(started + expiresIn).toISOString() },
})
let cleanups: (() => void)[] = []
function watch(value: GatewaySession, invalidate = vi.fn()) {
  const cleanup = watchGatewayAuthority(value, invalidate)
  cleanups.push(cleanup)
  return { invalidate, cleanup }
}

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(started); localStorage.clear() })
afterEach(() => { for (const cleanup of cleanups) cleanup(); cleanups = []; localStorage.clear(); vi.useRealTimers(); vi.restoreAllMocks() })

describe("Gateway authority expiry during live requests", () => {
  it("keeps a renewed same-authority stream alive past its old expiry", async () => {
    const original = session(1000)
    saveGatewaySession(original)
    const { invalidate } = watch(original)
    await vi.advanceTimersByTimeAsync(500)
    saveGatewaySession(session(5000))
    expect(vi.getTimerCount()).toBe(1)
    await vi.advanceTimersByTimeAsync(501)
    expect(invalidate).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(3999)
    expect(invalidate).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it("moves expiry earlier when the same authority receives a shorter lease", async () => {
    const original = session(10000)
    saveGatewaySession(original)
    const { invalidate } = watch(original)
    await vi.advanceTimersByTimeAsync(100)
    saveGatewaySession(session(500))
    await vi.advanceTimersByTimeAsync(399)
    expect(invalidate).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(invalidate).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(10000)
    expect(invalidate).toHaveBeenCalledOnce()
  })

  it("rechecks the current saved expiry before a delayed old timer invalidates it", async () => {
    const original = session(1000)
    saveGatewaySession(original)
    const { invalidate } = watch(original)
    // A timer may run before the storage/account notification is delivered.
    const dispatch = vi.spyOn(window, "dispatchEvent").mockReturnValue(true)
    saveGatewaySession(session(5000))
    dispatch.mockRestore()
    await vi.advanceTimersByTimeAsync(1000)
    expect(invalidate).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(1)
    await vi.advanceTimersByTimeAsync(4000)
    expect(invalidate).toHaveBeenCalledOnce()
  })

  it.each(["logout", "401", "403", "replacement", "disabled", "approval"] as const)("still invalidates on %s and releases the old expiry timer", (cause) => {
    const original = session(10000)
    saveGatewaySession(original)
    const { invalidate } = watch(original)
    if (cause === "logout") clearGatewaySession()
    else if (cause === "401" || cause === "403") notifyGatewayRefusal(original, Number(cause))
    else if (cause === "replacement") saveGatewaySession({ ...original, token: "agm_" + "b".repeat(64) })
    else saveGatewaySession({ ...original, user: { ...original.user, ...(cause === "disabled" ? { enabled: false } : { approval_status: "pending" }) } })
    expect(invalidate).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
    if (cause === "403") expect(loadGatewaySession()?.token).toBe(original.token)
    if (cause === "401" || cause === "logout") expect(loadGatewaySession()).toBeNull()
  })

  it("ignores refusals for a previous credential and cleanup removes notifications and timers", async () => {
    const original = session(1000)
    saveGatewaySession(original)
    const { invalidate, cleanup } = watch(original)
    notifyGatewayRefusal({ ...original, token: "agm_" + "b".repeat(64) }, 403)
    expect(invalidate).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(1)
    cleanup(); cleanup()
    expect(vi.getTimerCount()).toBe(0)
    clearGatewaySession()
    await vi.advanceTimersByTimeAsync(10000)
    expect(invalidate).not.toHaveBeenCalled()
  })
})
