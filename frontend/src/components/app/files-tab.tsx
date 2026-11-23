import {
  ChevronRight,
  Download,
  File as FileIcon,
  FileCode,
  FileImage,
  FileJson,
  FileText,
  Folder,
  FolderOpen,
  RefreshCw,
  Search,
  Trash2,
  Upload,
} from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"

import { CopyButton } from "@/components/app/copy-button"
import { ConfirmDeleteDialog } from "@/components/app/confirm-delete-dialog"
import { FilePreviewDialog } from "@/components/app/file-preview"
import { useWorkspaceFileOpen } from "@/components/app/workspace-file-open"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import {
  buildFileTree,
  fileKind,
  fileTreeKey,
  initialExpanded,
  pruneExpanded,
  toggleExpanded,
  visibleRows,
  type FileKind,
  type FileTreeNode,
} from "@/lib/file-tree"
import type { FileEntry, FileLocation, FileScope, FileView } from "@/lib/types"
import { cn, formatBytes } from "@/lib/utils"
import { useT } from "@/lib/use-t"

const kindClass: Record<FileKind, string> = {
  folder: "text-file-folder",
  code: "text-file-code",
  doc: "text-file-doc",
  config: "text-file-config",
  image: "text-file-image",
  file: "text-muted-foreground",
}

export function FilesTab({
  files,
  workspace,
  threadId,
  fileScope = "task",
  fileView,
  onFileScope,
  canReveal,
  loadedDirs,
  dirErrors,
  onUpload,
  onDelete,
  onRefresh,
  onReveal,
  onOpenDir,
  onSearch,
}: {
  files: FileEntry[]
  workspace: string
  threadId?: string
  fileScope?: FileScope
  fileView?: FileView
  onFileScope?: (scope: FileScope) => void
  canReveal: boolean
  loadedDirs?: readonly string[]
  dirErrors?: Record<string, string>
  onUpload: (files: File[]) => Promise<unknown>
  onDelete: (path: string) => void
  onRefresh: () => void
  onReveal: (path?: string) => void
  onOpenDir?: (path: string) => void
  onSearch?: (query: string) => Promise<FileEntry[]>
}) {
  const t = useT()
  const openWorkspaceFile = useWorkspaceFileOpen()
  const project = fileScope === "project"
  const isolated = !project && Boolean(fileView?.isolated)
  const location: FileLocation | undefined = project || fileView?.root ? { scope: fileScope, root: fileView?.root } : undefined
  const inputRef = useRef<HTMLInputElement>(null)
  const treeRef = useRef<HTMLDivElement>(null)
  const userTouched = useRef(false)
  const [filter, setFilter] = useState("")
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [focused, setFocused] = useState<string>()
  const [doomed, setDoomed] = useState<string>()
  const [preview, setPreview] = useState<{ threadId: string; path: string }>()
  const [hits, setHits] = useState<FileEntry[]>()
  const [searching, setSearching] = useState(false)
  const [searchError, setSearchError] = useState("")
  const loaded = useMemo(() => new Set(loadedDirs ?? []), [loadedDirs])
  const loadingRoot = Boolean(threadId && loadedDirs && !loaded.has(""))
  const query = filter.trim()
  const serverSearch = Boolean(onSearch) && query.length > 0
  const tree = useMemo(
    () => buildFileTree(serverSearch ? (hits ?? []) : files),
    [files, hits, serverSearch],
  )
  const rows = useMemo(
    () => visibleRows(tree, expanded, serverSearch ? query : onSearch ? "" : query),
    [tree, expanded, serverSearch, onSearch, query],
  )
  const filtering = query.length > 0

  useEffect(() => {
    userTouched.current = false
    setFilter("")
    setFocused(undefined)
    setPreview(undefined)
  }, [threadId])

  useEffect(() => {
    if (!userTouched.current) {
      setExpanded(isolated ? new Set(tree.some((node) => node.path === "results" && node.dir) ? ["results"] : [])
        : project ? new Set() : initialExpanded(tree))
      return
    }
    setExpanded((prev) => pruneExpanded(prev, tree))
  }, [threadId, tree, isolated, project])

  useEffect(() => {
    if (!onOpenDir) return
    for (const path of expanded) {
      if (!loaded.has(path) && !dirErrors?.[path]) onOpenDir(path)
    }
  }, [expanded, loaded, dirErrors, onOpenDir])

  useEffect(() => {
    if (!onSearch || query.length === 0) {
      setHits(undefined)
      setSearching(false)
      setSearchError("")
      return
    }
    let cancel = false
    setHits(undefined)
    setSearching(true)
    setSearchError("")
    const timer = window.setTimeout(() => {
      void onSearch(query).then((rows) => {
        if (cancel) return
        setHits(rows)
        setSearching(false)
      }).catch((error: unknown) => {
        if (cancel) return
        setHits(undefined)
        setSearching(false)
        setSearchError(error instanceof Error ? error.message : String(error))
      })
    }, 200)
    return () => {
      cancel = true
      window.clearTimeout(timer)
    }
  }, [onSearch, query])

  const applyToggle = (path: string) => {
    userTouched.current = true
    setExpanded((prev) => toggleExpanded(prev, path))
  }

  const openFile = (path: string) => {
    if (openWorkspaceFile) {
      void openWorkspaceFile(path, { listed: true, location, returnFocus: treeRef.current })
      return
    }
    if (threadId) setPreview({ threadId, path })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-col gap-2 px-2 pb-2 pt-2">
        {fileView?.has_project && onFileScope ? (
          <div className="flex items-center gap-1" role="group" aria-label={t("files.tree")}>
            <Button size="sm" variant={project ? "ghost" : "secondary"} aria-pressed={!project} onClick={() => onFileScope("task")}>
              {t("files.taskScope")}
            </Button>
            <Button size="sm" variant={project ? "secondary" : "ghost"} aria-pressed={project} onClick={() => onFileScope("project")}>
              {t("files.projectScope")}
            </Button>
          </div>
        ) : null}
        {!project && fileView?.has_project && !isolated ? <p className="px-1 text-xs text-muted-foreground">{t("files.legacyShared")}</p> : null}
        <div className="flex items-center gap-1">
          {!project ? <>
          <input
            ref={inputRef}
            type="file"
            multiple
            className="hidden"
            onChange={async (e) => {
              const picked = Array.from(e.target.files ?? [])
              e.target.value = ""
              if (picked.length > 0) await onUpload(picked)
            }}
          />
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5"
            onClick={() => inputRef.current?.click()}
          >
            <Upload />
            {t("files.upload")}
          </Button>
          </> : null}
          <Button variant="ghost" size="icon-sm" onClick={onRefresh} title={t("files.refresh")}>
            <RefreshCw />
          </Button>
          {canReveal ? (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => onReveal()}
              disabled={!workspace}
              title={t("files.reveal")}
            >
              <FolderOpen />
            </Button>
          ) : null}
          {workspace ? <CopyButton text={workspace} className="ml-auto" /> : null}
        </div>
        {files.length > 0 ? (
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              id="file-filter"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              aria-label={t("files.filter")}
              placeholder={t("files.filterPlaceholder")}
              spellCheck={false}
              className="h-8 pl-7 text-(length:--chrome-font-size) shadow-none"
            />
          </div>
        ) : null}
      </div>

      {dirErrors?.[""] && !serverSearch ? (
        <p role="alert" className="px-3 py-2 text-center text-(length:--chrome-font-size) text-destructive">{dirErrors[""]}</p>
      ) : null}
      {searchError ? (
        <p role="alert" className="px-3 py-8 text-center text-(length:--chrome-font-size) text-destructive">
          {searchError}
        </p>
      ) : serverSearch && searching && !hits ? (
        <p role="status" className="px-3 py-8 text-center text-(length:--chrome-font-size) text-muted-foreground">
          {t("files.searching")}
        </p>
      ) : files.length === 0 && !serverSearch && !dirErrors?.[""] ? (
        <p role={loadingRoot ? "status" : undefined} className="px-3 py-8 text-center text-(length:--chrome-font-size) text-muted-foreground">
          {t(loadingRoot ? "files.loading" : "files.empty")}
        </p>
      ) : rows.length === 0 && !(dirErrors?.[""] && !serverSearch && files.length === 0) ? (
        <p className="px-3 py-8 text-center text-(length:--chrome-font-size) text-muted-foreground">
          {t("files.noMatch")}
        </p>
      ) : rows.length === 0 ? null : (
        <div
          ref={treeRef}
          data-testid="file-tree"
          role="tree"
          aria-label={t("files.tree")}
          tabIndex={0}
          className="thin-scrollbar min-h-0 flex-1 overflow-y-auto px-1 pb-2 outline-hidden"
          onKeyDown={(e) => {
            if ((e.target as HTMLElement).closest("button, a")) return
            const action = fileTreeKey(e.key, rows, focused, expanded, filtering)
            if (!action) return
            e.preventDefault()
            if (action.type === "focus") setFocused(action.path)
            if (action.type === "toggle") {
              setFocused(action.path)
              applyToggle(action.path)
            }
            if (action.type === "open") {
              setFocused(action.path)
              openFile(action.path)
            }
          }}
        >
          {rows.map(({ node, depth }) => {
            const pending = !serverSearch && onOpenDir && node.dir && expanded.has(node.path) && !loaded.has(node.path)
              ? dirErrors?.[node.path] ?? t("files.loadingDir")
              : ""
            const section = isolated ? taskSectionLabel(node.path) : undefined
            const noResults = isolated && !filtering && node.path === "results" &&
              expanded.has(node.path) && loaded.has(node.path) && node.children.length === 0
            return (
              <div key={node.path}>
                <FileRow
                  node={node}
                  depth={depth}
                  open={expanded.has(node.path)}
                  focused={focused === node.path}
                  threadId={threadId}
                  location={location}
                  label={section ? t(section) : project && node.path === ".zwai-tasks" ? t("files.tasksFolder") : node.name}
                  filtering={filtering}
                  canReveal={canReveal}
                  onFocus={setFocused}
                  onToggle={applyToggle}
                  onOpen={openFile}
                  onReveal={onReveal}
                  onDelete={project ? undefined : setDoomed}
                />
                {pending || noResults ? (
                  <p
                    role={dirErrors?.[node.path] ? "alert" : "status"}
                    className="truncate px-3 py-1 text-(length:--chrome-font-size) text-muted-foreground"
                    style={{ paddingLeft: 28 + depth * 12 }}
                  >
                    {pending || t("files.noResults")}
                  </p>
                ) : null}
              </div>
            )
          })}
        </div>
      )}
      {!openWorkspaceFile && preview && preview.threadId === threadId ? (
        <FilePreviewDialog
          key={`${preview.threadId}/${preview.path}`}
          threadId={preview.threadId}
          path={preview.path}
          location={location}
          canReveal={canReveal}
          onReveal={onReveal}
          onClose={() => setPreview(undefined)}
          onReturnFocus={() => treeRef.current?.focus()}
        />
      ) : null}
      <ConfirmDeleteDialog
        open={Boolean(doomed)}
        title={t("file.deleteTitle", {
          name: doomed ? doomed.slice(doomed.lastIndexOf("/") + 1) : "",
        })}
        description={t("file.deleteDesc")}
        confirmLabel={t("file.deleteConfirm")}
        cancelLabel={t("confirm.cancel")}
        onOpenChange={(open) => {
          if (!open) setDoomed(undefined)
        }}
        onConfirm={() => {
          if (doomed) onDelete(doomed)
        }}
      />
    </div>
  )
}

function taskSectionLabel(path: string) {
  if (path === "results") return "files.resultsFolder"
  if (path === "work") return "files.workFolder"
  if (path === "uploads") return "files.uploadsFolder"
  return undefined
}

function FileRow({
  node,
  depth,
  open,
  focused,
  threadId,
  location,
  label,
  filtering,
  canReveal,
  onFocus,
  onToggle,
  onOpen,
  onReveal,
  onDelete,
}: {
  node: FileTreeNode
  depth: number
  open: boolean
  focused: boolean
  threadId?: string
  location?: FileLocation
  label: string
  filtering: boolean
  canReveal: boolean
  onFocus: (path: string) => void
  onToggle: (path: string) => void
  onOpen: (path: string) => void
  onReveal: (path?: string) => void
  onDelete?: (path: string) => void
}) {
  const t = useT()
  const expanded = node.dir ? filtering || open : undefined
  const title = node.dir
    ? node.path
    : `${node.path} · ${formatBytes(node.size)}`

  return (
    <div
      role="treeitem"
      aria-label={label}
      aria-expanded={expanded}
      aria-level={depth + 1}
      aria-selected={focused}
      data-path={node.path}
      className={cn(
        "group flex h-7 cursor-default items-center gap-1 rounded-md pr-1 text-[13px] hover:bg-accent/60",
        focused && "bg-accent",
      )}
      style={{ paddingLeft: 6 + depth * 12 }}
      onClick={() => {
        onFocus(node.path)
        if (node.dir) onToggle(node.path)
        else onOpen(node.path)
      }}
    >
      <span className="flex size-3.5 shrink-0 items-center justify-center">
        {node.dir ? (
          <ChevronRight
            className={cn(
              "size-3 text-muted-foreground transition-transform",
              expanded && "rotate-90",
            )}
          />
        ) : null}
      </span>
      <FileGlyph node={node} open={Boolean(expanded)} />
      <span className="min-w-0 flex-1 truncate" title={title}>
        {label}
      </span>
      {node.uploaded ? (
        <Badge variant="outline" className="shrink-0 px-1 py-0 text-[10px]">
          {t("files.yours")}
        </Badge>
      ) : null}
      {node.dir && canReveal ? (
        <Button
          variant="ghost"
          size="icon-sm"
          className={cn(
            "shrink-0 opacity-0 pointer-events-none transition-opacity group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100",
            focused && "pointer-events-auto opacity-100",
          )}
          title={t("files.revealDir", { name: node.name })}
          aria-label={t("files.revealDir", { name: node.name })}
          onClick={(e) => {
            e.stopPropagation()
            onReveal(node.path)
          }}
        >
          <FolderOpen />
        </Button>
      ) : null}
      {!node.dir && threadId ? (
        <div
          className="flex shrink-0 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100"
          onClick={(e) => e.stopPropagation()}
        >
          <Button variant="ghost" size="icon-sm" asChild title={t("files.download")}>
            <a href={api.downloadURL(threadId, node.path, location)} download>
              <Download />
            </a>
          </Button>
          {onDelete ? <Button
            variant="ghost"
            size="icon-sm"
            title={t("files.delete")}
            onClick={() => onDelete(node.path)}
          >
            <Trash2 />
          </Button> : null}
        </div>
      ) : null}
    </div>
  )
}

function FileGlyph({ node, open }: { node: FileTreeNode; open: boolean }) {
  const kind = fileKind(node)
  const className = cn("size-3.5 shrink-0", kindClass[kind])
  if (node.dir) {
    return open ? (
      <FolderOpen className={className} />
    ) : (
      <Folder className={className} />
    )
  }
  if (kind === "code") return <FileCode className={className} />
  if (kind === "doc") return <FileText className={className} />
  if (kind === "config") return <FileJson className={className} />
  if (kind === "image") return <FileImage className={className} />
  return <FileIcon className={className} />
}
