import { bytesToHex } from "@noble/hashes/utils.js"
import { sha256 } from "@noble/hashes/sha2.js"
import { isKnowledgePageID } from "../../../frontend/src/lib/knowledge-link-ref"
import type { KnowledgePage, KnowledgePageRef, KnowledgeSource } from "../../../frontend/src/lib/knowledge"
import type { RemoteLink } from "./link"
import { OpKnowledgeCancel, OpKnowledgePage, OpProjectKnowledge, type KnowledgeChunk } from "./rpc"

const chunkBytes = 36 << 10
const maxTransferBytes = 16 << 20
const listSize = 32

export class KnowledgeReadError extends Error {
  constructor(readonly code: string) { super(code) }
}

const invalid = () => new KnowledgeReadError("invalid_knowledge_data")
function ensureActive(signal: AbortSignal) {
  if (signal.aborted) throw new DOMException("Aborted", "AbortError")
}
const object = (value: unknown): value is Record<string, unknown> => Boolean(value && typeof value === "object" && !Array.isArray(value))
const pageRef = (value: unknown): value is KnowledgePageRef =>
  object(value) && typeof value.id === "string" && isKnowledgePageID(value.id) &&
  typeof value.title === "string" && typeof value.type === "string"

export async function readKnowledgeList(link: RemoteLink, projectId: string, query: string, offset: number, signal: AbortSignal) {
  const response = await link.rpc({ op: OpProjectKnowledge, project_id: projectId, text: query, offset }, { signal, dropOnTimeout: false })
  ensureActive(signal)
  if (!response.ok) throw new KnowledgeReadError(response.code ?? "read_failed")
  const result = response.knowledge
  if (!result || result.project_id !== projectId || typeof result.enabled !== "boolean" ||
    !Array.isArray(result.pages) || result.pages.length > listSize || !result.pages.every(pageRef) ||
    (!result.enabled && (result.pages.length > 0 || result.next_offset !== undefined)) ||
    (result.next_offset !== undefined && (result.pages.length !== listSize || result.next_offset !== offset + listSize))) {
    throw invalid()
  }
  return result
}

function chunkData(chunk: KnowledgeChunk | undefined, projectId: string, pageId: string, offset: number, first?: KnowledgeChunk) {
  if (!chunk || !/^[0-9a-f]{32}$/.test(chunk.id) || chunk.project_id !== projectId || chunk.page_id !== pageId ||
    !/^[0-9a-f]{64}$/.test(chunk.sha256) || !Number.isSafeInteger(chunk.size) || chunk.size <= 0 || chunk.size > maxTransferBytes ||
    chunk.offset !== offset || typeof chunk.more !== "boolean" || typeof chunk.data !== "string" ||
    chunk.data.length > 4 * Math.ceil(chunkBytes / 3) ||
    (first && (chunk.id !== first.id || chunk.sha256 !== first.sha256 || chunk.size !== first.size))) throw invalid()
  let decoded: string
  try { decoded = atob(chunk.data) } catch { throw invalid() }
  if (decoded.length !== Math.min(chunkBytes, chunk.size - offset) || decoded.length === 0 ||
    chunk.next_offset !== offset + decoded.length || chunk.more !== (chunk.next_offset < chunk.size)) throw invalid()
  return Uint8Array.from(decoded, (char) => char.charCodeAt(0))
}

function source(value: unknown): value is KnowledgeSource {
  return object(value) && (value.uri === undefined || typeof value.uri === "string") &&
    (value.thread_id === undefined || typeof value.thread_id === "string") &&
    (value.seq === undefined || Number.isSafeInteger(value.seq)) &&
    (value.timestamp === undefined || typeof value.timestamp === "string") &&
    (value.available === undefined || typeof value.available === "boolean")
}

function arrayOf(value: unknown, check: (item: unknown) => boolean) {
  return value === undefined || (Array.isArray(value) && value.every(check))
}

function parsePage(bytes: Uint8Array, pageId: string): KnowledgePage {
  let value: unknown
  try { value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)) } catch { throw invalid() }
  const connection = (entry: unknown) => object(entry) && typeof entry.label === "string" &&
    typeof entry.target === "string" && (entry.page === undefined || pageRef(entry.page))
  if (!object(value) || !pageRef(value) || value.id !== pageId || typeof value.body !== "string" ||
    !arrayOf(value.incoming, connection) || !arrayOf(value.outgoing, connection) ||
    !arrayOf(value.mentions, pageRef) || !arrayOf(value.sources, source) ||
    !arrayOf(value.wiki_observations, (entry) => object(entry) && source(entry.source) && typeof entry.quote === "string")) throw invalid()
  return value as unknown as KnowledgePage
}

export async function readKnowledgePage(link: RemoteLink, projectId: string, pageId: string, signal: AbortSignal) {
  let first: KnowledgeChunk | undefined, bytes: Uint8Array | undefined, offset = 0
  const scope = { project_id: projectId, knowledge_page_id: pageId }
  try {
    for (;;) {
      ensureActive(signal)
      const response = await link.rpc({ op: OpKnowledgePage, ...scope, offset,
        ...(first ? { knowledge_transfer_id: first.id } : {}),
      }, { signal, dropOnTimeout: false })
      ensureActive(signal)
      if (!response.ok) throw new KnowledgeReadError(response.code ?? "read_failed")
      const chunk = response.knowledge_chunk
      const data = chunkData(chunk, projectId, pageId, offset, first)
      if (!first) { first = chunk!; bytes = new Uint8Array(first.size) }
      bytes!.set(data, offset)
      offset = chunk!.next_offset
      if (!chunk!.more) break
    }
    if (bytesToHex(sha256(bytes!)) !== first!.sha256) throw invalid()
    return parsePage(bytes!, pageId)
  } finally {
    if (first && offset < first.size && link.alive()) {
      void link.rpc({ op: OpKnowledgeCancel, ...scope, knowledge_transfer_id: first.id }, { dropOnTimeout: false }).catch(() => undefined)
    }
  }
}
