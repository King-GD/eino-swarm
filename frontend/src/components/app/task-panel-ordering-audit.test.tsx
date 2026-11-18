import { act, cleanup, render, screen } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import catalog from "@/task-panels/catalog.json"
import type { TaskPanel } from "@/task-panels/types"
import { TaskPanelFrame } from "./task-panel-frame"

const definition = catalog[0]
const panel: TaskPanel = { id: "audit-hidden", thread_id: "audit", plugin_id: definition.id, plugin_version: definition.version, revision: 1, state: definition.initial_state, definition }
class Port {
  onmessage?: (event: { data: unknown }) => void
  postMessage = vi.fn()
  start = vi.fn()
  close = vi.fn()
}
class Channel {
  static last: Channel
  port1 = new Port()
  port2 = new Port()
  constructor() { Channel.last = this }
}
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals() })

// A callback may reject after the inspector is hidden. Its outstanding data
// still needs a full retry when shown, even though no new revision arrived.
it("resends the unapplied snapshot after a hidden panel requests resynchronization", async () => {
  vi.useFakeTimers()
  vi.stubGlobal("MessageChannel", Channel)
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<!doctype html><body>Audit plugin</body>")))
  let view: ReturnType<typeof render>
  await act(async () => { view = render(<TaskPanelFrame panel={panel} visible locale="en" />) })
  const frame = screen.getByTestId("task-panel-frame") as HTMLIFrameElement
  act(() => {
    window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow, data: { type: "task-panel:ready", protocol_version: 1 } }))
    Channel.last.port1.onmessage?.({ data: { type: "ready", protocol_version: 1 } })
  })
  const sentSnapshots = () => Channel.last.port1.postMessage.mock.calls.filter(([message]) => message.type === "snapshot")
  expect(sentSnapshots()).toHaveLength(1)
  view!.rerender(<TaskPanelFrame panel={panel} visible={false} locale="en" />)
  act(() => {
    // These are the SDK's exact two messages for a rejected render callback.
    Channel.last.port1.onmessage?.({ data: { type: "error", message: "temporary draw failure" } })
    Channel.last.port1.onmessage?.({ data: { type: "resync" } })
  })
  expect(sentSnapshots()).toHaveLength(1)
  view!.rerender(<TaskPanelFrame panel={panel} visible locale="en" />)
  expect(screen.getByTestId("task-panel-frame")).toBe(frame)
  expect(sentSnapshots()).toHaveLength(2)
  act(() => vi.advanceTimersByTime(10_001))
  expect(screen.getByRole("alert")).toHaveTextContent("did not apply")
})

it("waits for the actual applied acknowledgement before delivering the next patch", async () => {
  vi.stubGlobal("MessageChannel", Channel)
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<!doctype html><body>Audit plugin</body>")))
  let view: ReturnType<typeof render>
  await act(async () => { view = render(<TaskPanelFrame panel={panel} visible locale="en" />) })
  const frame = screen.getByTestId("task-panel-frame") as HTMLIFrameElement
  act(() => {
    window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow, data: { type: "task-panel:ready", protocol_version: 1 } }))
    Channel.last.port1.onmessage?.({ data: { type: "ready", protocol_version: 1 } })
  })
  const change = { panel_id: panel.id, thread_id: panel.thread_id, cursor: 2, revision: 2, base_revision: 1, kind: "patch" as const, operations: [] }
  view!.rerender(<TaskPanelFrame panel={{ ...panel, revision: 2 }} change={change} visible locale="en" />)
  expect(Channel.last.port1.postMessage.mock.calls.filter(([message]) => message.type === "patch")).toHaveLength(0)
  act(() => { Channel.last.port1.onmessage?.({ data: { type: "applied", revision: 99 } }) })
  expect(Channel.last.port1.postMessage.mock.calls.filter(([message]) => message.type === "patch")).toHaveLength(0)
  act(() => { Channel.last.port1.onmessage?.({ data: { type: "applied", revision: 1 } }) })
  expect(Channel.last.port1.postMessage).toHaveBeenLastCalledWith(expect.objectContaining({ type: "patch", base_revision: 1 }))
})
