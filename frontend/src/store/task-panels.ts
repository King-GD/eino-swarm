import { useEffect } from "react"
import { create } from "zustand"
import { useShallow } from "zustand/react/shallow"
import { applyJsonPatch } from "@/task-panels/json-patch"
import { subscribeTaskPanelEvents } from "@/lib/task-panel-events"
import type { PanelChange, TaskPanel } from "@/task-panels/types"

interface TaskPanelState {
  threadId?: string
  panels: TaskPanel[]
  changes: Record<string, PanelChange>
  connected: boolean
  error?: string
  automaticPanelId?: string
  closingPanelId?: string
  closeError?: boolean
  closePanel: (id: string) => Promise<void>
  connect: (threadId: string) => () => void
  refresh: () => Promise<void>
}

let visit = 0
let stopStream: (() => void) | undefined
let cursor = 0
let snapshotCursor = 0
let loading: { token: number; promise: Promise<void> } | undefined
let closed = new Set<string>()
let observed = new Map<string, { cursor: number; revision: number; deleted: boolean }>()
const seen = new Map<string, Set<string>>()

export const useTaskPanels = create<TaskPanelState>((set, get) => {
  function accept(panels: TaskPanel[], at: number) {
    const threadId = get().threadId
    if (!threadId || at < snapshotCursor) return
    snapshotCursor = at
    cursor = Math.max(cursor, at)
    const valid = panels.filter((panel) => panel.thread_id === threadId && !closed.has(panel.id))
    const previous = new Map(get().panels.map((panel) => [panel.id, panel]))
    const accepted = valid.filter((panel) => {
      const last = observed.get(panel.id)
      if (last?.deleted && last.cursor > at) return false
      return previous.has(panel.id) || !last || panel.revision >= last.revision
    }).map((panel) => {
      const old = previous.get(panel.id)
      return old && old.revision > panel.revision ? old : panel
    })
    // This snapshot may predate another panel's live update. Keep that newer
    // instance, while admitting unrelated new instances from the same read.
    for (const old of previous.values()) {
      const last = observed.get(old.id)
      if (!accepted.some((panel) => panel.id === old.id) && last && !last.deleted && last.cursor > at) accepted.push(old)
    }
    // An atomic snapshot settles every observation it covers, including an
    // instance absent after a deletion whose notification was disconnected.
    for (const [id, last] of observed) if (last.cursor <= at) observed.delete(id)
    const known = seen.get(threadId) ?? new Set<string>()
    const fresh = accepted.find((panel) => !known.has(panel.id))
    for (const panel of accepted) known.add(panel.id)
    seen.set(threadId, known)
    set({ panels: accepted, automaticPanelId: fresh?.id ?? get().automaticPanelId })
  }
  function incomplete() {
    const panels = new Map(get().panels.map((panel) => [panel.id, panel]))
    return [...observed].some(([id, last]) => !last.deleted && (panels.get(id)?.revision ?? -1) < last.revision)
  }
  return {
    panels: [], changes: {}, connected: false,
    connect: (threadId) => {
      stopStream?.()
      const token = ++visit
      cursor = 0
      snapshotCursor = 0
      observed = new Map()
      closed = new Set()
      set({ threadId, panels: [], changes: {}, connected: false, error: undefined, automaticPanelId: undefined, closingPanelId: undefined, closeError: undefined })
      const stop = subscribeTaskPanelEvents(threadId, (event) => {
        if (token !== visit) return
        if (event.kind === "connection") {
          set({ connected: event.connected })
          if (event.connected) void get().refresh()
          return
        }
        if (event.kind === "ready") {
          accept(event.panels, event.cursor)
          set({ error: undefined, connected: true })
          if (incomplete()) void get().refresh()
          return
        }
        if (event.kind === "error") {
          set({ error: event.error, connected: false })
          return
        }
        try {
          const change = event.change
          if (change.thread_id !== threadId || !Number.isSafeInteger(change.cursor) || change.cursor <= cursor || !Number.isSafeInteger(change.revision) || change.revision < 0 || !["snapshot", "patch", "deleted", "removed"].includes(change.kind)) return
          cursor = change.cursor
          if (closed.has(change.panel_id)) return
          observed.set(change.panel_id, { cursor: change.cursor, revision: change.revision, deleted: change.kind === "deleted" || change.kind === "removed" })
          const current = get().panels.find((panel) => panel.id === change.panel_id)
          if (change.kind === "deleted" || change.kind === "removed") {
            set({ panels: get().panels.filter((panel) => panel.id !== change.panel_id) })
            return
          }
          if (!current) { void get().refresh(); return }
          if (change.revision <= current.revision) return
          const state = change.kind === "snapshot" ? change.state : change.base_revision === current.revision && change.operations ? applyJsonPatch(current.state, change.operations) : undefined
          if (state === undefined) { void get().refresh(); return }
          set({
            panels: get().panels.map((panel) => panel.id === current.id ? { ...panel, revision: change.revision, state, source_status: change.source_status } : panel),
            changes: { ...get().changes, [current.id]: change },
          })
        } catch { void get().refresh() }
      })
      stopStream = stop
      void get().refresh()
      return () => {
        if (token !== visit) return
        ++visit
        stop()
        stopStream = undefined
        observed = new Map()
        cursor = 0; snapshotCursor = 0
        set({ threadId: undefined, panels: [], changes: {}, automaticPanelId: undefined, error: undefined, connected: false, closingPanelId: undefined, closeError: undefined })
      }
    },
    closePanel: async (id) => {
      const { threadId, closingPanelId } = get()
      const token = visit
      if (!threadId || closingPanelId || !get().panels.some((panel) => panel.id === id)) return
      set({ closingPanelId: id, closeError: undefined })
      try {
        const response = await fetch(`/api/threads/${encodeURIComponent(threadId)}/panels/${encodeURIComponent(id)}`, { method: "DELETE" })
        if (token !== visit) return
        if (!response.ok) throw new Error("Panel close failed")
        closed.add(id)
        observed.delete(id)
        set((state) => ({
          panels: state.panels.filter((panel) => panel.id !== id),
          changes: Object.fromEntries(Object.entries(state.changes).filter(([panelId]) => panelId !== id)),
          automaticPanelId: state.automaticPanelId === id ? undefined : state.automaticPanelId,
        }))
      } catch {
        if (token === visit) set({ closeError: true })
      } finally {
        if (token === visit) set({ closingPanelId: undefined })
      }
    },
    refresh: async () => {
      const threadId = get().threadId, token = visit
      if (!threadId) return
      if (loading?.token === token) return loading.promise
      const promise = (async () => {
        try {
          do {
            const response = await fetch(`/api/threads/${encodeURIComponent(threadId)}/panels`)
            if (token !== visit || response.status === 404) return
            if (!response.ok) throw new Error(response.statusText || "Could not load task panels")
            const data = await response.json() as { panels: TaskPanel[]; cursor: number }
            if (token !== visit) return
            accept(data.panels, data.cursor)
            set({ error: undefined })
            // Only this panel's missing version requires another read. A busy
            // unrelated panel cannot invalidate an otherwise useful snapshot.
          } while (incomplete())
        } catch (error) {
          if (token === visit) set({ error: error instanceof Error ? error.message : String(error) })
        }
      })()
      loading = { token, promise }
      try { await promise } finally { if (loading?.promise === promise) loading = undefined }
    },
  }
})

/** One app shell observes the shared chat stream even with its inspector closed. */
export function useTaskPanelConnection(threadId?: string) {
  // Map data can change several times per second; only admission changes need
  // to rerender the app shell that owns this connection.
  const state = useTaskPanels(useShallow((current) => ({ threadId: current.threadId, automaticPanelId: current.automaticPanelId })))
  useEffect(() => threadId ? useTaskPanels.getState().connect(threadId) : undefined, [threadId])
  return state
}
