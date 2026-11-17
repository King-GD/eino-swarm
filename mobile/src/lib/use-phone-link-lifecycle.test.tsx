import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, expect, it, vi } from "vitest"
import { openLink, type RemoteLink } from "./link"
import { loadSavedLinks, saveActiveFingerprint, saveLink, type SavedLink } from "./store"
import { usePhoneLinkLifecycle } from "./use-phone-link-lifecycle"

vi.mock("./link", () => ({ openLink: vi.fn() }))

const pc = (fingerprint: string): SavedLink => ({ fingerprint, label: fingerprint,
  hubURL: "https://hub.invalid", ticket: "ticket-" + fingerprint, hostPub: fingerprint, sessionID: fingerprint })
const transport = () => {
  let alive = true
  return { alive: () => alive, close: vi.fn(() => { alive = false }), path: "relay",
    rpc: vi.fn(), announceDevice: vi.fn() } as unknown as RemoteLink
}
function fixture() {
  const first = pc("first"), second = pc("second")
  saveLink(first); saveLink(second); saveActiveFingerprint(first.fingerprint)
  return { first, second, options: {
    link: { current: null as RemoteLink | null }, authority: { current: null as SavedLink | null },
    generation: { current: 0 }, unlinking: { current: false }, retryTimer: { current: 0 },
    recoverRef: { current: async (_force?: boolean) => undefined }, allowsLink: (_saved: SavedLink) => true,
    install: vi.fn(), attach: vi.fn(), restore: vi.fn(async () => undefined), firstList: vi.fn(async () => undefined),
    busy: vi.fn(), reconnecting: vi.fn(), error: vi.fn(), message: () => "offline",
  } }
}
beforeEach(() => { localStorage.clear(); vi.mocked(openLink).mockReset() })

it("keeps both PCs connected and reuses the original socket when switching back", async () => {
  const { first, second, options } = fixture(), a = transport(), b = transport()
  vi.mocked(openLink).mockResolvedValueOnce(a).mockResolvedValueOnce(b)
  const view = renderHook(() => usePhoneLinkLifecycle(options))
  await waitFor(() => expect(options.link.current).toBe(a))
  const select = async (saved: SavedLink) => {
    await act(async () => {
      options.generation.current++
      view.result.current.detach()
      saveActiveFingerprint(saved.fingerprint)
      await view.result.current.boot()
    })
  }
  await select(second)
  expect(options.link.current).toBe(b)
  expect(a.close).not.toHaveBeenCalled()
  await select(first)
  expect(options.link.current).toBe(a)
  expect(openLink).toHaveBeenCalledTimes(2)
  expect(b.close).not.toHaveBeenCalled()
  view.unmount()
  expect(a.close).toHaveBeenCalledOnce()
  expect(b.close).toHaveBeenCalledOnce()
})

it("closes an inactive PC immediately when its saved credential changes", async () => {
  const { first, second, options } = fixture(), a = transport(), b = transport(), replacement = transport()
  vi.mocked(openLink).mockResolvedValueOnce(a).mockResolvedValueOnce(b).mockResolvedValueOnce(replacement)
  const view = renderHook(() => usePhoneLinkLifecycle(options))
  await waitFor(() => expect(options.link.current).toBe(a))
  await act(async () => {
    options.generation.current++; view.result.current.detach()
    saveActiveFingerprint(second.fingerprint); await view.result.current.boot()
  })
  saveLink({ ...first, ticket: "rotated" }); saveActiveFingerprint(second.fingerprint)
  view.rerender()
  expect(a.close).toHaveBeenCalledOnce()
  await act(async () => {
    options.generation.current++; view.result.current.detach()
    saveActiveFingerprint(first.fingerprint); await view.result.current.boot()
  })
  expect(options.link.current).toBe(replacement)
  expect(openLink).toHaveBeenLastCalledWith(expect.objectContaining({ ticket: "rotated" }))
  view.unmount()
})

it("releases retained PCs after account access is revoked", async () => {
  const { second, options } = fixture(), a = transport(), b = transport()
  vi.mocked(openLink).mockResolvedValueOnce(a).mockResolvedValueOnce(b)
  const view = renderHook(() => usePhoneLinkLifecycle(options))
  await waitFor(() => expect(options.link.current).toBe(a))
  await act(async () => {
    options.generation.current++; view.result.current.detach()
    saveActiveFingerprint(second.fingerprint); await view.result.current.boot()
  })
  options.allowsLink = saved => saved.fingerprint === second.fingerprint
  view.rerender()
  expect(a.close).toHaveBeenCalledOnce()
  expect(loadSavedLinks()).toHaveLength(2)
  expect(b.close).not.toHaveBeenCalled()
  view.unmount()
})
