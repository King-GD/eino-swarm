import { Download, ImageIcon, Loader2 } from "lucide-react"
import { useState } from "react"

import { InputImageThumb } from "@/components/app/input-thumbs"
import { api } from "@/lib/api"
import type { ImageGeneration } from "@/lib/image-generation"
import { imageGenerationErrorKey } from "@/lib/messages-image-generation"
import { useT } from "@/lib/use-t"

export function GeneratedImageCard({ data, threadId }: { data: ImageGeneration; threadId: string }) {
  const t = useT()
  return (
    <div data-testid="image-generation" data-status={data.status}
      className="my-2 w-fit max-w-full overflow-hidden rounded-xl border border-border bg-card">
      {data.status === "running" ? (
        <div role="status" className="flex w-64 max-w-full items-center gap-2 bg-muted/40 px-4 py-6 text-sm text-muted-foreground">
          <Loader2 className="size-4 motion-safe:animate-spin" aria-hidden />
          {t("imageGen.running")}
        </div>
      ) : data.status === "completed" && data.media ? (
        <GeneratedImage key={data.media.id} media={data.media} threadId={threadId} />
      ) : (
        <div role="status" className="flex items-center gap-2 px-3 py-2 text-sm text-muted-foreground">
          <ImageIcon className="size-4 shrink-0" aria-hidden />
          {t(imageGenerationErrorKey(data.code) ??
            (data.status === "cancelled" ? "imageGen.cancelled" : data.status === "unknown" ? "imageGen.unknown" : "imageGen.failed"))}
        </div>
      )}
    </div>
  )
}

function GeneratedImage({ media, threadId }: { media: NonNullable<ImageGeneration["media"]>; threadId: string }) {
  const t = useT()
  const [status, setStatus] = useState<"loading" | "loaded" | "error">("loading")
  const src = api.generatedImageURL(threadId, media.id)
  const name = media.name || t("imageGen.image")
  return (
    <>
      {status !== "loaded" ? <p role={status === "error" ? "alert" : "status"} className="px-3 py-2 text-sm text-muted-foreground">
        {t(status === "error" ? "imageGen.loadFailed" : "imageGen.loading")}
      </p> : null}
      <div className={status === "loaded" ? "p-2 motion-safe:animate-in motion-safe:fade-in motion-safe:ease-out" : "invisible absolute pointer-events-none"}>
        <InputImageThumb src={src} name={name} large onLoad={() => setStatus("loaded")} onError={() => setStatus("error")} />
      </div>
      {status === "loaded" ? <div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2 text-sm">
        <span className="truncate text-muted-foreground">{t("imageGen.image")}</span>
        <a href={src} download={name} className="inline-flex shrink-0 items-center gap-1.5 rounded-sm text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-ring">
          <Download className="size-4" aria-hidden />
          {t("imageGen.save")}
        </a>
      </div> : null}
    </>
  )
}
