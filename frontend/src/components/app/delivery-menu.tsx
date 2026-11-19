import { ChevronDown, Ellipsis } from "lucide-react"

import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { insertModes, type InsertMode } from "@/lib/delivery-mode"
import { useT } from "@/lib/use-t"

export function DeliveryMenu({ onSelect, disabled, queuedText }: {
  onSelect: (mode: InsertMode) => void
  disabled?: boolean
  queuedText?: string
}) {
  const t = useT()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" disabled={disabled}
          aria-label={queuedText === undefined ? t("delivery.options") : t("delivery.queuedOptions", { text: queuedText })}>
          {queuedText === undefined ? <ChevronDown /> : <Ellipsis />}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="end">
        {(queuedText === undefined ? insertModes : ["interrupt"] as const).map((mode) => (
          <DropdownMenuItem key={mode} onSelect={() => onSelect(mode)}>{t(`delivery.${mode}`)}</DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
