import { act, cleanup, renderHook, waitFor } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import type { RemoteLink } from "./link"
import { TaskPanelReadError } from "./remote-task-panels"
import { useTaskPanels } from "./use-task-panels"
import type { TaskPanel } from "../../../frontend/src/task-panels/types"

const calls = vi.hoisted(() => ({ list: vi.fn(), read: vi.fn() }))
vi.mock("./remote-task-panels", async (original) => ({ ...await original<typeof import("./remote-task-panels")>(), listTaskPanels: calls.list, readTaskPanel: calls.read }))
const link = (rpc = vi.fn()) => ({ alive: () => true, rpc }) as unknown as RemoteLink
const panel = (id = "p", revision = 1, thread = "t"): TaskPanel => ({ id, thread_id: thread, plugin_id: "demo", plugin_version: "1", revision,
  state: { count: revision }, definition: { id: "demo", version: "1", name: "Demo", description: "Demo", entry: "task-panels/demo.html", schema: {} } })
const manifest = (value: TaskPanel) => ({ id: value.id, thread_id: value.thread_id, plugin_id: value.plugin_id, plugin_version: value.plugin_version, revision: value.revision })
afterEach(() => { cleanup(); calls.list.mockReset(); calls.read.mockReset(); vi.useRealTimers() })

it("discovers one initial panel, then keeps hidden data reads idle until opened", async () => {
  const pc = link(), first = panel(), second = panel("q")
  calls.list.mockResolvedValue([manifest(first), manifest(second)])
  calls.read.mockResolvedValue({ panel: first })
  const view = renderHook(({ visible }) => useTaskPanels(pc, "t", "pc", visible), { initialProps: { visible: false } })
  await waitFor(() => expect(view.result.current.panels).toHaveLength(1))
  expect(calls.read).toHaveBeenCalledTimes(1)
  calls.read.mockImplementation(async (_link, _thread, id) => ({ panel: id === "q" ? second : first }))
  view.rerender({ visible: true })
  await waitFor(() => expect(view.result.current.panels).toHaveLength(2))
})

it("applies a valid patch and repairs an invalid one with one complete snapshot", async () => {
  const pc = link(), first = panel()
  calls.list.mockResolvedValue([manifest(first)])
  calls.read.mockResolvedValueOnce({ panel: first })
  const view = renderHook(() => useTaskPanels(pc, "t", "pc", true))
  await waitFor(() => expect(view.result.current.panels[0]?.revision).toBe(1))
  calls.list.mockResolvedValue([manifest(panel("p", 2))])
  calls.read.mockResolvedValueOnce({ change: { thread_id: "t", panel_id: "p", revision: 2, base_revision: 1, cursor: 99, kind: "patch", operations: [{ op: "replace", path: "/count", value: 2 }] } })
  await act(async () => { await view.result.current.refresh() })
  expect(view.result.current.panels[0].state).toEqual({ count: 2 })
  expect(view.result.current.changes.p.base_revision).toBe(1)
  calls.list.mockResolvedValue([manifest(panel("p", 3))])
  calls.read.mockResolvedValueOnce({ change: { revision: 3, base_revision: 2, operations: [{ op: "remove", path: "/missing" }] } }).mockResolvedValueOnce({ panel: panel("p", 3) })
  await act(async () => { await view.result.current.refresh() })
  expect(view.result.current.panels[0].state).toEqual({ count: 3 })
  expect(view.result.current.changes.p).toBeUndefined()
  expect(first.state).toEqual({ count: 1 })
})

it("rejects late replies across PC and A to B to A conversation visits", async () => {
  const a = link(), b = link()
  let oldReply: ((value: unknown) => void) | undefined
  calls.list.mockImplementation(async (_link, thread) => [manifest(panel("p", 1, thread))])
  calls.read.mockReturnValueOnce(new Promise((resolve) => { oldReply = resolve }))
    .mockResolvedValueOnce({ panel: panel("p", 2, "b") }).mockResolvedValue({ panel: panel("p", 3, "a") })
  const view = renderHook(({ pc, thread, owner }) => useTaskPanels(pc, thread, owner, true), { initialProps: { pc: a, thread: "a", owner: "pc-a" } })
  await waitFor(() => expect(calls.read).toHaveBeenCalledTimes(1))
  const oldSignal = calls.read.mock.calls[0][4] as AbortSignal
  view.rerender({ pc: b, thread: "b", owner: "pc-b" })
  await waitFor(() => expect(view.result.current.panels[0]?.thread_id).toBe("b"))
  view.rerender({ pc: a, thread: "a", owner: "pc-a" })
  await waitFor(() => expect(view.result.current.panels[0]?.revision).toBe(3))
  await act(async () => { oldReply?.({ panel: panel("p", 1, "a") }); await Promise.resolve() })
  expect(oldSignal.aborted).toBe(true)
  expect(view.result.current.panels[0]).toMatchObject({ thread_id: "a", revision: 3 })
})

it("retains the painted projection while the same PC reconnects on a replacement link", async () => {
  const first = link(), replacement = link()
  let reply: ((value: unknown) => void) | undefined
  calls.list.mockResolvedValue([manifest(panel())])
  calls.read.mockResolvedValueOnce({ panel: panel() })
  const view = renderHook(({ pc }) => useTaskPanels(pc, "t", "pc", true), { initialProps: { pc: first as RemoteLink | undefined } })
  await waitFor(() => expect(view.result.current.panels).toHaveLength(1))
  const painted = view.result.current.panels[0]
  view.rerender({ pc: undefined })
  expect(view.result.current.panels[0]).toBe(painted)
  expect(view.result.current.connected).toBe(false)
  calls.list.mockResolvedValue([manifest(panel("p", 2))])
  calls.read.mockReturnValueOnce(new Promise((resolve) => { reply = resolve }))
  view.rerender({ pc: replacement })
  await waitFor(() => expect(calls.read).toHaveBeenCalledTimes(2))
  expect(view.result.current.panels[0]).toBe(painted)
  await act(async () => { reply?.({ panel: panel("p", 2) }) })
  expect(view.result.current.panels[0].revision).toBe(2)
})

it("finishes a frozen read before observing refreshed manifests instead of cancelling it", async () => {
  const pc = link()
  let complete: ((value: unknown) => void) | undefined
  calls.list.mockResolvedValue([manifest(panel())])
  calls.read.mockReturnValueOnce(new Promise((resolve) => { complete = resolve }))
  const view = renderHook(({ visible }) => useTaskPanels(pc, "t", "pc", visible), { initialProps: { visible: true } })
  await waitFor(() => expect(calls.read).toHaveBeenCalledTimes(1))
  const signal = calls.read.mock.calls[0][4] as AbortSignal
  view.rerender({ visible: false })
  calls.list.mockResolvedValue([manifest(panel("p", 2))])
  let refreshed: Promise<void>
  act(() => { refreshed = view.result.current.refresh() })
  expect(signal.aborted).toBe(false)
  expect(calls.read).toHaveBeenCalledTimes(1)
  await act(async () => { complete?.({ panel: panel() }); await refreshed! })
  expect(view.result.current.panels[0]?.revision).toBe(1)
  expect(calls.read).toHaveBeenCalledTimes(1)
})

it("keeps a failed first read reachable for explicit retry and removes absent panels", async () => {
  const pc = link()
  calls.list.mockResolvedValue([manifest(panel())])
  calls.read.mockRejectedValueOnce(new Error("read failed")).mockResolvedValue({ panel: panel() })
  const view = renderHook(() => useTaskPanels(pc, "t", "pc", false))
  await waitFor(() => expect(view.result.current.error).toBe("read failed"))
  expect(view.result.current).toMatchObject({ available: true, panels: [] })
  await act(async () => { await view.result.current.refresh() })
  expect(view.result.current.panels).toHaveLength(1)
  calls.list.mockResolvedValue([])
  await act(async () => { await view.result.current.refresh() })
  expect(view.result.current).toMatchObject({ available: false, panels: [] })
})

it("stops unsupported-host polls and does not read without a bound PC identity", async () => {
  vi.useFakeTimers()
  const pc = link()
  calls.list.mockRejectedValue(new TaskPanelReadError("unknown_op", "older host"))
  const view = renderHook(({ owner }) => useTaskPanels(pc, "t", owner, false), { initialProps: { owner: "" } })
  expect(calls.list).not.toHaveBeenCalled()
  await act(async () => { view.rerender({ owner: "pc" }) })
  expect(view.result.current.supported).toBe(false)
  await act(async () => { vi.advanceTimersByTime(5000) })
  expect(calls.list).toHaveBeenCalledTimes(1)
})

it("discards a frozen body from before close and keeps the sibling instance", async () => {
  const rpc = vi.fn().mockResolvedValue({ ok: true })
  const pc = link(rpc)
  const first = panel(), sibling = panel("q")
  calls.list.mockResolvedValue([manifest(first), manifest(sibling)])
  calls.read.mockResolvedValueOnce({ panel: first }).mockResolvedValueOnce({ panel: sibling })
  const view = renderHook(() => useTaskPanels(pc, "t", "pc", true))
  await waitFor(() => expect(view.result.current.panels).toEqual([first, sibling]))

  const newer = panel("p", 2)
  let sendOldBody!: (value: unknown) => void
  const body = new Promise((resolve) => { sendOldBody = resolve })
  calls.list.mockResolvedValueOnce([manifest(newer), manifest(sibling)]).mockResolvedValue([manifest(sibling)])
  calls.read.mockReturnValueOnce(body)
  let refresh!: Promise<void>
  act(() => { refresh = view.result.current.refresh() })
  await waitFor(() => expect(calls.read).toHaveBeenLastCalledWith(pc, "t", "p", 1, expect.any(AbortSignal)))

  await act(async () => { await view.result.current.closePanel("p") })
  expect(rpc).toHaveBeenCalledWith({ op: "task_panel_close", thread_id: "t", panel_id: "p" }, expect.anything())
  expect(view.result.current.panels).toEqual([sibling])

  // Hold the next manifest so it cannot conceal resurrection from the old body.
  let sendFreshManifest!: (items: ReturnType<typeof manifest>[]) => void
  calls.list.mockReturnValueOnce(new Promise((resolve) => { sendFreshManifest = resolve }))
  try {
    await act(async () => {
      sendOldBody({ change: { thread_id: "t", panel_id: "p", revision: 2, base_revision: 1, cursor: 2, kind: "patch",
        operations: [{ op: "replace", path: "/count", value: 2 }] } })
    })
    expect(view.result.current.panels).toEqual([sibling])
    expect(view.result.current.changes.p).toBeUndefined()
  } finally {
    await act(async () => { sendFreshManifest([manifest(sibling)]); await refresh })
  }
})

it("does not offer the entry when a pre-close manifest arrives after close", async () => {
  const pc = link(vi.fn().mockResolvedValue({ ok: true }))
  const first = panel()
  calls.list.mockResolvedValue([manifest(first)])
  calls.read.mockResolvedValue({ panel: first })
  const view = renderHook(() => useTaskPanels(pc, "t", "pc", true))
  await waitFor(() => expect(view.result.current.panels).toEqual([first]))

  type Manifest = ReturnType<typeof manifest>[]
  let sendOld!: (items: Manifest) => void
  let sendFresh!: (items: Manifest) => void
  calls.list.mockReturnValueOnce(new Promise<Manifest>((resolve) => { sendOld = resolve }))
    .mockReturnValueOnce(new Promise<Manifest>((resolve) => { sendFresh = resolve }))
    .mockResolvedValue([])
  let refresh!: Promise<void>
  act(() => { refresh = view.result.current.refresh() })
  await act(async () => { await view.result.current.closePanel(first.id) })
  expect(view.result.current.available).toBe(false)

  try {
    // Until the fresh manifest returns, only the close acknowledgement can
    // prevent the captured listing from bringing back the entry.
    await act(async () => { sendOld([manifest(first)]) })
    expect(view.result.current).toMatchObject({ available: false, panels: [] })
  } finally {
    await act(async () => { sendFresh([]); await refresh })
  }
})

it("does not apply an old PC close acknowledgement to a same-ID instance on the new PC", async () => {
  let acknowledge!: (response: { ok: boolean }) => void
  const old = link(vi.fn(() => new Promise((resolve) => { acknowledge = resolve })))
  const replacement = link()
  const first = panel(), fresh = panel("p", 2)
  calls.list.mockImplementation(async (pc) => [manifest(pc === old ? first : fresh)])
  calls.read.mockImplementation(async (pc) => ({ panel: pc === old ? first : fresh }))
  const view = renderHook(({ pc, owner }) => useTaskPanels(pc, "t", owner, true), { initialProps: { pc: old, owner: "old" } })
  await waitFor(() => expect(view.result.current.panels).toEqual([first]))

  let pending!: Promise<void>
  act(() => { pending = view.result.current.closePanel(first.id) })
  view.rerender({ pc: replacement, owner: "replacement" })
  await waitFor(() => expect(view.result.current.panels).toEqual([fresh]))
  await act(async () => { acknowledge({ ok: true }); await pending })
  expect(view.result.current.panels).toEqual([fresh])
})
