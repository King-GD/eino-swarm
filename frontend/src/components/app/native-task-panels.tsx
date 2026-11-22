import { lazy, Suspense, type ComponentType } from "react"
import { panelText } from "../../task-panels/messages"
import type { TaskPanel } from "../../task-panels/types"

export interface NativeTaskPanelProps {
  panel: TaskPanel
  visible: boolean
  locale: string
  /** Phone sheet that portals overlays; undefined on the desktop. */
  sheet?: HTMLElement | null
}

const EasmTaskPanel = lazy(() => import("./easm/easm-task-panel").then((module) => ({ default: module.EasmTaskPanel })))

/** The phone imports this file from another package, so imports stay relative. */

/** Bundled renderers for catalog definitions with renderer "native". Only
 *  repository code can add one; stored panel data never selects code. */
const NATIVE_TASK_PANELS: Record<string, ComponentType<NativeTaskPanelProps>> = {
  "easm-asset-portrait": EasmTaskPanel,
}

export function isNativeTaskPanel(panel: TaskPanel) {
  return panel.definition.renderer === "native"
}

/** Renders a native panel; an unregistered one reports incompatibility
 *  instead of falling back to an iframe with no entry. */
export function NativeTaskPanel(props: NativeTaskPanelProps) {
  const Renderer = NATIVE_TASK_PANELS[props.panel.plugin_id]
  if (!Renderer) return <p role="alert" className="p-3 text-sm">{panelText(props.locale, "incompatible")}</p>
  return <Suspense fallback={null}><Renderer {...props} /></Suspense>
}
