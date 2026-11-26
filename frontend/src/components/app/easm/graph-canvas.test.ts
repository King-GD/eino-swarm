import { describe, expect, it, vi } from "vitest"
import { badgeCenter, badgeLabel, drawLink, drawNode, type DrawState, type GraphNode, type Palette } from "./graph-canvas"
import { KIND_ICONS } from "./kind-style"

it("draws the same enterprise icon at its intrinsic ratio and highlights only the center", () => {
  expect.soft(KIND_ICONS.root).toBe(KIND_ICONS.company)

  const icon = { complete: true, naturalWidth: 20, naturalHeight: 10 } as HTMLImageElement
  const state: DrawState = {
    rootId: "root", highlighted: new Set(), labels: new Map(), icons: new Map([["root", icon], ["company", icon]]),
  }
  const root: GraphNode = {
    id: "root", kind: "root", node: { id: "root", kind: "root", label: "Parent" },
    x: 0, y: 0, radius: 8, lines: [], labelWidth: 0, degree: 1,
  }
  const company: GraphNode = {
    ...root, id: "company", kind: "company", node: { id: "company", kind: "company", label: "Subsidiary" }, x: 100,
  }
  const palette: Palette = {
    text: "#000", muted: "#555", background: "#fff", edge: "#000", highlight: "#f00",
    kind: () => "#000", rootRing: (alpha) => `rgba(0, 0, 0, ${alpha})`,
  }
  const strokes: { color: string; blur: number }[] = []
  const drawImage = vi.fn()
  const canvas = {
    strokeStyle: "", shadowBlur: 0,
    stroke() { strokes.push({ color: this.strokeStyle, blur: this.shadowBlur }) },
    beginPath: vi.fn(), arc: vi.fn(), save: vi.fn(), fill: vi.fn(), restore: vi.fn(), drawImage,
  }
  const ctx = canvas as unknown as CanvasRenderingContext2D

  drawNode(root, ctx, 1, state, palette)
  drawNode(company, ctx, 1, state, palette)

  expect.soft(drawImage.mock.calls).toEqual([[icon, -5, -2.5, 10, 5], [icon, 95, -2.5, 10, 5]])
  expect(strokes).toEqual([
    { color: palette.rootRing(0.1), blur: 10 }, { color: palette.rootRing(0.4), blur: 0 },
    { color: "#000", blur: 0 }, { color: "#000", blur: 0 },
  ])
})

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
// The badge is the only collapse affordance on the canvas, so its visibility
// rules decide when a branch can be folded at all.
describe("EASM collapse badge", () => {
  const node: GraphNode = {
    id: "cert", kind: "cert", node: { id: "cert", kind: "feature", featureKind: "cert", label: "cert" },
    x: 40, y: 60, radius: 8, lines: [], labelWidth: 0, degree: 2,
  }
  const base = (): DrawState => ({ rootId: null, highlighted: new Set(), labels: new Map(), icons: new Map() })

  it("stays silent until the pointer or the selection reaches a branching node", () => {
    const state = { ...base(), descendants: new Map([["cert", 4]]) }
    expect(badgeLabel(node, state)).toBeNull()
    expect(badgeLabel(node, { ...state, hoveredId: "cert" })).toBe("−")
    expect(badgeLabel(node, { ...state, selectedId: "cert" })).toBe("−")
    // An open card owns the action; the canvas must not offer it twice.
    expect(badgeLabel(node, { ...state, selectedId: "cert", cardId: "cert" })).toBeNull()
    expect(badgeLabel({ ...node, id: "leaf" }, state)).toBeNull()
  })

  it("always quotes a folded branch, capped so a huge branch cannot outgrow the circle", () => {
    const state = { ...base(), collapsed: new Set(["cert"]) }
    expect(badgeLabel(node, { ...state, descendants: new Map([["cert", 4]]) })).toBe("+4")
    expect(badgeLabel(node, { ...state, descendants: new Map([["cert", 120]]) })).toBe("99+")
  })

  it("grows the badge outside the node edge and lands the hit where it paints", () => {
    const center = badgeCenter(node, 1)
    expect(center.x).toBeGreaterThan(node.x!)
    expect(center.y).toBeGreaterThan(node.y!)
    expect(Math.hypot(center.x - node.x!, center.y - node.y!)).toBeGreaterThan(node.radius)
    // On screen the badge sits a fixed 6.3px outside the node edge, so zooming
    // moves it outward only as far as the node itself grows.
    const wide = badgeCenter(node, 4)
    const near = Math.hypot(center.x - node.x!, center.y - node.y!)
    const far = Math.hypot(wide.x - node.x!, wide.y - node.y!) * 4
    expect(far - near).toBeCloseTo(node.radius * 3, 1)
  })
})
