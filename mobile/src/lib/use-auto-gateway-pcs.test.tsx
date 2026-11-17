import { act, renderHook, waitFor } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import { useAutoGatewayPCs } from "./use-auto-gateway-pcs"
import type { useGatewayAccount } from "./use-gateway-account"
import { loadActiveFingerprint, loadSavedLinks, saveLink, type SavedLink } from "./store"

it("provisions every online PC on a returning session without replacing a manual selection", async () => {
  localStorage.clear()
  const opened: string[] = []
  const observe = (event: Event) => opened.push((event as CustomEvent<string>).detail)
  window.addEventListener("zwai.gateway.connect", observe)
  let finish!: () => void
  const connectHost = vi.fn(async (id: string, options?: { select?: boolean }) => {
    if (id === "second") await new Promise<void>(resolve => { finish = resolve })
    const saved: SavedLink = { fingerprint: id, gatewayHostID: id, gatewayOrigin: "https://gateway.invalid", gatewayOwnerID: "owner", gatewayMobileID: "phone", hubURL: "https://hub.invalid", ticket: id, hostPub: id, sessionID: id }
    saveLink(saved, options?.select !== false)
    return saved
  })
  const account = { status: "authorized", session: { origin: "https://gateway.invalid", user: { id: "owner" }, device: { id: "phone" } }, bootstrap: {}, hosts: ["first", "second", "offline"].map(id => ({ id, online: id !== "offline", enabled: true })), connectHost } as unknown as ReturnType<typeof useGatewayAccount>
  const view = renderHook(() => useAutoGatewayPCs(account))
  try {
    await waitFor(() => expect(connectHost).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(opened).toEqual(["first"]))
    expect(connectHost).toHaveBeenCalledWith("first", { select: false })
    expect(connectHost).toHaveBeenCalledWith("second", { select: false })
    window.dispatchEvent(new CustomEvent("zwai.gateway.connect", { detail: "first" }))
    await act(async () => finish())
    expect(loadActiveFingerprint()).toBe("first")
    expect(loadSavedLinks()).toHaveLength(2)
    expect(opened).toEqual(["first", "first"])
  } finally { view.unmount(); window.removeEventListener("zwai.gateway.connect", observe) }
})
