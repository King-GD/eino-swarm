import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { ChevronLeft, PanelsTopLeft, RefreshCw, X } from "lucide-react"
import { TaskPanelFrame } from "../../../frontend/src/components/app/task-panel-frame"
import { panelText } from "../../../frontend/src/task-panels/messages"
import { Button } from "./ui/button"
import { installAndroidBack } from "@/lib/android-back"
import { getLocale, t } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { useTaskPanels } from "@/lib/use-task-panels"

const opened = new Set<string>()

const EasmTaskPanel = lazy(() => import("../../../frontend/src/components/app/easm/easm-task-panel")
  .then(({ EasmTaskPanel }) => ({ default: EasmTaskPanel })))

/** Closing only changes presentation; the paired PC retains task ownership. */
export function usePhoneTaskPanelView(link: RemoteLink | undefined, threadId: string, ownerKey: string) {
  const scope = `${ownerKey}\0${threadId}`
  const [view, setView] = useState({ scope, open: false, selected: "" })
  const open = view.scope === scope && view.open
  const data = useTaskPanels(link, threadId, ownerKey, open)
  const panels = ownerKey && link && data.supported ? data.panels.filter((panel) => panel.thread_id === threadId) : []
  const current = panels.find((panel) => view.scope === scope && panel.id === view.selected) ?? panels[0]
  const close = useCallback(() => setView((value) => ({ ...value, open: false })), [])
  useEffect(() => {
    if (panels.length && !opened.has(scope)) {
      opened.add(scope)
      setView({ scope, open: true, selected: panels[0].id })
    } else if (view.scope !== scope || (!panels.length && !data.available && !data.loading)) {
      setView((value) => value.scope !== scope || value.open ? { scope, open: false, selected: "" } : value)
    }
  }, [scope, panels, data.available, data.loading, view.scope])
  return {
    ...data, available: Boolean(ownerKey && link && data.available), panels, current, scope, open, close,
    show: () => setView({ scope, open: true, selected: current?.id ?? "" }),
    select: (id: string) => setView({ scope, open: true, selected: id }),
  }
}

type PanelView = ReturnType<typeof usePhoneTaskPanelView>

export function PhoneTaskPanelButton({ view }: { view: PanelView }) {
  if (!view.available && !view.panels.length) return null
  const caption = panelText(getLocale(), "tab")
  return <Button type="button" variant="ghost" className="size-10 shrink-0 px-0" onClick={view.show} aria-label={caption} title={caption}>
    <PanelsTopLeft className="size-5" aria-hidden />
  </Button>
}

export function PhoneTaskPanelSheet({ view }: { view: PanelView }) {
  const [dialog, setDialog] = useState<HTMLElement | null>(null)
  const closeButton = useRef<HTMLButtonElement>(null)
  const close = view.close
  useEffect(() => {
    const backButton = closeButton.current
    if (!view.open || !dialog || !backButton) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    backButton.focus({ preventScroll: true })
    const back = installAndroidBack(() => {
      const dialogClose = dialog.querySelector<HTMLButtonElement>('[role="dialog"] [data-dialog-close]')
      if (dialogClose) dialogClose.click()
      else close()
      return true
    })
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) { event.stopPropagation(); close() }
    }
    const focus = (event: FocusEvent) => {
      if (event.target instanceof Node && !dialog.contains(event.target)) backButton.focus({ preventScroll: true })
    }
    window.addEventListener("keydown", escape)
    document.addEventListener("focusin", focus)
    return () => { back(); window.removeEventListener("keydown", escape); document.removeEventListener("focusin", focus); if (previous?.isConnected) previous.focus({ preventScroll: true }) }
  }, [view.open, dialog, close])
  if (!view.available && !view.panels.length) return null
  const locale = getLocale(), caption = panelText(locale, "tab")
  return createPortal(<section ref={setDialog} role={view.open ? "dialog" : undefined} aria-modal={view.open || undefined} aria-label={caption}
    aria-hidden={!view.open} inert={!view.open} data-testid="phone-task-panels"
    className={`${view.open ? "flex" : "hidden"} fixed inset-0 z-50 min-h-0 min-w-0 flex-col bg-background pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)]`}>
    <header className="flex min-h-12 shrink-0 items-center gap-2 border-b border-border px-1">
      <button ref={closeButton} type="button" className="flex size-10 shrink-0 items-center justify-center rounded-md hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring" onClick={view.close} aria-label={t("thread.back")}><ChevronLeft className="size-5" aria-hidden /></button>
      <h2 className="min-w-0 flex-1 truncate text-sm font-medium">{caption}</h2>
      <Button type="button" variant="ghost" className="size-10 shrink-0 px-0" onClick={view.refresh} disabled={view.loading} aria-label={panelText(locale, "retry")}><RefreshCw className={`size-4 ${view.loading ? "animate-spin motion-reduce:animate-none" : ""}`} aria-hidden /></Button>
      <Button type="button" variant="ghost" className="size-10 shrink-0 px-0" onClick={view.close} aria-label={t("home.close")}><X className="size-5" aria-hidden /></Button>
    </header>
    {!view.connected ? <p role="status" className="border-b border-border px-3 py-2 text-xs text-muted-foreground">{panelText(locale, "disconnected")}</p> : null}
    {view.loading ? <p role="status" className="px-3 py-1 text-xs text-muted-foreground">{panelText(locale, "loading")}</p> : null}
    {view.error ? <p role="alert" className="px-3 py-2 text-xs text-destructive">{view.error}</p> : null}
    {!view.panels.length && !view.loading && !view.error ? <p role="status" className="p-3 text-sm text-muted-foreground">{panelText(locale, "loading")}</p> : null}
    {view.panels.length > 1 ? <label className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-1 text-xs">
      <span className="shrink-0">{panelText(locale, "choose")}</span>
      <select className="h-11 min-w-0 flex-1 rounded border border-border bg-background px-2 text-sm" value={view.current?.id ?? ""} onChange={(event) => view.select(event.target.value)}>
        {view.panels.map((panel, index) => <option key={panel.id} value={panel.id}>{panel.definition.name} · {index + 1}</option>)}
      </select>
    </label> : null}
    {view.panels.map((panel) => <div key={`${view.scope}:${panel.id}`} className={panel.id === view.current?.id ? "flex min-h-0 min-w-0 flex-1 flex-col" : "hidden"}>
      {panel.plugin_id === "easm-asset-portrait" && !panel.definition.entry ? (
        <Suspense fallback={<p role="status" className="p-3 text-sm text-muted-foreground">{panelText(locale, "loading")}</p>}>
          <EasmTaskPanel panel={panel} visible={view.open && panel.id === view.current?.id} locale={locale} sheet={dialog} />
        </Suspense>
      ) : (
        <TaskPanelFrame panel={panel} change={view.changes[panel.id]} visible={view.open && panel.id === view.current?.id} locale={locale} />
      )}
    </div>)}
  </section>, document.body)
}
