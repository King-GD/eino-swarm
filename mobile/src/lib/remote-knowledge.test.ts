import { bytesToHex } from "@noble/hashes/utils.js"
import { sha256 } from "@noble/hashes/sha2.js"
import { describe, expect, it, vi } from "vitest"
import type { RemoteLink } from "./link"
import { readKnowledgeList, readKnowledgePage } from "./remote-knowledge"
import { OpKnowledgeCancel, OpKnowledgePage, type RemoteRequest, type RemoteResponse } from "./rpc"

const body = "完整内容 ".repeat(5200)
const encoded = new TextEncoder().encode(JSON.stringify({ id: "record", type: "entry", title: "Record", body }))
const hash = bytesToHex(sha256(encoded))
const transfer = "1".repeat(32)

function chunk(offset = 0, project = "project") {
  const end = Math.min(offset + (36 << 10), encoded.length)
  return {
    id: transfer, project_id: project, page_id: "record", sha256: hash, size: encoded.length, offset, next_offset: end,
    data: btoa(String.fromCharCode(...encoded.subarray(offset, end))), more: end < encoded.length,
  }
}

const ok = (fields: Partial<RemoteResponse> = {}): RemoteResponse => ({ v: 1, id: "request", ok: true, ...fields })
const linkOf = (rpc: unknown) => ({ alive: () => true, rpc }) as RemoteLink
const signal = () => new AbortController().signal

describe("paired phone knowledge reads", () => {
  it("waits for the complete multi-frame Markdown including split UTF-8 bytes", async () => {
    const rpc = vi.fn(async (request: Partial<RemoteRequest>) => ok({ knowledge_chunk: chunk(request.offset) }))
    const page = await readKnowledgePage(linkOf(rpc), "project", "record", signal())
    expect(page.body).toBe(body)
    expect(rpc.mock.calls.length).toBeGreaterThan(1)
    expect(rpc.mock.calls[1][0]).toMatchObject({
      op: OpKnowledgePage, project_id: "project", knowledge_page_id: "record", knowledge_transfer_id: transfer,
    })
  })

  it("rejects a chunk from another project and retires only the original token", async () => {
    const rpc = vi.fn(async (request: Partial<RemoteRequest>) => {
      if (request.op === OpKnowledgeCancel) return ok()
      return ok({ knowledge_chunk: chunk(request.offset, request.offset ? "other-project" : "project") })
    })
    await expect(readKnowledgePage(linkOf(rpc), "project", "record", signal())).rejects.toMatchObject({ code: "invalid_knowledge_data" })
    expect(rpc.mock.lastCall?.[0]).toEqual({
      op: OpKnowledgeCancel, project_id: "project", knowledge_page_id: "record", knowledge_transfer_id: transfer,
    })
  })

  it("rejects a corrupt checksum and a listing with mismatched scope", async () => {
    const rpc = vi.fn(async (request: Partial<RemoteRequest>) => ok({
      knowledge_chunk: { ...chunk(request.offset), sha256: "0".repeat(64) },
    }))
    await expect(readKnowledgePage(linkOf(rpc), "project", "record", signal())).rejects.toMatchObject({ code: "invalid_knowledge_data" })
    const other = vi.fn(async () => ok({ knowledge: { project_id: "other-project", enabled: true, pages: [] } }))
    await expect(readKnowledgeList(linkOf(other), "project", "", 0, signal())).rejects.toMatchObject({ code: "invalid_knowledge_data" })
  })

  it("rejects ambiguous next offsets and reports a legacy PC distinctly", async () => {
    const rpc = vi.fn(async () => ok({ knowledge: { project_id: "project", enabled: true, pages: [], next_offset: 100 } }))
    await expect(readKnowledgeList(linkOf(rpc), "project", "", 0, signal())).rejects.toMatchObject({ code: "invalid_knowledge_data" })
    const old = vi.fn(async () => ok({ ok: false, code: "unknown_op" }))
    await expect(readKnowledgeList(linkOf(old), "project", "", 0, signal())).rejects.toMatchObject({ code: "unknown_op" })
  })
})
