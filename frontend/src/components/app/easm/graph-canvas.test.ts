import { expect, it, vi } from "vitest"
import { drawLink, type DrawState, type GraphNode, type Palette } from "./graph-canvas"

it("paints confirmed relationships solid and pending or unknown relationships dashed", () => {
  const state: DrawState = { rootId: "root", highlighted: new Set(), labels: new Map(), icons: new Map() }
  const palette = { edge: "#000", highlight: "#fff" } as Palette
  const source = { id: "root", x: 0, y: 0 } as GraphNode
  const target = { id: "domain", x: 100, y: 0 } as GraphNode
  const strokePatterns: number[][] = []
  let dash: number[] = []
  const ctx = {
    setLineDash(pattern: number[]) { dash = pattern },
    stroke() { strokePatterns.push(dash) },
    beginPath: vi.fn(), moveTo: vi.fn(), lineTo: vi.fn(), closePath: vi.fn(),
    save: vi.fn(), fill: vi.fn(), restore: vi.fn(),
  } as unknown as CanvasRenderingContext2D

  for (const confirmed of [true, false, undefined]) {
    const link = { source, target, confirmed }
    drawLink(link, ctx, 1, state, palette)
    expect(dash).toEqual([])
  }

  expect(strokePatterns).toEqual([[], [4, 3], [4, 3]])
})
