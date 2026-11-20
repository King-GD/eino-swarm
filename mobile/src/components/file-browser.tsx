import { useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { TextFilePreview } from "@/components/text-file-preview"
import { ImageFilePreview } from "@/components/image-file-preview"
import { PhoneRichPreview } from "@/components/document-preview"
import { PhoneEpubPreview } from "@/components/epub-preview"
import { FileDownload } from "@/components/file-download"
import { ChevronDown, ChevronLeft, ChevronRight, FileText, Folder, Loader2, RefreshCw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { installAndroidBack } from "@/lib/android-back"
import { t } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { listWorkspaceView, previewKind, readWorkspaceFile, RemoteFileError } from "@/lib/remote-files"
import { documentFormat, isHtmlPreviewPath, tableDelimiter } from "../../../frontend/src/lib/documents/kind"
import type { WorkspaceFile } from "@/lib/rpc"
import type { FileLocation, FileScope, FileView } from "../../../frontend/src/lib/types"

type LoadedFile = { url: string; kind: ReturnType<typeof previewKind>; text: string; bytes: Uint8Array; mime: string }
type FileProgress = { loaded: number; total?: number; waiting?: boolean }

function fileSize(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

function fileFailure(cause: unknown): string {
  if (cause instanceof RemoteFileError && cause.code === "unknown_op") return t("files.updatePC")
  if (cause instanceof RemoteFileError && cause.code === "file_too_large") return t("files.tooLarge")
  return cause instanceof Error ? cause.message : String(cause)
}

// Files arrive over the sealed PC link. HTML is a sandboxed artifact.
// EPUB stays script-free. Office packages are parsed, never executed.
export function FileBrowser({ link, threadId, onClose, status, running }: {
  link: RemoteLink
  threadId: string
  onClose: () => void
  status?: ReactNode
  running?: boolean
}) {
  const [scope, setScope] = useState<FileScope>("task")
  const [fileView, setFileView] = useState<FileView>()
  const isolated = scope === "task" && fileView?.isolated
  const root = fileView?.root
  const location: FileLocation = { scope, root }
  const [listings, setListings] = useState<Record<string, WorkspaceFile[]>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [query, setQuery] = useState("")
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [hits, setHits] = useState<{ query: string; files: WorkspaceFile[] } | null>(null)
  const [searchError, setSearchError] = useState("")
  const listedGeneration = useRef(0)
  const loadingDirs = useRef(new Set<string>())
  const [selected, setSelected] = useState<(WorkspaceFile & { location: FileLocation }) | null>(null)
  const [file, setFile] = useState<LoadedFile | null>(null)
  const [readRevision, setReadRevision] = useState(0)
  const [progress, setProgress] = useState<FileProgress | null>(null)
  const [fileError, setFileError] = useState("")

  useEffect(() => {
    const generation = ++listedGeneration.current
    loadingDirs.current.clear()
    let active = true
    setLoading(true)
    setError("")
    setListings({})
    setOpen(new Set())
    void listWorkspaceView(link, threadId, "", "", { scope }).then(async ({ files, view }) => {
      if (!active || listedGeneration.current !== generation) return
      setFileView(view)
      setListings({ "": files })
      if (scope === "task" && view?.isolated && files.some((entry) => entry.path === "results" && entry.dir)) {
        setOpen(new Set(["results"]))
        const results = await listWorkspaceView(link, threadId, "results", "", { scope, root: view.root })
        if (!active || listedGeneration.current !== generation) return
        setListings((current) => ({ ...current, results: results.files }))
      }
    }).catch((cause: unknown) => {
      if (active && listedGeneration.current === generation) setError(fileFailure(cause))
    }).finally(() => {
      if (active && listedGeneration.current === generation) setLoading(false)
    })
    return () => { active = false }
  }, [link, threadId, revision, scope])

  const wasRunning = useRef(running)
  useEffect(() => {
    if (wasRunning.current && !running) setRevision((current) => current + 1)
    wasRunning.current = running
  }, [running])

  useEffect(() => {
    const needle = query.trim()
    if (!needle || loading) {
      setHits(null)
      setSearchError("")
      return
    }
    let active = true
    setHits(null)
    setSearchError("")
    const timer = window.setTimeout(() => {
      void listWorkspaceView(link, threadId, "", needle, { scope, root }).then(({ files }) => {
        if (!active) return
        setHits({ query: needle, files })
      }).catch((cause: unknown) => {
        if (!active) return
        setHits(null)
        setSearchError(fileFailure(cause))
      })
    }, 200)
    return () => {
      active = false
      window.clearTimeout(timer)
    }
  }, [link, threadId, query, revision, scope, root, loading])

  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    const { signal } = controller
    let url = ""
    // Pending download intent belongs to this read, including across link replacement.
    setReadRevision((current) => current + 1)
    setFile(null)
    setProgress({ loaded: 0 })
    setFileError("")
    void readWorkspaceFile(link, threadId, selected.path, {
      signal,
      onProgress: (loaded, total) => setProgress({ loaded, total }),
      onSlow: () => setProgress((current) => current && { ...current, waiting: true }),
    }, selected.location).then(({ bytes, mime }) => {
      if (signal.aborted) return
      const kind = previewKind(selected.name, mime, bytes.length)
      const safeMime = kind === "image" || kind === "pdf" ? mime : "application/octet-stream"
      url = URL.createObjectURL(new Blob([bytes], { type: safeMime }))
      const text = kind === "text" ? new TextDecoder().decode(bytes) : ""
      setFile({ url, kind, text, bytes, mime })
    }).catch((cause: unknown) => {
      if (!signal.aborted) setFileError(fileFailure(cause))
    }).finally(() => {
      if (!signal.aborted) setProgress(null)
    })
    return () => {
      controller.abort()
      if (url) URL.revokeObjectURL(url)
    }
  }, [link, threadId, selected])

  useEffect(() => installAndroidBack(() => {
    if (selected) setSelected(null)
    else onClose()
    return true
  }), [selected, onClose])

  const needle = query.trim()
  const matches = hits?.query === needle ? hits.files : null
  const awaitingSearch = needle !== "" && matches === null && !searchError
  const visible = useMemo(() => {
    if (needle) {
      return (matches ?? []).map((entry) => ({ entry, depth: entry.path.split("/").length - 1 }))
    }
    const rows: { entry: WorkspaceFile; depth: number }[] = []
    const walk = (dir: string, depth: number) => {
      for (const entry of listings[dir] ?? []) {
        rows.push({ entry, depth })
        if (entry.dir && open.has(entry.path)) walk(entry.path, depth + 1)
      }
    }
    walk("", 0)
    return rows
  }, [needle, matches, listings, open])

  const changeScope = (next: FileScope) => {
    if (next === scope) return
    listedGeneration.current += 1
    setScope(next)
    setListings({})
    setOpen(new Set())
    setQuery("")
    setHits(null)
    setError("")
    setSearchError("")
    setLoading(true)
  }

  const toggle = (path: string) => {
    setOpen((current) => {
      const next = new Set(current)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
    if (Object.prototype.hasOwnProperty.call(listings, path) || loadingDirs.current.has(path)) return
    loadingDirs.current.add(path)
    const generation = listedGeneration.current
    void listWorkspaceView(link, threadId, path, "", location).then(({ files }) => {
      if (listedGeneration.current !== generation) return
      setListings((current) => ({ ...current, [path]: files }))
    }).catch((cause: unknown) => {
      if (listedGeneration.current !== generation) return
      setError(fileFailure(cause))
    }).finally(() => {
      if (listedGeneration.current === generation) loadingDirs.current.delete(path)
    })
  }

  const progressText = progress?.total ? t("files.progress", {
    percent: Math.floor(100 * progress.loaded / progress.total),
    loaded: fileSize(progress.loaded),
    total: fileSize(progress.total),
  }) : undefined
  const epub = selected && documentFormat(selected.name) === "epub"
  const previewStatus = <>
    {progress ? (
      <div className="space-y-2">
        <p role="status" className="flex items-center gap-2 text-sm">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" />
          {t(progress.waiting ? "files.waiting" : "files.loading")}
        </p>
        <progress aria-label={t("files.progressLabel")} aria-valuetext={progressText}
          max={progress.total || undefined} value={progress.total ? progress.loaded : undefined}
          className="block h-2 w-full accent-primary" />
        {progressText ? <p className="text-xs tabular-nums text-muted-foreground">{progressText}</p> : null}
      </div>
    ) : null}
    {fileError ? <p role="alert" className="text-sm text-destructive">{fileError}</p> : null}
    {file?.kind === "download" ? <p className="text-sm text-muted-foreground">{t("files.unavailable")}</p> : null}
  </>
  const download = selected && !fileError ? <FileDownload key={readRevision} name={selected.name} file={file} /> : null

  return (
    <main data-testid="phone-file-browser" className="mx-auto flex h-full min-w-0 w-full max-w-lg flex-col overflow-hidden bg-background">
      <header className="flex h-12 shrink-0 items-center gap-1 border-b border-border px-1">
        <Button variant="ghost" className="size-10 shrink-0 px-0" onClick={() => selected ? setSelected(null) : onClose()}
          aria-label={selected ? t("files.backList") : t("thread.back")}>
          <ChevronLeft className="size-5" />
        </Button>
        <h1 className="min-w-0 flex-1 truncate text-sm font-medium">{selected?.name ?? t("thread.files")}</h1>
        {selected ? null : (
          <Button variant="ghost" className="size-10 shrink-0 px-0" onClick={() => setRevision((n) => n + 1)}
            aria-label={t("files.refresh")}><RefreshCw className="size-4" /></Button>
        )}
      </header>
      {status}
      {selected ? (
        <section data-testid="phone-file-preview" className={`flex min-h-0 flex-1 flex-col overflow-hidden ${epub ? "" : "p-4"}`}>
          {epub ? (
            <PhoneEpubPreview key={selected.path} bytes={file?.kind === "document" ? file.bytes : undefined} status={previewStatus} download={download} />
          ) : <>
            <p className="mb-3 max-h-16 shrink-0 overflow-auto break-all text-xs text-muted-foreground">{selected.path}</p>
            {previewStatus}
            {file?.kind === "text" && (isHtmlPreviewPath(selected.path) || tableDelimiter(selected.path)) ? <PhoneRichPreview key={file.url} link={link} threadId={threadId} path={selected.path} text={file.text} bytes={file.bytes} location={selected.location} /> : null}
            {file?.kind === "text" && !isHtmlPreviewPath(selected.path) && !tableDelimiter(selected.path) ? <TextFilePreview key={file.url} path={selected.path} text={file.text} /> : null}
            {file?.kind === "document" ? <PhoneRichPreview key={file.url} link={link} threadId={threadId} path={selected.path} text="" bytes={file.bytes} /> : null}
            {file?.kind === "image" ? <ImageFilePreview key={file.url} src={file.url} name={selected.name} /> : null}
            {file?.kind === "pdf" ? <iframe src={file.url} title={selected.name} sandbox="allow-scripts" className="min-h-0 w-full flex-1 border-0" /> : null}
            {download ? <div className="shrink-0">{download}</div> : null}
          </>}
        </section>
      ) : (
        <section className="flex min-h-0 flex-1 flex-col">
          {fileView?.has_project ? <div role="group" aria-label={t("thread.files")} className="flex gap-1 border-b border-border px-3 py-2">
            <Button variant={scope === "task" ? "outline" : "ghost"} aria-pressed={scope === "task"} onClick={() => changeScope("task")}>{t("files.taskScope")}</Button>
            <Button variant={scope === "project" ? "outline" : "ghost"} aria-pressed={scope === "project"} onClick={() => changeScope("project")}>{t("files.projectScope")}</Button>
          </div> : null}
          {scope === "task" && fileView?.has_project && !isolated ? <p className="px-3 pt-2 text-xs text-muted-foreground">{t("files.legacyShared")}</p> : null}
          <div className="border-b border-border p-3">
            <input value={query} onChange={(event) => setQuery(event.target.value)} aria-label={t("files.search")}
              placeholder={t("files.search")} className="w-full rounded-md border border-input bg-background px-3 py-2 text-base" />
          </div>
          {loading ? <p className="p-4 text-sm text-muted-foreground">{t("files.loading")}</p> : null}
          {error ? <div className="p-4 text-sm text-destructive" role="alert">{error}
            <Button variant="ghost" onClick={() => setRevision((n) => n + 1)}>{t("files.retry")}</Button>
          </div> : null}
          {searchError ? <p role="alert" className="px-4 py-2 text-sm text-destructive">{searchError}</p> : null}
          {awaitingSearch ? <p role="status" className="p-4 text-sm text-muted-foreground">{t("files.searching")}</p> : null}
          {!loading && !error && !searchError && !awaitingSearch && visible.length === 0 ? <p className="p-4 text-sm text-muted-foreground">{t("files.empty")}</p> : null}
          <div className="min-h-0 flex-1 overflow-auto">
            {visible.map(({ entry, depth }) => (
              <div key={entry.path}>
                <button type="button" onClick={() => entry.dir ? toggle(entry.path) : setSelected({ ...entry, location })}
                  className="flex min-h-11 w-full items-center gap-2 border-b border-border px-3 text-left text-sm hover:bg-muted"
                  style={{ paddingLeft: `${12 + Math.min(depth, 8) * 16}px` }}>
                  {entry.dir ? (open.has(entry.path) ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />) : null}
                  {entry.dir ? <Folder className="size-4 shrink-0" /> : <FileText className="size-4 shrink-0" />}
                  <span className="min-w-0 flex-1 truncate">{fileLabel(entry, scope, Boolean(isolated))}</span>
                  {entry.uploaded ? <span className="text-xs text-muted-foreground">{t("files.uploaded")}</span> : null}
                  {!entry.dir ? <span aria-hidden className="shrink-0 text-xs text-muted-foreground">{fileSize(entry.size)}</span> : null}
                </button>
                {!needle && entry.dir && open.has(entry.path) && listings[entry.path] === undefined ? (
                  <p role="status" className="px-3 py-2 text-xs text-muted-foreground" style={{ paddingLeft: `${28 + Math.min(depth, 8) * 16}px` }}>
                    {t("files.loadingDir")}
                  </p>
                ) : null}
                {!needle && isolated && entry.path === "results" && open.has(entry.path) && listings.results?.length === 0
                  ? <p className="px-7 py-2 text-xs text-muted-foreground">{t("files.noResults")}</p> : null}
              </div>
            ))}
          </div>
        </section>
      )}
    </main>
  )
}

function fileLabel(entry: WorkspaceFile, scope: FileScope, isolated: boolean): string {
  if (scope === "project" && entry.path === ".zwai-tasks") return t("files.tasksFolder")
  if (!isolated || !entry.dir) return entry.name
  if (entry.path === "results") return t("files.resultsFolder")
  if (entry.path === "work") return t("files.workFolder")
  if (entry.path === "uploads") return t("files.uploadsFolder")
  return entry.name
}
