import type { EasmText } from "./messages"
import type { DisplayKind, EasmBasis, EasmComponent, EasmEdge, EasmNode, EasmResult, FeatureKind, TagType } from "./types"

/** Card feature groups, in the reference order; asset cards also list domains. */
export const FEATURE_GROUPS: (FeatureKind | "domain")[] = ["cert", "org", "asn", "icon_hash", "domain", "ipc", "fid", "icp", "mx", "vuln", "url", "person", "address", "other"]
const TAG_ORDER: TagType[] = ["hot", "kev", "cve"]

export const displayKind = (node: EasmNode): DisplayKind =>
  node.kind === "feature" ? node.featureKind ?? "org" : node.kind

export const kindLabel = (kind: DisplayKind, text: EasmText): string => text.kinds[kind]

/** Display the confirmed subject's source tree without rewriting saved clues. */
export function projectEasmResult(result: EasmResult): EasmResult {
  const root = result.nodes.find((node) => node.kind === "root")
  if (!root) return result

  const candidates = new Set<string>()
  const serials = new Set<string>()
  for (const node of result.nodes) {
    const savedSerial = node.detail?.serial
    const serial = displayKind(node) === "cert" && typeof savedSerial === "string" ? savedSerial.trim() : ""
    // Older portraits could save one node per issuer for the same serial.
    // Node order is discovery order, so only the first keeps its source link.
    if (serial && serials.has(serial)) continue
    if (serial) serials.add(serial)
    candidates.add(node.id)
  }
  const children = new Map<string, string[]>()
  for (const { source, target } of result.edges) {
    if (!candidates.has(source) || !candidates.has(target)) continue
    const targets = children.get(source) ?? []
    targets.push(target)
    children.set(source, targets)
  }

  const reachable = new Set<string>()
  const pending = [root.id]
  while (pending.length) {
    const id = pending.pop()!
    if (reachable.has(id)) continue
    reachable.add(id)
    for (const child of children.get(id) ?? []) pending.push(child)
  }

  if (reachable.size === result.nodes.length) return result
  return {
    ...result,
    nodes: result.nodes.filter((node) => reachable.has(node.id)),
    edges: result.edges.filter((edge) => reachable.has(edge.source) && reachable.has(edge.target)),
  }
}

/** Group node id for a folded kind. Real ids carry a kind prefix such as
 *  `domain:` or `feature:`, so `group:` never collides with one. */
export const groupId = (kind: DisplayKind) => `group:${kind}`

/** The "fold same-type nodes" view: every node of one kind hangs under a single
 *  group node tied to the root, wherever it sat before. Grouping is only a
 *  classification, so member edges are the group's and nothing else. */
export function foldByKind(result: EasmResult, label: (kind: DisplayKind) => string): EasmResult {
  const root = result.nodes.find((node) => node.kind === "root")
  if (!root) return result
  const members = new Map<DisplayKind, EasmNode[]>()
  for (const node of result.nodes) {
    if (node.id === root.id) continue
    const kind = displayKind(node)
    const list = members.get(kind) ?? []
    list.push(node)
    members.set(kind, list)
  }
  const nodes: EasmNode[] = [root]
  const edges: EasmEdge[] = []
  for (const [kind, list] of members) {
    const id = groupId(kind)
    nodes.push({ id, kind: list[0].kind, featureKind: list[0].featureKind, label: label(kind) })
    edges.push({ source: root.id, target: id, confirmed: true })
    for (const member of list) {
      nodes.push(member)
      edges.push({ source: id, target: member.id, confirmed: true })
    }
  }
  return { ...result, nodes, edges }
}

/** The tree the canvas shrinks with: every node hangs under the first clue that
 *  introduced it. Later observations stay as evidence, not as a second parent. */
export interface EasmTree {
  parent: Map<string, string>
  children: Map<string, string[]>
}

export function primaryTree(edges: EasmEdge[]): EasmTree {
  const parent = new Map<string, string>()
  const children = new Map<string, string[]>()
  for (const { source, target } of edges) {
    if (source === target || parent.has(target)) continue
    parent.set(target, source)
    const list = children.get(source) ?? []
    list.push(target)
    children.set(source, list)
  }
  return { parent, children }
}

/** Everything a collapsed node stands for. Hidden nodes keep their settled
 *  positions, so expanding again returns the branch where the user left it. */
export function collapsedHidden(tree: EasmTree, collapsed: ReadonlySet<string>): Set<string> {
  const hidden = new Set<string>()
  for (const id of collapsed) {
    const pending = [...(tree.children.get(id) ?? [])]
    while (pending.length) {
      const child = pending.pop()!
      // A cycle can walk back to the node this fold starts from; it stays drawn.
      if (child === id || hidden.has(child)) continue
      hidden.add(child)
      // Its own walk hides the subtree, but the node itself still disappears
      // under the collapsed ancestor: folding the root must fold every group.
      if (collapsed.has(child)) continue
      pending.push(...(tree.children.get(child) ?? []))
    }
  }
  return hidden
}

/** The branch a hovered node stands on: its first-reach path back to the
 *  root and everything below it. Cycles cannot loop the walk. */
export function branchIds(tree: EasmTree, id: string | null): Set<string> {
  if (!id) return new Set()
  const ids = new Set([id])
  for (let up = tree.parent.get(id); up !== undefined && !ids.has(up); up = tree.parent.get(up)) ids.add(up)
  const pending = [...(tree.children.get(id) ?? [])]
  while (pending.length) {
    const child = pending.pop()!
    if (ids.has(child)) continue
    ids.add(child)
    pending.push(...(tree.children.get(child) ?? []))
  }
  return ids
}

/** The nodes to light while one is hovered. The root stops at its first level:
 *  lighting every leaf under it says no more than the whole graph already does. */
export function hoverIds(tree: EasmTree, id: string | null, rootId: string | null): Set<string> {
  if (!id) return new Set()
  if (id !== rootId) return branchIds(tree, id)
  return new Set([id, ...(tree.children.get(id) ?? [])])
}

/** Node id → how many nodes its badge quotes. Cycles cannot inflate a count. */
export function descendantCounts(tree: EasmTree): Map<string, number> {
  const counts = new Map<string, number>()
  for (const id of tree.children.keys()) counts.set(id, collapsedHidden(tree, new Set([id])).size)
  return counts
}

/** Precomputed lookups the graph, card and search share. */
export interface EasmIndex {
  byId: Map<string, EasmNode>
  rootId: string | null
  degree: Map<string, number>
  /** Feature id → the non-feature nodes it connects. */
  members: Map<string, string[]>
  related: Map<string, Set<string>>
  filingDomains: Map<string, Set<string>>
  confirmedLinks: Map<string, Set<string>>
  children: Map<string, EasmNode[]>
  tree: EasmTree
  /** Node id → descendants behind its collapse badge. */
  descendants: Map<string, number>
  /** Root first, then each kind in first-appearance order. */
  legend: DisplayKind[]
}

export function indexResult(result: EasmResult): EasmIndex {
  const byId = new Map(result.nodes.map((node) => [node.id, node]))
  const degree = new Map(result.nodes.map((node) => [node.id, 0]))
  const members = new Map<string, string[]>()
  const related = new Map<string, Set<string>>()
  const filingDomains = new Map<string, Set<string>>()
  const confirmedLinks = new Map<string, Set<string>>()
  const children = new Map<string, EasmNode[]>()
  for (const edge of result.edges) {
    degree.set(edge.source, (degree.get(edge.source) ?? 0) + 1)
    degree.set(edge.target, (degree.get(edge.target) ?? 0) + 1)
    const child = byId.get(edge.target)
    if (child && edge.source !== edge.target && byId.has(edge.source)) {
      const directChildren = children.get(edge.source) ?? []
      directChildren.push(child)
      children.set(edge.source, directChildren)
    }
    for (const [a, b] of [[edge.source, edge.target], [edge.target, edge.source]]) {
      if (a !== b && byId.has(a) && byId.has(b)) {
        const neighbors = related.get(a) ?? new Set<string>()
        neighbors.add(b)
        related.set(a, neighbors)
      }
      if (edge.confirmed === true && a !== b && byId.has(a) && byId.has(b)) {
        const links = confirmedLinks.get(a) ?? new Set<string>()
        links.add(b)
        confirmedLinks.set(a, links)
      }
      if (byId.get(a)?.kind !== "feature" || byId.get(b)?.kind === "feature" || !byId.has(b)) continue
      const list = members.get(a) ?? []
      if (!list.includes(b)) list.push(b)
      members.set(a, list)
      const feature = byId.get(a)?.featureKind
      if (byId.get(b)?.kind !== "domain") continue
      if (feature === "icp" && a === edge.source && edge.confirmed === true) {
        const domains = filingDomains.get(a) ?? new Set()
        domains.add(b)
        filingDomains.set(a, domains)
      }
    }
  }
  const kinds = [...new Set(result.nodes.map(displayKind))]
  const legend = kinds.includes("root") ? ["root" as DisplayKind, ...kinds.filter((k) => k !== "root")] : kinds
  const tree = primaryTree(result.edges)
  return {
    byId, rootId: result.nodes.find((node) => node.kind === "root")?.id ?? null, degree, members, related,
    filingDomains, confirmedLinks, children, tree, descendants: descendantCounts(tree), legend,
  }
}

/** Show the target while results arrive. Other nodes need a visible neighbour. */
export function visibleIds(nodes: EasmNode[], edges: EasmEdge[], hidden?: ReadonlySet<DisplayKind>): Set<string> {
  const candidates = new Set<string>()
  const connected = new Set<string>()
  for (const node of nodes) {
    if (hidden?.has(displayKind(node))) continue
    candidates.add(node.id)
    if (node.kind === "root") connected.add(node.id)
  }
  for (const edge of edges) {
    if (edge.source !== edge.target && candidates.has(edge.source) && candidates.has(edge.target)) {
      connected.add(edge.source)
      connected.add(edge.target)
    }
  }
  return connected
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
  const drawable = visibleIds(nodes, edges)
  const byId = new Map(nodes.filter((node) => drawable.has(node.id)).map((node) => [node.id, node]))
  const children = new Map<string, EasmEdge[]>()
  for (const edge of edges) {
    if (edge.source !== edge.target && byId.has(edge.source) && byId.has(edge.target)) {
      children.set(edge.source, [...(children.get(edge.source) ?? []), edge])
    }
  }
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
  for (const id of byId.keys()) visit(id)
  return { nodes: orderedNodes, edges: orderedEdges }
}

/** Disc radius in graph units; IP count grows a node, capped like the reference. */
export function nodeRadius(ipCount?: number) {
  const ips = Number.isFinite(ipCount) && (ipCount as number) >= 0 ? (ipCount as number) : 0
  return Math.min(20 + Math.sqrt(ips / 100), 50) / 2.5
}

/** Short canvas label. Certificates and ICP numbers are long identifiers, so
 *  the graph shows their tail and the card shows them in full. */
export function graphLabel(node: EasmNode, text: EasmText) {
  if (node.id.startsWith("group:")) return node.label
  if (node.featureKind === "cert") return text.certificateLabel((node.detail?.serial || node.label).slice(-6))
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

export function summaryStats(summary: EasmResult["summary"], text: EasmText): [number, string][] {
  const rows: [number | undefined, string][] = [[summary?.clues, text.stats.clues], [summary?.fofa_assets, text.stats.fofa_assets], [summary?.fofa_ips, text.stats.fofa_ips]]
  return rows.filter((row): row is [number, string] => Number.isFinite(row[0]))
}

export interface ComponentRow extends EasmComponent {
  tags: NonNullable<EasmComponent["tags"]>
}

/** Tagged components first (hot > KEV > CVE), then by count, then name. */
export function sortComponents(components: EasmComponent[]): ComponentRow[] {
  const rank = (row: ComponentRow) => Math.min(3, ...row.tags.map((tag) => TAG_ORDER.indexOf(tag.type)))
  return components
    .map((component) => ({ ...component, tags: [...(component.tags ?? [])].sort((a, b) => TAG_ORDER.indexOf(a.type) - TAG_ORDER.indexOf(b.type)) }))
    .sort((a, b) => rank(a) - rank(b) || b.count - a.count || a.name.localeCompare(b.name, "zh-CN"))
}

/** Trending days: the stated count, else the distinct evidence dates. */
export function hotDays(row: ComponentRow) {
  const hot = row.tags.find((tag) => tag.type === "hot")
  if (!hot) return null
  return Number.isInteger(row.hot_days) ? (row.hot_days as number) : new Set((hot.evidence ?? []).map((e) => e.date).filter(Boolean)).size
}

export function validity(text: EasmText, notBefore?: string, notAfter?: string) {
  return notBefore && notAfter ? text.validity(notBefore.slice(0, 10), notAfter.slice(0, 10)) : ""
}

export const isSafeLink = (url?: string) => {
  try { return !!url && new URL(url).protocol === "https:" } catch { return false }
}


/** One line of saved evidence. The producer stores only structured fields;
 *  the record name in `via` is shown as the source returned it. */
export function basisText(basis: EasmBasis, text: EasmText) {
  return text.basis(text.basisSources[basis.source] ?? basis.source, basis.via, basis.date?.slice(0, 10))
}
