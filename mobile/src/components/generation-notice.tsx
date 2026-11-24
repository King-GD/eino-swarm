import type { CompactBlock } from "@/lib/transcript"
import { t } from "@/lib/i18n"

export function GenerationNotice({ block }: { block: CompactBlock }) {
  const generation = block.generation
  if (!generation) return null
  const detail = generation.detail === "model response read timed out" ? t("notice.modelServiceReadTimeout") : generation.detail
  return (
    <p data-testid="generation-notice" role="status" className="text-[11px] text-muted-foreground">
      {t("generation.interrupted", { reason: t(`generation.reason.${generation.reason}`) })}{" "}
      {t(`generation.recovery.${generation.recovery}`)}
      {detail ? <span className="ml-1 whitespace-pre-wrap wrap-break-word">{detail}</span> : null}
    </p>
  )
}
