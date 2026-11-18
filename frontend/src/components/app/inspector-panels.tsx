import { useEffect, type ReactNode } from "react"
import type { Project, Turn } from "@/lib/types"
import type { Translate } from "@/lib/use-t"
import { useTaskPanelConnection, useTaskPanels } from "@/store/task-panels"
import { panelText } from "@/task-panels/messages"
import type { TaskPanel } from "@/task-panels/types"
import { NuwaPanel } from "./nuwa-panel"
import { TaskPanels } from "./task-panels"

interface TaskPanelProjection {
  threadId?: string
  panels: readonly TaskPanel[]
}

interface InspectorContext {
  threadId?: string
  project?: Project
  taskPanels: TaskPanelProjection
}

interface InspectorView {
  visible: boolean
  latestTurn?: Pick<Turn, "id" | "status">
  openFiles: () => void
  selectAgent: (id: string) => void
}

interface InspectorBinding {
  ownerId: string
  render: (view: InspectorView) => ReactNode
}

type InspectorRegistration = {
  id: string
  label: (t: Translate) => string
  bind: (context: InspectorContext) => InspectorBinding | undefined
} & (
  | { scope: "project"; activation: "project_mode"; renderer: "react"; mount: "active" }
  | { scope: "thread"; activation: "agent_open"; renderer: "iframe"; mount: "retained" }
)

const registrations = [
  {
    id: "task-panels",
    scope: "thread",
    activation: "agent_open",
    renderer: "iframe",
    mount: "retained",
    label: (t) => panelText(t.locale, "tab"),
    bind: ({ threadId, taskPanels }) => {
      if (!threadId || taskPanels.threadId !== threadId || !taskPanels.panels.some((panel) => panel.thread_id === threadId)) return
      return {
        ownerId: threadId,
        render: ({ visible }) => <TaskPanels threadId={threadId} visible={visible} />,
      }
    },
  },
  {
    id: "nuwa",
    scope: "project",
    activation: "project_mode",
    renderer: "react",
    mount: "active",
    label: (t) => t("nuwa.tab"),
    bind: ({ project, threadId }) => {
      if (!project || !(project.nuwa_enabled || (project.nuwa_revision ?? 0) > 0)) return
      return {
        ownerId: project.id,
        render: ({ latestTurn, openFiles, selectAgent }) => (
          <NuwaPanel
            project={project}
            threadId={threadId}
            activity={threadId && latestTurn ? {
              id: latestTurn.id,
              status: latestTurn.status,
              onOpenFiles: openFiles,
            } : undefined}
            onSelectAgent={selectAgent}
          />
        ),
      }
    },
  },
] as const satisfies readonly InspectorRegistration[]

export type RegisteredInspectorTab = (typeof registrations)[number]["id"]

export function isRegisteredInspectorTab(tab: string): tab is RegisteredInspectorTab {
  return registrations.some((registration) => registration.id === tab)
}

export function resolveInspectorPanels(context: InspectorContext) {
  return registrations.flatMap((registration) => {
    const binding = registration.bind(context)
    return binding ? [{ ...registration, ...binding }] : []
  })
}

export function useRegisteredInspectorAutoOpen(threadId: string | undefined, open: (tab: RegisteredInspectorTab) => void) {
  const signal = useTaskPanelConnection(threadId)
  useEffect(() => {
    const taskPanels = useTaskPanels.getState()
    if (!threadId || signal.threadId !== threadId || taskPanels.threadId !== threadId || !signal.automaticPanelId ||
      taskPanels.automaticPanelId !== signal.automaticPanelId ||
      !taskPanels.panels.some((panel) => panel.id === signal.automaticPanelId && panel.thread_id === threadId)) return
    const automatic = resolveInspectorPanels({ threadId, taskPanels }).find(
      (panel) => panel.activation === "agent_open" && panel.ownerId === threadId,
    )
    if (automatic) open(automatic.id)
  }, [threadId, open, signal.threadId, signal.automaticPanelId])
}
