import { bytesToHex } from "@noble/hashes/utils.js"
import { sha256 } from "@noble/hashes/sha2.js"
import type { KnowledgePage, KnowledgeSource } from "../../../frontend/src/lib/knowledge"
import { OpKnowledgeCancel, OpKnowledgePage, OpProjectKnowledge, type RemoteRequest, type RemoteResponse } from "./rpc"

const related = { id: "delivery", type: "entry", title: "Delivery notes" }
const original: KnowledgeSource = { kind: "event", thread_id: "t-live", seq: 12, available: true, classification: "primary" }
const primary: KnowledgePage = {
  id: "design-log", type: "entry", title: "Design log", body: "# Working notes\n\nOpen the [[delivery|delivery reference]].",
  sources: [original, { kind: "message", seq: 4, thread_id: "t-live", available: false }],
  wiki_observations: [{ source: original, quote: "The reading view keeps the reference visible." }],
  outgoing: [{ relation: "related", label: "Related", page: related, target: related.id, resolved: true }],
}

const entries: KnowledgePage[] = [
  primary,
  { ...related, body: "The companion entry." },
  ...Array.from({ length: 32 }, (_, i) => ({
    id: `entry-${i + 1}`, type: "entry", title: `Project entry ${i + 1}`, body: "A short entry.",
  })),
]

export function mockKnowledge(req: Pick<RemoteRequest, "op" | "project_id" | "text" | "offset" | "knowledge_page_id">, projectKnown: boolean): Partial<RemoteResponse> | undefined {
  if (req.op !== OpProjectKnowledge && req.op !== OpKnowledgePage && req.op !== OpKnowledgeCancel) return
  if (new URLSearchParams(location.search).get("knowledge") === "legacy") return { ok: false, code: "unknown_op" }
  if (!projectKnown) return { ok: false, code: "not_found" }
  const projectId = req.project_id!
  const enabled = projectId === "p-platform"
  const offset = req.offset ?? 0
  if (req.op === OpProjectKnowledge) {
    const query = req.text?.trim().toLowerCase() || ""
    const matched = enabled ? entries.filter((row) => row.title.toLowerCase().includes(query)) : []
    return { knowledge: {
      project_id: projectId, enabled,
      pages: matched.slice(offset, offset + 32).map(({ id, type, title }) => ({ id, type, title })),
      ...(offset + 32 < matched.length ? { next_offset: offset + 32 } : {}),
    } }
  }
  if (req.op === OpKnowledgeCancel) return { ok: true }
  const entry = enabled && entries.find((row) => row.id === req.knowledge_page_id)
  if (!entry) return { ok: false, code: "knowledge_page_not_found" }
  const bytes = new TextEncoder().encode(JSON.stringify(entry))
  const end = Math.min(offset + (36 << 10), bytes.length)
  return { knowledge_chunk: {
    id: "5".repeat(32), project_id: projectId, page_id: entry.id, sha256: bytesToHex(sha256(bytes)), size: bytes.length,
    offset, next_offset: end, data: btoa(String.fromCharCode(...bytes.subarray(offset, end))), more: end < bytes.length,
  } }
}
