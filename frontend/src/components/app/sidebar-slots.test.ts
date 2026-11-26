import { readFileSync } from "node:fs"
import path from "node:path"
import tailwindcss from "@tailwindcss/postcss"
import postcss from "postcss"
import { describe, expect, it } from "vitest"

import {
  sidebarRowClass,
  sidebarSectionLabelClass,
  sidebarStackClass,
} from "./sidebar-slots"

describe("sidebar density tokens", () => {
  // html font-size is the transcript. A rem row (h-7) became 23px at the
  // default 13px root and the directory looked like a spreadsheet.
  it("sizes the directory from chrome font, not a rem utility", async () => {
    const from = path.resolve(__dirname, "../../index.css")
    const { css } = await postcss([tailwindcss({ optimize: false })]).process(
      readFileSync(from, "utf8"),
      { from },
    )
    expect(css).toMatch(/--sidebar-row-height:\s*calc\(var\(--chrome-font-size\)/)
    expect(css).not.toMatch(/--sidebar-row-height:\s*\d+px/)
    expect(css).toMatch(/--sidebar-kind:\s*calc\(var\(--chrome-font-size\)/)
    expect(sidebarRowClass).toContain("sidebar-row")
    expect(sidebarRowClass).not.toMatch(/\bh-7\b/)
    expect(sidebarRowClass).toContain("rounded-lg")
    expect(sidebarSectionLabelClass).toContain("sidebar-section-label")
    expect(sidebarSectionLabelClass).not.toMatch(/text-\[11px\]/)
    expect(sidebarStackClass).toContain("--sidebar-row-gap")
  })
})
