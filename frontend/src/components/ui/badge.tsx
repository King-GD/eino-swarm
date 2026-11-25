import { cva, type VariantProps } from "class-variance-authority"
import type * as React from "react"

import { cn } from "@/lib/cn"

const badgeVariants = cva(
  "inline-flex h-[var(--badge-height,auto)] items-center gap-1 rounded-md border px-2 py-0.5 text-[length:var(--badge-font-size,calc(var(--chrome-font-size)*0.85))] leading-[var(--badge-leading,inherit)] font-medium transition-colors",
  {
    variants: {
      variant: {
        default: "border-transparent bg-[var(--badge-bg,hsl(var(--secondary)))] text-[var(--badge-ink,hsl(var(--secondary-foreground)))]",
        outline: "border-[var(--badge-outline-border,hsl(var(--border)))] text-muted-foreground",
        success: "border-transparent bg-[var(--badge-bg,hsl(var(--done)/0.15))] text-done",
        warning: "border-transparent bg-[var(--badge-bg,hsl(var(--running)/0.15))] text-running",
        danger: "border-transparent bg-[var(--badge-bg,hsl(var(--destructive)/0.15))] text-destructive",
        ask: "border-transparent bg-[var(--badge-bg,hsl(var(--ask)/0.15))] text-ask",
      },
    },
    defaultVariants: { variant: "default" },
  },
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div
      className={cn(badgeVariants({ variant }), className)}
      {...props}
    />
  )
}

export { badgeVariants }
