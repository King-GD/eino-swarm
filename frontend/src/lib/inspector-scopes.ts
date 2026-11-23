export type InspectorScope = "thread" | "project"

export const builtinInspectorScopes = {
  agents: "thread",
  files: "thread",
  trace: "thread",
  memory: "project",
  knowledge: "project",
} as const satisfies Record<string, InspectorScope>

export function groupInspectorTabs<T extends { scope: InspectorScope }>(tabs: readonly T[]) {
  const groups: Record<InspectorScope, T[]> = { thread: [], project: [] }
  for (const tab of tabs) groups[tab.scope].push(tab)
  return groups
}

export function projectScopeLabel(projectName: string | undefined, sharedLabel: string) {
  return projectName || sharedLabel
}
