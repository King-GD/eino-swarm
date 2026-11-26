import * as React from "react"

import { cn } from "@/lib/utils"

const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.ComponentProps<"textarea">
>(({ className, ...props }, ref) => (
  <textarea
    className={cn(
      // Same chrome as Input. Without a border an empty box is just
      // leftover whitespace (New project's instruction, Memory notes).
      "flex w-full rounded-[var(--radius-surface,var(--radius-md))] border border-input bg-[var(--field-bg,hsl(var(--background)))] px-3 py-2 text-(length:--chrome-font-size) shadow-xs placeholder:text-muted-foreground focus-visible:border-[var(--field-focus-border,hsl(var(--input)))] focus-visible:outline-hidden focus-visible:ring-[length:var(--field-focus-ring,2px)] focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 resize-none",
      className,
    )}
    ref={ref}
    {...props}
  />
))
Textarea.displayName = "Textarea"

export { Textarea }
