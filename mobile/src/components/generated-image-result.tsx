import { Loader2, X } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"

import { FileDownload } from "@/components/file-download"
import { ImageFilePreview } from "@/components/image-file-preview"
import { Button } from "@/components/ui/button"
import { installAndroidBack } from "@/lib/android-back"
import { t } from "@/lib/i18n"
import { imageGenerationLabel } from "@/lib/image-generation-labels"

type ImageStatus = "running" | "completed" | "failed" | "cancelled" | "unknown"

export function GeneratedImageResult({ status, code, media, load }: {
  status: ImageStatus
  code?: string
  media?: { id: string; name?: string; mime: string; width?: number; height?: number }
  load?: (signal: AbortSignal) => Promise<Blob>
}) {
  const [file, setFile] = useState<{ url: string; mime: string; bytes: Uint8Array }>()
  const [failed, setFailed] = useState(false)
  const [zoomed, setZoomed] = useState(false)
  const [retry, setRetry] = useState(0)
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === "undefined")
  const section = useRef<HTMLElement>(null)
  const opener = useRef<HTMLButtonElement>(null)
  const closeButton = useRef<HTMLButtonElement>(null)
  const ready = status === "completed" && Boolean(media)
  const mediaID = media?.id
  const mime = media?.mime

  useEffect(() => {
    if (!ready || !section.current || typeof IntersectionObserver === "undefined") return
    const observer = new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting), { rootMargin: "240px" })
    observer.observe(section.current)
    return () => observer.disconnect()
  }, [ready])

  const shouldLoad = ready && (visible || zoomed)
  useEffect(() => {
    setFile(undefined)
    setFailed(false)
    if (!shouldLoad || !load || !mediaID) return
    const controller = new AbortController()
    let url: string | undefined
    void load(controller.signal).then(async blob => {
      if (blob.type !== mime || !blob.size) throw new Error("image_load_failed")
      const bytes = new Uint8Array(await blob.arrayBuffer())
      if (controller.signal.aborted) return
      url = URL.createObjectURL(blob)
      setFile({ url, mime: blob.type, bytes })
    }).catch(() => {
      if (!controller.signal.aborted) setFailed(true)
    })
    return () => {
      controller.abort()
      if (url) URL.revokeObjectURL(url)
    }
  }, [shouldLoad, load, mediaID, mime, retry])

  useEffect(() => {
    if (!zoomed) return
    closeButton.current?.focus()
    function close() {
      setZoomed(false)
      return true
    }
    function key(event: KeyboardEvent) {
      if (event.key === "Escape") { event.stopPropagation(); close() }
    }
    const unbind = installAndroidBack(close)
    window.addEventListener("keydown", key)
    return () => {
      unbind()
      window.removeEventListener("keydown", key)
      opener.current?.focus()
    }
  }, [zoomed])

  const name = media?.name || imageGenerationLabel("completed")
  return (
    <section ref={section} className="my-2 max-w-lg rounded-xl border border-border bg-card p-3"
      data-testid="generated-image-result" data-status={status}>
      {status === "running" ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground" role="status">
          <Loader2 aria-hidden="true" className="size-4 motion-safe:animate-spin" />{imageGenerationLabel(status)}
        </p>
      ) : status !== "completed" ? (
        <p role={status === "failed" ? "alert" : "status"} className="text-sm text-muted-foreground">{imageGenerationLabel(code || status)}</p>
      ) : !media || !load ? (
        <p role="alert" className="text-sm text-destructive">{imageGenerationLabel("loadFailed")}</p>
      ) : failed ? (
        <div>
          <p role="alert" className="text-sm text-destructive">{imageGenerationLabel("loadFailed")}</p>
          <Button type="button" variant="ghost" onClick={() => setRetry(n => n + 1)}>{t("files.retry")}</Button>
        </div>
      ) : (
        <>
          <div className="relative grid w-full max-h-96 place-items-center overflow-hidden rounded-lg bg-muted/30"
            style={{ aspectRatio: `${media.width || 4} / ${media.height || 3}` }}>
            {file ? (
              <button ref={opener} type="button"
                className="absolute inset-0 flex items-center justify-center focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                aria-label={imageGenerationLabel("preview")} onClick={() => setZoomed(true)}>
                <img src={file.url} alt={imageGenerationLabel("completed")} width={media.width} height={media.height}
                  onError={() => setFailed(true)}
                  className="max-h-full max-w-full object-contain motion-safe:animate-generated-image" />
              </button>
            ) : (
              <p role="status" className="px-2 text-sm text-muted-foreground">{imageGenerationLabel("loading")}</p>
            )}
          </div>
          <div className="min-h-12">{file ? <FileDownload name={name} file={file} /> : null}</div>
          {zoomed && file ? createPortal(
            <div role="dialog" aria-modal="true" aria-label={name}
              className="fixed inset-0 z-50 flex flex-col bg-background px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-[max(0.75rem,env(safe-area-inset-top))]">
              <div className="flex shrink-0 justify-end">
                <button ref={closeButton} type="button" className="rounded-md p-2 hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring" aria-label={t("home.close")} onClick={() => setZoomed(false)}>
                  <X aria-hidden="true" className="size-5" />
                </button>
              </div>
              <ImageFilePreview src={file.url} name={name} />
            </div>, document.body) : null}
        </>
      )}
    </section>
  )
}
