import type { DisplayKind, EasmComponent, EasmEdge, EasmNode, EasmResult, FeatureKind, TagType } from "./types"

export const KIND_LABELS: Record<"root" | "company" | "org" | "domain", string> = {
  root: "目标企业", company: "企业组织", org: "证书组织", domain: "主域名",
}
export const FEATURE_LABELS: Record<FeatureKind, string> = {
  org: "证书组织", cert: "证书", asn: "ASN", icon_hash: "网站图标", ipc: "IP 段", fid: "FID", icp: "ICP 备案", mx: "MX 记录",
}
/** Card feature groups, in the reference order; asset cards also list domains. */
export const FEATURE_GROUPS: [FeatureKind | "domain", string][] = [
  ["cert", "证书"], ["org", "证书组织"], ["asn", "ASN"], ["icon_hash", "图标指纹"], ["domain", "域名"],
  ["ipc", "IP 段"], ["fid", "FID"], ["icp", "ICP 备案"], ["mx", "MX 记录"],
]
export const TAG_LABELS: Record<TagType, string> = { hot: "热搜", kev: "KEV", cve: "CVE" }
const TAG_ORDER: TagType[] = ["hot", "kev", "cve"]

export const displayKind = (node: EasmNode): DisplayKind =>
  node.kind === "feature" ? node.featureKind ?? "org" : node.kind

// An org node and an org feature share both the key and the label, as in the reference.
export const kindLabel = (kind: DisplayKind): string =>
  (KIND_LABELS as Record<string, string>)[kind] ?? FEATURE_LABELS[kind as FeatureKind]

/** Precomputed lookups the graph, card and search share. */
export interface EasmIndex {
  byId: Map<string, EasmNode>
  rootId: string | null
  degree: Map<string, number>
  /** Feature id → the non-feature nodes it connects. */
  members: Map<string, string[]>
  /** Root first, then each kind in first-appearance order. */
  legend: DisplayKind[]
}

export function indexResult(result: EasmResult): EasmIndex {
  const byId = new Map(result.nodes.map((node) => [node.id, node]))
  const degree = new Map(result.nodes.map((node) => [node.id, 0]))
  const members = new Map<string, string[]>()
  for (const edge of result.edges) {
    degree.set(edge.source, (degree.get(edge.source) ?? 0) + 1)
    degree.set(edge.target, (degree.get(edge.target) ?? 0) + 1)
    for (const [a, b] of [[edge.source, edge.target], [edge.target, edge.source]]) {
      if (byId.get(a)?.kind !== "feature" || byId.get(b)?.kind === "feature" || !byId.has(b)) continue
      const list = members.get(a) ?? []
      if (!list.includes(b)) list.push(b)
      members.set(a, list)
    }
  }
  const kinds = [...new Set(result.nodes.map(displayKind))]
  const legend = kinds.includes("root") ? ["root" as DisplayKind, ...kinds.filter((k) => k !== "root")] : kinds
  return { byId, rootId: result.nodes.find((node) => node.kind === "root")?.id ?? null, degree, members, legend }
}

/** Nodes left after hiding kinds. With anything hidden, a node also needs a
 *  visible neighbour, so filtering never leaves orphans floating in space. */
export function visibleIds(nodes: EasmNode[], edges: EasmEdge[], hidden: ReadonlySet<DisplayKind>): Set<string> {
  const candidates = new Set(nodes.filter((node) => !hidden.has(displayKind(node))).map((node) => node.id))
  if (!hidden.size) return candidates
  const connected = new Set<string>()
  for (const edge of edges) {
    if (edge.source !== edge.target && candidates.has(edge.source) && candidates.has(edge.target)) {
      connected.add(edge.source)
      connected.add(edge.target)
    }
  }
  return connected
}

/** The neighbourhood within `hops` edges of one node ("只看当前关联"). */
export function localView(nodes: EasmNode[], edges: EasmEdge[], id: string, hops: number) {
  const included = new Set([id])
  for (let hop = 0; hop < hops; hop++) {
    const current = new Set(included)
    for (const edge of edges) {
      if (current.has(edge.source) || current.has(edge.target)) {
        included.add(edge.source)
        included.add(edge.target)
      }
    }
  }
  return { nodes: nodes.filter((node) => included.has(node.id)), edges: edges.filter((edge) => included.has(edge.source) && included.has(edge.target)) }
}

/** The path from the selected node back to the root plus its direct children. */
export function highlightIds(edges: EasmEdge[], id: string | null): Set<string> {
  if (!id) return new Set()
  const parents = new Map<string, string[]>()
  for (const edge of edges) parents.set(edge.target, [...(parents.get(edge.target) ?? []), edge.source])
  const highlighted = new Set<string>()
  const pending = [id]
  while (pending.length) {
    const current = pending.pop()!
    if (highlighted.has(current)) continue
    highlighted.add(current)
    pending.push(...(parents.get(current) ?? []))
  }
  for (const edge of edges) if (edge.source === id) highlighted.add(edge.target)
  return highlighted
}

/** Depth-first order from the root. The force layout seeds positions in node
 *  order, so this keeps branches together the way the reference graph does. */
export function layoutOrder(nodes: EasmNode[], edges: EasmEdge[], rootId: string | null) {
  const byId = new Map(nodes.map((node) => [node.id, node]))
  const children = new Map<string, EasmEdge[]>()
  for (const edge of edges) children.set(edge.source, [...(children.get(edge.source) ?? []), edge])
  const visited = new Set<string>()
  const orderedNodes: EasmNode[] = []
  const orderedEdges: EasmEdge[] = []
  const visit = (start: string) => {
    const stack = [start]
    while (stack.length) {
      const id = stack.pop()!
      if (visited.has(id) || !byId.has(id)) continue
      visited.add(id)
      orderedNodes.push(byId.get(id)!)
      const next = children.get(id) ?? []
      orderedEdges.push(...next)
      for (let i = next.length - 1; i >= 0; i--) stack.push(next[i].target)
    }
  }
  if (rootId) visit(rootId)
  for (const node of nodes) visit(node.id)
  const seen = new Set(orderedEdges)
  return { nodes: orderedNodes, edges: [...orderedEdges, ...edges.filter((edge) => !seen.has(edge))] }
}

/** Disc radius in graph units; IP count grows a node, capped like the reference. */
export function nodeRadius(ipCount?: number) {
  const ips = Number.isFinite(ipCount) && (ipCount as number) >= 0 ? (ipCount as number) : 0
  return Math.min(20 + Math.sqrt(ips / 100), 50) / 2.5
}

/** Short canvas label. Certificates and ICP numbers are long identifiers, so
 *  the graph shows their tail and the card shows them in full. */
export function graphLabel(node: EasmNode) {
  if (node.featureKind === "cert") return `证书 · ${(node.detail?.serial || node.label).slice(-6)}`
  if (node.featureKind === "icp") return `ICP · …${node.label.slice(-7)}`
  return node.label
}

/** Wrap at a fixed pixel width, character by character (CJK has no spaces). */
export function wrapLabel(text: string, width: (s: string) => number, max: number): string[] {
  const lines = [""]
  for (const char of text) {
    const last = lines[lines.length - 1]
    if (last && width(last + char) > max) lines.push(char)
    else lines[lines.length - 1] = last + char
  }
  return lines
}

export function searchNodes(nodes: EasmNode[], query: string, visible: ReadonlySet<string>) {
  const q = query.trim().toLowerCase()
  if (!q) return []
  return nodes.filter((node) => visible.has(node.id) && `${node.label} ${node.detail?.serial ?? ""}`.toLowerCase().includes(q))
}

export function summaryStats(summary: EasmResult["summary"]): [number, string][] {
  const rows: [number | undefined, string][] = [[summary?.clues, "个线索"], [summary?.fofa_assets, "个 FOFA 资产"], [summary?.fofa_ips, "个资产 IP"]]
  return rows.filter((row): row is [number, string] => Number.isFinite(row[0]))
}

export interface ComponentRow extends EasmComponent {
  tags: NonNullable<EasmComponent["tags"]>
}

/** Tagged components first (热搜 > KEV > CVE), then by count, then name. */
export function sortComponents(components: EasmComponent[]): ComponentRow[] {
  const rank = (row: ComponentRow) => Math.min(3, ...row.tags.map((tag) => TAG_ORDER.indexOf(tag.type)))
  return components
    .map((component) => ({ ...component, tags: [...(component.tags ?? [])].sort((a, b) => TAG_ORDER.indexOf(a.type) - TAG_ORDER.indexOf(b.type)) }))
    .sort((a, b) => rank(a) - rank(b) || b.count - a.count || a.name.localeCompare(b.name, "zh-CN"))
}

/** 热搜 days: the stated count, else the distinct evidence dates. */
export function hotDays(row: ComponentRow) {
  const hot = row.tags.find((tag) => tag.type === "hot")
  if (!hot) return null
  return Number.isInteger(row.hot_days) ? (row.hot_days as number) : new Set((hot.evidence ?? []).map((e) => e.date).filter(Boolean)).size
}

export function validity(notBefore?: string, notAfter?: string) {
  return notBefore && notAfter ? `${notBefore.slice(0, 10)} 至 ${notAfter.slice(0, 10)}` : ""
}

export const isSafeLink = (url?: string) => {
  try { return !!url && new URL(url).protocol === "https:" } catch { return false }
}

export const formatCount = (n: number) => n.toLocaleString("zh-CN")
