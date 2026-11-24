import { sha256 } from "@noble/hashes/sha2.js"
import catalog from "../../../frontend/src/task-panels/catalog.json"
import type { PanelChange, TaskPanel } from "../../../frontend/src/task-panels/types"
import { bytesToHex } from "./bytes"
import { OpTaskPanelCancel, OpTaskPanelClose, OpTaskPanelRead, OpTaskPanels, type RemoteRequest, type RemoteResponse } from "./rpc"

type Request = Omit<RemoteRequest, "v" | "id">
type Transfer = { id: string; threadId: string; panelId: string; revision: number; bytes: Uint8Array; sha256: string }
let transfers = new WeakMap<object, Transfer>()
let panels = new Map<string, TaskPanel>()
let changes = new Map<string, PanelChange>()
let initialized = false, sequence = 0, failures = 0
const definition = catalog[0]

function state(finished = false) {
  const rows = [
    { id: "asset-a", ip: "203.0.113.1", country: "CN", longitude: 116.4, latitude: 39.9 },
    { id: "asset-b", ip: "198.51.100.2", country: "CN", longitude: 121.5, latitude: 31.2 },
    { id: "asset-c", ip: "192.0.2.3", country: "US", longitude: -74, latitude: 40.7 },
  ]
  return {
    collection: { phase: finished ? "finished" : "collecting", completeness: finished ? "complete" : "unknown", received: 3, discovered: 3 },
    assets_by_id: Object.fromEntries(rows.map((asset, index) => [asset.id, { ...asset, port: 443, protocol: "https", host: `${asset.id}.example`, region: "", city: "", status: finished ? index === 2 ? "no_response" : "responded" : "pending" }])),
    probe: { pending: finished ? 0 : 3, running: 0, responded: finished ? 2 : 0, no_response: finished ? 1 : 0, skipped: 0, interrupted: 0 },
    results_by_id: {},
  }
}
function add(id = "mobile-map", version = definition.version) {
  panels.set(id, { id, thread_id: "t-live", plugin_id: definition.id, plugin_version: version, revision: 1,
    definition: { ...definition, version }, state: state() })
}
function initialize() {
  if (initialized) return
  initialized = true
  const mode = new URLSearchParams(location.search).get("panels")
  if (!mode || mode === "easm") return
  if (mode !== "delayed") add("mobile-map", mode === "incompatible" ? "older-version" : definition.version)
  if (mode === "multi") add("mobile-map-second")
  if (mode === "failed") failures = 1
  if (mode === "large") panels.get("mobile-map")!.state = { ...state(), synthetic_note: "合成测试数据🌍".repeat(20_000) }
}

/** This event exists only in the offline walkthrough. It exercises normal
 * manifest/chunk RPCs and the shipped panel renderer. */
function control(event: Event) {
  const mode = new URLSearchParams(location.search).get("panels")
  if (!mode) return
  initialize()
  const detail = (event as CustomEvent<{ kind: string; panel_id?: string; state?: unknown }>).detail
  if (!detail) return
  if (mode === "easm") {
    if (detail.kind !== "snapshot" || !detail.panel_id || !detail.state) return
    const easm = catalog.find((item) => item.id === "easm-asset-portrait")
    if (!easm) return
    const previous = panels.get(detail.panel_id)
    panels.set(detail.panel_id, { id: detail.panel_id, thread_id: "t-live", plugin_id: easm.id, plugin_version: easm.version,
      revision: (previous?.revision ?? 0) + 1, definition: easm, state: detail.state })
    return
  }
  if (detail.kind === "show") { add(); return }
  if (detail.kind === "remove") { panels.delete(detail.panel_id ?? "mobile-map"); return }
  const id = detail.panel_id ?? "mobile-map", panel = panels.get(id)
  if (!panel) return
  const previous = panel.revision
  if (detail.kind === "patch") {
    const next = structuredClone(panel.state) as ReturnType<typeof state>
    next.assets_by_id["asset-a"].status = "responded"
    next.probe.pending = 2; next.probe.responded = 1
    panel.state = next; panel.revision++
    changes.set(id, { thread_id: panel.thread_id, panel_id: id, revision: panel.revision, base_revision: previous, cursor: panel.revision, kind: "patch", operations: [
      { op: "replace", path: "/assets_by_id/asset-a/status", value: "responded" },
      { op: "replace", path: "/probe/pending", value: 2 }, { op: "replace", path: "/probe/responded", value: 1 },
    ] })
  } else if (detail.kind === "snapshot") {
    panel.state = state(true); panel.revision++; changes.delete(id)
  }
}
if (typeof window !== "undefined") window.addEventListener("zwai.mock.task-panel", control)

export function resetMockTaskPanels() {
  transfers = new WeakMap(); panels = new Map(); changes = new Map(); initialized = false; failures = 0
}
export function releaseMockTaskPanels(owner: object) { transfers.delete(owner) }

export function mockTaskPanelRPC(owner: object, request: Request, threadExists: boolean): Partial<RemoteResponse> | undefined {
  if (![OpTaskPanels, OpTaskPanelRead, OpTaskPanelCancel, OpTaskPanelClose].includes(request.op)) return
  initialize()
  const error = (code: string): Partial<RemoteResponse> => ({ ok: false, code, error: code })
  if (!threadExists) return error("not_found")
  if (request.op === OpTaskPanels) return { task_panels: [...panels.values()].filter((panel) => panel.thread_id === request.thread_id).map(({ id, thread_id, plugin_id, plugin_version, revision, source_status }) => ({ id, thread_id, plugin_id, plugin_version, revision, source_status })) }
  let transfer = transfers.get(owner)
  if (request.op === OpTaskPanelClose) {
    const panel = panels.get(request.panel_id ?? "")
    if (!panel || panel.thread_id !== request.thread_id) return error("not_found")
    panels.delete(panel.id)
    changes.delete(panel.id)
    if (transfer?.panelId === panel.id) transfers.delete(owner)
    return { ok: true }
  }
  if (request.transfer_id) {
    if (!transfer || transfer.id !== request.transfer_id || transfer.threadId !== request.thread_id || transfer.panelId !== request.panel_id) return error("transfer_expired")
    if (request.op === OpTaskPanelCancel) { transfers.delete(owner); return { ok: true } }
  } else {
    if (request.op === OpTaskPanelCancel) return error("bad_request")
    if (failures > 0) { failures--; return error("read_failed") }
    const panel = panels.get(request.panel_id ?? "")
    if (!panel || panel.thread_id !== request.thread_id) return error("not_found")
    const change = changes.get(panel.id)
    const body = change?.revision === panel.revision && change.base_revision === request.base_revision ? { change } : { panel }
    const bytes = new TextEncoder().encode(JSON.stringify(body))
    transfer = { id: `mock-panel-${++sequence}`, threadId: panel.thread_id, panelId: panel.id, revision: panel.revision, bytes, sha256: bytesToHex(sha256(bytes)) }
    transfers.set(owner, transfer)
  }
  const offset = request.offset ?? 0
  if (!Number.isSafeInteger(offset) || offset < 0 || offset >= transfer!.bytes.length) return error("bad_request")
  const end = Math.min(offset + (36 << 10), transfer!.bytes.length)
  return { panel_transfer: { id: transfer!.id, thread_id: transfer!.threadId, panel_id: transfer!.panelId, revision: transfer!.revision,
    size: transfer!.bytes.length, sha256: transfer!.sha256, offset, next_offset: end, more: end < transfer!.bytes.length,
    data: btoa(Array.from(transfer!.bytes.slice(offset, end), (byte) => String.fromCharCode(byte)).join("")),
  } }
}
