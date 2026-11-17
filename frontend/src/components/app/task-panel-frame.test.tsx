import { act, cleanup, render, screen } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import catalog from "@/task-panels/catalog.json"
import type { TaskPanel } from "@/task-panels/types"
import { TaskPanelFrame } from "./task-panel-frame"

const definition = catalog[0]
const panel: TaskPanel = { id: "p", thread_id: "t", plugin_id: definition.id, plugin_version: definition.version, revision: 1, state: definition.initial_state, definition }
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

it("refuses an old definition before executing the current plugin", async () => {
  const fetcher = vi.fn()
  vi.stubGlobal("fetch", fetcher)
  render(<TaskPanelFrame panel={{ ...panel, plugin_version: "old" }} visible locale="en" />)
  expect(await screen.findByRole("alert")).toHaveTextContent("incompatible")
  expect(fetcher).not.toHaveBeenCalled()
  expect(screen.queryByTestId("task-panel-frame")).toBeNull()
})

it("reports a plugin startup deadline without reloading it automatically", async () => {
  vi.useFakeTimers()
  const fetcher = vi.fn().mockResolvedValue(new Response("<!doctype html><body>Broken plugin</body>"))
  vi.stubGlobal("fetch", fetcher)
  await act(async () => { render(<TaskPanelFrame panel={panel} visible locale="en" />) })
  expect(screen.getByTestId("task-panel-frame")).toBeInTheDocument()
  act(() => vi.advanceTimersByTime(10_001))
  expect(screen.getByRole("alert")).toHaveTextContent("did not become ready")
  act(() => vi.advanceTimersByTime(30_000))
  expect(fetcher).toHaveBeenCalledOnce()
})

it("reports a hung data callback and does not queue more updates", async () => {
  vi.useFakeTimers()
  vi.stubGlobal("MessageChannel", Channel)
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<!doctype html><body>Slow plugin</body>")))
  let view: ReturnType<typeof render>
  await act(async () => { view = render(<TaskPanelFrame panel={panel} visible locale="en" />) })
  const frame = screen.getByTestId("task-panel-frame") as HTMLIFrameElement
  act(() => {
    window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow, data: { type: "task-panel:ready", protocol_version: 1 } }))
    Channel.last.port1.onmessage?.({ data: { type: "ready", protocol_version: 1 } })
  })
  expect(Channel.last.port1.postMessage).toHaveBeenCalledWith(expect.objectContaining({ type: "snapshot" }))
  act(() => vi.advanceTimersByTime(10_001))
  expect(screen.getByRole("alert")).toHaveTextContent("did not apply")
  const calls = Channel.last.port1.postMessage.mock.calls.length
  view!.rerender(<TaskPanelFrame panel={{ ...panel, revision: 2 }} visible locale="en" />)
  expect(Channel.last.port1.postMessage).toHaveBeenCalledTimes(calls)
})
