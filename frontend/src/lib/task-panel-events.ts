import type { PanelChange, TaskPanel } from "@/task-panels/types"

export type TaskPanelEvent =
  | { kind: "ready"; panels: TaskPanel[]; cursor: number }
  | { kind: "update"; change: PanelChange }
  | { kind: "connection"; connected: boolean }
  | { kind: "error"; error: string }

const subscribers = new Map<string, Set<(event: TaskPanelEvent) => void>>()
const connected = new Set<string>()

/** Panel revisions travel beside chat events but never enter transcript replay. */
export function publishTaskPanelEvent(threadId: string, event: TaskPanelEvent) {
  if (event.kind === "connection") {
    if (event.connected) connected.add(threadId)
    else connected.delete(threadId)
  }
  for (const subscriber of subscribers.get(threadId) ?? []) subscriber(event)
}

export function subscribeTaskPanelEvents(threadId: string, listener: (event: TaskPanelEvent) => void): () => void {
  const group = subscribers.get(threadId) ?? new Set()
  group.add(listener)
  subscribers.set(threadId, group)
  listener({ kind: "connection", connected: connected.has(threadId) })
  return () => {
    group.delete(listener)
    if (!group.size) subscribers.delete(threadId)
  }
}
