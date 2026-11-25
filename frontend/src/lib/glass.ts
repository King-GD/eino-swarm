/** The translucent hover surface FOFA uses for chart tooltips and hints:
 *  the popover tint over a blurred backdrop, with a hairline of its own
 *  text colour. Opacity stays on `--popover`, so every palette keeps its
 *  own tint instead of hardcoding a dark panel. */
export const glassSurface =
  "border border-popover-foreground/10 bg-popover/70 shadow-xl backdrop-blur-xl backdrop-saturate-150"

export const chartTooltipSurface = `${glassSurface} chart-tooltip-glass`
