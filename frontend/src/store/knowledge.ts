import { create } from "zustand"

import { backKnowledgePage, pushKnowledgePage } from "@/lib/knowledge"

const emptyStack: readonly string[] = []

interface KnowledgeNavigation {
  projectId?: string
  stack: readonly string[]
  refreshByProject: Record<string, number>
  openPage: (projectId: string, page: string) => void
  back: (projectId: string) => void
  invalidate: (projectId: string) => void
}

/** The transcript can open a page before the Knowledge tab has mounted. */
export const useKnowledge = create<KnowledgeNavigation>((set) => ({
  stack: emptyStack,
  refreshByProject: {},
  openPage: (projectId, page) => set((state) => ({
    projectId,
    stack: pushKnowledgePage(knowledgeStack(state, projectId), page),
  })),
  back: (projectId) => set((state) => state.projectId === projectId
    ? { stack: backKnowledgePage(state.stack) }
    : state),
  invalidate: (projectId) => set((state) => ({
    refreshByProject: {
      ...state.refreshByProject,
      [projectId]: (state.refreshByProject[projectId] ?? 0) + 1,
    },
  })),
}))

export function knowledgeStack(state: Pick<KnowledgeNavigation, "projectId" | "stack">, projectId: string): readonly string[] {
  return state.projectId === projectId ? state.stack : emptyStack
}
