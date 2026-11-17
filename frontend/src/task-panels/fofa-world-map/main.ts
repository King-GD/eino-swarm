import { geoEqualEarth, geoPath, interpolateBlues, scaleSequentialSymlog, select, zoom, type ZoomBehavior } from "d3"
import type { FeatureCollection, Geometry } from "geojson"
import { createTaskPanel } from "../sdk"
import type { HostContext } from "../types"
import worldRaw from "./world.json"
import "./style.css"

interface Asset {
  id: string; ip: string; port: number; protocol: string; host: string; country: string
  region: string; city: string; longitude?: number; latitude?: number; url?: string; status: string
}
interface Result { status: string; status_code?: number; elapsed_ms?: number; error?: string; finished_at: string }
interface State {
  collection: { phase: string; received: number; discovered: number; reported_total?: number; completeness: string; reason?: string }
  assets_by_id: Record<string, Asset>
  probe: { pending: number; running: number; responded: number; no_response: number; skipped: number; interrupted: number }
  results_by_id: Record<string, Result>
}
interface Country { geometry_id: string; code: string; name: string }
interface Point { key: string; coordinate: [number, number]; assets: Asset[] }

const world = worldRaw as unknown as FeatureCollection<Geometry, Country>
const countryCodes = new Set(world.features.map((feature) => feature.properties.code).filter(Boolean))
const svg = select<SVGSVGElement, unknown>("#map")
const scene = svg.append("g").attr("data-testid", "map-scene")
const countries = scene.append("g").attr("class", "countries")
const points = scene.append("g").attr("class", "points")
const projection = geoEqualEarth()
const path = geoPath(projection)
let state: State | undefined
let context: HostContext | undefined
let selectedCountry: string | undefined
let width = 0
const pan: ZoomBehavior<SVGSVGElement, unknown> = zoom<SVGSVGElement, unknown>().scaleExtent([1, 12]).on("zoom", (event) => {
  scene.attr("transform", event.transform.toString())
  svg.attr("data-zoom", event.transform.toString())
})
svg.call(pan)

const words = {
  en: { title: "Asset collection", collection: "Collection", checking: "HTTP checks", discovered: "collected", responded: "responded", noResponse: "no response", running: "checking", skipped: "unsupported", interrupted: "interrupted", unmapped: "without coordinates", all: "All regions", approximate: "Locations are provided by FOFA and are approximate.", unknown: "Collection completeness unverified", partial: "Partial collection", complete: "Collection complete", collecting: "Collecting", finished: "Collection ended", failed: "Collection failed", more: "Showing first 100 assets", empty: "Waiting for collected assets", legend: "Collected assets per country", noData: "No collected assets", pending: "pending" },
  zh: { title: "资产采集", collection: "采集", checking: "HTTP 验活", discovered: "已采集", responded: "已响应", noResponse: "未取得响应", running: "验活中", skipped: "未支持", interrupted: "已中断", unmapped: "缺少坐标", all: "全部区域", approximate: "位置由 FOFA 提供，为近似地理位置。", unknown: "采集完整性未确认", partial: "部分采集结果", complete: "采集完整", collecting: "正在采集", finished: "采集已结束", failed: "采集失败", more: "显示前 100 个资产", empty: "等待采集数据", legend: "各区域已采集资产数", noData: "暂无已采集资产", pending: "待验活" },
}
function text() { return context?.locale.startsWith("zh") ? words.zh : words.en }
function element(id: string): HTMLElement { return document.getElementById(id)! }
function statusName(status: string): string {
  const t = text()
  return ({ responded: t.responded, no_response: t.noResponse, running: t.running, pending: t.pending, skipped: t.skipped, interrupted: t.interrupted } as Record<string, string>)[status] ?? status
}
function located(asset: Asset): asset is Asset & { longitude: number; latitude: number } {
  return Number.isFinite(asset.longitude) && Number.isFinite(asset.latitude) && Math.abs(asset.longitude!) <= 180 && Math.abs(asset.latitude!) <= 90
}
function markerStatus(assets: Asset[]): string {
  if (assets.some((asset) => asset.status === "running")) return "running"
  if (assets.some((asset) => asset.status === "responded")) return "responded"
  if (assets.some((asset) => asset.status === "no_response")) return "no_response"
  return "interrupted"
}
function detail(assets: Asset[]) {
  const t = text(), list = element("details")
  list.replaceChildren()
  const visible = assets.filter((asset) => !selectedCountry || asset.country === selectedCountry)
  for (const asset of visible.slice(0, 100)) {
    const result = state?.results_by_id[asset.id], item = document.createElement("li")
    const label = document.createElement("span"), status = document.createElement("span")
    label.textContent = asset.url ?? asset.host ?? `${asset.ip}:${asset.port}`
    label.className = "endpoint"
    status.textContent = `${statusName(asset.status)}${result?.status_code ? ` · HTTP ${result.status_code}` : ""}${result?.elapsed_ms !== undefined ? ` · ${Math.round(result.elapsed_ms)} ms` : ""}${result?.error ? ` · ${result.error}` : ""}`
    status.className = `status ${asset.status}`
    item.append(label, status); list.append(item)
  }
  element("detail-label").textContent = `${selectedCountry ?? t.all} · ${visible.length}${visible.length > 100 ? ` · ${t.more}` : ""}`
}
function render() {
  if (!context || !state || !context.visible) return
  const t = text(), stopped = context.source_status === "interrupted"
  const assets = Object.values(state.assets_by_id).map((asset) => stopped && (asset.status === "pending" || asset.status === "running") ? { ...asset, status: "interrupted" } : asset)
  const count = new Map<string, number>()
  for (const asset of assets) if (countryCodes.has(asset.country)) count.set(asset.country, (count.get(asset.country) ?? 0) + 1)
  const max = Math.max(1, ...count.values()), color = scaleSequentialSymlog(interpolateBlues).domain([0, max])
  document.documentElement.dataset.theme = context.theme
  document.documentElement.lang = context.locale
  element("title").textContent = t.title
  const phase = stopped && ["pending", "collecting"].includes(state.collection.phase) ? "interrupted" : state.collection.phase
  element("phase").textContent = `${phase === "collecting" ? t.collecting : phase === "failed" ? t.failed : phase === "interrupted" ? t.interrupted : t.finished} · ${assets.length} ${t.discovered}`
  const complete = state.collection.completeness
  element("completeness").textContent = `${complete === "complete" ? t.complete : complete === "partial" ? t.partial : t.unknown}${state.collection.reason ? ` · ${state.collection.reason}` : ""}`
  const p = stopped ? { ...state.probe, pending: 0, running: 0, interrupted: state.probe.interrupted + state.probe.pending + state.probe.running } : state.probe
  const processed = p.responded + p.no_response
  const total = p.pending + p.running + processed + p.interrupted
  const fixed = phase !== "collecting"
  element("progress-label").textContent = `${t.checking}: ${processed} / ${total}${fixed && total ? ` · ${Math.round(100 * processed / total)}%` : ""}`
  const progress = element("progress") as HTMLProgressElement
  progress.max = Math.max(1, total); progress.value = processed
  if (!fixed) progress.removeAttribute("value")
  element("counters").textContent = `${p.responded} ${t.responded} · ${p.no_response} ${t.noResponse} · ${p.running} ${t.running} · ${p.skipped} ${t.skipped} · ${p.interrupted} ${t.interrupted}`
  const nextWidth = Math.max(240, Math.round(context.width || window.innerWidth)), height = Math.max(200, nextWidth * .6)
  if (nextWidth !== width) {
    width = nextWidth
    svg.attr("viewBox", `0 0 ${width} ${height}`)
    projection.fitExtent([[8, 12], [width - 8, height - 12]], world)
  }
  countries.selectAll<SVGPathElement, typeof world.features[number]>("path").data(world.features, (feature) => feature.properties.geometry_id).join("path")
    .attr("d", path).attr("data-geometry", (feature) => feature.properties.geometry_id).attr("data-country", (feature) => feature.properties.code || null)
    .attr("fill", (feature) => count.has(feature.properties.code) ? color(count.get(feature.properties.code)!) : "var(--empty)")
    .attr("class", (feature) => selectedCountry === feature.properties.code ? "selected" : "")
    .on("click", (_, feature) => { if (!feature.properties.code) return; selectedCountry = selectedCountry === feature.properties.code ? undefined : feature.properties.code; render() })
    .each(function (feature) { select(this).selectAll("title").data([feature]).join("title").text(`${feature.properties.name}: ${count.get(feature.properties.code) ?? 0}`) })
  const grouped = new Map<string, Point>()
  for (const asset of assets) {
    if (!located(asset) || asset.status === "pending" || asset.status === "skipped") continue
    const key = `${asset.longitude},${asset.latitude}`
    const group = grouped.get(key) ?? { key, coordinate: [asset.longitude, asset.latitude], assets: [] }
    group.assets.push(asset); grouped.set(key, group)
  }
  points.selectAll<SVGCircleElement, Point>("circle").data([...grouped.values()], (point) => point.key).join("circle")
    .attr("data-point", (point) => point.key).attr("data-count", (point) => point.assets.length)
    .attr("cx", (point) => projection(point.coordinate)?.[0] ?? 0).attr("cy", (point) => projection(point.coordinate)?.[1] ?? 0)
    .attr("r", (point) => Math.min(7, 2.5 + Math.log2(point.assets.length + 1)))
    .attr("class", (point) => markerStatus(point.assets))
    .each(function (point) { select(this).selectAll("title").data([point]).join("title").text(point.assets.map((asset) => `${asset.host} · ${statusName(asset.status)}`).join("\n")) })
  element("legend-label").textContent = `${t.legend}: 0 — ${max}`
  element("unmapped").textContent = `${assets.filter((asset) => !located(asset)).length} ${t.unmapped} · ${t.approximate}`
  element("empty").textContent = assets.length ? "" : t.empty
  detail(assets)
  document.body.dataset.revision = String(context.revision)
}
element("clear-filter").addEventListener("click", () => { selectedCountry = undefined; render() })
createTaskPanel<State>({
  onSnapshot(next, nextContext) { state = next; context = nextContext; render() },
  onPatch(_, next, nextContext) { state = next; context = nextContext; render() },
  onHostContextChanged(nextContext) { context = nextContext; render() },
  onDispose() { svg.on(".zoom", null) },
})
