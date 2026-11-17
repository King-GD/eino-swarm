import { Button } from "@/components/ui/button"
import { useT } from "@/lib/use-t"
import type { Block } from "@/lib/transcript"
import { useApp } from "@/store/app"

export function IterationLimitCard({ block }: { block: Block }) {
  const t = useT()
  const extendTurn = useApp((s) => s.extendTurn)
  // The composer follows this server flag. The folded transcript can be idle
  // while the turn is still owned, and a decline is stored before that flag
  // clears. Either one used to paint the idle sentence over a live turn.
  const live = useApp((s) => s.status.running)
  const { limit = 0, extendBy = 0, continued, reason, automaticResume } = block.confirm ?? {}
  const collection = reason === "workers_uncollected"
  const open = Boolean(block.confirm?.pending && live)
  const closing = collection && live && block.confirm?.pending === false && !continued
  return (
    <div data-testid="iteration-limit" className="my-2 rounded-lg border border-border bg-muted/50 px-3 py-3 text-sm">
      <p className="text-foreground">
        {collection
          ? t(open ? automaticResume ? "transcript.collectionWaiting" : "transcript.collectionPaused"
            : continued ? "transcript.collectionContinued"
            : closing ? "transcript.collectionClosing"
            : "transcript.collectionStopped")
          : t("transcript.limitAsk", { limit, extendBy })}
      </p>
      {open ? (
        <div className="mt-3 flex gap-2">
          <Button size="sm" aria-label={t(collection ? "transcript.collectionContinue" : "transcript.continueAria")}
            onClick={() => void extendTurn(true)}>{t("transcript.continue")}</Button>
          <Button size="sm" variant="outline" aria-label={t(collection ? "transcript.collectionStop" : "transcript.stopAria")}
            onClick={() => void extendTurn(false)}>{t("transcript.stop")}</Button>
        </div>
      ) : !collection ? (
        <p className="mt-2 text-[13px] text-muted-foreground">
          {continued ? t("transcript.limitContinued", { extendBy }) : t("transcript.limitStopped", { limit })}
        </p>
      ) : null}
    </div>
  )
}
