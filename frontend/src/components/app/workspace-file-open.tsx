import { createContext, useContext, useRef, type ReactNode } from "react"

import type { FileLocation } from "@/lib/types"
import { useT } from "@/lib/use-t"

export type OpenWorkspaceFileOptions = {
  /** The file tree already resolved this path. Do not search for another. */
  listed?: boolean
  location?: FileLocation
  returnFocus?: HTMLElement | null
}

export type OpenWorkspaceFile = (
  path: string,
  options?: OpenWorkspaceFileOptions,
) => Promise<void>

const WorkspaceFileOpenContext = createContext<OpenWorkspaceFile | null>(null)

export function WorkspaceFileOpenProvider({
  open,
  children,
}: {
  open: OpenWorkspaceFile | null
  children: ReactNode
}) {
  return (
    <WorkspaceFileOpenContext.Provider value={open}>
      {children}
    </WorkspaceFileOpenContext.Provider>
  )
}

export function useWorkspaceFileOpen(): OpenWorkspaceFile | null {
  return useContext(WorkspaceFileOpenContext)
}

export function WorkspaceFileLink({ path, children }: { path: string; children: ReactNode }) {
  const open = useWorkspaceFileOpen()
  const t = useT()
  const pending = useRef(false)
  if (!open) return <>{children}</>
  return (
    <button
      type="button"
      className="inline cursor-pointer whitespace-normal break-all border-0 bg-transparent p-0 text-left align-baseline font-[inherit] text-inherit underline underline-offset-2 hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
      data-testid="workspace-file-link"
      data-path={path}
      aria-label={t("files.openPreview", { path })}
      onClick={(event) => {
        if (pending.current) return
        pending.current = true
        const target = event.currentTarget
        void open(path, { returnFocus: target }).finally(() => {
          pending.current = false
        })
      }}
    >
      {children}
    </button>
  )
}
