import { Check, Copy } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { copyText } from "@/lib/copy-text"
import { useT } from "@/lib/use-t"
import { cn } from "@/lib/utils"

export function CopyButton({
  text,
  className,
  label,
  size = "icon-sm",
  "aria-label": ariaLabel,
}: {
  text: string
  className?: string
  label?: string
  size?: "icon-sm" | "icon-xs" | "icon-2xs"
  "aria-label"?: string
}) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  const name = ariaLabel ?? label ?? t("transcript.copy")
  // The button size owns the hit area; the glyph keeps the old optical size
  // so a labelled copy button does not inherit Button's larger icon.
  const glyph = size === "icon-2xs" ? "size-3" : "size-3.5"
  return (
    <Button
      type="button"
      variant="ghost"
      size={label ? "sm" : size}
      className={className}
      onClick={() => {
        void copyText(text).then((ok) => {
          if (!ok) return
          setCopied(true)
          setTimeout(() => setCopied(false), 1200)
        })
      }}
      title={copied ? t("transcript.copied") : name}
      aria-label={name}
    >
      {/* Both glyphs share one grid cell so the swap cross-fades in place
          instead of replacing the icon in a single frame. */}
      <span data-copy-glyph className="relative inline-grid shrink-0 place-items-center">
        <Copy
          className={cn(
            glyph,
            "col-start-1 row-start-1 transition-[opacity,transform] duration-(--duration-fast) ease-out motion-reduce:transition-none",
            copied ? "scale-75 opacity-0" : "opacity-100",
          )}
        />
        <Check
          className={cn(
            glyph,
            "col-start-1 row-start-1 transition-[opacity,transform] duration-(--duration-fast) ease-out motion-reduce:transition-none",
            copied ? "opacity-100" : "scale-75 opacity-0",
          )}
        />
      </span>
      {label ? <span>{copied ? t("transcript.copied") : label}</span> : null}
    </Button>
  )
}
