/** Scrolls jump instead of gliding when the person asks for less motion.
 *  The browser's smooth scroll ignores that preference for programmatic
 *  `behavior: "smooth"`, so every caller has to ask. */
export function scrollBehavior(): ScrollBehavior {
  if (typeof window === "undefined") return "auto"
  return window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches
    ? "auto"
    : "smooth"
}
