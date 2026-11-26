import { sha256 } from "@noble/hashes/sha2.js"
import { bytesToHex } from "./bytes"
import type { RemoteLink } from "./link"
import { OpTaskPanelCancel, OpTaskPanelClose, OpTaskPanelRead, OpTaskPanels, type TaskPanelManifest, type TaskPanelTransfer } from "./rpc"
import type { PanelChange, TaskPanel } from "../../../frontend/src/task-panels/types"

// Match the protocol ceiling, including the pinned definition and envelope.
// The PC still enforces its configured, potentially smaller state budget.
export const maxTaskPanelTransferBytes = 64 << 20
const chunkBytes = 36 << 10
const easmPanelVersion = "2"

export class TaskPanelReadError extends Error {
  constructor(readonly code: string, message: string) { super(message) }
}

export type TaskPanelPayload = { panel: TaskPanel; change?: never } | { change: PanelChange; panel?: never }
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value)
const version = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) > 0

export function validatePanelManifest(value: unknown, threadId: string): TaskPanelManifest[] {
  if (!Array.isArray(value)) throw new TaskPanelReadError("invalid_panel_data", "Missing task panel manifest")
  const ids = new Set<string>()
  for (const panel of value) {
    if (!object(panel) || typeof panel.id !== "string" || !panel.id || ids.has(panel.id) || panel.thread_id !== threadId ||
      typeof panel.plugin_id !== "string" || !panel.plugin_id || typeof panel.plugin_version !== "string" || !panel.plugin_version ||
      !version(panel.revision) || (panel.source_status !== undefined && !["running", "finished", "interrupted"].includes(String(panel.source_status)))) {
      throw new TaskPanelReadError("invalid_panel_data", "Invalid task panel manifest")
    }
    ids.add(panel.id)
  }
  return value as TaskPanelManifest[]
}

export async function listTaskPanels(link: RemoteLink, threadId: string, signal?: AbortSignal): Promise<TaskPanelManifest[]> {
  const result = await link.rpc({ op: OpTaskPanels, thread_id: threadId, easm_panel_version: easmPanelVersion }, { signal, dropOnTimeout: false })
  if (signal?.aborted) throw new DOMException("Aborted", "AbortError")
  if (!result.ok) throw new TaskPanelReadError(result.code ?? "panel_read_failed", result.error ?? "Could not load task panels")
  return validatePanelManifest(result.task_panels, threadId)
}

export async function closeTaskPanel(link: RemoteLink, threadId: string, panelId: string, signal: AbortSignal): Promise<void> {
  const result = await link.rpc({ op: OpTaskPanelClose, thread_id: threadId, panel_id: panelId }, { signal, dropOnTimeout: false })
  if (signal.aborted) throw new DOMException("Aborted", "AbortError")
  if (!result.ok) throw new TaskPanelReadError(result.code ?? "panel_close_failed", result.error ?? "Could not close panel")
}

function validateChunk(chunk: TaskPanelTransfer | undefined, expected: { threadId: string; panelId: string; offset: number; first?: TaskPanelTransfer }) {
  const first = expected.first
  if (!chunk || !chunk.id || chunk.thread_id !== expected.threadId || chunk.panel_id !== expected.panelId ||
    !version(chunk.revision) || !Number.isSafeInteger(chunk.size) || chunk.size <= 0 || chunk.size > maxTaskPanelTransferBytes ||
    !/^[0-9a-f]{64}$/.test(chunk.sha256) || chunk.offset !== expected.offset || typeof chunk.data !== "string" ||
    chunk.data.length > 4 * Math.ceil(chunkBytes / 3) || typeof chunk.more !== "boolean" ||
    (first && (chunk.id !== first.id || chunk.revision !== first.revision || chunk.size !== first.size || chunk.sha256 !== first.sha256))) {
    throw new TaskPanelReadError("invalid_panel_data", "Panel transfer changed identity or exceeded its budget")
  }
  let raw: string
  try { raw = atob(chunk.data) } catch { throw new TaskPanelReadError("invalid_panel_data", "Invalid panel chunk encoding") }
  if (!raw.length || raw.length > chunkBytes || chunk.next_offset !== expected.offset + raw.length ||
    chunk.next_offset > chunk.size || chunk.more !== (chunk.next_offset < chunk.size)) {
    throw new TaskPanelReadError("invalid_panel_data", "Panel chunk is incomplete")
  }
  return Uint8Array.from(raw, (character) => character.charCodeAt(0))
}

function validatePayload(value: unknown, threadId: string, panelId: string, revision: number, baseRevision?: number): TaskPanelPayload {
  if (!object(value) || (Object.hasOwn(value, "panel") === Object.hasOwn(value, "change"))) throw new TaskPanelReadError("invalid_panel_data", "Invalid panel body")
  if (object(value.panel)) {
    const panel = value.panel
    validatePanelManifest([panel], threadId)
    if (panel.id !== panelId || panel.revision !== revision || !object(panel.state) || !object(panel.definition) ||
      panel.definition.id !== panel.plugin_id || panel.definition.version !== panel.plugin_version ||
      typeof panel.definition.name !== "string" || typeof panel.definition.entry !== "string" || !object(panel.definition.schema)) {
      throw new TaskPanelReadError("invalid_panel_data", "Invalid panel snapshot identity")
    }
  } else if (object(value.change)) {
    const change = value.change
    if (change.thread_id !== threadId || change.panel_id !== panelId || change.revision !== revision ||
      change.kind !== "patch" || baseRevision === undefined || change.base_revision !== baseRevision ||
      !Array.isArray(change.operations) || change.operations.length > 256 || revision <= baseRevision) {
      throw new TaskPanelReadError("invalid_panel_data", "Invalid panel patch identity or base")
    }
  } else throw new TaskPanelReadError("invalid_panel_data", "Missing panel body")
  return value as TaskPanelPayload
}

/** Only a complete, identity-bound and checksum-verified body can reach a plugin. */
export async function readTaskPanel(link: RemoteLink, threadId: string, panelId: string, baseRevision?: number, signal?: AbortSignal): Promise<TaskPanelPayload> {
  let first: TaskPanelTransfer | undefined, bytes: Uint8Array | undefined, offset = 0
  try {
    for (;;) {
      if (signal?.aborted) throw new DOMException("Aborted", "AbortError")
      const response = await link.rpc({ op: OpTaskPanelRead, thread_id: threadId, panel_id: panelId,
        ...(first ? { transfer_id: first.id, offset } : { easm_panel_version: easmPanelVersion, base_revision: baseRevision }),
      }, { signal, dropOnTimeout: false })
      if (signal?.aborted) throw new DOMException("Aborted", "AbortError")
      if (!response.ok) throw new TaskPanelReadError(response.code ?? "panel_read_failed", response.error ?? "Panel read failed")
      const chunk = response.panel_transfer
      const part = validateChunk(chunk, { threadId, panelId, offset, first })
      if (!first) { first = { ...chunk! }; bytes = new Uint8Array(first.size) }
      bytes!.set(part, offset)
      offset = chunk!.next_offset
      if (!chunk!.more) break
    }
    if (bytesToHex(sha256(bytes!)) !== first!.sha256) throw new TaskPanelReadError("invalid_panel_data", "Panel checksum mismatch")
    let value: unknown
    try { value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)) } catch { throw new TaskPanelReadError("invalid_panel_data", "Invalid panel JSON") }
    return validatePayload(value, threadId, panelId, first!.revision, baseRevision)
  } finally {
    // The exact token prevents a late cleanup from cancelling the next read.
    if (first && link.alive()) void link.rpc({ op: OpTaskPanelCancel, thread_id: threadId, panel_id: panelId, transfer_id: first.id }, { dropOnTimeout: false }).catch(() => undefined)
  }
}
