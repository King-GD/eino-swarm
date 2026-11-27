import { Brain, ChevronDown } from "lucide-react"
import { useRef } from "react"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { useT } from "@/lib/use-t"
import { switchingComposerPopup, useComposerPopup } from "@/lib/use-composer-popup"
import { thinkingLabel } from "./composer-widgets"

export function ThinkingPicker({ levels, value, onChange, open, onOpenChange }: {
  levels: string[]
  value: string
  onChange: (value: string) => void
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const t = useT()
  const trigger = useRef<HTMLButtonElement>(null)
  const place = useComposerPopup(open, trigger)
  const label = (level: string) => level ? thinkingLabel(level, t) : t("composer.thinkingDefault")
  return (
    <DropdownMenu modal={false} open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        <Button ref={trigger} variant="ghost" className="composer-picker" aria-label={t("composer.thinking")}>
          <Brain /><span>{label(value)}</span><ChevronDown />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent data-composer-popup="thinking" align="start" side={place.side}
        onCloseAutoFocus={event => {
          if (switchingComposerPopup(trigger.current, "thinking")) event.preventDefault()
        }}
        sideOffset={place.sideOffset} collisionPadding={8} style={{ maxHeight: place.maxHeight }} loop>
        <DropdownMenuRadioGroup value={value} onValueChange={onChange}>
          {["", ...levels].map(level => (
            <DropdownMenuRadioItem key={level} value={level}>{label(level)}</DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
