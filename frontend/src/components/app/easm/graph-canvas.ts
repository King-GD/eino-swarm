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
  confirmed?: boolean
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
  /** Nodes whose branch the user collapsed, and the counts their badges quote. */
  collapsed?: ReadonlySet<string>
  descendants?: Map<string, number>
  hoveredId?: string | null
  selectedId?: string | null
  /** The node whose details are open: the card carries the same action. */
  cardId?: string | null
}

export const FONT_SIZE = 12
const LABEL_WIDTH = FONT_SIZE * 12

/** Collapse badge: a screen-constant circle just outside the lower-right edge,
 *  so it never covers the type glyph and stays clickable at any zoom. */
export const BADGE_RADIUS = 9

export function badgeCenter(node: GraphNode, zoom: number) {
  const scale = zoom || 1
  const reach = (node.radius * scale + BADGE_RADIUS * 0.7) / scale / Math.SQRT2
  return { x: (node.x ?? 0) + reach, y: (node.y ?? 0) + reach }
}

/** What the badge says, or null when this node shows none. A collapsed node
 *  always quotes its hidden branch; an expanded one only reveals "−" while the
 *  pointer or the selection is on it, and never behind an open card. */
export function badgeLabel(node: GraphNode, state: DrawState): string | null {
  const count = state.descendants?.get(node.id) ?? 0
  if (count < 1 || state.cardId === node.id) return null
  if (state.collapsed?.has(node.id)) return count > 99 ? "99+" : `+${count}`
  return state.hoveredId === node.id || state.selectedId === node.id ? "−" : null
}

export function drawCollapseBadge(node: GraphNode, ctx: CanvasRenderingContext2D, zoom: number, state: DrawState, palette: Palette) {
  const label = badgeLabel(node, state)
  if (!label) return
  const scale = zoom || 1
  const { x, y } = badgeCenter(node, scale)
  const radius = BADGE_RADIUS / scale
  const collapsed = state.collapsed?.has(node.id) === true
  ctx.save()
  ctx.beginPath(); ctx.arc(x, y, radius, 0, Math.PI * 2)
  if (collapsed) {
    ctx.fillStyle = palette.kind(node.kind)
    ctx.fill()
    ctx.fillStyle = palette.background
  } else {
    ctx.fillStyle = palette.background
    ctx.fill()
    ctx.strokeStyle = palette.kind(node.kind)
    ctx.lineWidth = Math.max(1, 1.4 / scale)
    ctx.stroke()
    ctx.fillStyle = palette.kind(node.kind)
  }
  const max = radius * 1.7
  let size = 11 / scale
  ctx.font = `600 ${size}px sans-serif`
  const width = ctx.measureText(label).width
  if (width > max) {
    size *= max / width
    ctx.font = `600 ${size}px sans-serif`
  }
  ctx.textAlign = "center"
  ctx.textBaseline = "middle"
  ctx.fillText(label, x, y)
  ctx.restore()
}

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
    const height = (width * icon.naturalHeight) / icon.naturalWidth
    ctx.drawImage(icon, x - width / 2, y - height / 2, width, height)
  }
  drawCollapseBadge(node, ctx, zoom, state, palette)
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
  ctx.setLineDash(link.confirmed === true ? [] : [4, 3])
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
