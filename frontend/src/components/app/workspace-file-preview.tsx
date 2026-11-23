import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"

import { FilePreviewDialog } from "@/components/app/file-preview"
import {
  WorkspaceFileOpenProvider,
  type OpenWorkspaceFile,
  type OpenWorkspaceFileOptions,
} from "@/components/app/workspace-file-open"
import { api } from "@/lib/api"
import type { FileLocation } from "@/lib/types"
import { chooseWorkspaceFile } from "@/lib/workspace-file-ref"
import { useApp } from "@/store/app"

type Preview = { threadId: string; path: string; location?: FileLocation }

function taskPreviewLocation(): FileLocation | undefined {
  const { fileScope, fileView } = useApp.getState()
  if (fileScope === "task" && fileView?.root) return { scope: "task", root: fileView.root }
  return undefined
}

/** One preview for the file tree and for paths named in the conversation. */
export function WorkspaceFilePreview({ children }: { children: ReactNode }) {
  const threadId = useApp((s) => s.activeId)
  const canReveal = useApp((s) => Boolean(s.meta?.capabilities?.reveal))
  const [preview, setPreview] = useState<Preview>()
  const returnFocus = useRef<HTMLElement | null>(null)
  const ticket = useRef(0)

  useEffect(() => {
    ticket.current += 1
    setPreview(undefined)
  }, [threadId])

  const open = useCallback<OpenWorkspaceFile>(async (path, options) => {
    const id = useApp.getState().activeId
    if (!id || !path) return
    const mine = ++ticket.current
    returnFocus.current = options?.returnFocus ?? null
    const location = previewLocation(options)
    let resolved = path
    if (!options?.listed && !useApp.getState().files.some((file) => !file.dir && file.path === path)) {
      try {
        const found = await api.searchFiles(id, path, location)
        if (mine !== ticket.current || useApp.getState().activeId !== id) return
        resolved = chooseWorkspaceFile(path, found.files)
      } catch {
        if (mine !== ticket.current || useApp.getState().activeId !== id) return
      }
    }
    if (mine !== ticket.current || useApp.getState().activeId !== id) return
    setPreview({ threadId: id, path: resolved, location })
  }, [])

  const shown = preview && preview.threadId === threadId ? preview : undefined
  return (
    <WorkspaceFileOpenProvider open={threadId ? open : null}>
      {children}
      {shown ? (
        <FilePreviewDialog
          key={`${shown.threadId}/${shown.path}`}
          threadId={shown.threadId}
          path={shown.path}
          location={shown.location}
          canReveal={canReveal}
          onReveal={(path) => {
            void api.reveal(shown.threadId, path, shown.location).catch(() => undefined)
          }}
          onClose={() => setPreview(undefined)}
          onReturnFocus={() => {
            const target = returnFocus.current
            returnFocus.current = null
            if (target?.isConnected) target.focus()
          }}
        />
      ) : null}
    </WorkspaceFileOpenProvider>
  )
}

function previewLocation(options: OpenWorkspaceFileOptions | undefined): FileLocation | undefined {
  if (options?.listed) return options.location
  return taskPreviewLocation()
}
