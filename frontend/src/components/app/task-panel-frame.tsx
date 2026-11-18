import { useEffect, useRef, useState } from "react"
import type { HostContext, PanelChange, TaskPanel } from "../../task-panels/types"
import { panelText } from "../../task-panels/messages"
import { panelDefinitionCompatible } from "../../task-panels/definition"

const panelDeadlineMS = 10_000

export function TaskPanelFrame({ panel, change, visible, locale }: { panel: TaskPanel; change?: PanelChange; visible: boolean; locale: string }) {
  const frame = useRef<HTMLIFrameElement>(null)
  const latest = useRef({ panel, change, visible, locale })
  latest.current = { panel, change, visible, locale }
  const [html, setHtml] = useState<string>()
  const [error, setError] = useState<string>()
  const [attempt, setAttempt] = useState(0)
  const compatible = panelDefinitionCompatible(panel)
  const port = useRef<MessagePort | undefined>(undefined)
  const sentRevision = useRef(-1)
  const appliedRevision = useRef(-1)
  const needsSnapshot = useRef(true)
  const ready = useRef(false)
  const inflight = useRef(false)
  const resynced = useRef(-1)
  const failed = useRef(false)
  const deadline = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const sentContext = useRef("")
  const clearDeadline = () => { clearTimeout(deadline.current); deadline.current = undefined }
  const armDeadline = (kind: "handshakeTimeout" | "updateTimeout") => {
    clearDeadline()
    deadline.current = setTimeout(() => { failed.current = true; setError(panelText(latest.current.locale, kind)) }, panelDeadlineMS)
  }
  const context = (): HostContext => {
    const current = latest.current, rect = frame.current?.getBoundingClientRect()
    return { panel_id: current.panel.id, thread_id: current.panel.thread_id, revision: current.panel.revision, locale: current.locale, theme: document.documentElement.classList.contains("dark") ? "dark" : "light", width: rect?.width ?? 0, height: rect?.height ?? 0, visible: current.visible, source_status: current.panel.source_status }
  }
  const send = (snapshot = false) => {
    if (!ready.current || !port.current || failed.current) return
    const current = latest.current, ctx = context()
    const signature = JSON.stringify({ ...ctx, revision: 0 })
    const sendContext = () => {
      if (sentContext.current !== signature) { port.current?.postMessage({ type: "context", context: ctx }); sentContext.current = signature }
    }
    if (!current.visible) { sendContext(); return }
    if (inflight.current) return
    const full = snapshot || needsSnapshot.current
    if (!full && appliedRevision.current === current.panel.revision) {
      sendContext(); return
    }
    const delta = current.change
    if (!full && delta?.kind === "patch" && delta.base_revision === appliedRevision.current && delta.revision === current.panel.revision) {
      port.current.postMessage({ type: "patch", base_revision: delta.base_revision, operations: delta.operations, context: ctx })
    } else port.current.postMessage({ type: "snapshot", state: current.panel.state, context: ctx })
    sentRevision.current = current.panel.revision
    needsSnapshot.current = false
    sentContext.current = signature
    inflight.current = true
    armDeadline("updateTimeout")
  }
  const sendRef = useRef(send)
  sendRef.current = send
  useEffect(() => {
    const abort = new AbortController()
    setHtml(undefined); setError(undefined)
    if (!compatible) { setError(panelText(locale, "incompatible")); return }
    // Only a bundled, registered entry can be loaded; no workspace or remote HTML.
    const entry = "/" + panel.definition.entry.replace(/^\//, "")
    if (!/^\/task-panels\/[a-z0-9-]+\.html$/.test(entry)) { setError("Invalid plugin entry"); return }
    void fetch(entry, { signal: abort.signal }).then(async (response) => {
      if (!response.ok) throw new Error(response.statusText)
      const source = await response.text()
      if (!abort.signal.aborted) setHtml(source)
    }).catch((reason: unknown) => { if (!abort.signal.aborted) setError(reason instanceof Error ? reason.message : String(reason)) })
    return () => abort.abort()
  }, [panel.id, panel.definition.entry, attempt, compatible])
  useEffect(() => {
    if (!html) return
    ready.current = false; sentRevision.current = -1; appliedRevision.current = -1; needsSnapshot.current = true; inflight.current = false; resynced.current = -1; failed.current = false; sentContext.current = ""
    armDeadline("handshakeTimeout")
    let channel: MessageChannel | undefined
    const listener = (event: MessageEvent) => {
      if (event.source !== frame.current?.contentWindow || event.data?.type !== "task-panel:ready" || event.data.protocol_version !== 1 || channel) return
      channel = new MessageChannel()
      port.current = channel.port1
      channel.port1.onmessage = (message) => {
        if (failed.current) return
        if (message.data?.type === "ready" && message.data.protocol_version === 1) { clearDeadline(); ready.current = true; sendRef.current(true) }
        if (message.data?.type === "resync" && resynced.current !== latest.current.panel.revision) {
          clearDeadline(); resynced.current = latest.current.panel.revision; inflight.current = false; needsSnapshot.current = true; sendRef.current(true)
        } else if (message.data?.type === "resync") {
          clearDeadline(); failed.current = true; setError(panelText(latest.current.locale, "resyncFailed"))
        }
        if (message.data?.type === "error") setError(String(message.data.message ?? "Plugin update failed"))
        if (message.data?.type === "applied" && inflight.current && message.data.revision === sentRevision.current) { clearDeadline(); appliedRevision.current = sentRevision.current; inflight.current = false; setError(undefined); sendRef.current() }
      }
      channel.port1.start()
      frame.current?.contentWindow?.postMessage({ type: "task-panel:connect", protocol_version: 1 }, "*", [channel.port2])
    }
    window.addEventListener("message", listener)
    const resize = new ResizeObserver(() => sendRef.current())
    if (frame.current) resize.observe(frame.current)
    const theme = new MutationObserver(() => sendRef.current())
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] })
    return () => {
      window.removeEventListener("message", listener)
      clearDeadline()
      resize.disconnect(); theme.disconnect()
      port.current?.postMessage({ type: "dispose" }); channel?.port1.close(); channel?.port2.close()
      port.current = undefined; ready.current = false
    }
  }, [html, panel.id])
  useEffect(() => { sendRef.current() }, [panel.revision, visible, locale])
  return <div className="relative flex min-h-0 flex-1 flex-col" data-testid="task-panel-frame-host">
    {error ? <div role="alert" className="border-b border-border p-2 text-xs">{panelText(locale, "failed")}: {error} <button className="underline" onClick={() => setAttempt((value) => value + 1)}>{panelText(locale, "retry")}</button></div> : null}
    {!html && !error ? <p role="status" className="p-3 text-sm">{panelText(locale, "loading")}</p> : null}
    {html && compatible ? <iframe ref={frame} title={panel.definition.name} sandbox="allow-scripts" referrerPolicy="no-referrer" srcDoc={html} className="min-h-0 w-full flex-1 border-0" data-testid="task-panel-frame" /> : null}
  </div>
}
