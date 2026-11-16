import { ContextMeter } from "./context-meter"
import { SlashMenu } from "./slash-menu"
import { compactHint, withSlashHints, type SlashCommand } from "@/lib/slash"
import type { UsageSnapshot } from "@/lib/types"
import { useT, type Translate } from "@/lib/use-t"
import { useApp } from "@/store/app"

/** Isolated so a live `usage` pulse (one per model call) cannot rewrite the
 *  controlled textarea while a CJK IME is composing. */
export function ComposerContextMeter({
  usage,
  window,
  scale,
}: {
  usage?: UsageSnapshot | null
  window: number
  scale: number
}) {
  const live = useApp((s) => s.usage)
  return <ContextMeter usage={usage ?? live} window={window} scale={scale} />
}

export function ComposerSlashMenu({
  commands,
  activeIndex,
  usage,
  window,
  contextChars,
  contextBudget,
  onHover,
  onSelect,
}: {
  commands: SlashCommand[]
  activeIndex: number
  usage?: UsageSnapshot | null
  window: number
  contextChars: number
  contextBudget: number
  onHover: (index: number) => void
  onSelect: (cmd: SlashCommand) => void
}) {
  const t = useT()
  const live = useApp((s) => s.usage)
  const snap = usage ?? live
  const items = withSlashHints(
    commands,
    {
      compact: compactHint(
        snap?.context_tokens,
        snap?.context_window || window,
        contextChars,
        contextBudget,
        t.locale,
      ),
    },
  )
  return (
    <SlashMenu
      items={items}
      activeIndex={Math.min(activeIndex, Math.max(0, items.length - 1))}
      onHover={onHover}
      onSelect={onSelect}
    />
  )
}

export function thinkingLabel(level: string, t: Translate): string {
  if (level === "low") return t("reason.low")
  if (level === "medium") return t("reason.medium")
  if (level === "high") return t("reason.high")
  return t("composer.thinkingLevel", { level })
}
