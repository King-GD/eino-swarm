import { afterEach, expect, it, vi } from "vitest"
import { useTaskPanels } from "./task-panels"
import { publishTaskPanelEvent } from "@/lib/task-panel-events"
import type { PanelChange, TaskPanel } from "@/task-panels/types"

function panel(thread: string, revision = 1): TaskPanel {
  return { id: `panel-${thread}`, thread_id: thread, plugin_id: "demo", plugin_version: "1", revision, state: { count: revision }, definition: { id: "demo", version: "1", name: "Demo", description: "Demo", entry: "task-panels/demo.html", schema: {} } }
}
afterEach(() => { vi.unstubAllGlobals() })

it("keeps the same instance on incremental updates and ignores duplicate deliveries", async () => {
  const streamConstructor = vi.fn(); vi.stubGlobal("EventSource", streamConstructor)
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ panels: [panel("incremental")], cursor: 1 }))))
  const stop = useTaskPanels.getState().connect("incremental")
  await vi.waitFor(() => expect(useTaskPanels.getState().panels).toHaveLength(1))
  const update: PanelChange = { cursor: 2, thread_id: "incremental", panel_id: "panel-incremental", revision: 2, base_revision: 1, kind: "patch", operations: [{ op: "replace", path: "/count", value: 2 }] }
  publishTaskPanelEvent("incremental", { kind: "update", change: update }); publishTaskPanelEvent("incremental", { kind: "update", change: update })
  expect(useTaskPanels.getState().panels[0].state).toEqual({ count: 2 })
  expect(useTaskPanels.getState().automaticPanelId).toBe("panel-incremental")
  publishTaskPanelEvent("incremental", { kind: "update", change: { ...update, cursor: 3, revision: 3, kind: "snapshot", state: { count: 2 }, source_status: "interrupted" } })
  expect(useTaskPanels.getState().panels[0].source_status).toBe("interrupted")
  publishTaskPanelEvent("incremental", { kind: "update", change: { ...update, cursor: 4, revision: 4, kind: "snapshot", state: { count: 4 } } })
  expect(useTaskPanels.getState().panels[0].source_status).toBeUndefined()
  stop(); expect(streamConstructor).not.toHaveBeenCalled()
  publishTaskPanelEvent("incremental", { kind: "ready", panels: [panel("incremental")], cursor: 99 })
  expect(useTaskPanels.getState()).toMatchObject({ threadId: undefined, panels: [], changes: {}, automaticPanelId: undefined, connected: false })
})

it("rejects late loads and events from a previously selected conversation", async () => {
  const streamConstructor = vi.fn(); vi.stubGlobal("EventSource", streamConstructor)
  let oldReply: ((response: Response) => void) | undefined
  const first = new Promise<Response>((resolve) => { oldReply = resolve })
  vi.stubGlobal("fetch", vi.fn().mockReturnValueOnce(first).mockResolvedValueOnce(new Response(JSON.stringify({ panels: [panel("new")], cursor: 2 }))))
  const oldStop = useTaskPanels.getState().connect("old")
  const stop = useTaskPanels.getState().connect("new")
  oldReply?.(new Response(JSON.stringify({ panels: [panel("old")], cursor: 99 })))
  publishTaskPanelEvent("old", { kind: "ready", panels: [panel("old")], cursor: 100 })
  await vi.waitFor(() => expect(useTaskPanels.getState().panels[0]?.thread_id).toBe("new"))
  oldStop(); expect(useTaskPanels.getState().threadId).toBe("new")
  stop(); expect(streamConstructor).not.toHaveBeenCalled()
})
