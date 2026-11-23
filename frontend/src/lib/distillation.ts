import type { MemoryChange } from "./types"

export type DistillScope = "latest_turn" | "thread" | "project"
export type DistillState = "idle" | "no_work" | "running" | "completed" | "partial" | "failed" | "interrupted"

export interface DistillRequest {
  scope: DistillScope
  thread_id?: string
  enable_knowledge?: boolean
}

export interface DistillResult {
  state: DistillState
  review_id?: string
  scope?: DistillScope
  thread_id?: string
  turn_id?: string
  history_omitted?: boolean
  memory_enabled?: boolean
  knowledge_enabled?: boolean
  changes?: MemoryChange[]
  knowledge?: { action: "write_page" | "append_schema"; id?: string; title?: string; revision: string }[]
  error?: string
  uncertain?: boolean
}

export function distillationPath(projectId: string): string {
  return `/api/projects/${encodeURIComponent(projectId)}/distill`
}
