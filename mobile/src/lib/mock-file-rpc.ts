import { mockFileChunk, mockListedFiles, mockScopedFiles, type mockOutputFiles } from "./mock-files"
import { mockGeneratedImageChunk } from "./mock-generated-image"
import { mockInputImageChunk } from "./mock-transcript-images"
import { OpFileChunk, OpFiles, OpGeneratedImageChunk, OpInputImageChunk, type RemoteRequest, type RemoteResponse } from "./rpc"

export async function mockFileRPC(
  req: Omit<RemoteRequest, "v" | "id">,
  threadId: string | undefined,
  outputFiles: ReturnType<typeof mockOutputFiles>,
): Promise<Partial<RemoteResponse>> {
  const missing = { ok: false, error: "no such thread" }
  if (!threadId) return missing
  const before = req.before ?? 0
  if (req.op === OpGeneratedImageChunk || req.op === OpInputImageChunk) {
    const file_chunk = req.op === OpGeneratedImageChunk
      ? await mockGeneratedImageChunk(req.image_id, before)
      : threadId === "t-recent" ? await mockInputImageChunk(req.image_id, before) : undefined
    return file_chunk ? { file_chunk } : missing
  }
  const scoped = new URLSearchParams(location.search).get("file-preview") === "task-workspaces"
    ? mockScopedFiles(req.file_scope === "project", location.search) : undefined
  const files = scoped?.files ?? outputFiles
  if (scoped && (req.op === OpFileChunk || req.file_root) && req.file_root !== scoped.view.root) {
    return { ok: false, code: "file_root_changed" }
  }
  if (req.op === OpFiles) {
    return { files: mockListedFiles(files, req.directory, req.text, scoped?.folders), file_view: scoped?.view, more: false }
  }
  const file_chunk = await mockFileChunk(files, req.file_path ?? "", before)
  return file_chunk ? { file_chunk, file_view: scoped?.view } : missing
}
