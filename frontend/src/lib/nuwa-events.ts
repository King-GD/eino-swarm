import type { WorkVersion } from "./nuwa"

type WorkEvent =
  | { kind: "revision"; version: WorkVersion }
  | { kind: "connection"; connected: boolean }

const subscribers = new Map<string, Set<(event: WorkEvent) => void>>()
const connected = new Set<string>()

/** Work invalidations share the browser's conversation connection. */
export function publishWorkEvent(threadId: string, event: WorkEvent) {
  if (event.kind === "connection") {
    if (event.connected) connected.add(threadId)
    else connected.delete(threadId)
  }
  for (const listener of subscribers.get(threadId) ?? []) listener(event)
}

export function subscribeWorkEvents(threadId: string, listener: (event: WorkEvent) => void): () => void {
  const group = subscribers.get(threadId) ?? new Set()
  group.add(listener)
  subscribers.set(threadId, group)
  listener({ kind: "connection", connected: connected.has(threadId) })
  return () => {
    group.delete(listener)
    if (!group.size) subscribers.delete(threadId)
  }
}
