import ForceGraph from "force-graph"
import { Search } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import enlargeIcon from "../../../assets/easm/enlarge.svg"
import expandIcon from "../../../assets/easm/expand.svg"
import shrinkIcon from "../../../assets/easm/shrink.svg"
import fitIcon from "../../../assets/easm/zoomfit.svg"
import { Input } from "../../ui/input"
import {
  displayKind, graphLabel, highlightIds, kindLabel, layoutOrder, localView, nodeRadius, searchNodes, summaryStats, visibleIds, wrapLabel,
  type EasmIndex,
} from "../../../lib/easm/model"
import type { DisplayKind, EasmResult } from "../../../lib/easm/types"
import { cn } from "@/lib/cn"
import { chooseLabels, drawLabels, drawLink, drawNode, FONT_SIZE, MAX_LABEL_WIDTH, type DrawState, type GraphLink, type GraphNode, type Palette } from "./graph-canvas"
import { KIND_ICONS, KIND_TOKENS, tokenColor } from "./kind-style"
import { KindIcon, NodeCard } from "./node-card"

/** Tests and the dialog read a node's on-canvas point to click it. */
export interface GraphElement extends HTMLDivElement { __easmNodePoint?: (id: string) => { x: number; y: number } | undefined }

type Focus = { id: string; mode: "path" | "node" } | null
type Position = { x: number; y: number; vx?: number; vy?: number }

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

export function AssetGraph({ result, index, expanded = false, mobile = false, visible: active = true, onExpand }: {
  result: EasmResult
  index: EasmIndex
  expanded?: boolean
  mobile?: boolean
  visible?: boolean
  onExpand?: () => void
}) {
  const [hidden, setHidden] = useState<Set<DisplayKind>>(() => new Set())
  const [local, setLocal] = useState(false)
  const [selected, setSelected] = useState<string | null>(index.rootId ?? result.nodes[0]?.id ?? null)
  const [focus, setFocus] = useState<Focus>(null)
  const [cardId, setCardId] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const currentSelection = selected && index.byId.has(selected) ? selected : index.rootId ?? result.nodes[0]?.id ?? null

  const sectionRef = useRef<HTMLElement>(null)
  const stageRef = useRef<GraphElement>(null)
  const cardRef = useRef<HTMLElement>(null)
  const graphRef = useRef<InstanceType<typeof ForceGraph<GraphNode, GraphLink>> | null>(null)
  const drawState = useRef<DrawState>({ rootId: index.rootId, highlighted: new Set(), labels: new Map(), icons: new Map() })
  const paletteRef = useRef<Palette | null>(null)
  const nodesRef = useRef<GraphNode[]>([])
  const visibleRef = useRef<Set<string>>(new Set())
  const hoveredRef = useRef<string | null>(null)
  const focusRef = useRef({ selected, cardId })
  const positions = useRef(new Map<string, Map<string, Position>>())
  const viewKeyRef = useRef("")
  const labelTimer = useRef(0)
  const lastLabels = useRef(0)
  const retainedViewport = useRef<{ zoom: number; x: number; y: number } | null>(null)
  const activeRef = useRef(active)
  activeRef.current = active
  const fitOnStop = useRef(false)
  // First data and an explicitly changed scope are fitted before they appear.
  const pendingFit = useRef(false)
  // The settled layout is fitted again unless the user has panned or zoomed.
  const userMoved = useRef(false)
  focusRef.current = { selected: currentSelection, cardId }

  // Large graphs only show direct neighbours in the local view, as in the reference.
  const hops = result.nodes.length > 500 ? 1 : 2
  // A click in the full view must not rebuild the graph: that restarts the
  // layout and the canvas goes blank. Only the local view depends on selection.
  const scope = local ? currentSelection : null
  const view = useMemo(() => {
    const scoped = scope ? localView(result.nodes, result.edges, scope, hops) : result
    return layoutOrder(scoped.nodes, scoped.edges, index.rootId)
  }, [result, scope, hops, index.rootId])
  const visible = useMemo(() => visibleIds(view.nodes, view.edges, hidden), [view, hidden])
  const stats = summaryStats(result.summary)
  const legend = index.legend.filter((kind) => kind !== "root")

  useEffect(() => {
    setSelected(currentSelection)
    setFocus((current) => current && index.byId.has(current.id) ? current : null)
    setCardId((current) => current && index.byId.has(current) ? current : null)
    drawState.current.rootId = index.rootId
    if (stageRef.current) stageRef.current.dataset.selectedId = currentSelection ?? ""
  }, [currentSelection, index.byId, index.rootId])

  const repositionCard = useCallback(() => {
    const graph = graphRef.current
    const card = cardRef.current
    const section = sectionRef.current
    const stage = stageRef.current
    const id = focusRef.current.cardId
    if (!graph || !card || !section || !stage || !id) return
    const node = nodesRef.current.find((n) => n.id === id)
    const point = node && Number.isFinite(node.x) ? graph.graph2ScreenCoords(node.x ?? 0, node.y ?? 0) : { x: stage.clientWidth / 2, y: stage.clientHeight / 2 }
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
  const fit = useCallback((duration = 0) => {
    const graph = graphRef.current
    if (!graph || !nodesRef.current.length || !Number.isFinite(nodesRef.current[0].x)) return
    retainedViewport.current = null
    graph.zoomToFit(duration, 40, (n) => visibleRef.current.has(n.id))
    if (graph.zoom() > 1) graph.zoom(1, duration)
    queueLabels()
  }, [queueLabels])

  const fitRef = useRef(() => {})
  fitRef.current = () => fit()

  // Reveal the canvas only once it holds the whole fitted view. The frames
  // between a data swap and its fit would otherwise show whatever node happens
  // to sit at the origin, then the real graph.
  const settleFit = useCallback(() => {
    const stage = stageRef.current
    if (!stage || !nodesRef.current.length || !Number.isFinite(nodesRef.current[0].x)) return
    pendingFit.current = false
    fitRef.current()
    stage.style.opacity = "1"
    stage.dataset.ready = "true"
  }, [])

  const select = useCallback((id: string, open: boolean, locate: boolean, mode: "path" | "node" = "path") => {
    if (!index.byId.has(id)) return
    setSelected(id)
    setFocus({ id, mode })
    if (open) setCardId(id)
    const graph = graphRef.current
    const node = nodesRef.current.find((n) => n.id === id)
    if (locate && !local && graph && node && visibleRef.current.has(id)) {
      retainedViewport.current = null
      userMoved.current = true
      graph.centerAt(node.x, node.y, 1000).zoom(MAX_ZOOM, 1000)
    }
  }, [index.byId, local])
  const selectRef = useRef(select)
  selectRef.current = select

  // One graph instance per mount; view changes swap its data.
  useEffect(() => {
    const stage = stageRef.current
    if (!stage) return
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
    const graph = new ForceGraph<GraphNode, GraphLink>(stage)
      .width(stage.clientWidth).height(stage.clientHeight)
      .backgroundColor(palette().background).minZoom(MIN_ZOOM).maxZoom(MAX_ZOOM).cooldownTime(15000)
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
        if (!stage.dataset.viewportMoving && performance.now() - lastLabels.current >= 100) updateLabels()
      })
      .onEngineStop(() => {
        stage.dataset.layoutMoving = "false"
        positions.current.set(viewKeyRef.current, new Map(nodesRef.current.map(({ id, x, y, vx, vy }) => [id, { x: x ?? 0, y: y ?? 0, vx, vy }])))
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
    // Theme switches change the tokens the canvas resolved at mount.
    const theme = new MutationObserver(() => { paletteRef.current = readPalette(stage); graph.backgroundColor(paletteRef.current.background) })
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
    const key = scope ? `local:${scope}` : "global"
    const previousNodes = nodesRef.current
    const changedScope = viewKeyRef.current !== key
    if (changedScope && viewKeyRef.current) {
      positions.current.set(viewKeyRef.current, new Map(previousNodes.map(({ id, x, y, vx, vy }) => [id, { x: x ?? 0, y: y ?? 0, vx, vy }])))
    }
    const previousById = new Map(changedScope ? [] : previousNodes.map((node) => [node.id, node] as const))
    viewKeyRef.current = key
    const cached = positions.current.get(key)
    const nodes: GraphNode[] = view.nodes.map((node) => {
      const lines = wrapLabel(graphLabel(node), measure, MAX_LABEL_WIDTH)
      return Object.assign(previousById.get(node.id) ?? { ...cached?.get(node.id) }, {
        id: node.id, node, kind: displayKind(node), radius: nodeRadius(node.ip_count), lines,
        labelWidth: Math.max(...lines.map(measure)), degree: index.degree.get(node.id) ?? 0,
      })
    })
    const byId = new Map(nodes.map((n) => [n.id, n]))
    const previousLinks = graph.graphData().links
    const changedTopology = changedScope ||
      nodes.length !== previousNodes.length || nodes.some((node, i) => node !== previousNodes[i]) ||
      view.edges.length !== previousLinks.length ||
      view.edges.some((edge, i) => previousLinks[i].source.id !== edge.source || previousLinks[i].target.id !== edge.target)
    nodesRef.current = nodes
    drawState.current.labels = new Map()
    fitOnStop.current = changedScope || !previousNodes.length
    pendingFit.current = fitOnStop.current && nodes.length > 0
    if (pendingFit.current) {
      delete stage.dataset.ready
      stage.style.opacity = "0"
      userMoved.current = false
    }
    if (changedTopology) {
      retainedViewport.current = pendingFit.current ? null : { zoom: graph.zoom(), ...graph.centerAt() }
      const links = view.edges.map((edge) => ({ source: byId.get(edge.source)!, target: byId.get(edge.target)!, confirmed: !!edge.confirmed }))
      stage.dataset.layouts = String(Number(stage.dataset.layouts ?? 0) + 1)
      graph.warmupTicks(pendingFit.current && !cached ? 300 : 0).graphData({ nodes, links })
    } else {
      previousLinks.forEach((link, i) => { link.confirmed = !!view.edges[i].confirmed })
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
  }, [view, scope, index.degree, settleFit, queueLabels])

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
    if (currentSelection && !stillVisible.has(currentSelection)) { setFocus(null); setCardId(null) }
  }

  const search = () => {
    const first = searchNodes(result.nodes, query, local ? visibleIds(result.nodes, result.edges, hidden) : visible)[0]
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
        <Input type="search" aria-label="搜索资产或特征" placeholder="企业、域名、证书或备案号" value={query} className="h-8 pl-8 text-xs"
          onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") search() }} />
      </div>
      <section ref={sectionRef} aria-label="资产关联知识库" className={cn("relative flex min-h-0 flex-col overflow-hidden rounded-xl border bg-card", expanded ? "flex-1" : "")}>
        <div className="flex flex-col gap-1 border-b px-3 py-2.5">
          <div role="group" aria-label="节点类型筛选，点击隐藏或显示" className="flex flex-wrap gap-x-2 gap-y-0.5">
            {legend.map((kind) => (
              <button key={kind} type="button" aria-pressed={!hidden.has(kind)} title={hidden.has(kind) ? "点击显示" : "点击隐藏"} onClick={() => toggleKind(kind)}
                className="inline-flex min-h-7 items-center gap-1.5 rounded-md border border-transparent px-1.5 text-xs whitespace-nowrap hover:border-border hover:bg-accent aria-[pressed=false]:text-muted-foreground aria-[pressed=false]:line-through aria-[pressed=false]:[&>span]:opacity-40 aria-[pressed=false]:[&>span]:grayscale">
                <KindIcon kind={kind} className="size-[18px]" />{kindLabel(kind)}
              </button>
            ))}
          </div>
          {stats.length ? (
            <p className="m-0 flex flex-wrap items-center gap-x-1 text-[11px] text-muted-foreground">
              {stats.map(([count, label], i) => (
                <span key={label} className="whitespace-nowrap">
                  {i ? <span aria-hidden className="mx-1.5">·</span> : null}
                  <b className="text-xs font-semibold text-foreground tabular-nums">{count}</b> {label}
                </span>
              ))}
            </p>
          ) : null}
        </div>
        <div className={cn("relative", expanded ? "min-h-0 flex-1" : "h-[460px]")}>
          {/* Hidden until the first fit (settleFit reveals it): the frames
              between mount and fit would otherwise paint whatever node sits at
              the origin. */}
          <div ref={stageRef} data-testid="easm-graph" role="group" aria-label="资产与具体特征网络" className="absolute inset-0" style={{ opacity: 0 }} />
          {!result.nodes.length ? <p className="pointer-events-none absolute inset-0 m-0 grid place-items-center text-sm text-muted-foreground">等待资产数据</p> : null}
          <div role="toolbar" aria-label="关联网络操作" className="absolute top-3 left-3 z-10 flex gap-1.5">
            {!mobile ? <>
              <button type="button" className={toolButton} aria-label="放大关联网络" title="放大" onClick={() => zoomBy(0.3)}><MaskIcon src={enlargeIcon} /></button>
              <button type="button" className={toolButton} aria-label="缩小关联网络" title="缩小" onClick={() => zoomBy(-0.3)}><MaskIcon src={shrinkIcon} /></button>
            </> : null}
            {onExpand ? <button type="button" className={toolButton} aria-label="展开资产图" title="展开资产图" onClick={onExpand}><MaskIcon src={expandIcon} /></button> : null}
            <button type="button" className={toolButton} aria-label="适配视图" title="适配视图" onClick={() => fit(mobile ? 0 : 1000)}><MaskIcon src={fitIcon} /></button>
          </div>
          <button type="button" aria-pressed={local} onClick={() => setLocal(!local)}
            className="absolute top-3 right-3 z-10 h-7 rounded-full border bg-card/95 px-2.5 text-xs text-muted-foreground hover:border-primary hover:text-primary aria-pressed:border-primary aria-pressed:text-primary">
            {local ? "显示全局网络" : "只看当前关联"}
          </button>
        </div>
        {cardNode ? (
          <NodeCard key={cardNode.id} ref={mobile ? undefined : cardRef}
            className={mobile ? "bottom-3 left-3 max-h-[400px]" : undefined}
            node={cardNode} index={index} summary={result.summary}
            onSelect={(id) => select(id, true, true)} onClose={() => { setCardId(null); setFocus(null) }} />
        ) : null}
      </section>
    </div>
  )
}
