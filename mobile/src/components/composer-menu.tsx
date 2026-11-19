import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { Flag, Image, ListTodo, Paperclip } from "lucide-react"

import { installAndroidBack } from "@/lib/android-back"
import { cn } from "@/lib/cn"
import { t } from "@/lib/i18n"
import type { SlashCommand } from "../../../frontend/src/lib/slash-core"

function CommandIcon({ id }: { id: string }) {
  const Icon = id === "image" ? Image : id === "plan" ? ListTodo : Flag
  return <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
}

function placeAbove(anchor: HTMLElement, matchWidth: boolean) {
  const rect = anchor.getBoundingClientRect()
  const margin = 12
  const width = matchWidth ? rect.width : Math.min(320, window.innerWidth - margin * 2)
  const left = Math.min(Math.max(margin, rect.left), Math.max(margin, window.innerWidth - width - margin))
  return { left, width, bottom: window.innerHeight - rect.top + 8 }
}

export function useAbove(anchor: HTMLElement | null, matchWidth: boolean) {
  const [box, setBox] = useState<ReturnType<typeof placeAbove>>()
  useLayoutEffect(() => {
    if (!anchor) return
    const measure = () => setBox(placeAbove(anchor, matchWidth))
    measure()
    const viewport = window.visualViewport
    window.addEventListener("resize", measure)
    viewport?.addEventListener("resize", measure)
    viewport?.addEventListener("scroll", measure)
    return () => {
      window.removeEventListener("resize", measure)
      viewport?.removeEventListener("resize", measure)
      viewport?.removeEventListener("scroll", measure)
    }
  }, [anchor, matchWidth])
  return box
}

/** Files and the commands a person may not remember, in one list above Add. */
export function ComposerAddMenu({
  anchor,
  commands,
  onAttach,
  onPick,
  onClose,
}: {
  anchor: HTMLElement | null
  commands: readonly SlashCommand[]
  onAttach: () => void
  onPick: (command: SlashCommand) => void
  onClose: () => void
}) {
  const box = useAbove(anchor, false)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  useEffect(() => installAndroidBack(() => {
    closeRef.current()
    return true
  }), [])
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      event.preventDefault()
      event.stopPropagation()
      closeRef.current()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])
  if (!box) return null
  return createPortal(
    <>
      <div data-testid="composer-add-scrim" className="fixed inset-0 z-40" onPointerDown={() => closeRef.current()} />
      <div
        id="composer-add-menu"
        role="menu"
        aria-label={t("composer.add")}
        data-testid="composer-add-menu"
        className="fixed z-50 overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-lg"
        style={{ left: box.left, width: box.width, bottom: box.bottom }}
        onPointerDown={(event) => event.stopPropagation()}
      >
        <button
          type="button"
          role="menuitem"
          className="flex min-h-11 w-full items-center gap-3 px-3 text-left text-sm active:bg-accent"
          onMouseDown={(event) => event.preventDefault()}
          onClick={onAttach}
        >
          <Paperclip className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          <span>{t("composer.attach")}</span>
        </button>
        {commands.length > 0 ? (
          <div className="border-t border-border" role="group" aria-label={t("slash.commands")}>
            {commands.map((command) => (
                <button
                  key={command.id}
                  type="button"
                  role="menuitem"
                  data-testid={`composer-command-${command.id}`}
                  className="flex min-h-11 w-full items-center gap-3 px-3 text-left text-sm active:bg-accent"
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => onPick(command)}
                >
                  <CommandIcon id={command.id} />
                  <span className="w-14 shrink-0 font-medium">/{command.name}</span>
                  <span className="min-w-0 flex-1 truncate text-muted-foreground">{command.description}</span>
                </button>
              ))}
          </div>
        ) : null}
      </div>
    </>,
    document.body,
  )
}

/** The same catalog, filtered by the `/` token still being typed. */
export function ComposerSlashMenu({
  anchor,
  commands,
  activeIndex,
  onSelect,
}: {
  anchor: HTMLElement | null
  commands: readonly SlashCommand[]
  activeIndex: number
  onSelect: (command: SlashCommand) => void
}) {
  const box = useAbove(anchor, true)
  if (!box || commands.length === 0) return null
  const active = Math.min(activeIndex, commands.length - 1)
  return createPortal(
    <div
      id="slash-menu"
      role="listbox"
      aria-label={t("slash.commands")}
      data-testid="slash-menu"
      className="fixed z-50 overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-lg"
      style={{ left: box.left, width: box.width, bottom: box.bottom }}
    >
      <ul className="max-h-64 overflow-y-auto p-1">
        {commands.map((command, index) => {
          const selected = index === active
          return (
            <li key={command.id}>
              <button
                type="button"
                role="option"
                aria-selected={selected}
                data-testid={`slash-command-${command.id}`}
                className={cn(
                  "flex min-h-11 w-full items-center gap-3 rounded-xl px-2 text-left text-sm",
                  selected ? "bg-accent text-accent-foreground" : "active:bg-accent",
                )}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => onSelect(command)}
              >
                <CommandIcon id={command.id} />
                <span className="w-14 shrink-0 font-medium">/{command.name}</span>
                <span className="min-w-0 flex-1 truncate text-muted-foreground">{command.description}</span>
              </button>
            </li>
          )
        })}
      </ul>
    </div>,
    document.body,
  )
}
