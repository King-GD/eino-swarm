import { easmText } from "./messages"
import { describe, expect, it } from "vitest"
import {
  basisText, displayKind, graphLabel, highlightIds, hotDays, indexResult, isSafeLink, kindLabel, layoutOrder,
  nodeRadius, projectEasmResult, searchNodes, sortComponents, summaryStats, validity, visibleIds, wrapLabel,
} from "./model"
import type { EasmNode, EasmResult } from "./types"

const node = (id: string, kind: EasmNode["kind"], extra: Partial<EasmNode> = {}): EasmNode => ({ id, kind, label: id, ...extra })
// The root has two company children. The domain links to its certificate.
const result: EasmResult = {
  schema_version: 1, collection: { status: "done", steps: [] }, title: "EASM",
  nodes: [node("r", "root"), node("c", "company"), node("d", "domain"), node("o", "company"),
    node("cert", "feature", { featureKind: "cert", label: "*.example.test", detail: { serial: "0011223344556677" } }),
    node("mx", "feature", { featureKind: "mx" })],
  edges: [{ source: "r", target: "c", confirmed: true }, { source: "c", target: "d" }, { source: "d", target: "cert" }, { source: "r", target: "o" }],
  heat: [], components: [],
}

const zh = easmText("zh")

describe("EASM model", () => {
  it("shows only root-reachable clues and keeps a certificate under its first saved source", () => {
    const certificate = result.nodes[4]
    const other = node("other-domain", "domain", { detail: { features: [{ kind: "cert", value: certificate.label, node_id: certificate.id }] } })
    const duplicate = node("newer-cert", "feature", { featureKind: "cert", detail: { ...certificate.detail, issuer: "Other CA" } })
    const detached = [node("detached-a", "company"), node("detached-b", "company")]
    const saved: EasmResult = {
      ...result, nodes: [...result.nodes, other, duplicate, ...detached],
      edges: [
        { source: "r", target: other.id }, { source: other.id, target: duplicate.id }, ...result.edges,
        { source: "detached-a", target: "detached-b" }, { source: "detached-b", target: "detached-a" },
      ],
    }
    const projection = projectEasmResult(saved)
    const index = indexResult(projection)
    expect(projection.nodes.map((item) => item.id)).toEqual(["r", "c", "d", "o", "cert", other.id])
    expect(projection.edges).toEqual([saved.edges[0], ...result.edges])
    expect(index.children.get("d")).toEqual([certificate])
    expect(index.children.has(other.id)).toBe(false)
    expect(saved.nodes).toContain(duplicate)
    expect(saved.edges).toContainEqual({ source: "detached-b", target: "detached-a" })
  })

  it("derives feature members and groups asset organizations apart from certificate subjects", () => {
    const index = indexResult(result)
    expect(index.rootId).toBe("r")
    expect(index.members.get("cert")).toEqual(["d"])
    expect(index.members.has("mx")).toBe(false)
    expect(index.children.get("r")?.map((child) => child.id)).toEqual(["c", "o"])
    expect(index.children.get("d")?.map((child) => child.id)).toEqual(["cert"])
    expect(index.children.has("cert")).toBe(false)
    expect(index.degree.get("c")).toBe(2)
    expect(index.legend).toEqual(["root", "company", "domain", "cert", "mx"])
    expect(displayKind(result.nodes[3])).toBe("company")
    expect(displayKind(node("subject", "feature", { featureKind: "org" }))).toBe("org")
    expect(kindLabel("company", zh)).toBe("关联组织")
    expect(kindLabel("org", zh)).toBe("证书组织")
    expect(kindLabel("root", zh)).toBe("目标企业")
    expect(kindLabel("cert", zh)).toBe("域名证书")
    expect(displayKind(result.nodes[4])).toBe("cert")
  })

  it("hides a kind without stranding nodes that lose their only link", () => {
    expect([...visibleIds(result.nodes, result.edges)].sort()).toEqual(["c", "cert", "d", "o", "r"])
    // Hiding company leaves the root and the domain-certificate link.
    expect([...visibleIds(result.nodes, result.edges, new Set(["company"]))].sort()).toEqual(["cert", "d", "r"])
  })

  it("highlights the path back to the root and the direct children", () => {
    expect([...highlightIds(result.edges, "c")].sort()).toEqual(["c", "d", "r"])
    expect(highlightIds(result.edges, null).size).toBe(0)
  })

  it("keeps isolated observations out of the layout until a link arrives", () => {
    const invalidEdges = [{ source: "mx", target: "mx" }, { source: "mx", target: "missing" }]
    const ordered = layoutOrder(result.nodes, [...result.edges, ...invalidEdges], "r")
    expect(ordered.nodes.map((n) => n.id)).toEqual(["r", "c", "d", "cert", "o"])
    expect(ordered.edges).toHaveLength(result.edges.length)
    expect(ordered.edges).toEqual(expect.arrayContaining(result.edges))
    expect(layoutOrder(result.nodes, [], "r").nodes.map((n) => n.id)).toEqual(["r"])

    const link = { source: "d", target: "mx", confirmed: false }
    const updated = layoutOrder(result.nodes, [...result.edges, link], "r")
    expect(updated.nodes.map((n) => n.id)).toEqual(["r", "c", "d", "cert", "mx", "o"])
    expect(updated.edges).toContainEqual(link)
  })

  it("sizes, labels and wraps nodes like the reference graph", () => {
    expect(nodeRadius()).toBe(8)
    expect(nodeRadius(-5)).toBe(8)
    expect(nodeRadius(1e9)).toBe(20)
    expect(graphLabel(result.nodes[4], zh)).toBe("证书 · 556677")
    expect(graphLabel(node("i", "feature", { featureKind: "icp", label: "京ICP备12345678号" }), zh)).toBe("ICP · …345678号")
    expect(graphLabel(result.nodes[1], zh)).toBe("c")
    expect(wrapLabel("abcdef", (s) => s.length, 4)).toEqual(["abcd", "ef"])
  })

  it("searches visible nodes by label and certificate serial", () => {
    const all = new Set(result.nodes.map((n) => n.id))
    expect(searchNodes(result.nodes, "556677", all).map((n) => n.id)).toEqual(["cert"])
    expect(searchNodes(result.nodes, "  ", all)).toEqual([])
    expect(searchNodes(result.nodes, "cert", new Set())).toEqual([])
  })

  it("lists only the summary figures that were supplied, keeping a real zero", () => {
    expect(summaryStats({ clues: 0, fofa_ips: 4 }, zh)).toEqual([[0, "个线索"], [4, "个资产 IP"]])
    expect(summaryStats(undefined, zh)).toEqual([])
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
    expect(validity(zh, "2026-01-01T00:00:00Z", "2027-01-01")).toBe("2026-01-01 至 2027-01-01")
    expect(validity(zh, "2026-01-01")).toBe("")
    expect(isSafeLink("https://example.test/a")).toBe(true)
    expect(isSafeLink("http://example.test/a")).toBe(false)
    expect(isSafeLink("javascript:alert(1)")).toBe(false)
    expect(isSafeLink("not a url")).toBe(false)
  })
})

// Every label exists in both languages, so switching the app locale never
// leaves a blank badge or an untranslated card heading.
describe("EASM copy", () => {
  it("has the same keys in English and Chinese and follows the locale", () => {
    const en = easmText("en")
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort())
    for (const key of ["kinds", "featureGroups", "tags", "basisSources", "stats"] as const) {
      expect(Object.keys(en[key]).sort()).toEqual(Object.keys(zh[key]).sort())
    }
    expect(kindLabel("cert", en)).toBe(en.kinds.cert)
    expect(easmText("zh-CN")).toBe(zh)
    expect(basisText({ source: "filings", via: "R-1", date: "2026-01-02T00:00:00Z" }, en)).toContain("R-1")
    expect(basisText({ source: "unknown_source" }, en)).toContain("unknown_source")
  })
})
