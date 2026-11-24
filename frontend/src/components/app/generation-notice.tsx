import { generationNotice } from "@/lib/transcript-generation"
import type { Block } from "@/lib/transcript"
import { useT } from "@/lib/use-t"

/** Recovery updates this output's receipt, never its worker's failure state. */
export function GenerationNotice({ block }: { block: Block }) {
  const t = useT()
  const generation = block.generation
  if (!generation) return null
  const detail = generation.detail === "model response read timed out" ? t("notice.modelServiceReadTimeout") : generation.detail
  return (
    <p data-testid="generation-notice" role="status" className="px-2 py-1 text-xs text-muted-foreground">
      {generationNotice(generation, t.locale)}
      {detail ? <span className="ml-1 whitespace-pre-wrap wrap-break-word">{detail}</span> : null}
    </p>
  )
}
