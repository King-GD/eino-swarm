import { Shield } from "lucide-react"

import { MODEL_REQUEST_BLOCKED } from "@/lib/model-request-blocked"
import { generationNotice } from "@/lib/transcript-generation"
import type { Block } from "@/lib/transcript"
import { useT } from "@/lib/use-t"

export function ModelRequestBlockedNotice() {
  const t = useT()
  return (
    <div role="status" className="my-2 flex items-start gap-2 rounded-lg border border-border bg-muted/50 px-3 py-2 text-(length:--chrome-font-size) text-foreground">
      <Shield aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <p>{t("generation.requestBlocked")}</p>
    </div>
  )
}

/** Recovery updates this output's receipt, never its worker's failure state. */
export function GenerationNotice({ block }: { block: Block }) {
  const t = useT()
  const generation = block.generation
  if (!generation) return null
  if (generation.reason === MODEL_REQUEST_BLOCKED) return <ModelRequestBlockedNotice />
  const detail = generation.detail === "model response read timed out" ? t("notice.modelServiceReadTimeout") : generation.detail
  return (
    <p data-testid="generation-notice" role="status" className="px-2 py-1 text-xs text-muted-foreground">
      {generationNotice(generation, t.locale)}
      {detail ? <span className="ml-1 whitespace-pre-wrap wrap-break-word">{detail}</span> : null}
    </p>
  )
}
