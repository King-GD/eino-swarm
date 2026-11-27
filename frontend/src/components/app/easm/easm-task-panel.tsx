import { useMemo, type ReactNode } from "react"
import { graphSectionLoading, isEasmResult, sectionStatus } from "../../../lib/easm/data"
import { easmText, EasmTextContext } from "../../../lib/easm/messages"
import { projectEasmResult } from "../../../lib/easm/model"
import { panelDefinitionCompatible } from "../../../task-panels/definition"
import { panelText } from "../../../task-panels/messages"
import type { TaskPanel } from "../../../task-panels/types"
import { EasmPanel } from "./easm-panel"

export function EasmTaskPanel({ panel, visible, locale, sheet, active = false, headerActions }: {
  panel: TaskPanel; visible: boolean; locale: string; sheet?: HTMLElement | null
  /** The owning conversation's task is still running. */
  active?: boolean
  headerActions?: ReactNode
}) {
  const compatible = useMemo(
    () => panelDefinitionCompatible(panel),
    [panel.definition, panel.plugin_id, panel.plugin_version],
  )
  const result = useMemo(() => isEasmResult(panel.state) ? projectEasmResult(panel.state) : undefined, [panel.state])
  // A stored definition can outlive this bundle; the host's controls must
  // survive that, or the panel cannot be closed at all.
  const failure = (message: string) => <div className="flex min-h-0 flex-1 flex-col" data-testid="easm-task-panel" data-revision={panel.revision}>
    {headerActions ? <div className="flex items-center justify-end border-b px-2 py-1">{headerActions}</div> : null}
    <p role="alert" className="p-3 text-sm">{message}</p>
  </div>
  if (!compatible) return failure(`${panelText(locale, "incompatible")} ${panelText(locale, "regenerateEasm")}`)
  if (!result) return failure(`${panelText(locale, "invalidEasm")} ${panelText(locale, "regenerateEasm")}`)
  const cloudsStatus = result.collection.steps.some((step) => step.id === "clouds")
    ? sectionStatus(result, "clouds", active, panel.source_status)
    : undefined
  return <div className="min-h-0 flex-1" data-testid="easm-task-panel" data-revision={panel.revision}>
    <EasmTextContext value={easmText(locale)}>
      <EasmPanel
        result={result}
        graphLoading={graphSectionLoading(active, panel.source_status)}
        heatStatus={sectionStatus(result, "heat", active, panel.source_status)}
        componentsStatus={sectionStatus(result, "components", active, panel.source_status)}
        cloudsStatus={cloudsStatus}
        visible={visible}
        sheet={sheet}
        headerActions={headerActions}
      />
    </EasmTextContext>
  </div>
}
