import { useMemo } from "react"
import { isEasmResult, stepLoading } from "../../../lib/easm/data"
import { easmText, EasmTextContext } from "../../../lib/easm/messages"
import { panelDefinitionCompatible } from "../../../task-panels/definition"
import { panelText } from "../../../task-panels/messages"
import type { TaskPanel } from "../../../task-panels/types"
import { EasmPanel } from "./easm-panel"

export function EasmTaskPanel({ panel, visible, locale, sheet }: {
  panel: TaskPanel; visible: boolean; locale: string; sheet?: HTMLElement | null
}) {
  const compatible = useMemo(
    () => panelDefinitionCompatible(panel),
    [panel.definition, panel.plugin_id, panel.plugin_version],
  )
  const result = useMemo(() => isEasmResult(panel.state) ? panel.state : undefined, [panel.state])
  if (!compatible) return <p role="alert" className="p-3 text-sm">{panelText(locale, "incompatible")} {panelText(locale, "regenerateEasm")}</p>
  if (!result) return <p role="alert" className="p-3 text-sm">{panelText(locale, "invalidEasm")} {panelText(locale, "regenerateEasm")}</p>
  return <div className="min-h-0 flex-1" data-testid="easm-task-panel" data-revision={panel.revision}>
    <EasmTextContext value={easmText(locale)}>
      <EasmPanel result={result} heatLoading={stepLoading(result, "heat")} visible={visible} sheet={sheet} />
    </EasmTextContext>
  </div>
}
