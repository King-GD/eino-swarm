import * as TooltipPrimitive from "@radix-ui/react-tooltip"
import * as React from "react"

import { cn } from "@/lib/cn"
import { glassSurface } from "../../lib/glass"

const TooltipProvider = TooltipPrimitive.Provider
const Tooltip = TooltipPrimitive.Root
const TooltipTrigger = TooltipPrimitive.Trigger

const TooltipContent = React.forwardRef<
  React.ElementRef<typeof TooltipPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>
>(({ className, sideOffset = 6, ...props }, ref) => (
  <TooltipPrimitive.Portal>
    <TooltipPrimitive.Content
      ref={ref}
      sideOffset={sideOffset}
      className={cn(
        glassSurface,
        "z-50 max-w-[var(--tooltip-max-width,none)] overflow-hidden rounded-[var(--radius-tooltip,var(--radius-lg))] p-[var(--tooltip-pad,0.375rem_0.625rem)] text-[length:var(--tooltip-font-size,var(--text-xs))] data-[state=open]:animate-in data-[state=open]:ease-out data-[state=closed]:animate-out data-[state=closed]:ease-in data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[side=top]:slide-in-from-bottom-1 data-[side=bottom]:slide-in-from-top-1 data-[side=left]:slide-in-from-right-1 data-[side=right]:slide-in-from-left-1",
        className,
      )}
      {...props}
    />
  </TooltipPrimitive.Portal>
))
TooltipContent.displayName = TooltipPrimitive.Content.displayName

export { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger }
