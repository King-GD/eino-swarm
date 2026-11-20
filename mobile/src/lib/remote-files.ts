import type { RemoteLink } from "./link"
import { OpFileChunk, OpFiles, OpGeneratedImageChunk, type RemoteRequest, type WorkspaceFile } from "./rpc"
import { documentFormat } from "../../../frontend/src/lib/documents/kind"
import type { FileLocation, FileView } from "../../../frontend/src/lib/types"

// One response is one directory or one search. A long path may leave room for
// only one entry per sealed frame, so the phone follows every page. A listing
// that never finishes is an error, not a shorter tree.
const maxListPages = 2000
export const maxPhoneFileBytes = 32 * 1024 * 1024
export const maxInlinePreviewBytes = 8 * 1024 * 1024

export class RemoteFileError extends Error {
  constructor(readonly code: string, message: string) {
    super(message)
  }
}

export async function listWorkspaceFiles(
  link: RemoteLink,
  threadId: string,
  directory = "",
  query = "",
): Promise<WorkspaceFile[]> {
  return (await listWorkspaceView(link, threadId, directory, query)).files
}

export async function listWorkspaceView(
  link: RemoteLink, threadId: string, directory = "", query = "", location?: FileLocation,
): Promise<{ files: WorkspaceFile[]; view?: FileView }> {
  const files: WorkspaceFile[] = []
  const seen = new Set<string>()
  let cursor = ""
  let view: FileView | undefined
  let root = location?.root
  for (let page = 0; page < maxListPages; page++) {
    const resp = await link.rpc({
      op: OpFiles,
      thread_id: threadId,
      cursor: cursor || undefined,
      directory: directory || undefined,
      text: query || undefined,
      ...(location?.scope === "project" ? { file_scope: location.scope } : {}),
      ...(root ? { file_root: root } : {}),
    })
    if (!resp.ok) throw new RemoteFileError(resp.code || "remote_error", resp.error || "file listing failed")
    if ((root && resp.file_view?.root !== root) || (location?.scope === "project" && !resp.file_view)) {
      throw new RemoteFileError("file_root_changed", "File directory changed; refresh the file list")
    }
    view = resp.file_view
    root = view?.root
    for (const file of resp.files ?? []) {
      if (seen.has(file.path)) throw new Error("file listing repeated an entry")
      seen.add(file.path)
      files.push(file)
    }
    if (!resp.more) return { files, view }
    if (!resp.next || resp.next === cursor) throw new Error("file listing cursor did not advance")
    cursor = resp.next
  }
  throw new Error("file listing has too many pages")
}

export async function readWorkspaceFile(
  link: RemoteLink,
  threadId: string,
  path: string,
  options?: {
    signal: AbortSignal
    onProgress?: (loaded: number, total: number) => void
    onSlow?: () => void
  },
  location?: FileLocation,
): Promise<{ bytes: Uint8Array<ArrayBuffer>; mime: string }> {
  return readFileChunks(link, path, {
    op: OpFileChunk, thread_id: threadId, file_path: path,
    ...(location?.scope === "project" ? { file_scope: location.scope } : {}),
    ...(location?.root ? { file_root: location.root } : {}),
  }, maxPhoneFileBytes, options)
}

export async function readGeneratedImage(link: RemoteLink, threadId: string, imageId: string, signal: AbortSignal): Promise<Blob> {
  const { bytes, mime } = await readFileChunks(link, imageId,
    { op: OpGeneratedImageChunk, thread_id: threadId, image_id: imageId }, maxInlinePreviewBytes, { signal })
  if (!["image/png", "image/jpeg", "image/webp"].includes(mime) || bytes.length === 0) {
    throw new RemoteFileError("image_invalid_response", "invalid generated image")
  }
  return new Blob([bytes], { type: mime })
}

async function readFileChunks(link: RemoteLink, id: string, request: Omit<RemoteRequest, "before" | "v" | "id">, maxBytes: number, options?: {
  signal: AbortSignal
  onProgress?: (loaded: number, total: number) => void
  onSlow?: () => void
}): Promise<{ bytes: Uint8Array<ArrayBuffer>; mime: string }> {
  const signal = options?.signal
  const rpcOptions = options && { signal, onSlow: options.onSlow }
  let assembled: Uint8Array<ArrayBuffer> | undefined
  let offset = 0
  let mime = "application/octet-stream"
  for (;;) {
    if (signal?.aborted) throw new DOMException("Aborted", "AbortError")
    const resp = await link.rpc({ ...request, before: offset }, rpcOptions)
    if (signal?.aborted) throw new DOMException("Aborted", "AbortError")
    const chunk = resp.file_chunk
    if (!resp.ok || !chunk) throw new RemoteFileError(resp.code || "remote_error", resp.error || "file read failed")
    if (request.file_root && resp.file_view?.root !== request.file_root) {
      throw new RemoteFileError("file_root_changed", "File directory changed; refresh the file list")
    }
    if (chunk.path !== id || chunk.offset !== offset || !Number.isSafeInteger(chunk.size) || chunk.size < 0 ||
      (assembled && (chunk.size !== assembled.length || chunk.mime !== mime))) throw new Error("file changed while reading")
    if (chunk.size > maxBytes) throw new RemoteFileError("file_too_large", "file exceeds the phone read limit")
    if (typeof chunk.data !== "string" || typeof chunk.mime !== "string") throw new Error("file chunk is incomplete")
    const bytes = base64Bytes(chunk.data)
    if (chunk.next_offset !== offset + bytes.length || chunk.next_offset > chunk.size ||
      chunk.more !== (chunk.next_offset < chunk.size)) throw new Error("file chunk is incomplete")
    if (chunk.more && bytes.length === 0) throw new Error("file chunk made no progress")
    if (!assembled) {
      assembled = new Uint8Array(chunk.size)
      mime = chunk.mime
    }
    assembled.set(bytes, offset)
    offset = chunk.next_offset
    options?.onProgress?.(offset, chunk.size)
    if (!chunk.more) return { bytes: assembled, mime }
  }
}

function base64Bytes(data: string): Uint8Array {
  const raw = atob(data)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

export type PreviewKind = "text" | "image" | "pdf" | "download" | "document"

export function previewKind(name: string, mime: string, size: number): PreviewKind {
  if (documentFormat(name)) return size <= maxInlinePreviewBytes ? "document" : "download"
  const kind = mime.toLowerCase()
  if (["image/png", "image/jpeg", "image/gif", "image/webp"].includes(kind)) {
    return size <= maxPhoneFileBytes ? "image" : "download"
  }
  if (size > maxInlinePreviewBytes) return "download"
  if (kind === "application/pdf") return "pdf"
  if (kind.startsWith("text/") || ["application/json", "application/xml", "application/javascript"].includes(kind) ||
    /\.(md|markdown|mdx|csv|tsv|json|ya?ml|xml|svg|go|py|tsx?|jsx?|css|html?)$/i.test(name)) return "text"
  return "download"
}
