import ForceGraph from "force-graph"
import { Loader2, Search } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import enlargeIcon from "../../../assets/easm/enlarge.svg"
import shrinkIcon from "../../../assets/easm/shrink.svg"
import fitIcon from "../../../assets/easm/zoomfit.svg"
import { Input } from "../../ui/input"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../../ui/tooltip"
import {
  displayKind, graphLabel, highlightIds, kindLabel, layoutOrder, nodeRadius, searchNodes, summaryStats, visibleIds, wrapLabel,
  type EasmIndex,
} from "../../../lib/easm/model"
import { useEasmText } from "../../../lib/easm/messages"
import type { DisplayKind, EasmResult } from "../../../lib/easm/types"
import { cn } from "@/lib/cn"
import { chooseLabels, drawLabels, drawLink, drawNode, FONT_SIZE, MAX_LABEL_WIDTH, type DrawState, type GraphLink, type GraphNode, type Palette } from "./graph-canvas"
import { KIND_ICONS, KIND_TOKENS, tokenColor } from "./kind-style"
import { KindIcon, NodeCard } from "./node-card"

/** Tests and the dialog read a node's on-canvas point to click it. */
export interface GraphElement extends HTMLDivElement { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

type Focus = { id: string; mode: "path" | "node" } | null

const MIN_ZOOM = 0.01
const MAX_ZOOM = 1.5
let measureContext: CanvasRenderingContext2D | null = null
function measure(text: string) {
  measureContext ??= document.createElement("canvas").getContext("2d")
  if (!measureContext) return text.length * FONT_SIZE
  measureContext.font = `${FONT_SIZE}px sans-serif`
  return measureContext.measureText(text).width
}

function readPalette(element: Element): Palette {
  const kinds = new Map(Object.entries(KIND_TOKENS).map(([kind, token]) => [kind, tokenColor(element, token)]))
  const root = getComputedStyle(element).getPropertyValue(KIND_TOKENS.root).trim()
  return {
    text: tokenColor(element, "--foreground"),
    muted: tokenColor(element, "--muted-foreground"),
    background: tokenColor(element, "--card"),
    edge: tokenColor(element, "--easm-edge"),
    highlight: tokenColor(element, "--easm-highlight"),
    kind: (kind: DisplayKind) => kinds.get(kind) ?? "gray",
    rootRing: (alpha: number) => `hsl(${root} / ${alpha})`,
  }
}

function MaskIcon({ src }: { src: string }) {
  return <span aria-hidden className="size-4 bg-current" style={{ mask: `url("${src}") no-repeat center / contain`, WebkitMask: `url("${src}") no-repeat center / contain` }} />
}

const toolButton = "grid size-7 place-items-center rounded-md border bg-card/95 text-muted-foreground shadow-sm hover:border-primary hover:text-primary"

export function AssetGraph({ result, index, loading = false, expanded = false, mobile = false, visible: active = true }: {
  result: EasmResult
  index: EasmIndex
  loading?: boolean
  expanded?: boolean
  mobile?: boolean
  visible?: boolean
}) {
  const text = useEasmText()
  const textRef = useRef(text)
  textRef.current = text
  const [hidden, setHidden] = useState<Set<DisplayKind>>(() => new Set())
  const [selected, setSelected] = useState<string | null>(index.rootId ?? result.nodes[0]?.id ?? null)
  const [focus, setFocus] = useState<Focus>(null)
  const [cardId, setCardId] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const view = useMemo(() => layoutOrder(result.nodes, result.edges, index.rootId), [result, index.rootId])
  const visible = useMemo(() => visibleIds(view.nodes, view.edges, hidden), [view, hidden])
  const currentSelection = selected && visible.has(selected) ? selected : index.rootId

  const sectionRef = useRef<HTMLElement>(null)
  const stageRef = useRef<GraphElement>(null)
  const canvasMountRef = useRef<HTMLDivElement>(null)
  const cardRef = useRef<HTMLElement>(null)
  const graphRef = useRef<InstanceType<typeof ForceGraph<GraphNode, GraphLink>> | null>(null)
  const drawState = useRef<DrawState>({ rootId: index.rootId, highlighted: new Set(), labels: new Map(), icons: new Map() })
  const paletteRef = useRef<Palette | null>(null)
  const nodesRef = useRef<GraphNode[]>([])
  const visibleRef = useRef<Set<string>>(new Set())
  const hoveredRef = useRef<string | null>(null)
  const focusRef = useRef({ selected, cardId })
  const labelTimer = useRef(0)
  const lastLabels = useRef(0)
  const lastAutoFit = useRef(0)
  const retainedViewport = useRef<{ zoom: number; x: number; y: number } | null>(null)
  const activeRef = useRef(active)
  activeRef.current = active
  const fitOnStop = useRef(false)
  // First data is fitted before it appears.
  const pendingFit = useRef(false)
  // The settled layout is fitted again unless the user has panned or zoomed.
  const userMoved = useRef(false)
  focusRef.current = { selected: currentSelection, cardId }

  const stats = summaryStats(result.summary, text)
  const graphKinds = new Set(view.nodes.map(displayKind))
  const legend = index.legend.filter((kind) => kind !== "root" && graphKinds.has(kind))

  useEffect(() => {
    setSelected(currentSelection)
    setFocus((current) => current && visible.has(current.id) ? current : null)
    setCardId((current) => current && index.byId.has(current) ? current : null)
    drawState.current.rootId = index.rootId
    if (stageRef.current) stageRef.current.dataset.selectedId = currentSelection ?? ""
  }, [currentSelection, index.byId, index.rootId, visible])

  const repositionCard = useCallback(() => {
    const graph = graphRef.current
    const card = cardRef.current
    const section = sectionRef.current
    const stage = stageRef.current
    const id = focusRef.current.cardId
    if (!graph || !card || !section || !stage || !id) return
    const node = nodesRef.current.find((n) => n.id === id)
    const point = node && visibleRef.current.has(id) && Number.isFinite(node.x)
      ? graph.graph2ScreenCoords(node.x ?? 0, node.y ?? 0)
      : { x: stage.clientWidth / 2, y: stage.clientHeight / 2 }
    const x = stage.offsetLeft + point.x
    const y = stage.offsetTop + point.y
    const width = card.offsetWidth
    const height = card.offsetHeight
    let left = x + 24
    if (left + width > section.clientWidth - 12) left = x - width - 24
    card.style.left = `${Math.max(12, Math.min(left, section.clientWidth - width - 12))}px`
    card.style.top = `${Math.max(12, Math.min(y - 40, section.clientHeight - height - 12))}px`
  }, [])

  const updateLabels = useCallback(() => {
    window.clearTimeout(labelTimer.current)
    labelTimer.current = 0
    const graph = graphRef.current
    const stage = stageRef.current
    if (!graph || !stage || !nodesRef.current.length || !Number.isFinite(nodesRef.current[0].x)) return
    drawState.current.labels = chooseLabels(nodesRef.current.filter((n) => visibleRef.current.has(n.id)), { hovered: hoveredRef.current })
    stage.dataset.visibleLabels = String(drawState.current.labels.size)
    lastLabels.current = performance.now()
    graph.nodeCanvasObject(graph.nodeCanvasObject())
    repositionCard()
  }, [repositionCard])
  const queueLabels = useCallback((delay = 0) => {
    window.clearTimeout(labelTimer.current)
    labelTimer.current = window.setTimeout(updateLabels, delay)
  }, [updateLabels])

  // Fit the whole visible view; the reference never zooms past 1:1, where one
  // node and its label would fill the canvas.
  const fit = useCallback(() => {
    const graph = graphRef.current
    if (!graph || !nodesRef.current.length || !Number.isFinite(nodesRef.current[0].x)) return
    retainedViewport.current = null
    graph.zoomToFit(0, 40, (n) => visibleRef.current.has(n.id))
    if (graph.zoom() > 1) graph.zoom(1)
    queueLabels()
  }, [queueLabels])

  const fitRef = useRef(() => {})
  fitRef.current = () => fit()

  // Reveal the canvas only once it holds the whole fitted view. The frames
  // between a data swap and its fit would otherwise show whatever node happens
  // to sit at the origin, then the real graph.
  const settleFit = useCallback(() => {
    const stage = stageRef.current
    const graph = graphRef.current
    if (!stage?.clientWidth || !stage.clientHeight || !graph ||
      graph.width() !== stage.clientWidth || graph.height() !== stage.clientHeight ||
      !nodesRef.current.length || !Number.isFinite(nodesRef.current[0].x)) return
    pendingFit.current = false
    fitRef.current()
    stage.style.opacity = "1"
    stage.dataset.ready = "true"
  }, [])

  const select = useCallback((id: string, open: boolean, locate: boolean, mode: "path" | "node" = "path") => {
    if (!index.byId.has(id)) return
    if (open) setCardId(id)
    if (!visible.has(id)) return
    if (!open) setCardId(null)
    setSelected(id)
    setFocus({ id, mode })
    const graph = graphRef.current
    const node = nodesRef.current.find((n) => n.id === id)
    if (locate && graph && node) {
      retainedViewport.current = null
      userMoved.current = true
      graph.centerAt(node.x, node.y, 1000).zoom(MAX_ZOOM, 1000)
    }
  }, [index.byId, visible])
  const selectRef = useRef(select)
  selectRef.current = select

  // One graph instance per mount; view changes swap its data.
  useEffect(() => {
    const stage = stageRef.current
    const canvasMount = canvasMountRef.current
    if (!stage || !canvasMount) return
    paletteRef.current = readPalette(stage)
    for (const [kind, src] of Object.entries(KIND_ICONS)) {
      const image = new Image()
      image.onload = () => graphRef.current?.nodeCanvasObject(graphRef.current.nodeCanvasObject())
      image.src = src
      drawState.current.icons.set(kind as DisplayKind, image)
    }
    const palette = () => paletteRef.current ?? readPalette(stage)
    const moved = () => { retainedViewport.current = null; userMoved.current = true }
    stage.addEventListener("wheel", moved, { passive: true })
    stage.addEventListener("pointerdown", moved)
    const graph = new ForceGraph<GraphNode, GraphLink>(canvasMount)
      .width(stage.clientWidth).height(stage.clientHeight)
      .minZoom(MIN_ZOOM).maxZoom(MAX_ZOOM).cooldownTime(15000)
      .nodeId("id").nodeVal((n) => n.radius * 2.5)
      .nodeLabel(() => "")
      .nodeVisibility((n) => visibleRef.current.has(n.id))
      .linkVisibility((l) => visibleRef.current.has(l.source.id) && visibleRef.current.has(l.target.id))
      .nodeCanvasObject((n, ctx, zoom) => drawNode(n, ctx, zoom, drawState.current, palette()))
      .linkCanvasObject((l, ctx, zoom) => drawLink(l, ctx, zoom, drawState.current, palette()))
      .onRenderFramePost((ctx, zoom) => drawLabels(ctx, zoom, drawState.current, palette()))
      .linkLabel(() => "")
      .onNodeClick((n) => selectRef.current(n.id, true, false))
      .onBackgroundClick(() => { setCardId(null); setFocus(null) })
      .onNodeHover((n) => { hoveredRef.current = n?.id ?? null; stage.style.cursor = n ? "pointer" : "grab"; queueLabels() })
      .onZoom(({ k }) => {
        // ForceGraph auto-scales some graphData updates before painting.
        const retained = retainedViewport.current
        if (retained && graphRef.current) {
          retainedViewport.current = null
          graphRef.current.zoom(retained.zoom).centerAt(retained.x, retained.y)
          return
        }
        stage.dataset.zoom = String(k)
        stage.dataset.viewportMoving = "true"
        queueLabels(160)
        repositionCard()
      })
      .onZoomEnd(() => { delete stage.dataset.viewportMoving; queueLabels(160) })
      .onNodeDrag(() => repositionCard())
      .onEngineTick(() => {
        stage.dataset.layoutMoving = "true"
        if (pendingFit.current) settleFit()
        if (fitOnStop.current && !userMoved.current && performance.now() - lastAutoFit.current >= 200) {
          lastAutoFit.current = performance.now()
          fitRef.current()
        }
        if (!stage.dataset.viewportMoving && performance.now() - lastLabels.current >= 100) updateLabels()
      })
      .onEngineStop(() => {
        stage.dataset.layoutMoving = "false"
        if (pendingFit.current || (fitOnStop.current && !userMoved.current)) settleFit()
        fitOnStop.current = false
        updateLabels()
      })
    graph.d3Force("charge")?.strength(-300)
    graph.d3Force("link")?.distance(150)
    graph.d3Force("center")?.strength(0.1)
    graphRef.current = graph
    stage.dataset.zoom = String(graph.zoom())
    stage.__easmNodePoint = (id) => { const n = nodesRef.current.find((node) => node.id === id); return n && graph.graph2ScreenCoords(n.x ?? 0, n.y ?? 0) }
    // The tab or dialog can lay out after mount; refit whenever the canvas size changes.
    const resize = new ResizeObserver(() => {
      if (!stage.clientWidth || !stage.clientHeight || (graph.width() === stage.clientWidth && graph.height() === stage.clientHeight)) return
      graph.width(stage.clientWidth).height(stage.clientHeight)
      if (pendingFit.current || (fitOnStop.current && !userMoved.current)) settleFit()
    })
    resize.observe(stage)
    // The transparent canvas uses the card background. Repaint the drawn colors on theme changes.
    const theme = new MutationObserver(() => {
      paletteRef.current = readPalette(stage)
      graph.nodeCanvasObject(graph.nodeCanvasObject())
    })
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ["class", "data-palette"] })
    const pause = () => (document.hidden || !activeRef.current ? graph.pauseAnimation() : graph.resumeAnimation())
    document.addEventListener("visibilitychange", pause)
    pause()
    return () => {
      document.removeEventListener("visibilitychange", pause)
      stage.removeEventListener("wheel", moved)
      stage.removeEventListener("pointerdown", moved)
      resize.disconnect()
      theme.disconnect()
      window.clearTimeout(labelTimer.current)
      graph._destructor()
      graphRef.current = null
      delete stage.__easmNodePoint
    }
  }, [queueLabels, repositionCard, settleFit, updateLabels])

  useEffect(() => {
    const graph = graphRef.current
    if (graph) document.hidden || !active ? graph.pauseAnimation() : graph.resumeAnimation()
  }, [active])

  useEffect(() => {
    const graph = graphRef.current
    const stage = stageRef.current
    if (!graph || !stage) return
    const previousNodes = nodesRef.current
    const previousById = new Map(previousNodes.map((node) => [node.id, node] as const))
    const nodes: GraphNode[] = view.nodes.map((node) => {
      const lines = wrapLabel(graphLabel(node, textRef.current), measure, MAX_LABEL_WIDTH)
      return Object.assign(previousById.get(node.id) ?? {}, {
        id: node.id, node, kind: displayKind(node), radius: nodeRadius(node.ip_count), lines,
        labelWidth: Math.max(...lines.map(measure)), degree: index.degree.get(node.id) ?? 0,
      })
    })
    const byId = new Map(nodes.map((n) => [n.id, n]))
    if (previousNodes.length) {
      const neighbors = new Map<string, string[]>()
      for (const { source, target } of view.edges) {
        const sources = neighbors.get(source) ?? []
        const targets = neighbors.get(target) ?? []
        sources.push(target)
        targets.push(source)
        neighbors.set(source, sources)
        neighbors.set(target, targets)
      }
      const siblings = new Map([...neighbors].map(([id, ids]) => [id, ids.filter((id) => previousById.has(id)).length]))
      const root = byId.get(index.rootId ?? "") ?? previousNodes[0]
      // Grow each new branch from its connected node instead of the layout origin.
      for (const node of nodes) {
        if (Number.isFinite(node.x) && Number.isFinite(node.y)) continue
        const anchor = (neighbors.get(node.id) ?? []).map((id) => byId.get(id))
          .find((neighbor) => neighbor && Number.isFinite(neighbor.x) && Number.isFinite(neighbor.y)) ?? root
        const sibling = siblings.get(anchor.id) ?? 0
        siblings.set(anchor.id, sibling + 1)
        const outward = Math.atan2((anchor.y ?? 0) - (root.y ?? 0), (anchor.x ?? 0) - (root.x ?? 0))
        const angle = outward + sibling * Math.PI * (3 - Math.sqrt(5))
        const distance = 40 * Math.sqrt(sibling + 1)
        const branchOffset = anchor === root ? 0 : 90
        node.x = (anchor.x ?? 0) + Math.cos(outward) * branchOffset + Math.cos(angle) * distance
        node.y = (anchor.y ?? 0) + Math.sin(outward) * branchOffset + Math.sin(angle) * distance
      }
    }
    const previousLinks = graph.graphData().links
    const changedTopology =
      nodes.length !== previousNodes.length || nodes.some((node, i) => node !== previousNodes[i]) ||
      view.edges.length !== previousLinks.length ||
      view.edges.some((edge, i) => previousLinks[i].source.id !== edge.source || previousLinks[i].target.id !== edge.target)
    nodesRef.current = nodes
    drawState.current.labels = new Map()
    if (changedTopology) fitOnStop.current = !previousNodes.length || !userMoved.current
    // A hidden tab can receive more snapshots before its first fit runs.
    if (!previousNodes.length && nodes.length) {
      pendingFit.current = true
      delete stage.dataset.ready
      stage.style.opacity = "0"
      userMoved.current = false
    }
    if (changedTopology) {
      retainedViewport.current = pendingFit.current ? null : { zoom: graph.zoom(), ...graph.centerAt() }
      const links = view.edges.map((edge) => ({
        source: byId.get(edge.source)!, target: byId.get(edge.target)!, confirmed: edge.confirmed,
      }))
      stage.dataset.layouts = String(Number(stage.dataset.layouts ?? 0) + 1)
      // An empty panel can finish its simulation before the first results arrive.
      graph.warmupTicks(pendingFit.current ? 300 : 0).graphData({ nodes, links })
    } else {
      previousLinks.forEach((link, index) => { link.confirmed = view.edges[index].confirmed })
      graph.nodeCanvasObject(graph.nodeCanvasObject()).linkCanvasObject(graph.linkCanvasObject())
    }
    if (!nodes.length) {
      pendingFit.current = false
      stage.style.opacity = "1"
      stage.dataset.ready = "true"
      stage.dataset.visibleLabels = "0"
    } else if (pendingFit.current) queueMicrotask(settleFit)
    queueLabels()
    if (document.hidden || !activeRef.current) graph.pauseAnimation()
  }, [view, index.degree, index.rootId, settleFit, queueLabels])

  useEffect(() => {
    visibleRef.current = visible
    const graph = graphRef.current
    if (!graph) return
    graph.nodeVisibility(graph.nodeVisibility()).linkVisibility(graph.linkVisibility())
    queueLabels()
  }, [visible, queueLabels])

  useEffect(() => {
    drawState.current.highlighted = focus?.mode === "node" ? new Set([focus.id]) : highlightIds(result.edges, focus?.id ?? null)
    if (stageRef.current) stageRef.current.dataset.highlightedNodes = String(drawState.current.highlighted.size)
    queueLabels()
  }, [focus, result.edges, queueLabels])

  useEffect(() => { if (cardId) requestAnimationFrame(repositionCard) }, [cardId, repositionCard])

  const toggleKind = (kind: DisplayKind) => {
    const next = new Set(hidden)
    if (next.has(kind)) next.delete(kind)
    else next.add(kind)
    setHidden(next)
    const stillVisible = visibleIds(view.nodes, view.edges, next)
    if (cardId && visible.has(cardId) && !stillVisible.has(cardId)) setCardId(null)
  }

  const search = () => {
    const first = searchNodes(result.nodes, query, visible)[0]
    if (first) select(first.id, false, true, "node")
  }

  const zoomBy = (step: number) => {
    const graph = graphRef.current
    if (!graph) return
    retainedViewport.current = null
    userMoved.current = true
    graph.zoom(Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, graph.zoom() + step)), 500)
  }

  const cardNode = cardId ? index.byId.get(cardId) : undefined
  return (
    <div className={cn("flex min-h-0 flex-col gap-2", expanded && "h-full max-md:min-h-[460px]")}>
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
        <Input type="search" aria-label={text.search} placeholder={text.searchPlaceholder} value={query} className="h-8 pl-8 text-xs"
          onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") search() }} />
      </div>
      <section ref={sectionRef} aria-label={text.graph} className={cn("relative flex min-h-0 flex-col overflow-hidden rounded-xl border bg-card", expanded ? "flex-1" : "")}>
        {stats.length ? (
          <p className="m-0 flex flex-wrap items-center gap-x-1 border-b px-3 py-2.5 text-[11px] text-muted-foreground">
            {stats.map(([count, label], i) => (
              <span key={label} className="whitespace-nowrap">
                {i ? <span aria-hidden className="mx-1.5">·</span> : null}
                <b className="text-xs font-semibold text-foreground tabular-nums">{count}</b> {label}
              </span>
            ))}
          </p>
        ) : null}
        <div className={cn("relative", expanded ? "min-h-0 flex-1" : "h-[460px]")}>
          {/* Hidden until the first fit (settleFit reveals it): the frames
              between mount and fit would otherwise paint whatever node sits at
              the origin. */}
          <div ref={stageRef} data-testid="easm-graph" role="group" aria-label={text.graphNetwork} className="absolute inset-0 overflow-hidden" style={{ opacity: 0 }}>
            <div ref={canvasMountRef} className="absolute inset-0" />
          </div>
          {!view.nodes.length ? loading ? (
            <div role="progressbar" aria-label={text.graphLoading} className="pointer-events-none absolute inset-0 flex items-center justify-center text-muted-foreground">
              <Loader2 className="size-5 motion-safe:animate-spin" aria-hidden />
            </div>
          ) : <p className="pointer-events-none absolute inset-0 m-0 grid place-items-center text-xs text-muted-foreground">{text.graphEmpty}</p> : null}
          <div className="pointer-events-none absolute inset-x-3 top-3 z-10 flex justify-between gap-2">
            <div role="group" aria-label={text.graphActions} className="flex shrink-0 items-start gap-1.5 [&>button]:pointer-events-auto">
              {!mobile ? <>
                <button type="button" className={toolButton} aria-label={text.zoomInGraph} title={text.zoomIn} onClick={() => zoomBy(0.3)}><MaskIcon src={enlargeIcon} /></button>
                <button type="button" className={toolButton} aria-label={text.zoomOutGraph} title={text.zoomOut} onClick={() => zoomBy(-0.3)}><MaskIcon src={shrinkIcon} /></button>
              </> : null}
              <button type="button" className={toolButton} aria-label={text.fit} title={text.fit} onClick={fit}><MaskIcon src={fitIcon} /></button>
            </div>
            {legend.length ? (
              <div role="group" aria-label={text.kindFilter} className="flex min-w-0 flex-wrap justify-end gap-1.5">
                <TooltipProvider delayDuration={300}>
                  {legend.map((kind) => {
                    const label = kindLabel(kind, text)
                    return (
                      <Tooltip key={kind}>
                        <TooltipTrigger asChild aria-describedby={undefined}>
                          <button type="button" aria-label={label} aria-pressed={!hidden.has(kind)} onClick={() => toggleKind(kind)}
                            className={cn(toolButton, "pointer-events-auto shrink-0 rounded-full aria-[pressed=false]:[&>span]:opacity-35")}>
                            <KindIcon kind={kind} />
                          </button>
                        </TooltipTrigger>
                        <TooltipContent side="bottom">{label}</TooltipContent>
                      </Tooltip>
                    )
                  })}
                </TooltipProvider>
              </div>
            ) : null}
          </div>
        </div>
        {cardNode ? (
          <NodeCard key={cardNode.id} ref={mobile ? undefined : cardRef}
            className={mobile ? "bottom-3 left-3 max-h-[400px]" : undefined}
            node={cardNode} index={index}
            onSelect={(id) => select(id, true, true)} onClose={() => { setCardId(null); setFocus(null) }} />
        ) : null}
      </section>
    </div>
  )
}
