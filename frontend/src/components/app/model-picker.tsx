import { refreshCapabilities } from "@/lib/refresh-capabilities"
import { policyConfigurable } from "@/lib/model-policy"
import { FocusScope } from "@radix-ui/react-focus-scope"
import { useComposerPopup } from "@/lib/use-composer-popup"
import { Box, Check, ChevronDown, Loader2, Pencil, RefreshCw } from "lucide-react"
import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { officialModelLock } from "@/lib/account-models"
import { displayedModel, groupModels, modelChoiceId } from "@/lib/models"
import { chromeTypeClass } from "@/lib/chrome-type"
import type { ModelInfo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useT } from "@/lib/use-t"
import { useApp } from "@/store/app"

export function ModelPicker({
  models,
  providerId,
  model,
  onChange,
  onRefresh,
  onEdit,
  id,
  label: accessibleName,
  defaultLabel,
  disabled,
  open: controlledOpen,
  onOpenChange,
}: {
  models: ModelInfo[]
  providerId?: string
  model?: string
  onChange: (providerId: string, model: string) => void
  onRefresh?: () => Promise<void>
  onEdit?: () => void
  id?: string
  /** Accessible name when this picker is not the composer control. */
  label?: string
  /** If present, an empty selection represents the caller's own default. */
  defaultLabel?: string
  open?: boolean
  onOpenChange?: (open: boolean) => void
  disabled?: boolean
}) {
  const t = useT()
  const account = useApp((s) => s.meta?.account_models)
  const policy = useApp((s) => s.meta?.model_policy)
  // A fixed third-party conversation binding stays selectable in local mode.
  // Locking the whole menu from that row's capabilities traps the composer
  // on the first choice. Gateway policy and an official profile still hide it.
  const locked = officialModelLock(account, policy) || (policy?.mode === "gateway" && !policyConfigurable(policy, "conversation"))
  const [localOpen, setLocalOpen] = useState(false)
  const open = controlledOpen ?? localOpen
  const setOpen = onOpenChange ?? setLocalOpen
  const [dialogMenu, setDialogMenu] = useState(false)
  const [busy, setBusy] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const wrapRef = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const place = useComposerPopup(open, wrapRef)
  const restoreTrigger = () => wrapRef.current?.querySelector("button")?.focus({ preventScroll: true })
  const ready = models.filter((m) => m.ready && m.model)
  const groups = groupModels(ready)
  // An empty id follows the default row, including one the catalog no longer
  // contains. A known name that left the list stays on the button.
  const followsDefault = Boolean(defaultLabel && !providerId && !model)
  const selected = followsDefault ? undefined : displayedModel(models, providerId, model)
  const unavailable = Boolean((model?.trim() && !selected) || (selected && !selected.ready))
  const label = locked
    ? account?.label || t("model.fallback")
    : followsDefault ? defaultLabel : selected?.label || selected?.model || model?.trim() || t("model.fallback")

  useLayoutEffect(() => {
    if (open && !disabled && !dialogMenu) inputRef.current?.focus({ preventScroll: true })
  }, [open, disabled, dialogMenu])

  useEffect(() => {
    if (open && (disabled || locked)) setOpen(false)
  }, [open, disabled, locked, setOpen])

  useEffect(() => { if (open) void refreshCapabilities().catch(() => undefined) }, [open])

  useEffect(() => {
    if (!open) return
    const onDown = (event: PointerEvent) => {
      const t = event.target as Node
      if (wrapRef.current?.contains(t) || menuRef.current?.contains(t)) return
      setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") { setOpen(false); restoreTrigger() }
    }
    const onFocus = (event: FocusEvent) => {
      const target = event.target as Node
      if (!menuRef.current?.contains(target) && !wrapRef.current?.contains(target)) setOpen(false)
    }
    document.addEventListener("focusin", onFocus)
    document.addEventListener("pointerdown", onDown)
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("focusin", onFocus)
      document.removeEventListener("pointerdown", onDown)
      document.removeEventListener("keydown", onKey)
    }
  }, [open, setOpen])

  const command = (
    <>
      <Command
        label={t("model.search")}
        className="h-auto **:[[cmdk-group-heading]]:px-2 **:[[cmdk-group-heading]]:py-1.5 **:[[cmdk-group-heading]]:text-[11px] **:[[cmdk-group-heading]]:font-medium **:[[cmdk-group-heading]]:uppercase **:[[cmdk-group-heading]]:tracking-wide **:[[cmdk-group-heading]]:text-muted-foreground"
      >
        <CommandInput
          ref={inputRef}
          placeholder={t("model.search")}
          aria-label={t("model.search")}
          className="h-8 py-2 text-xs"
        />
        <CommandList className="max-h-64">
          <CommandEmpty>
            {ready.length === 0
              ? t("model.emptyNone")
              : t("model.emptyFilter")}
          </CommandEmpty>
          {defaultLabel ? (
            <CommandItem onSelect={() => { onChange("", ""); setOpen(false); restoreTrigger() }}>
              <Check className={cn("size-3.5", !followsDefault && "opacity-0")} />
              {defaultLabel}
            </CommandItem>
          ) : null}
          {groups.map((g) => (
            <CommandGroup key={g.providerId} heading={g.label}>
              {g.models.map((m) => {
                const id = m.id || modelChoiceId(m.provider_id, m.model)
                const isSelected =
                  selected?.provider_id === m.provider_id &&
                  selected?.model === m.model
                return (
                  <CommandItem
                    key={id}
                    value={`${g.providerId} ${g.label} ${m.label || m.model} ${m.model}`}
                    onSelect={() => {
                      onChange(m.provider_id, m.model)
                      setOpen(false)
                      restoreTrigger()
                    }}
                  >
                    <Check
                      className={cn(
                        "size-3.5",
                        isSelected ? "opacity-100" : "opacity-0",
                      )}
                    />
                    <span className="truncate">{m.label || m.model}</span>
                  </CommandItem>
                )
              })}
            </CommandGroup>
          ))}
        </CommandList>
      </Command>
      {onRefresh || onEdit ? (
        <div className="flex flex-col border-t">
          {onRefresh ? (
            <button
              type="button"
              className="wb-menu-action flex h-8 items-center gap-2 px-3 text-xs text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50"
              disabled={busy}
              onClick={() => {
                setBusy(true)
                void onRefresh()
                  .catch(() => undefined)
                  .finally(() => setBusy(false))
              }}
            >
              {busy ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <RefreshCw className="size-3.5" />
              )}
              {t("model.refresh")}
            </button>
          ) : null}
          {onEdit ? (
            <button
              type="button"
              className="wb-menu-action flex h-8 items-center gap-2 px-3 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
              onClick={() => {
                setOpen(false)
                onEdit()
              }}
            >
              <Pencil className="size-3.5" />
              {t("model.edit")}
            </button>
          ) : null}
        </div>
      ) : null}
    </>
  )
  // The menu is portaled onto the page. A modal dialog sets pointer-events:none
  // on that page and closes when a pointerdown reaches the document, so a click
  // on a model would land on the dialog and leave the saved choice unchanged.
  const menu =
    open && !disabled && !locked && typeof document !== "undefined"
      ? createPortal(
          <div
            ref={menuRef}
            data-composer-popup={controlledOpen === undefined ? undefined : "model"}
            className="pointer-events-auto fixed toast-layer w-72 max-w-[calc(100vw-1rem)] overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md"
            style={{ left: `clamp(8px, ${place.left}px, calc(100vw - min(18rem, 100vw - 1rem) - 8px))`,
              [place.side === "top" ? "bottom" : "top"]: place.edge, maxHeight: place.maxHeight, overflowY: "auto" }}
            onPointerDown={(event) => event.stopPropagation()}
          >
            {dialogMenu ? (
              <FocusScope loop trapped>
                {command}
              </FocusScope>
            ) : (
              command
            )}
          </div>,
          document.body,
        )
      : null

  return (
    <div ref={wrapRef} className="relative min-w-0 max-w-full" onKeyDown={(event) => {
      if (!open || event.key !== "Escape") return
      event.stopPropagation()
      setOpen(false)
      wrapRef.current?.querySelector("button")?.focus()
    }}>
      <button
        type="button"
        disabled={disabled}
        aria-disabled={locked || undefined}
        id={id}
        aria-label={accessibleName ?? t("composer.model")}
        aria-expanded={locked ? undefined : open && !disabled}
        aria-haspopup={locked ? undefined : "listbox"}
        title={locked ? undefined : unavailable ? t("model.unavailable") : undefined}
        data-unavailable={!locked && unavailable ? "true" : undefined}
        className={cn(
          "inline-flex h-7 max-w-[min(100%,14rem)] items-center gap-1.5 rounded-md px-2 disabled:opacity-50",
          !locked && "hover:bg-accent",
          chromeTypeClass,
          controlledOpen !== undefined && "composer-picker",
          unavailable && "text-muted-foreground",
          open && "bg-accent",
        )}
        onClick={() => {
          if (locked) return
          setDialogMenu(Boolean(wrapRef.current?.closest("[role='dialog']")))
          setOpen(!open)
        }}
      >
        {controlledOpen !== undefined ? <Box aria-hidden="true" /> : null}
        <span className="truncate">{label}</span>
        {locked ? null : <ChevronDown className="size-3.5 text-muted-foreground" />}
      </button>
      {menu}
    </div>
  )
}
