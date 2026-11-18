import { sha256 } from "@noble/hashes/sha2.js"
import catalog from "../../../frontend/src/task-panels/catalog.json"
import type { EasmResult } from "../../../frontend/src/lib/easm/types"
import type { PanelChange, TaskPanel } from "../../../frontend/src/task-panels/types"
import { bytesToHex } from "./bytes"
import { OpTaskPanelCancel, OpTaskPanelRead, OpTaskPanels, type RemoteRequest, type RemoteResponse } from "./rpc"

type Request = Omit<RemoteRequest, "v" | "id">
type Transfer = { id: string; threadId: string; panelId: string; revision: number; bytes: Uint8Array; sha256: string }
let transfers = new WeakMap<object, Transfer>()
let panels = new Map<string, TaskPanel>()
let changes = new Map<string, PanelChange>()
let initialized = false, sequence = 0, failures = 0
const definition = catalog[0]
const easmDefinition = catalog.find((panel) => panel.id === "easm-asset-portrait")!

/** Complete, cumulative offline batches; the mock never collects real assets. */
function easmState(batch: number): EasmResult {
  const root = { id: "root|sample", kind: "root" as const, label: "示例大学", ip_count: [0, 90, 310, 670][batch] }
  const company = { id: "company|sample", kind: "company" as const, label: "示例机构" }
  const domain = { id: "domain|example.test", kind: "domain" as const, label: "example.test", detail: { reason: batch === 3 ? "第三批已复核的域名归属依据" : "本地样例域名依据" } }
  const certificate = { id: "feature|certificate", kind: "feature" as const, featureKind: "cert" as const, label: "*.example.test" }
  const domains = (names: string[]): EasmResult["nodes"] => names.map((name) => ({ id: `domain|${name}.example.test`, kind: "domain", label: `${name}.example.test` }))
  const allNodes: EasmResult["nodes"] = [root, company, domain,
    { id: "company|research", kind: "company", label: "示例研究院" },
    { id: "org|laboratory", kind: "org", label: "联合实验室" },
    ...domains(["mail", "vpn"]),
    { id: "feature|mx", kind: "feature", featureKind: "mx", label: "mx.example.test" },
    ...domains(["www", "library", "admissions", "alumni", "campus", "oa", "sso", "portal", "data", "lab"]),
    certificate,
    { id: "company|cloud", kind: "company", label: "示例云服务" },
    { id: "org|information", kind: "org", label: "信息技术中心" },
    { id: "feature|asn", kind: "feature", featureKind: "asn", label: "AS64500" },
    { id: "feature|icp", kind: "feature", featureKind: "icp", label: "示例备案 001" },
    { id: "feature|icon", kind: "feature", featureKind: "icon_hash", label: "示例站点图标" },
    ...domains(["git", "video", "api", "storage", "learn", "status", "news", "backup"]),
  ]
  const nodes = allNodes.slice(0, [0, 8, 18, 32][batch])
  const edges: EasmResult["edges"] = nodes.slice(1).map((node, index) => ({
    source: nodes[index === 1 ? 1 : Math.floor(index / 3)].id, target: node.id, confirmed: true,
  }))
  if (batch === 3) edges.push(...[[2, 18], [4, 20], [6, 26], [10, 31]].map(([source, target]) => ({ source: nodes[source].id, target: nodes[target].id })))
  const components: EasmResult["components"] = [
    { name: "nginx", display_name: "NGINX", count: 180, hot_days: 6, tags: [{ type: "hot" }, { type: "kev", summary: "本地示例标签依据" }] },
    { name: "openssl", display_name: "OpenSSL", count: 96, tags: [{ type: "cve", summary: "合成样例组件线索" }] },
    { name: "apache-tomcat", display_name: "Apache Tomcat", count: 72 },
    { name: "redis", display_name: "Redis", count: 48 },
    { name: "vue", display_name: "Vue", count: 42 },
    { name: "spring", display_name: "Spring", count: 36 },
    { name: "jquery", display_name: "jQuery", count: 24 },
  ]
  return {
    schema_version: 1, title: "EASM", nodes, edges,
    summary: { clues: nodes.length, fofa_assets: [0, 350, 1200, 2600][batch], fofa_ips: [0, 90, 310, 670][batch] },
    heat: Array.from({ length: [0, 7, 10, 14][batch] }, (_, index) => ({ date: `2026-11-${String(index + 1).padStart(2, "0")}`, value: 45 + index * 8 + (index % 3) * 12 })),
    components: components.slice(0, [0, 3, 5, 7][batch]),
  }
}

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
function addEasm() {
  panels.set("mobile-easm", { id: "mobile-easm", thread_id: "t-live", plugin_id: easmDefinition.id, plugin_version: easmDefinition.version,
    revision: 1, definition: easmDefinition, state: easmState(0) })
}
function initialize() {
  if (initialized) return
  initialized = true
  const mode = new URLSearchParams(location.search).get("panels")
  if (!mode) return
  if (mode !== "delayed" && mode !== "easm") add("mobile-map", mode === "incompatible" ? "older-version" : definition.version)
  if (mode === "multi") add("mobile-map-second")
  if (mode === "failed") failures = 1
  if (mode === "large") panels.get("mobile-map")!.state = { ...state(), synthetic_note: "合成测试数据🌍".repeat(20_000) }
}

/** The offline walkthrough exercises normal manifest/chunk RPCs and bundled renderers. */
function control(event: Event) {
  if (!new URLSearchParams(location.search).get("panels")) return
  initialize()
  const detail = (event as CustomEvent<{ kind: string; panel_id?: string }>).detail
  if (!detail) return
  if (detail.kind === "show") { new URLSearchParams(location.search).get("panels") === "easm" ? addEasm() : add(); return }
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
    panel.state = panel.plugin_id === easmDefinition.id ? easmState(Math.min(panel.revision, 3)) : state(true)
    panel.revision++; changes.delete(id)
  }
}
if (typeof window !== "undefined") window.addEventListener("zwai.mock.task-panel", control)

export function resetMockTaskPanels() {
  transfers = new WeakMap(); panels = new Map(); changes = new Map(); initialized = false; failures = 0
}
export function releaseMockTaskPanels(owner: object) { transfers.delete(owner) }

export function mockTaskPanelRPC(owner: object, request: Request, threadExists: boolean): Partial<RemoteResponse> | undefined {
  if (![OpTaskPanels, OpTaskPanelRead, OpTaskPanelCancel].includes(request.op)) return
  initialize()
  const error = (code: string): Partial<RemoteResponse> => ({ ok: false, code, error: code })
  if (!threadExists) return error("not_found")
  if (request.op === OpTaskPanels) return { task_panels: [...panels.values()].filter((panel) => panel.thread_id === request.thread_id).map(({ id, thread_id, plugin_id, plugin_version, revision, source_status }) => ({ id, thread_id, plugin_id, plugin_version, revision, source_status })) }
  let transfer = transfers.get(owner)
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
