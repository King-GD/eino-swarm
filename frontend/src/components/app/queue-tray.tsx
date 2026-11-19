import { Trash2, X } from "lucide-react"
import { useState } from "react"

import { ConfirmDeleteDialog } from "@/components/app/confirm-delete-dialog"
import { DeliveryMenu } from "@/components/app/delivery-menu"
import { Button } from "@/components/ui/button"
import type { Followup } from "@/lib/types"
import type { InsertMode } from "@/lib/delivery-mode"
import { useT } from "@/lib/use-t"

/** Codex-style tray: messages waiting for the current turn to finish.
 *  Click a row to pull it into the composer and drop it from this list.
 *  Enter while the turn is still running queues that draft at the back.
 *  Steer injects one into this turn, or starts it when the turn has already
 *  stopped. Trash asks, then drops it. */
export function QueueTray({
  items,
  onSteer,
  onDelete,
  onClear,
  onEdit,
  turnId,
  disabled,
}: {
  items: Followup[]
  onSteer: (id: string, mode?: InsertMode) => void
  onDelete: (id: string) => void
  onClear: () => void
  onEdit: (item: Followup) => void
  turnId?: string
  disabled?: boolean
}) {
  const t = useT()
  const [doomed, setDoomed] = useState<Followup>()
  const [clearing, setClearing] = useState(false)

  if (items.length === 0) return null
  return (
    <div
      data-testid="followup-queue"
      className="mb-2 rounded-2xl border border-border bg-card px-3 py-2 shadow-lg"
    >
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium text-muted-foreground">
          {t("queue.count", { n: items.length })}
        </p>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("queue.clear")}
          onClick={() => setClearing(true)}
        >
          <X />
        </Button>
      </div>
      <ul className="mt-1 space-y-2">
        {items.map((item) => {
          const attached = (item.files?.length ?? 0) + (item.images?.length ?? 0)
          const text = item.text || t("delivery.attachments", { n: attached })
          return (
          <li key={item.id} className="flex items-start gap-2">
            {attached ? <span className="min-w-0 flex-1 truncate text-left text-sm" title={text}>{text}</span> : <button
              type="button"
              className="min-w-0 flex-1 truncate text-left text-sm hover:underline"
              title={item.text}
              aria-label={t("queue.editNamed", { text: item.text })}
              onClick={() => onEdit(item)}
            >
              {item.text}
            </button>}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 px-2"
              aria-label={t("queue.steerNamed", { text })}
              disabled={disabled}
              onClick={() => turnId ? onSteer(item.id, "steer") : onSteer(item.id)}
            >
              {turnId ? t("delivery.steer") : t("delivery.sendNow")}
            </Button>
            {turnId ? <DeliveryMenu disabled={disabled} queuedText={text} onSelect={(mode) => onSteer(item.id, mode)} /> : null}
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={t("queue.removeNamed", { text: item.text })}
              onClick={() => setDoomed(item)}
            >
              <Trash2 />
            </Button>
          </li>
          )
        })}
      </ul>
      <ConfirmDeleteDialog
        open={Boolean(doomed)}
        title={t("queue.deleteTitle")}
        description={t("queue.deleteDesc")}
        confirmLabel={t("queue.deleteConfirm")}
        cancelLabel={t("confirm.cancel")}
        onOpenChange={(open) => {
          if (!open) setDoomed(undefined)
        }}
        onConfirm={() => {
          if (doomed) onDelete(doomed.id)
        }}
      />
      <ConfirmDeleteDialog
        open={clearing}
        title={t("queue.clearTitle")}
        description={t("queue.clearDesc", { n: items.length })}
        confirmLabel={t("queue.clearConfirm")}
        cancelLabel={t("confirm.cancel")}
        onOpenChange={setClearing}
        onConfirm={onClear}
      />
    </div>
  )
}
