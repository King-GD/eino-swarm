import { ChevronDown, Ellipsis } from "lucide-react"
import { useEffect, useId, useRef, useState } from "react"
import { createPortal } from "react-dom"

import { insertModes, type InsertMode } from "../../../frontend/src/lib/delivery-mode"
import { installAndroidBack } from "@/lib/android-back"
import { t } from "@/lib/i18n"
import { useAbove } from "./composer-menu"

export function DeliveryMenu({ onSelect, disabled, queuedText }: {
  onSelect: (mode: InsertMode) => void
  disabled?: boolean
  queuedText?: string
}) {
  const id = useId()
  const anchor = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const box = useAbove(open ? anchor.current : null, false)
  const label = queuedText === undefined ? t("delivery.options") : t("delivery.queuedOptions", { text: queuedText })
  useEffect(() => {
    if (!open) return
    const close = () => { setOpen(false); return true }
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      event.preventDefault()
      close()
    }
    const removeBack = installAndroidBack(close)
    window.addEventListener("keydown", onKey)
    return () => { window.removeEventListener("keydown", onKey); removeBack() }
  }, [open])
  return (
    <>
      <button ref={anchor} type="button" aria-label={label} aria-haspopup="menu"
        aria-expanded={open && !disabled} aria-controls={open && !disabled ? id : undefined} disabled={disabled}
        className="flex size-9 shrink-0 items-center justify-center rounded-full text-muted-foreground active:bg-accent disabled:opacity-40"
        onClick={() => setOpen((current) => !current)}>
        {queuedText === undefined ? <ChevronDown className="size-4" aria-hidden /> : <Ellipsis className="size-4" aria-hidden />}
      </button>
      {open && !disabled && box ? createPortal(
        <>
          <div className="fixed inset-0 z-40" onPointerDown={() => setOpen(false)} />
          <div id={id} role="menu" aria-label={label} style={box}
            className="fixed z-50 overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-lg">
            {(queuedText === undefined ? insertModes : ["interrupt"] as const).map((mode) => (
              <button key={mode} type="button" role="menuitem"
                className="flex min-h-11 w-full items-center px-3 text-left text-sm active:bg-accent"
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => { setOpen(false); onSelect(mode) }}>
                {t(`delivery.${mode}`)}
              </button>
            ))}
          </div>
        </>, document.body,
      ) : null}
    </>
  )
}
