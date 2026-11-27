import { describe, expect, it } from "vitest"

import { filterRemoteTools, toolHoverText, toolMenuMatches } from "./composer-menu"

import { popupPlacement } from "./use-composer-popup"

it("anchors the popup to the toolbar button and flips only when space runs out", () => {
  expect(popupPlacement(new DOMRect(20, 700, 60, 30), 800, true)).toMatchObject({side: "top", edge: 104, maxHeight: 320})
  expect(popupPlacement(new DOMRect(20, 210, 60, 30), 800, true)).toMatchObject({side: "top", edge: 594})
  expect(popupPlacement(new DOMRect(20, 80, 60, 30), 360, true)).toMatchObject({side: "bottom", edge: 114, maxHeight: 238})
})

describe("composer tools menu filter", () => {
  const tools = [{ name: "lookup" }, { name: "status" }]

  it("matches a tool or a connection and keeps a blank query open", () => {
    expect(toolMenuMatches("  ", ["Read file"])).toBe(true)
    expect(toolMenuMatches("FILE", ["Read file", "read"])).toBe(true)
    expect(toolMenuMatches("python", ["Read file", "read"])).toBe(false)
    expect(filterRemoteTools(tools, "", "Desk", "desk")).toEqual({ shown: true, tools })
    expect(filterRemoteTools(tools, "desk", "Desk", "desk").tools).toEqual(tools)
    expect(filterRemoteTools(tools, "LOOK", "Desk", "desk").tools).toEqual([{ name: "lookup" }])
    expect(filterRemoteTools(tools, "missing", "Desk", "desk").shown).toBe(false)
    expect(filterRemoteTools([], "desk", "Desk", "desk")).toEqual({ shown: true, tools: [] })
    expect(toolHoverText("read", "Read a file")).toBe("read\nRead a file")
    expect(toolHoverText("read", "  ")).toBe("read")
    expect(toolHoverText("read", "read")).toBe("read")
  })
})
