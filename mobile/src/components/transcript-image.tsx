import { ImageIcon } from "lucide-react"
import { useEffect, useState } from "react"

import { Button } from "@/components/ui/button"
import { t } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { readInputImage } from "@/lib/remote-files"

export type TranscriptImageOwner = { link: RemoteLink; threadId: string }

type ImageRequest = TranscriptImageOwner & { id: string }

export function TranscriptImage({ id, owner: { link, threadId } }: { id: string; owner: TranscriptImageOwner }) {
  const [request, setRequest] = useState<ImageRequest>()
  const [result, setResult] = useState<{ url?: string }>()

  if (request && (request.link !== link || request.threadId !== threadId || request.id !== id)) {
    setRequest(undefined)
    setResult(undefined)
  }

  useEffect(() => {
    if (!request) return
    const controller = new AbortController()
    let url: string | undefined
    void readInputImage(request.link, request.threadId, request.id, controller.signal).then(blob => {
      if (controller.signal.aborted) return
      url = URL.createObjectURL(blob)
      setResult({ url })
    }).catch(() => {
      if (!controller.signal.aborted) setResult({})
    })
    return () => {
      controller.abort()
      if (url) URL.revokeObjectURL(url)
    }
  }, [request])

  function load() {
    setResult(undefined)
    setRequest({ link, threadId, id })
  }

  return (
    <div data-testid="transcript-image">
      {!request ? (
        <Button type="button" variant="outline" className="min-h-11 gap-2 px-3" onClick={load}>
          <ImageIcon className="size-4 shrink-0" aria-hidden="true" />
          {t("thread.viewImage")}
        </Button>
      ) : result?.url ? (
        <div className="grid aspect-[4/3] w-56 max-w-full place-items-center overflow-hidden rounded-lg bg-muted/30">
          <img src={result.url} alt={t("thread.image")} decoding="async"
            className="h-full w-full min-h-0 min-w-0 object-contain"
            onError={() => setResult({})} />
        </div>
      ) : result ? (
        <div className="text-xs text-muted-foreground">
          <p role="alert">{t("thread.imageLoadFailed")}</p>
          <Button type="button" variant="ghost" className="min-h-11 px-3" onClick={load}>{t("files.retry")}</Button>
        </div>
      ) : (
        <p role="status" className="flex min-h-11 items-center px-3 text-xs text-muted-foreground">{t("files.imageLoading")}</p>
      )}
    </div>
  )
}
