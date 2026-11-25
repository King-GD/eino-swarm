import { afterEach, expect, it, vi } from "vitest"
import { useTaskPanels } from "./task-panels"
import { publishTaskPanelEvent } from "@/lib/task-panel-events"
import type { TaskPanel } from "@/task-panels/types"

const thread = "audit-snapshot-cursor"
function panel(id: string, revision = 1): TaskPanel {
  return { id, thread_id: thread, plugin_id: "demo", plugin_version: "1", revision, state: { count: revision }, definition: { id: "demo", version: "1", name: "Demo", description: "Demo", entry: "task-panels/demo.html", schema: {} } }
}
let stop: (() => void) | undefined
afterEach(() => { stop?.(); stop = undefined; vi.unstubAllGlobals() })

// A new instance needs its definition from GET, while an existing instance
// can keep receiving updates during the round trip. Those updates must not
// cause the new instance's only snapshot to be silently thrown away.
it("discovers a new panel when another panel advances while its GET is in flight", async () => {
  let reply: ((response: Response) => void) | undefined
  const pending = new Promise<Response>((resolve) => { reply = resolve })
  const fetcher = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ panels: [panel("known")], cursor: 1 })))
    .mockReturnValueOnce(pending)
    .mockResolvedValue(new Response(JSON.stringify({ panels: [panel("known", 2), panel("new")], cursor: 3 })))
  vi.stubGlobal("fetch", fetcher)
  stop = useTaskPanels.getState().connect(thread)
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toHaveLength(1))
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "new", cursor: 2, revision: 1, kind: "snapshot", state: { count: 1 } } })
  expect(fetcher).toHaveBeenCalledTimes(2)
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "known", cursor: 3, revision: 2, base_revision: 1, kind: "patch", operations: [{ op: "replace", path: "/count", value: 2 }] } })
  reply?.(new Response(JSON.stringify({ panels: [panel("known"), panel("new")], cursor: 2 })))
  await vi.waitFor(() => expect(useTaskPanels.getState().panels.map(({ id }) => id)).toContain("new"))
  expect(useTaskPanels.getState().panels.find(({ id }) => id === "known")?.revision).toBe(2)
})

it("does not resurrect a panel deleted after a GET captured its state", async () => {
  let reply: ((response: Response) => void) | undefined
  const pending = new Promise<Response>((resolve) => { reply = resolve })
  vi.stubGlobal("fetch", vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ panels: [panel("known"), panel("removed")], cursor: 2 })))
    .mockReturnValueOnce(pending))
  stop = useTaskPanels.getState().connect(thread)
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toHaveLength(2))
  const refresh = useTaskPanels.getState().refresh()
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "removed", cursor: 3, revision: 2, kind: "deleted" } })
  reply?.(new Response(JSON.stringify({ panels: [panel("known"), panel("removed")], cursor: 2 })))
  await refresh
  expect(useTaskPanels.getState().panels.map(({ id }) => id)).toEqual(["known"])
})

it("coalesces missing-instance reads and fetches the observed P2 after a snapshot only contains P1", async () => {
  let reply: ((response: Response) => void) | undefined
  const pending = new Promise<Response>((resolve) => { reply = resolve })
  const fetcher = vi.fn().mockReturnValueOnce(pending)
    .mockResolvedValueOnce(new Response(JSON.stringify({ panels: [panel("new", 2)], cursor: 3 })))
  vi.stubGlobal("fetch", fetcher)
  stop = useTaskPanels.getState().connect(thread)
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "new", cursor: 2, revision: 1, kind: "snapshot", state: { count: 1 } } })
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "new", cursor: 3, revision: 2, base_revision: 1, kind: "patch", operations: [{ op: "replace", path: "/count", value: 2 }] } })
  expect(fetcher).toHaveBeenCalledOnce()
  reply?.(new Response(JSON.stringify({ panels: [panel("new")], cursor: 2 })))
  await vi.waitFor(() => expect(useTaskPanels.getState().panels[0]?.revision).toBe(2))
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(useTaskPanels.getState().panels[0].state).toEqual({ count: 2 })
})

it("keeps a newer complete reconnect snapshot when an older GET arrives later", async () => {
  let reply: ((response: Response) => void) | undefined
  vi.stubGlobal("fetch", vi.fn().mockReturnValueOnce(new Promise<Response>((resolve) => { reply = resolve })))
  stop = useTaskPanels.getState().connect(thread)
  publishTaskPanelEvent(thread, { kind: "ready", panels: [panel("known", 2), panel("new")], cursor: 3 })
  reply?.(new Response(JSON.stringify({ panels: [panel("known")], cursor: 1 })))
  await useTaskPanels.getState().refresh()
  expect(useTaskPanels.getState().panels.map(({ id }) => id)).toEqual(["known", "new"])
  expect(useTaskPanels.getState().panels[0].revision).toBe(2)
})

it("does not show removed content while a reopened panel waits for its current snapshot", async () => {
  let oldReply: ((response: Response) => void) | undefined
  let freshReply: ((response: Response) => void) | undefined
  const old = new Promise<Response>((resolve) => { oldReply = resolve })
  const fresh = new Promise<Response>((resolve) => { freshReply = resolve })
  const fetcher = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ panels: [panel("reopened")], cursor: 1 })))
    .mockReturnValueOnce(old).mockReturnValueOnce(fresh)
  vi.stubGlobal("fetch", fetcher)
  stop = useTaskPanels.getState().connect(thread)
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toHaveLength(1))
  const refresh = useTaskPanels.getState().refresh()
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "reopened", cursor: 2, revision: 2, kind: "deleted" } })
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "reopened", cursor: 3, revision: 3, kind: "snapshot", state: { count: 3 } } })
  oldReply?.(new Response(JSON.stringify({ panels: [panel("reopened")], cursor: 1 })))
  try {
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(3))
    expect(useTaskPanels.getState().panels).toEqual([])
  } finally {
    freshReply?.(new Response(JSON.stringify({ panels: [panel("reopened", 3)], cursor: 3 })))
    await refresh
  }
  expect(useTaskPanels.getState().panels[0].state).toEqual({ count: 3 })
})

it("settles a missing panel from an authoritative reconnect without repeating GET forever", async () => {
  let oldReply: ((response: Response) => void) | undefined
  let fallbackReply: ((response: Response) => void) | undefined
  const old = new Promise<Response>((resolve) => { oldReply = resolve })
  // A bounded pending fallback makes the faulty extra GET observable without
  // letting an infinite microtask loop exhaust the test runner.
  const fallback = new Promise<Response>((resolve) => { fallbackReply = resolve })
  const fetcher = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ panels: [panel("gone")], cursor: 1 })))
    .mockReturnValueOnce(old).mockReturnValue(fallback)
  vi.stubGlobal("fetch", fetcher)
  stop = useTaskPanels.getState().connect(thread)
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toHaveLength(1))
  publishTaskPanelEvent(thread, { kind: "update", change: { thread_id: thread, panel_id: "gone", cursor: 2, revision: 2, kind: "snapshot", state: { count: 2 } } })
  const refresh = useTaskPanels.getState().refresh()
  publishTaskPanelEvent(thread, { kind: "connection", connected: false })
  publishTaskPanelEvent(thread, { kind: "ready", panels: [], cursor: 3 })
  oldReply?.(new Response(JSON.stringify({ panels: [panel("gone", 2)], cursor: 2 })))
  try {
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(useTaskPanels.getState().panels).toEqual([])
  } finally {
    stop?.(); stop = undefined
    fallbackReply?.(new Response(JSON.stringify({ panels: [], cursor: 3 })))
    await refresh
  }
})

it("keeps a closed instance hidden when its pre-close snapshot arrives after the acknowledgement", async () => {
  const closing = panel("closing")
  const sibling = panel("sibling")
  let reply!: (response: Response) => void
  const captured = new Promise<Response>((resolve) => { reply = resolve })
  const snapshot = (panels: TaskPanel[], cursor: number) => Response.json({ panels, cursor })
  const fetcher = vi.fn()
    .mockResolvedValueOnce(snapshot([closing, sibling], 2))
    .mockReturnValueOnce(captured)
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal("fetch", fetcher)
  stop = useTaskPanels.getState().connect(thread)
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toHaveLength(2))

  const refresh = useTaskPanels.getState().refresh()
  await useTaskPanels.getState().closePanel(closing.id)
  expect(fetcher).toHaveBeenLastCalledWith(`/api/threads/${thread}/panels/${closing.id}`, { method: "DELETE" })
  expect(useTaskPanels.getState().panels).toEqual([sibling])

  // No deletion has arrived over SSE. The local close acknowledgement owns
  // the removal even while the earlier response is still in flight.
  reply(snapshot([closing, sibling], 2))
  await refresh
  expect(useTaskPanels.getState().panels).toEqual([sibling])
})

it("does not apply a close acknowledgement from a previous conversation visit", async () => {
  const id = "same-instance"
  let acknowledge!: (response: Response) => void
  const pending = new Promise<Response>((resolve) => { acknowledge = resolve })
  const nextThread = "different-panel-conversation"
  const nextPanel = { ...panel(id), thread_id: nextThread }
  vi.stubGlobal("fetch", vi.fn()
    .mockResolvedValueOnce(Response.json({ panels: [panel(id)], cursor: 1 }))
    .mockReturnValueOnce(pending)
    .mockResolvedValueOnce(Response.json({ panels: [nextPanel], cursor: 2 })))
  stop = useTaskPanels.getState().connect(thread)
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toHaveLength(1))

  const closing = useTaskPanels.getState().closePanel(id)
  stop()
  stop = useTaskPanels.getState().connect(nextThread)
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toEqual([nextPanel]))
  acknowledge(new Response(null, { status: 204 }))
  await closing
  expect(useTaskPanels.getState().panels).toEqual([nextPanel])
})
