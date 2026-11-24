import { create } from "zustand"

import { api } from "@/lib/api"
import type { DistillRequest, DistillResult } from "@/lib/distillation"
import { useKnowledge } from "./knowledge"
import { useProjects } from "./projects"
import { errorMessage } from "./toasts"

interface DistillationSlot {
  generation: number
  starting?: boolean
  result?: DistillResult
  error?: string
}

interface DistillationState {
  projects: Record<string, DistillationSlot>
  refresh(projectId: string, signal: AbortSignal): Promise<void>
  start(projectId: string, request: DistillRequest): Promise<void>
}

export const useDistillation = create<DistillationState>((set, get) => {
  const read = (projectId: string): DistillationSlot => get().projects[projectId] ?? { generation: 0 }
  const save = (projectId: string, slot: DistillationSlot) => set((s) => ({
    projects: { ...s.projects, [projectId]: slot },
  }))
  const report = (projectId: string, slot: DistillationSlot, result: DistillResult) => {
    const changed = slot.result?.review_id !== result.review_id || slot.result?.state !== result.state
    save(projectId, { generation: slot.generation, result })
    if (!changed || !result.review_id) return
    if (result.knowledge_enabled) useKnowledge.getState().invalidate(projectId)
    if (result.changes?.length || result.uncertain) {
      const projects = useProjects.getState()
      if (projects.memoryProjectId === projectId) void projects.loadMemory(projectId)
    }
  }

  return {
    projects: {},
    refresh: async (projectId, signal) => {
      const slot = read(projectId)
      if (slot.starting) return
      try {
        const result = await api.distillStatus(projectId, signal)
        const current = read(projectId)
        if (signal.aborted || current.generation !== slot.generation) return
        // no_work is an immediate response, not a replacement for the last
        // durable receipt. Keep it visible when switching between the panels.
        if (current.result?.state === "no_work" && sourceIsPrevious(current.result, result)) return
        report(projectId, current, result)
      } catch (error) {
        if (!signal.aborted && read(projectId).generation === slot.generation) {
          save(projectId, { ...read(projectId), error: errorMessage(error) })
        }
      }
    },
    start: async (projectId, request) => {
      const slot = read(projectId)
      if (slot.starting || slot.result?.state === "running") return
      const current = { ...slot, generation: slot.generation + 1 }
      save(projectId, { ...current, starting: true, error: undefined })
      try {
        const result = await api.startDistillation(projectId, request)
        report(projectId, current, result.state === "no_work" ? { ...result, review_id: slot.result?.review_id } : result)
      } catch (error) {
        save(projectId, { ...current, error: errorMessage(error) })
      }
    },
  }
})

function sourceIsPrevious(current: DistillResult, fetched: DistillResult): boolean {
  return fetched.state === "idle" || (current.review_id === fetched.review_id && fetched.state !== "running")
}
