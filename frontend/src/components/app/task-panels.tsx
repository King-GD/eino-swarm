import { useState } from "react"
import { useT } from "@/lib/use-t"
import { useTaskPanels } from "@/store/task-panels"
import { panelText } from "@/task-panels/messages"
import { isNativeTaskPanel, NativeTaskPanel } from "./native-task-panels"
import { TaskPanelFrame } from "./task-panel-frame"

export function TaskPanels({ threadId, visible }: { threadId: string; visible: boolean }) {
  const { threadId: owner, panels: storedPanels, changes, connected, error } = useTaskPanels()
  const panels = owner === threadId ? storedPanels.filter((panel) => panel.thread_id === threadId) : []
  const [selected, select] = useState<string>()
  const t = useT()
  const current = panels.find((panel) => panel.id === selected) ?? panels.at(0)
  return <section className="flex min-h-0 flex-1 flex-col" aria-label={panelText(t.locale, "tab")}>
    {panels.length > 1 ? <label className="border-b border-border p-2 text-xs">{panelText(t.locale, "choose")} <select value={current?.id ?? ""} onChange={(event) => select(event.target.value)} className="max-w-full rounded border border-border bg-background p-1">{panels.map((panel) => <option key={panel.id} value={panel.id}>{panel.definition.name}</option>)}</select></label> : null}
    {!connected && panels.length ? <p role="status" className="border-b border-border px-3 py-1 text-xs text-muted-foreground">{panelText(t.locale, "disconnected")}</p> : null}
    {owner === threadId && error ? <p role="alert" className="p-2 text-xs text-destructive">{error}</p> : null}
    {panels.map((panel) => <div key={panel.id} className={panel.id === current?.id ? "flex min-h-0 flex-1 flex-col" : "hidden"}>
      {isNativeTaskPanel(panel)
        ? <NativeTaskPanel panel={panel} visible={visible && current?.id === panel.id} locale={t.locale} />
        : <TaskPanelFrame panel={panel} change={changes[panel.id]} visible={visible && current?.id === panel.id} locale={t.locale} />}
    </div>)}
  </section>
}
