import type { NodeObject } from "force-graph"

import type { DisplayKind, EasmNode } from "../../../lib/easm/types"

export interface GraphNode extends NodeObject {
  id: string
  node: EasmNode
  kind: DisplayKind
  radius: number
  lines: string[]
  /** Widest wrapped line at FONT_SIZE. */
  labelWidth: number
  degree: number
}

export interface GraphLink {
  source: GraphNode
  target: GraphNode
  confirmed: boolean
}

export interface Palette {
  text: string
  muted: string
  background: string
  edge: string
  highlight: string
  kind: (kind: DisplayKind) => string
  /** Root glow rings use the root colour at these opacities. */
  rootRing: (alpha: number) => string
}

/** Mutable view state the draw callbacks read every frame. */
export interface DrawState {
  rootId: string | null
  highlighted: Set<string>
  labels: Map<string, GraphNode>
  icons: Map<DisplayKind, HTMLImageElement>
}

export const FONT_SIZE = 12
const LABEL_WIDTH = FONT_SIZE * 12

export function drawNode(node: GraphNode, ctx: CanvasRenderingContext2D, zoom: number, state: DrawState, palette: Palette) {
  const { radius } = node
  const x = node.x ?? 0
  const y = node.y ?? 0
  const marked = state.highlighted.has(node.id)
  const color = palette.kind(node.kind)
  ctx.save()
  if (node.id === state.rootId) {
    // Root glow and two concentric rings, as in the reference graph.
    ctx.shadowColor = palette.kind("root")
    ctx.shadowBlur = 10
    ctx.lineWidth = 2
    ctx.strokeStyle = palette.rootRing(0.1)
    ctx.beginPath(); ctx.arc(x, y, (radius * 2.5) / 1.2, 0, Math.PI * 2); ctx.stroke()
    ctx.shadowBlur = 0
    ctx.strokeStyle = palette.rootRing(0.4)
    ctx.beginPath(); ctx.arc(x, y, (radius * 2.5) / 1.5, 0, Math.PI * 2); ctx.stroke()
  } else if (state.highlighted.size && !marked) {
    ctx.globalAlpha = 0.3
  }
  ctx.strokeStyle = marked ? palette.highlight : color
  ctx.lineWidth = Math.max(2, 0.5 / zoom)
  ctx.beginPath(); ctx.arc(x, y, radius * 1.25, 0, Math.PI * 2); ctx.stroke()
  ctx.fillStyle = color
  ctx.beginPath(); ctx.arc(x, y, radius, 0, Math.PI * 2); ctx.fill()
  const icon = state.icons.get(node.kind)
  if (icon?.complete && icon.naturalWidth) {
    const width = radius * 1.25
    const height = node.id === state.rootId ? width : (width * icon.naturalHeight) / icon.naturalWidth
    ctx.drawImage(icon, x - width / 2, y - height / 2, width, height)
  }
  ctx.restore()
}

export function drawLink(link: GraphLink, ctx: CanvasRenderingContext2D, zoom: number, state: DrawState, palette: Palette) {
  const start = link.source
  const end = link.target
  const dx = (end.x ?? 0) - (start.x ?? 0)
  const dy = (end.y ?? 0) - (start.y ?? 0)
  const length = Math.hypot(dx, dy)
  if (!length) return
  const marked = state.highlighted.has(start.id) && state.highlighted.has(end.id)
  const angle = Math.atan2(dy, dx)
  const startRadius = start.id === state.rootId ? 15 : 18
  const endRadius = end.id === state.rootId ? 15 : 18
  const x1 = (start.x ?? 0) + (startRadius * dx) / length
  const y1 = (start.y ?? 0) + (startRadius * dy) / length
  const x2 = (end.x ?? 0) - (endRadius * dx) / length
  const y2 = (end.y ?? 0) - (endRadius * dy) / length
  ctx.save()
  ctx.strokeStyle = ctx.fillStyle = marked ? palette.highlight : palette.edge
  ctx.lineWidth = marked ? Math.max(2, 0.8 / zoom) : Math.max(1, 0.25 / zoom)
  // Unconfirmed relations are dashed so they never read as verified ownership.
  ctx.setLineDash(link.confirmed ? [] : [4, 3])
  ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke()
  ctx.setLineDash([])
  const ax = x2 + 2 * Math.cos(angle)
  const ay = y2 + 2 * Math.sin(angle)
  ctx.beginPath()
  ctx.moveTo(ax, ay)
  ctx.lineTo(ax - 12 * Math.cos(angle) - 5 * Math.cos(angle - Math.PI / 2), ay - 12 * Math.sin(angle) - 5 * Math.sin(angle - Math.PI / 2))
  ctx.lineTo(ax - 12 * Math.cos(angle) - 5 * Math.cos(angle + Math.PI / 2), ay - 12 * Math.sin(angle) - 5 * Math.sin(angle + Math.PI / 2))
  ctx.closePath()
  ctx.fill()
  ctx.restore()
}

export function drawLabels(ctx: CanvasRenderingContext2D, zoom: number, state: DrawState, palette: Palette) {
  ctx.save()
  ctx.textAlign = "center"
  ctx.textBaseline = "top"
  ctx.lineJoin = "round"
  for (const node of state.labels.values()) {
    const fontWorld = Math.max(FONT_SIZE, FONT_SIZE * zoom) / zoom
    const lineWorld = fontWorld * 1.35
    ctx.font = `${node.id === state.rootId ? 600 : 400} ${fontWorld}px sans-serif`
    const x = node.x ?? 0
    const y = (node.y ?? 0) + node.radius * 2.1 + 5 / zoom
    ctx.strokeStyle = palette.background
    ctx.lineWidth = 3 / zoom
    ctx.fillStyle = !state.highlighted.size || state.highlighted.has(node.id) ? palette.text : palette.muted
    node.lines.forEach((line, index) => {
      ctx.strokeText(line, x, y + index * lineWorld)
      ctx.fillText(line, x, y + index * lineWorld)
    })
  }
  ctx.restore()
}

/** Only the hovered node is named, so the canvas stays uncluttered until the
 *  pointer asks a specific node what it is. */
export function chooseLabels(nodes: GraphNode[], focus: { hovered: string | null }): Map<string, GraphNode> {
  const node = focus.hovered ? nodes.find((candidate) => candidate.id === focus.hovered) : undefined
  if (!node || !Number.isFinite(node.x)) return new Map()
  return new Map([[node.id, node]])
}

export const MAX_LABEL_WIDTH = LABEL_WIDTH
