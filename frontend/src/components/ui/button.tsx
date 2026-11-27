import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"
import * as React from "react"

import { cn } from "@/lib/cn"

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-(length:--chrome-font-size) font-medium transition-colors focus-visible:outline-hidden focus-visible:ring-[length:var(--button-focus-ring,2px)] focus-visible:ring-[var(--button-focus-color,hsl(var(--ring)))] focus-visible:ring-offset-[length:var(--button-focus-offset,1px)] focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        destructive:
          "bg-destructive text-destructive-foreground hover:bg-destructive/90",
        // Palette tokens restyle variants; a caller's className still wins.
        outline:
          "border border-input bg-[var(--button-quiet-bg,hsl(var(--background)))] text-[var(--button-quiet-ink,inherit)] hover:border-[var(--button-hover-border,hsl(var(--input)))] hover:bg-[var(--button-quiet-hover-bg,hsl(var(--accent)))] hover:text-[var(--button-hover-ink,hsl(var(--accent-foreground)))]",
        secondary:
          "bg-[var(--button-quiet-bg,hsl(var(--secondary)))] text-[var(--button-quiet-ink,hsl(var(--secondary-foreground)))] hover:bg-[var(--button-quiet-hover-bg,hsl(var(--secondary)/0.8))] hover:text-[var(--button-hover-ink,hsl(var(--secondary-foreground)))]",
        ghost: "hover:bg-accent hover:text-[var(--button-hover-ink,hsl(var(--accent-foreground)))]",
        link: "text-primary underline-offset-4 hover:underline hover:text-[var(--button-hover-ink,hsl(var(--primary)))]",
      },
      size: {
        default: "h-9 px-4 py-2",
        sm: "h-8 rounded-md px-3 text-[length:calc(var(--chrome-font-size)*0.92)]",
        lg: "h-10 rounded-md px-6",
        icon: "size-9 motion-safe:active:scale-[0.96]",
        "icon-sm": "size-7 rounded-md motion-safe:active:scale-[0.96]",
        "icon-xs": "size-6 rounded-md motion-safe:active:scale-[0.96] [&_svg]:size-3.5",
        "icon-2xs": "size-5 rounded-sm motion-safe:active:scale-[0.96] [&_svg]:size-3",
      },
    },
    compoundVariants: [
      {
        // Icon actions may drop the hover slab; row-shaped ghost buttons keep it.
        variant: "ghost",
        size: ["icon", "icon-sm", "icon-xs", "icon-2xs"],
        class: "hover:bg-[var(--button-icon-hover-bg,hsl(var(--accent)))]",
      },
    ],
    defaultVariants: { variant: "default", size: "default" },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button"
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        data-button-variant={variant ?? "default"}
        ref={ref}
        {...props}
      />
    )
  },
)
Button.displayName = "Button"

export { Button, buttonVariants }
