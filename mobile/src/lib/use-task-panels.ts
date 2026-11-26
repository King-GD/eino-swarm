import { useCallback, useEffect, useRef, useState } from "react"
import { applyJsonPatch } from "../../../frontend/src/task-panels/json-patch"
import type { PanelChange, TaskPanel } from "../../../frontend/src/task-panels/types"
import type { RemoteLink } from "./link"
import { closeTaskPanel, listTaskPanels, readTaskPanel, TaskPanelReadError } from "./remote-task-panels"

type Scope = { link: RemoteLink | undefined; threadId: string; ownerKey: string }
type State = Scope & {
  panels: TaskPanel[]
  changes: Record<string, PanelChange>
  loading: boolean
  error?: string
  closeError?: boolean
  closingPanelId?: string
  supported: boolean
  available: boolean
  connected: boolean
}
const sameScope = (a: Scope, b: Scope) => a.link === b.link && a.threadId === b.threadId && a.ownerKey === b.ownerKey
const sameBinding = (a: Scope, b: Scope) => Boolean(a.ownerKey) && a.threadId === b.threadId && a.ownerKey === b.ownerKey
const blank = (scope: Scope): State => ({ ...scope, panels: [], changes: {}, loading: false, supported: true, available: false, connected: false })

/** The phone reads its selected PC's projections, never the PC's HTTP origin.
 * One bounded transfer finishes before a newer manifest can start another. */
export function useTaskPanels(link: RemoteLink | undefined, threadId: string, ownerKey: string, visible: boolean) {
  const active = useRef<Scope>({ link, threadId, ownerKey })
  active.current = { link, threadId, ownerKey }
  const shown = useRef(visible)
  shown.current = visible
  const [state, setState] = useState<State>(() => blank(active.current))
  const latestState = useRef(state)
  latestState.current = state
  const refreshRef = useRef<() => Promise<void>>(async () => undefined)
  const refresh = useCallback(() => refreshRef.current(), [])
  const closeRef = useRef<(id: string) => Promise<void>>(async () => undefined)
  const closePanel = useCallback((id: string) => closeRef.current(id), [])

  useEffect(() => {
    const scope = { link, threadId, ownerKey }
    const previous = latestState.current
    const retained = sameBinding(previous, scope)
    if (!link || !threadId || !ownerKey) {
      setState(retained ? { ...previous, ...scope, connected: false, loading: false } : blank(scope)); refreshRef.current = async () => undefined
      closeRef.current = async () => undefined
      return
    }
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let pending: Promise<void> | undefined
    let again = false, unsupported = false
    let closingPanelId: string | undefined
    const closed = new Set<string>()
    let panels: TaskPanel[] = retained ? previous.panels : [], changes: Record<string, PanelChange> = retained ? previous.changes : {}
    let ids = new Set(panels.map((panel) => panel.id))
    const current = () => !controller.signal.aborted && sameScope(scope, active.current)
    const update = (value: Partial<State>) => { if (current()) setState((old) => ({ ...(sameScope(old, scope) ? old : blank(scope)), ...value })) }
    const tick = async (): Promise<void> => {
      if (!current() || unsupported) return
      if (pending) { again = true; return pending }
      clearTimeout(timer)
      pending = (async () => {
        do {
          again = false
          if (!link?.alive()) { update({ connected: false, loading: false }); return }
          update({ loading: panels.length === 0, connected: true })
          try {
            const manifest = (await listTaskPanels(link, threadId, controller.signal)).filter((panel) => !closed.has(panel.id))
            if (!current()) return
            ids = new Set(manifest.map((panel) => panel.id))
            panels = panels.filter((panel) => ids.has(panel.id))
            changes = Object.fromEntries(Object.entries(changes).filter(([id]) => ids.has(id)))
            update({ panels, changes, available: manifest.length > 0, supported: true, error: undefined })
            for (const item of manifest) {
              if (!current()) return
              if (closed.has(item.id)) continue
              const before = panels.find((panel) => panel.id === item.id)
              const sameDefinition = before?.plugin_id === item.plugin_id && before.plugin_version === item.plugin_version
              if ((sameDefinition && before.revision >= item.revision) || (!shown.current && panels.length > 0)) continue
              let payload = await readTaskPanel(link, threadId, item.id, sameDefinition ? before.revision : undefined, controller.signal)
              if (!current()) return
              let panel: TaskPanel
              if (payload.panel) panel = payload.panel
              else {
                // A patch failure never mutates the last good projection.
                try {
                  if (!sameDefinition || payload.change.base_revision !== before.revision) throw new Error("Panel patch base changed")
                  panel = { ...before, revision: payload.change.revision, source_status: payload.change.source_status,
                    state: applyJsonPatch(before.state, payload.change.operations!) }
                } catch {
                  payload = await readTaskPanel(link, threadId, item.id, undefined, controller.signal)
                  if (!current()) return
                  if (!payload.panel) throw new TaskPanelReadError("invalid_panel_data", "Panel resynchronization was not a snapshot")
                  panel = payload.panel
                }
              }
              if (closed.has(item.id)) continue
              if (panel.plugin_id !== item.plugin_id || panel.plugin_version !== item.plugin_version) {
                throw new TaskPanelReadError("invalid_panel_data", "Panel definition does not match its manifest")
              }
              if (panel.revision < (before?.revision ?? 0)) throw new TaskPanelReadError("invalid_panel_data", "Panel revision went backwards")
              panels = before ? panels.map((old) => old.id === item.id ? panel : old) : [...panels, panel]
              changes = payload.change ? { ...changes, [item.id]: payload.change } : Object.fromEntries(Object.entries(changes).filter(([id]) => id !== item.id))
              update({ panels, changes, loading: false, error: undefined })
            }
          } catch (error) {
            if (!current()) return
            if (error instanceof TaskPanelReadError && error.code === "unknown_op") {
              unsupported = true; panels = []; changes = {}
              update({ panels, changes, supported: false, available: false, error: undefined })
            } else update({ error: error instanceof Error ? error.message : String(error), connected: Boolean(link?.alive()) })
          } finally { update({ loading: false }) }
        } while (again && current() && !unsupported)
      })()
      try { await pending } finally {
        pending = undefined
        if (current() && !unsupported) timer = setTimeout(() => { void tick() }, shown.current ? 500 : 1500)
      }
    }
    setState({ ...blank(scope), panels, changes, available: retained && previous.available })
    refreshRef.current = tick
    closeRef.current = async (id) => {
      if (!current() || closingPanelId || !panels.some((panel) => panel.id === id)) return
      closingPanelId = id
      update({ closingPanelId, closeError: undefined })
      try {
        await closeTaskPanel(link, threadId, id, controller.signal)
        if (!current()) return
        closed.add(id)
        ids.delete(id)
        panels = panels.filter((panel) => panel.id !== id)
        changes = Object.fromEntries(Object.entries(changes).filter(([panelId]) => panelId !== id))
        update({ panels, changes, available: ids.size > 0 })
        void tick()
      } catch {
        update({ closeError: true })
      } finally {
        closingPanelId = undefined
        update({ closingPanelId })
      }
    }
    void tick()
    return () => { controller.abort(); clearTimeout(timer) }
  }, [link, threadId, ownerKey])

  useEffect(() => { if (visible) void refresh() }, [visible, refresh])
  const value = sameBinding(state, active.current) ? state : blank(active.current)
  return { ...value, connected: sameScope(value, active.current) && value.connected && Boolean(link?.alive()), refresh, closePanel }
}
