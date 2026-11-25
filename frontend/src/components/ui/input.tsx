import * as React from "react"

import { cn } from "@/lib/cn"

const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, type, ...props }, ref) => (
    <input
      type={type}
      className={cn(
        "flex h-9 w-full rounded-[var(--radius-surface,var(--radius-md))] border border-input bg-[var(--field-bg,hsl(var(--background)))] px-3 py-1 text-(length:--chrome-font-size) shadow-xs transition-colors file:border-0 file:bg-transparent file:text-(length:--chrome-font-size) file:font-medium placeholder:text-muted-foreground focus-visible:border-[var(--field-focus-border,hsl(var(--input)))] focus-visible:outline-hidden focus-visible:ring-[length:var(--field-focus-ring,2px)] focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      ref={ref}
      {...props}
    />
  ),
)
Input.displayName = "Input"

export { Input }
