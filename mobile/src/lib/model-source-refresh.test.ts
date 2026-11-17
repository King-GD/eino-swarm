import { act, renderHook, waitFor } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import type { SourceCapabilities } from "../../../frontend/src/lib/capabilities"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"
import { publishGatewayCapabilities } from "./gateway-capabilities"
import { setLocale } from "./i18n"
import type { RemoteLink } from "./link"
import { OpCatalog, OpRefreshCapabilities, type RemoteResponse } from "./rpc"
import { useModelCatalog } from "./use-model-catalog"

const source: SourceCapabilities = { authorized: true, available: true, configurable: true, capabilities: {
  conversation: { supported: true, available: true, configurable: true, endpoints: ["chat_completions"] },
} }
const rows = [{ provider_id: "gateway:native", model: "selected" }]
const response = (): RemoteResponse => ({ v: 1, id: "r", ok: true, models: rows,
  model_policy: { mode: "local", scope: "local", ready: true, configurable: true,
    capability_status: "ready", account_sources: { "gateway:native": source } } })
const link = (rpc: RemoteLink["rpc"]): RemoteLink => ({ rpc, path: "direct", alive: () => true, close: vi.fn(), announceDevice: vi.fn() })

it("refetches a mode change on the same PC after the older discovery has finished", async () => {
  let resolve!: (r: RemoteResponse) => void
  const old = new Promise<RemoteResponse>(yes => { resolve = yes })
  const rpc = vi.fn<RemoteLink["rpc"]>().mockReturnValueOnce(old).mockResolvedValue(response())
  const pc = link(rpc)
  const { result, rerender } = renderHook(({ scope }) => useModelCatalog(pc, scope), { initialProps: { scope: "gateway" } })
  rerender({ scope: "local" })
  expect(rpc).toHaveBeenCalledTimes(1)
  await act(async () => { resolve({ v: 1, id: "old", ok: true, models: [{ provider_id: "old", model: "old" }] }); await old })
  await waitFor(() => expect(result.current.models[0]?.provider_id).toBe("gateway:native"))
  expect(rpc).toHaveBeenCalledTimes(2)
  expect(rpc.mock.calls.every(([r, options]) => r.op === OpRefreshCapabilities && options?.dropOnTimeout === false)).toBe(true)
})

it("refreshes changed grants without clearing existing rows or refreshing on voice-only metadata", async () => {
  const session = { origin: "https://models.test", token: "private-fixture", user: { id: "u", username: "u" }, device: { id: "d", expires_at: "2099-01-01T00:00:00Z" } } satisfies GatewaySession
  const boot = { models: { source_id: "gateway:preset" }, capabilities: { version: 1, model_roles_version: 3,
    sources: { gateway: source } } } as GatewayBootstrap
  publishGatewayCapabilities(session, boot)
  let resolve!: (r: RemoteResponse) => void
  const pending = new Promise<RemoteResponse>(yes => { resolve = yes })
  const rpc = vi.fn<RemoteLink["rpc"]>().mockResolvedValueOnce(response()).mockReturnValueOnce(pending)
  const pc = link(rpc)
  const { result } = renderHook(() => useModelCatalog(pc, "local"))
  await waitFor(() => expect(result.current.models).toHaveLength(1))
  act(() => publishGatewayCapabilities(session, { ...boot, voice_models: { "gateway:preset": { speech: "voice" } } }))
  expect(rpc).toHaveBeenCalledTimes(1)
  act(() => publishGatewayCapabilities(session, { ...boot, capabilities: { ...boot.capabilities!, sources: { gateway: { ...source, authorized: false } } } }))
  expect(result.current.models).toHaveLength(1)
  await waitFor(() => expect(rpc).toHaveBeenCalledTimes(2))
  await act(async () => { resolve({ ...response(), models: [] }); await pending })
  await waitFor(() => expect(result.current.models).toEqual([]))
})

it("explains why an old PC cannot expose Gateway rows instead of silently showing only personal models", async () => {
  setLocale("en")
  const rpc = vi.fn<RemoteLink["rpc"]>(async r => r.op === OpCatalog
    ? { ...response(), models: [...rows, { provider_id: "personal", model: "local" }], model_policy: { mode: "local", scope: "local", ready: true, configurable: true } }
    : { v: 1, id: "r", ok: false, code: "unknown_op" })
  const pc = link(rpc)
  const { result } = renderHook(() => useModelCatalog(pc))
  await waitFor(() => expect(result.current.refreshError).toContain("does not report their permissions"))
  expect(result.current.models.map(row => row.provider_id)).toEqual(["personal"])
})
