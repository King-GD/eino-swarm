import { ApiError } from "./api"
import type { NuwaSnapshot, ThreadWorkSnapshot, WorkVersion } from "./nuwa"

async function checked(response: Response): Promise<Response> {
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as { error?: string; code?: string }
    throw new ApiError(body.error || response.statusText, response.status, body.code)
  }
  return response
}

export const nuwaAPI = {
  async snapshot(projectId: string): Promise<NuwaSnapshot> {
    const response = await checked(await fetch(`/api/projects/${encodeURIComponent(projectId)}/nuwa`))
    return response.json() as Promise<NuwaSnapshot>
  },
  async threadSnapshot(threadId: string): Promise<{ view: ThreadWorkSnapshot; tag?: string }> {
    const response = await checked(await fetch(`/api/threads/${encodeURIComponent(threadId)}/work`))
    return { view: await response.json() as ThreadWorkSnapshot, tag: response.headers.get("ETag") ?? undefined }
  },
  async distill(threadId: string, tag: string, signal: AbortSignal): Promise<{ created: number }> {
    const response = await checked(await fetch(`/api/threads/${encodeURIComponent(threadId)}/work/distill`, {
      method: "POST", headers: { "If-Match": tag }, signal,
    }))
    return response.json()
  },
  async promote(threadId: string, ids: string[], tag: string): Promise<WorkVersion> {
    const response = await checked(await fetch(`/api/threads/${encodeURIComponent(threadId)}/work/promote`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "If-Match": tag },
      body: JSON.stringify({ ids }),
    }))
    return response.json() as Promise<WorkVersion>
  },
}

/** The project stream only invalidates the snapshot. A revision is not a
 * conversation seq and must never be folded into transcript replay. */
export function subscribeNuwa(
  projectId: string,
  handlers: {
    onRevision: (revision: number) => void
    onOpen: () => void
    onError: () => void
  }
): () => void {
  const source = new EventSource(`/api/projects/${encodeURIComponent(projectId)}/nuwa/events`)
  let closed = false
  source.onopen = () => {
    if (!closed) handlers.onOpen()
  }
  source.onerror = () => {
    if (!closed) handlers.onError()
  }
  source.addEventListener("nuwa_updated", (event) => {
    if (closed) return
    try {
      const body = JSON.parse((event as MessageEvent).data) as {
        project_id: string
        revision: number
      }
      if (
        body.project_id === projectId &&
        Number.isSafeInteger(body.revision) &&
        body.revision >= 0
      )
        handlers.onRevision(body.revision)
    } catch {
      /* A malformed notification cannot replace the last snapshot. */
    }
  })
  return () => {
    closed = true
    source.close()
  }
}
