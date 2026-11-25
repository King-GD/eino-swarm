import { Loader2, X } from "lucide-react"
import { useEffect, useRef, useState } from "react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogClose, DialogContent, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Textarea } from "@/components/ui/textarea"
import { api, ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useT } from "@/lib/use-t"
import type { ImageRef } from "@/lib/types"

/** Thumbnails for pasted vision input. The pixels are fetched from the
 *  conversation's input-images endpoint — they are not workspace files. */
export function InputThumbs({
  threadId,
  images,
  className,
}: {
  threadId?: string
  images?: ImageRef[]
  className?: string
}) {
  if (!threadId || !images?.length) return null
  return (
    <ul
      data-testid="input-images"
      className={cn("flex flex-wrap gap-2", className)}
    >
      {images.map((img) => (
        <li key={`${threadId}:${img.id}`} className="min-w-0">
          <InputImageThumb
            src={api.inputImageURL(threadId, img.id)}
            name={img.name}
            extractText={(signal) => api.extractImageText(threadId, img.id, signal)}
          />
        </li>
      ))}
    </ul>
  )
}

type ExtractText = (signal: AbortSignal) => Promise<{ text: string }>

export function InputImageThumb({ src, name, large = false, onLoad, onError, extractText }: {
  src: string
  name?: string
  large?: boolean
  onLoad?: () => void
  onError?: () => void
  extractText?: ExtractText
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const label = name || t("image.pasted")
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          className="h-auto max-w-full cursor-zoom-in p-0 hover:bg-transparent"
          aria-label={t("image.enlarge", { name: label })}
        >
          <img src={src} alt={label} onLoad={onLoad} onError={onError} className={cn("max-w-full rounded-md border border-border object-contain", large ? "max-h-96" : "max-h-40")} />
        </Button>
      </DialogTrigger>
      {open ? (
        <DialogContent
          className={cn("input-image-preview data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-1/2", extractText && "overflow-y-auto")}
          hideClose
          aria-describedby={undefined}
        >
          <DialogTitle className="truncate pr-10 text-sm">{label}</DialogTitle>
          <DialogClose asChild>
            <Button type="button" variant="ghost" size="icon-sm"
              className="absolute right-2 top-2" aria-label={t("image.close")}>
              <X aria-hidden />
            </Button>
          </DialogClose>
          <ImagePreview src={src} name={label} extractText={extractText} />
        </DialogContent>
      ) : null}
    </Dialog>
  )
}

function ImagePreview({ src, name, extractText }: { src: string; name: string; extractText?: ExtractText }) {
  const t = useT()
  const [status, setStatus] = useState<"loading" | "loaded" | "error">("loading")
  return (
    <>
      {status !== "loaded" ? (
        <div className="input-image-preview-status" role={status === "error" ? "alert" : "status"}>
          {status === "loading" ? <Loader2 className="size-4 animate-spin" /> : null}
          {t(status === "error" ? "image.failed" : "image.loading")}
        </div>
      ) : null}
      <img
        src={src}
        alt={name}
        className={cn("input-image-preview-image", status !== "loaded" && "hidden")}
        onLoad={() => setStatus("loaded")}
        onError={() => setStatus("error")}
      />
      {status === "loaded" && extractText && <ImageTextExtraction extractText={extractText} />}
    </>
  )
}

function ImageTextExtraction({ extractText }: { extractText: ExtractText }) {
  const t = useT()
  const request = useRef<AbortController | null>(null)
  const [result, setResult] = useState<{ text: string } | "pending" | "unavailable" | "failed" | null>(null)
  useEffect(() => () => request.current?.abort(), [])

  const run = async () => {
    if (request.current) return
    const controller = new AbortController()
    request.current = controller
    setResult("pending")
    try {
      const response = await extractText(controller.signal)
      if (!controller.signal.aborted) setResult(response)
    } catch (err) {
      if (!controller.signal.aborted) {
        setResult(err instanceof ApiError && err.code === "image_ocr_unavailable" ? "unavailable" : "failed")
      }
    } finally {
      request.current = null
    }
  }

  return (
    <div className="grid gap-2">
      <Button type="button" size="sm" variant="outline" className="justify-self-start"
        disabled={result === "pending"} onClick={() => void run()}>
        {t(result === "pending" ? "image.ocr.pending" : "image.ocr.action")}
      </Button>
      {result === "unavailable" || result === "failed" ? (
        <p role="alert" className="text-sm text-muted-foreground">{t(result === "unavailable" ? "image.ocr.unavailable" : "image.ocr.failed")}</p>
      ) : result && typeof result === "object" ? (
        result.text ? (
          <Textarea aria-label={t("image.ocr.result")} readOnly rows={4} value={result.text} />
        ) : <p role="status" className="text-sm text-muted-foreground">{t("image.ocr.empty")}</p>
      ) : null}
    </div>
  )
}
