import { applyJsonPatch } from "./json-patch"
import type { HostContext, JsonPatchOperation } from "./types"

export interface PanelCallbacks<T = unknown> {
  onSnapshot: (state: T, context: HostContext) => void | Promise<void>
  onPatch?: (operations: JsonPatchOperation[], nextState: T, context: HostContext) => void | Promise<void>
  onHostContextChanged?: (context: HostContext) => void | Promise<void>
  onDispose?: () => void
}

function copy<T>(value: T): T { return JSON.parse(JSON.stringify(value)) as T }

/** Plugins receive only their instance's data over a dedicated transferred port. */
export function createTaskPanel<T>(callbacks: PanelCallbacks<T>): () => void {
  let port: MessagePort | undefined
  let state: unknown = null
  let context: HostContext | undefined
  let disposed = false
  let sequence = Promise.resolve()
  const connect = (event: MessageEvent) => {
    if (disposed || port || event.source !== window.parent || event.data?.type !== "task-panel:connect" || event.data.protocol_version !== 1 || event.ports.length !== 1) return
    port = event.ports[0]
    window.removeEventListener("message", connect)
    port.onmessage = (message) => {
      sequence = sequence.then(async () => {
        if (disposed || !port) return
        const data = message.data
        if (data.type === "dispose") { dispose(); return }
        if (data.type === "context") {
          const next = data.context as HostContext
          // Appearance/visibility cannot acknowledge data that was not applied.
          context = context ? { ...next, revision: context.revision } : next
          await callbacks.onHostContextChanged?.({ ...context })
          return
        }
        if (data.type !== "snapshot" && data.type !== "patch") return
        const nextContext = data.context as HostContext
        if (!nextContext || (context && (nextContext.panel_id !== context.panel_id || nextContext.thread_id !== context.thread_id))) throw new Error("Panel identity changed")
        if (context && nextContext.revision < context.revision) return
        if (data.type === "patch") {
          if (!context || data.base_revision !== context.revision) { port.postMessage({ type: "resync" }); return }
          const next = applyJsonPatch(state, data.operations as JsonPatchOperation[])
          if (callbacks.onPatch) await callbacks.onPatch(copy(data.operations), copy(next) as T, { ...nextContext })
          else await callbacks.onSnapshot(copy(next) as T, { ...nextContext })
          state = next
        } else {
          await callbacks.onSnapshot(copy(data.state) as T, { ...nextContext })
          state = data.state
        }
        context = nextContext
        port.postMessage({ type: "applied", revision: context.revision })
      }).catch((error: unknown) => {
        port?.postMessage({ type: "error", message: error instanceof Error ? error.message : "Plugin update failed" })
        port?.postMessage({ type: "resync" })
      })
    }
    port.start()
    port.postMessage({ type: "ready", protocol_version: 1, capabilities: { patch: Boolean(callbacks.onPatch) } })
  }
  function dispose() {
    if (disposed) return
    disposed = true
    window.removeEventListener("message", connect)
    port?.close()
    callbacks.onDispose?.()
  }
  window.addEventListener("message", connect)
  window.parent.postMessage({ type: "task-panel:ready", protocol_version: 1 }, "*")
  return dispose
}
