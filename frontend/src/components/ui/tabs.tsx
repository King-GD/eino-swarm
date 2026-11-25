import * as TabsPrimitive from "@radix-ui/react-tabs"
import * as React from "react"

import { cn } from "@/lib/utils"

const Tabs = TabsPrimitive.Root

const TabsList = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.List>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.List>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.List
    ref={ref}
    data-slot="tabs-list"
    className={cn(
      "inline-flex h-[var(--tabs-height,2rem)] items-center justify-center rounded-[var(--radius-tabs,var(--radius-md))] bg-[var(--tabs-bg,hsl(var(--muted)))] p-[var(--tabs-pad,0.125rem)] text-muted-foreground",
      className,
    )}
    {...props}
  />
))
TabsList.displayName = TabsPrimitive.List.displayName

const TabsTrigger = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Trigger>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Trigger
    ref={ref}
    data-slot="tabs-trigger"
    className={cn(
      // The indicator bar exists in every palette; it is transparent unless
      // the palette sets --tabs-indicator.
      "relative inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[var(--radius-tabs,0.25rem)] px-2.5 py-1 text-xs font-medium text-[var(--tabs-ink,inherit)] transition-[color,background-color,box-shadow] after:pointer-events-none after:absolute after:inset-x-0 after:-bottom-px after:h-0.5 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 data-[state=inactive]:hover:text-[var(--tabs-hover-ink,inherit)] data-[state=active]:bg-[var(--tabs-active-bg,hsl(var(--background)))] data-[state=active]:text-[var(--tabs-active-ink,hsl(var(--foreground)))] data-[state=active]:shadow-xs data-[state=active]:[--tw-shadow-color:var(--tabs-active-shadow)] data-[state=active]:after:bg-[var(--tabs-indicator,transparent)]",
      className,
    )}
    {...props}
  />
))
TabsTrigger.displayName = TabsPrimitive.Trigger.displayName

const TabsContent = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Content>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Content
    ref={ref}
    className={cn(
      // min-h-0 lets a flex-1 pane shrink below its content so Files,
      // Trace and Memory can scroll instead of clipping at the window.
      "flex-1 min-h-0 overflow-hidden focus-visible:outline-hidden",
      className,
      // After `className`: the Agents pane passes `flex` so a worker log can
      // fill it. Tailwind's [hidden] rule and .flex have equal specificity,
      // so an inactive flex pane ate half the Memory tab. The important
      // hidden has to win that fight without wiping `flex` on the active pane.
      "data-[state=inactive]:hidden!",
    )}
    {...props}
  />
))
TabsContent.displayName = TabsPrimitive.Content.displayName

export { Tabs, TabsContent, TabsList, TabsTrigger }
