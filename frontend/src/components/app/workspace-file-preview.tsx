import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"

import { FilePreviewDialog } from "@/components/app/file-preview"
import {
  WorkspaceFileOpenProvider,
  type OpenWorkspaceFile,
  type OpenWorkspaceFileOptions,
} from "@/components/app/workspace-file-open"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { api } from "@/lib/api"
import type { FileLocation } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { chooseWorkspaceFile } from "@/lib/workspace-file-ref"
import { useApp } from "@/store/app"

type Preview = {
  threadId: string
  path: string
  location?: FileLocation
  choices?: string[]
}

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
  const keepReturnFocus = useRef(false)
  const ticket = useRef(0)

  useEffect(() => {
    ticket.current += 1
    setPreview(undefined)
  }, [threadId])

  const open = useCallback<OpenWorkspaceFile>(async (path, options) => {
    const id = useApp.getState().activeId
    if (!id || !path) return
    const mine = ++ticket.current
    keepReturnFocus.current = false
    returnFocus.current = options?.returnFocus ?? null
    const location = previewLocation(options)
    let resolved = path
    if (!options?.listed && !useApp.getState().files.some((file) => !file.dir && file.path === path)) {
      try {
        const found = await api.searchFiles(id, path, location)
        if (mine !== ticket.current || useApp.getState().activeId !== id) return
        const match = chooseWorkspaceFile(path, found.files)
        if (match.status === "many") {
          setPreview({ threadId: id, path, location, choices: match.paths })
          return
        }
        resolved = match.path
      } catch {
        if (mine !== ticket.current || useApp.getState().activeId !== id) return
      }
    }
    if (mine !== ticket.current || useApp.getState().activeId !== id) return
    setPreview({ threadId: id, path: resolved, location })
  }, [])

  const shown = preview && preview.threadId === threadId ? preview : undefined
  const restoreFocus = () => {
    const target = returnFocus.current
    returnFocus.current = null
    if (target?.isConnected) target.focus()
  }
  return (
    <WorkspaceFileOpenProvider open={threadId ? open : null}>
      {children}
      {shown?.choices ? (
        <WorkspaceFileChoices
          name={shown.path}
          paths={shown.choices}
          onChoose={(path) => {
            keepReturnFocus.current = true
            setPreview({ threadId: shown.threadId, path, location: shown.location })
          }}
          onClose={() => setPreview(undefined)}
          onReturnFocus={() => {
            if (keepReturnFocus.current) {
              keepReturnFocus.current = false
              return
            }
            restoreFocus()
          }}
        />
      ) : null}
      {shown && !shown.choices ? (
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
          onReturnFocus={restoreFocus}
        />
      ) : null}
    </WorkspaceFileOpenProvider>
  )
}

function previewLocation(options: OpenWorkspaceFileOptions | undefined): FileLocation | undefined {
  if (options?.listed) return options.location
  return taskPreviewLocation()
}

function WorkspaceFileChoices({ name, paths, onChoose, onClose, onReturnFocus }: {
  name: string
  paths: readonly string[]
  onChoose: (path: string) => void
  onClose: () => void
  onReturnFocus: () => void
}) {
  const t = useT()
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        data-testid="workspace-file-choices"
        className="flex max-h-[70dvh] w-[calc(100%-2rem)] max-w-lg flex-col gap-0 overflow-hidden p-0"
        onCloseAutoFocus={(event) => { event.preventDefault(); onReturnFocus() }}
      >
        <header className="border-b px-5 py-4 pr-12">
          <DialogTitle className="break-all leading-5">{name}</DialogTitle>
          <DialogDescription className="mt-1">{t("files.chooseMatch")}</DialogDescription>
        </header>
        <ul className="thin-scrollbar min-h-0 flex-1 overflow-auto p-2">
          {paths.map((path) => (
            <li key={path}>
              <Button
                variant="ghost"
                className="h-auto w-full justify-start whitespace-normal break-all px-3 py-2 text-left"
                aria-label={t("files.openPreview", { path })}
                onClick={() => onChoose(path)}
              >
                {path}
              </Button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  )
}
