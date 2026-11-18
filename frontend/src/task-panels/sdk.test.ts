import { expect, it, vi } from "vitest"
import { createTaskPanel } from "./sdk"
import type { HostContext } from "./types"

class Port {
  onmessage?: (event: { data: unknown }) => void
  postMessage = vi.fn()
  start = vi.fn()
  close = vi.fn()
  deliver(data: unknown) { this.onmessage?.({ data }) }
}
const context: HostContext = { panel_id: "p", thread_id: "t", revision: 1, theme: "light", locale: "en", width: 400, height: 500, visible: true }
function connect(port: Port) {
  window.dispatchEvent(new MessageEvent("message", { source: window.parent, data: { type: "task-panel:connect", protocol_version: 1 }, ports: [port as unknown as MessagePort] }))
}

it("updates an existing plugin through snapshot and patches in delivery order", async () => {
  const snapshot = vi.fn(), patch = vi.fn(), dispose = vi.fn(), port = new Port()
  const stop = createTaskPanel({ onSnapshot: snapshot, onPatch: patch, onDispose: dispose })
  connect(port)
  port.deliver({ type: "snapshot", state: { count: 1 }, context })
  port.deliver({ type: "patch", base_revision: 1, operations: [{ op: "replace", path: "/count", value: 2 }], context: { ...context, revision: 2 } })
  await vi.waitFor(() => expect(patch).toHaveBeenCalledOnce())
  expect(snapshot).toHaveBeenCalledWith({ count: 1 }, context)
  expect(patch.mock.calls[0][1]).toEqual({ count: 2 })
  expect(port.postMessage).toHaveBeenCalledWith({ type: "applied", revision: 2 })
  stop(); expect(dispose).toHaveBeenCalledOnce(); expect(port.close).toHaveBeenCalledOnce()
})

it("supports snapshot-only plugins and requests a snapshot after a revision gap", async () => {
  const snapshot = vi.fn(), port = new Port()
  const stop = createTaskPanel({ onSnapshot: snapshot })
  connect(port)
  port.deliver({ type: "snapshot", state: { count: 1 }, context })
  port.deliver({ type: "patch", base_revision: 1, operations: [{ op: "replace", path: "/count", value: 3 }], context: { ...context, revision: 2 } })
  port.deliver({ type: "patch", base_revision: 9, operations: [], context: { ...context, revision: 10 } })
  await vi.waitFor(() => expect(port.postMessage).toHaveBeenCalledWith({ type: "resync" }))
  expect(snapshot).toHaveBeenLastCalledWith({ count: 3 }, { ...context, revision: 2 })
  stop()
})

it("retains the last applied state when a callback fails and tears down", async () => {
  const port = new Port(), stop = createTaskPanel({ onSnapshot: () => { throw new Error("draw failed") } })
  connect(port)
  port.deliver({ type: "snapshot", state: {}, context })
  await vi.waitFor(() => expect(port.postMessage).toHaveBeenCalledWith({ type: "error", message: "draw failed" }))
  port.deliver({ type: "dispose" })
  await vi.waitFor(() => expect(port.close).toHaveBeenCalledOnce())
  stop()
})

it("keeps plugin mutations and visibility context out of its canonical patch state", async () => {
  const port = new Port(), values: number[] = []
  const stop = createTaskPanel<{ count: number }>({ onSnapshot: (state) => { values.push(state.count); state.count = 99 } })
  connect(port)
  port.deliver({ type: "snapshot", state: { count: 1 }, context })
  port.deliver({ type: "context", context: { ...context, revision: 90, visible: false } })
  port.deliver({ type: "patch", base_revision: 1, operations: [{ op: "test", path: "/count", value: 1 }, { op: "replace", path: "/count", value: 2 }], context: { ...context, revision: 2 } })
  await vi.waitFor(() => expect(values).toEqual([1, 2]))
  expect(port.postMessage).not.toHaveBeenCalledWith({ type: "resync" })
  stop()
})
