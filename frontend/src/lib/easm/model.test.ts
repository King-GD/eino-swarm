import { describe, expect, it } from "vitest"
import {
  displayKind, graphLabel, highlightIds, hotDays, indexResult, isSafeLink, kindLabel, layoutOrder, localView,
  nodeRadius, searchNodes, sortComponents, summaryStats, validity, visibleIds, wrapLabel,
} from "./model"
import type { EasmNode, EasmResult } from "./types"

const node = (id: string, kind: EasmNode["kind"], extra: Partial<EasmNode> = {}): EasmNode => ({ id, kind, label: id, ...extra })
// root → company → domain ← cert feature; an org node hangs off root; mx is an orphan.
const result: EasmResult = {
  schema_version: 1, title: "EASM",
  nodes: [node("r", "root"), node("c", "company"), node("d", "domain"), node("o", "org"),
    node("cert", "feature", { featureKind: "cert", label: "*.example.test", detail: { serial: "0011223344556677" } }),
    node("mx", "feature", { featureKind: "mx" })],
  edges: [{ source: "r", target: "c", confirmed: true }, { source: "c", target: "d" }, { source: "d", target: "cert" }, { source: "r", target: "o" }],
  heat: [], components: [],
}

describe("EASM model", () => {
  it("derives feature members from edges so producers do not send them twice", () => {
    const index = indexResult(result)
    expect(index.rootId).toBe("r")
    expect(index.members.get("cert")).toEqual(["d"])
    expect(index.members.has("mx")).toBe(false)
    expect(index.degree.get("c")).toBe(2)
    // Root leads the legend; an org node and an org feature share one entry.
    expect(index.legend).toEqual(["root", "company", "domain", "org", "cert", "mx"])
    expect(kindLabel("root")).toBe("目标企业")
    expect(kindLabel("cert")).toBe("证书")
    expect(displayKind(result.nodes[4])).toBe("cert")
  })

  it("hides a kind without stranding nodes that lose their only link", () => {
    expect(visibleIds(result.nodes, result.edges, new Set()).size).toBe(6)
    // Hiding company leaves domain connected only through the feature.
    expect([...visibleIds(result.nodes, result.edges, new Set(["company"]))].sort()).toEqual(["cert", "d", "o", "r"])
  })

  it("limits the local view to the requested hops", () => {
    expect(localView(result.nodes, result.edges, "d", 1).nodes.map((n) => n.id).sort()).toEqual(["c", "cert", "d"])
    expect(localView(result.nodes, result.edges, "d", 2).nodes.map((n) => n.id).sort()).toEqual(["c", "cert", "d", "r"])
  })

  it("highlights the path back to the root and the direct children", () => {
    expect([...highlightIds(result.edges, "c")].sort()).toEqual(["c", "d", "r"])
    expect(highlightIds(result.edges, null).size).toBe(0)
  })

  it("orders the layout depth first from the root, keeping unreachable nodes", () => {
    const ordered = layoutOrder(result.nodes, result.edges, "r")
    expect(ordered.nodes.map((n) => n.id)).toEqual(["r", "c", "d", "cert", "o", "mx"])
    expect(ordered.edges).toHaveLength(result.edges.length)
  })

  it("sizes, labels and wraps nodes like the reference graph", () => {
    expect(nodeRadius()).toBe(8)
    expect(nodeRadius(-5)).toBe(8)
    expect(nodeRadius(1e9)).toBe(20)
    expect(graphLabel(result.nodes[4])).toBe("证书 · 556677")
    expect(graphLabel(node("i", "feature", { featureKind: "icp", label: "京ICP备12345678号" }))).toBe("ICP · …345678号")
    expect(graphLabel(result.nodes[1])).toBe("c")
    expect(wrapLabel("abcdef", (s) => s.length, 4)).toEqual(["abcd", "ef"])
  })

  it("searches visible nodes by label and certificate serial", () => {
    const all = new Set(result.nodes.map((n) => n.id))
    expect(searchNodes(result.nodes, "556677", all).map((n) => n.id)).toEqual(["cert"])
    expect(searchNodes(result.nodes, "  ", all)).toEqual([])
    expect(searchNodes(result.nodes, "cert", new Set())).toEqual([])
  })

  it("lists only the summary figures that were supplied, keeping a real zero", () => {
    expect(summaryStats({ clues: 0, fofa_ips: 4 })).toEqual([[0, "个线索"], [4, "个资产 IP"]])
    expect(summaryStats(undefined)).toEqual([])
  })

  it("sorts tagged components first and counts 热搜 days", () => {
    const rows = sortComponents([
      { name: "b", count: 50 },
      { name: "a", count: 1, tags: [{ type: "cve" }, { type: "hot", evidence: [{ title: "t", date: "2026-11-01" }, { title: "t", date: "2026-11-01" }] }] },
      { name: "c", count: 9, tags: [{ type: "kev" }] },
      { name: "d", count: 50 },
    ])
    expect(rows.map((r) => r.name)).toEqual(["a", "c", "b", "d"])
    expect(rows[0].tags.map((t) => t.type)).toEqual(["hot", "cve"])
    expect(hotDays(rows[0])).toBe(1)
    expect(hotDays({ ...rows[0], hot_days: 4 })).toBe(4)
    expect(hotDays(rows[1])).toBeNull()
  })

  it("formats certificate validity and allows only https links", () => {
    expect(validity("2026-01-01T00:00:00Z", "2027-01-01")).toBe("2026-01-01 至 2027-01-01")
    expect(validity("2026-01-01")).toBe("")
    expect(isSafeLink("https://example.test/a")).toBe(true)
    expect(isSafeLink("http://example.test/a")).toBe(false)
    expect(isSafeLink("javascript:alert(1)")).toBe(false)
    expect(isSafeLink("not a url")).toBe(false)
  })
})
