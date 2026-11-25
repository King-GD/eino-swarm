import type { NuwaNode } from "../../../frontend/src/lib/nuwa"
import type { EngineStartup, RemoteResponse } from "./rpc"

function workMode() {
  return new URLSearchParams(location.search).get("work-records")
}

export function mockWorkRecordsMeta(): EngineStartup {
  return workMode() === "on" ? { work_records_available: true, data_generation: 1 } : {}
}

export function mockThreadWork(thread?: { id: string; projectID: string }): Partial<RemoteResponse> {
  const mode = workMode()
  if (mode !== "on") return { ok: false, code: mode === "off" ? "work_disabled" : "unknown_op" }
  if (!thread) return { ok: false, code: "not_found" }
  const date = "2026-11-24T00:00:00Z"
  const node = (id: string, scope: NuwaNode["scope"], kind: NuwaNode["kind"], title: string, depends_on: string[] = []): NuwaNode => ({
    id, scope, kind, title, depends_on, version: 1, thread_id: thread.id, turn_id: "turn-1",
    status: kind === "task" ? "done" : "reported", summary: "", created_at: date, updated_at: date,
  })
  const scope = thread.projectID ? "project" : "thread"
  return {
    data_generation: 1, work_snapshot: {
      thread_id: thread.id, project_id: thread.projectID,
      nodes: [
        node("task", scope, "task", "Check the inputs"),
        node("fact", scope, "fact", "Inputs were verified", ["task"]),
        { ...node("result", "thread", "artifact", "Review notes", ["fact"]),
          summary: "Saved for this conversation.", artifact_path: "results/report.md",
          evidence: [{ thread_id: thread.id, seq: 5, label: "Saved review receipt" }] },
      ],
    },
  }
}
