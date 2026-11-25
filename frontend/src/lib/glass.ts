/** The translucent hover surface FOFA uses for chart tooltips and hints:
 *  the popover tint over a blurred backdrop, with a hairline of its own
 *  text colour. Opacity stays on `--popover`, so every palette keeps its
 *  own tint instead of hardcoding a dark panel. */
export const glassSurface =
  "border border-popover-foreground/10 bg-popover/70 shadow-xl backdrop-blur-xl backdrop-saturate-150"

/** Chart hovers use the same glass, but a palette can tighten its blur,
 *  fill and shadow through tokens without beating caller classes. */
export const chartTooltipSurface =
  "chart-tooltip-glass border border-[var(--chart-tip-border,hsl(var(--popover-foreground)/0.1))] bg-[var(--chart-tip-bg,hsl(var(--popover)/0.7))] shadow-[var(--chart-tip-shadow,0_20px_25px_-5px_rgb(0_0_0/0.1),0_8px_10px_-6px_rgb(0_0_0/0.1))] [-webkit-backdrop-filter:var(--chart-tip-filter,blur(24px)_saturate(1.5))] [backdrop-filter:var(--chart-tip-filter,blur(24px)_saturate(1.5))]"
