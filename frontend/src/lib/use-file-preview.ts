import { useEffect, useReducer, useState } from "react"

import { readFilePreview, type FilePreview } from "@/lib/file-preview"
import type { FileLocation } from "@/lib/types"

type PreviewState = { key: string; file?: FilePreview; url?: string; error?: string }

/** Each selection owns its request and image URL. Closing or changing the
 * conversation must not let a late file replace the currently visible one. */
export function useFilePreview(threadId: string, path: string, location?: FileLocation) {
  const [revision, refresh] = useReducer((n: number) => n + 1, 0)
  const { scope, root } = location ?? {}
  const key = JSON.stringify([threadId, path, scope, root, revision])
  const [state, setState] = useState<PreviewState>()

  useEffect(() => {
    const controller = new AbortController()
    let url: string | undefined
    const load = scope
      ? readFilePreview(threadId, path, controller.signal, { scope, root })
      : readFilePreview(threadId, path, controller.signal)
    void load.then((file) => {
      if (controller.signal.aborted) return
      if (file.kind === "image") url = URL.createObjectURL(file.blob)
      setState({ key, file, url })
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) {
        setState({ key, error: cause instanceof Error ? cause.message : String(cause) })
      }
    })
    return () => {
      controller.abort()
      if (url) URL.revokeObjectURL(url)
    }
  }, [threadId, path, scope, root, key])

  const current = state?.key === key ? state : undefined
  return { ...current, loading: !current, refresh }
}
