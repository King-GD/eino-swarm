import { act, renderHook } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import type { RemoteLink } from "./link"
import type { PhoneMode } from "./phone-mode"
import { usePcModeSwitch } from "./use-pc-mode-switch"

const target = (rpc: RemoteLink["rpc"]): RemoteLink => ({ rpc, alive: () => true, path: "relay", close: vi.fn(), announceDevice: vi.fn() })

describe("selected PC mode mutation", () => {
  it.each(["local", "account"] as const)("sends only explicit %s intent and requires its exact acknowledgement", async mode => {
    const product_mode = mode === "local" ? "local" : "fofa"
    const rpc = vi.fn().mockResolvedValue({ ok: true, product_mode })
    let switcher!: (mode: PhoneMode) => Promise<void>
    const unbind = vi.fn(), bind = vi.fn(fn => { switcher = fn; return unbind })
    const link = target(rpc)
    const { unmount } = renderHook(() => usePcModeSwitch(link, bind))
    await act(async () => { await switcher(mode) })
    expect(rpc).toHaveBeenCalledWith({ op: "set_product_mode", product_mode }, { dropOnTimeout: false, timeoutMs: 15_000 })
    unmount()
    expect(unbind).toHaveBeenCalledOnce()
  })

  it.each([
    [{ ok: false, code: "unknown_op" }, "unsupported"],
    [{ ok: false, code: "backup_busy" }, "busy"],
    [{ ok: false, code: "restoring" }, "busy"],
    [{ ok: false, code: "save_failed", error: "private endpoint" }, "failed"],
    [{ ok: true }, "failed"],
    [{ ok: true, product_mode: "fofa" }, "failed"],
  ])("refuses unacknowledged or failed responses without exposing raw detail (%j)", async (response, reason) => {
    let switcher!: (mode: PhoneMode) => Promise<void>
    const link = target(vi.fn().mockResolvedValue(response))
    renderHook(() => usePcModeSwitch(link, fn => { switcher = fn; return () => undefined }))
    await expect(switcher("local")).rejects.toMatchObject({ reason, message: reason })
  })

  it("does not send a queued intent to a replacement link or accept the old link's late ACK", async () => {
    let resolve!: (value: unknown) => void, firstSwitch!: (mode: PhoneMode) => Promise<void>
    const first = target(vi.fn().mockReturnValue(new Promise(yes => { resolve = yes })))
    const nextRpc = vi.fn(), next = target(nextRpc)
    const bind = vi.fn(fn => { firstSwitch = fn; return () => undefined })
    const hook = renderHook(({ link }) => usePcModeSwitch(link, bind), { initialProps: { link: first } })
    const original = firstSwitch
    const pending = original("local")
    hook.rerender({ link: next })
    resolve({ ok: true, product_mode: "local" })
    await expect(pending).rejects.toMatchObject({ reason: "changed" })
    await expect(original("local")).rejects.toMatchObject({ reason: "changed" })
    expect(nextRpc).not.toHaveBeenCalled()
    expect(first.rpc).toHaveBeenCalledOnce()
  })

  it("sanitizes transport failures and refuses a connection lost before or after ACK", async () => {
    let switcher!: (mode: PhoneMode) => Promise<void>
    const rpc = vi.fn().mockRejectedValue(new Error("secret endpoint and token")), link = target(rpc)
    renderHook(() => usePcModeSwitch(link, fn => { switcher = fn; return () => undefined }))
    await expect(switcher("local")).rejects.toMatchObject({ reason: "failed", message: "failed" })
    link.alive = () => false
    await expect(switcher("local")).rejects.toMatchObject({ reason: "changed" })
    expect(rpc).toHaveBeenCalledOnce()
    link.alive = () => true
    rpc.mockImplementation(async () => { link.alive = () => false; return { ok: true, product_mode: "local" } })
    await expect(switcher("local")).rejects.toMatchObject({ reason: "changed" })
  })

  it("does not register an absent or disconnected PC", () => {
    const bind = vi.fn(), link = target(vi.fn())
    link.alive = () => false
    const hook = renderHook(({ link }) => usePcModeSwitch(link, bind), { initialProps: { link: null as RemoteLink | null } })
    hook.rerender({ link })
    expect(bind).not.toHaveBeenCalled()
  })
})
