import { expect, it, vi } from "vitest"
import { sha256 } from "@noble/hashes/sha2.js"
import { bytesToHex } from "./bytes"
import type { RemoteLink } from "./link"
import { listTaskPanels, maxTaskPanelTransferBytes, readTaskPanel } from "./remote-task-panels"
import { OpTaskPanelCancel, OpTaskPanelRead, PROTOCOL_V, type RemoteRequest, type RemoteResponse, type TaskPanelTransfer } from "./rpc"

const panel = { id: "p", thread_id: "t", plugin_id: "map", plugin_version: "1", revision: 2,
  definition: { id: "map", version: "1", name: "Map", description: "Map", entry: "task-panels/map.html", schema: { type: "object" } }, state: { value: "海🌍".repeat(15_000) } }
const base = { v: PROTOCOL_V, id: "r", ok: true }
const bytesOf = (value: unknown) => new TextEncoder().encode(JSON.stringify(value))
function transfer(body: unknown, request: Pick<RemoteRequest, "offset">): TaskPanelTransfer {
  const bytes = bytesOf(body), offset = request.offset ?? 0, end = Math.min(offset + (36 << 10), bytes.length)
  return { id: "immutable-transfer", thread_id: "t", panel_id: "p", revision: 2, size: bytes.length,
    sha256: bytesToHex(sha256(bytes)), offset, next_offset: end, more: end < bytes.length,
    data: btoa(Array.from(bytes.slice(offset, end), (n) => String.fromCharCode(n)).join("")) }
}
function reader(body: unknown, alter?: (chunk: TaskPanelTransfer, request: Omit<RemoteRequest, "v" | "id">) => void) {
  const rpc = vi.fn(async (request: Omit<RemoteRequest, "v" | "id">): Promise<RemoteResponse> => {
    if (request.op === OpTaskPanelCancel) return base
    const chunk = transfer(body, request)
    alter?.(chunk, request)
    return { ...base, panel_transfer: chunk }
  })
  return { link: { rpc, alive: () => true } as unknown as RemoteLink, rpc }
}

it("assembles a large UTF-8 snapshot before exposing it and releases only its own token", async () => {
  const { link, rpc } = reader({ panel })
  expect(await readTaskPanel(link, "t", "p")).toEqual({ panel })
  const reads = rpc.mock.calls.filter(([r]) => r.op === OpTaskPanelRead)
  expect(reads.length).toBeGreaterThan(2)
  expect(reads[1][0]).toMatchObject({ transfer_id: "immutable-transfer", thread_id: "t", panel_id: "p", offset: 36 << 10 })
  expect(rpc).toHaveBeenLastCalledWith({ op: OpTaskPanelCancel, thread_id: "t", panel_id: "p", transfer_id: "immutable-transfer" }, { dropOnTimeout: false })
})

it.each(["revision", "id", "sha256", "size", "thread", "panel"])("rejects mixed %s across chunks", async (field) => {
  const { link } = reader({ panel }, (chunk, request) => {
    if (!request.transfer_id) return
    if (field === "revision") chunk.revision++
    if (field === "id") chunk.id = "successor-transfer"
    if (field === "sha256") chunk.sha256 = "0".repeat(64)
    if (field === "size") chunk.size++
    if (field === "thread") chunk.thread_id = "other"
    if (field === "panel") chunk.panel_id = "other"
  })
  await expect(readTaskPanel(link, "t", "p")).rejects.toThrow("changed identity")
})

it.each(["partial", "oversize", "offset", "encoding", "checksum"])("refuses %s bodies without publishing a partial projection", async (kind) => {
  const { link } = reader({ panel }, (chunk) => {
    if (kind === "partial") chunk.more = false
    if (kind === "oversize") chunk.size = maxTaskPanelTransferBytes + 1
    if (kind === "offset") chunk.next_offset++
    if (kind === "encoding") chunk.data = "!"
    if (kind === "checksum") chunk.sha256 = "0".repeat(64)
  })
  await expect(readTaskPanel(link, "t", "p")).rejects.toThrow()
})

it("binds a patch to the applied base and validates both envelope and snapshot identity", async () => {
  const change = { thread_id: "t", panel_id: "p", revision: 2, base_revision: 1, cursor: 9, kind: "patch", operations: [{ op: "replace", path: "/count", value: 2 }] }
  expect(await readTaskPanel(reader({ change }).link, "t", "p", 1)).toEqual({ change })
  await expect(readTaskPanel(reader({ change }).link, "t", "p", 0)).rejects.toThrow("base")
  await expect(readTaskPanel(reader({ panel: { ...panel, id: "wrong" } }).link, "t", "p")).rejects.toThrow("snapshot identity")
  await expect(readTaskPanel(reader({ panel, change }).link, "t", "p")).rejects.toThrow("body")
})

it("fences cancellation that arrives while a sealed read is pending", async () => {
  const controller = new AbortController(), { link } = reader({ panel }, () => controller.abort())
  await expect(readTaskPanel(link, "t", "p", undefined, controller.signal)).rejects.toMatchObject({ name: "AbortError" })
})

it("keeps an unknown older host separate from an empty manifest", async () => {
  const rpc = vi.fn().mockResolvedValueOnce({ ...base, task_panels: [] }).mockResolvedValueOnce({ ...base, ok: false, code: "unknown_op" })
    .mockResolvedValueOnce({ ...base, task_panels: [{ id: "p", thread_id: "other", plugin_id: "map", plugin_version: "1", revision: 1 }] })
  const link = { rpc } as unknown as RemoteLink
  expect(await listTaskPanels(link, "t")).toEqual([])
  await expect(listTaskPanels(link, "t")).rejects.toMatchObject({ code: "unknown_op" })
  await expect(listTaskPanels(link, "t")).rejects.toThrow("manifest")
})
