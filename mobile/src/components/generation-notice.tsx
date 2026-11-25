import { Shield } from "lucide-react"

import type { CompactBlock } from "@/lib/transcript"
import { t } from "@/lib/i18n"
import { MODEL_REQUEST_BLOCKED } from "../../../frontend/src/lib/model-request-blocked"

export function ModelRequestBlockedNotice() {
  return (
    <div role="status" className="flex min-w-0 items-start gap-2 rounded-lg border border-border bg-muted/50 px-3 py-2 text-sm text-foreground">
      <Shield aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <p>{t("generation.requestBlocked")}</p>
    </div>
  )
}

export function GenerationNotice({ block }: { block: CompactBlock }) {
  const generation = block.generation
  if (!generation) return null
  if (generation.reason === MODEL_REQUEST_BLOCKED) return <ModelRequestBlockedNotice />
  const detail = generation.detail === "model response read timed out" ? t("notice.modelServiceReadTimeout") : generation.detail
  return (
    <p data-testid="generation-notice" role="status" className="text-[11px] text-muted-foreground">
      {t("generation.interrupted", { reason: t(`generation.reason.${generation.reason}`) })}{" "}
      {t(`generation.recovery.${generation.recovery}`)}
      {detail ? <span className="ml-1 whitespace-pre-wrap wrap-break-word">{detail}</span> : null}
    </p>
  )
}
