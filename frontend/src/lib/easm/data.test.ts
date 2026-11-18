import { describe, expect, it } from "vitest"
import { isEasmResult } from "./data"
import type { EasmResult } from "./types"

const fixture: EasmResult = {
  schema_version: 1, title: "示例资产画像",
  nodes: [
    { id: "root|x", kind: "root", label: "x" },
    { id: "domain|x.test", kind: "domain", label: "x.test" },
  ],
  edges: [{ source: "root|x", target: "domain|x.test" }],
  heat: [{ date: "2026-01-01", value: 1 }, { date: "2026-01-02", value: 2 }],
  components: [{ name: "nginx", count: 1, tags: [{ type: "hot" }, { type: "cve" }] }],
}

describe("EASM snapshot relationships", () => {
  it("accepts a valid empty state and cumulative snapshots", () => {
    expect(isEasmResult({ ...fixture, nodes: [], edges: [], heat: [], components: [] })).toBe(true)
    expect(isEasmResult({ ...fixture, nodes: fixture.nodes.slice(0, 1), edges: [] })).toBe(true)
    expect(isEasmResult(fixture)).toBe(true)
    expect(isEasmResult({ ...fixture, schema_version: 2 })).toBe(false)
    expect(isEasmResult({ ...fixture, title: "" })).toBe(false)
    expect(isEasmResult(null)).toBe(false)
  })

  it("rejects duplicate IDs, invalid roots and dangling references", () => {
    expect(isEasmResult({ ...fixture, nodes: [...fixture.nodes, fixture.nodes[1]] })).toBe(false)
    expect(isEasmResult({ ...fixture, nodes: fixture.nodes.slice(1) })).toBe(false)
    expect(isEasmResult({ ...fixture, nodes: [...fixture.nodes, { ...fixture.nodes[0], id: "root|two" }] })).toBe(false)
    expect(isEasmResult({ ...fixture, edges: [{ source: "root|x", target: "missing" }] })).toBe(false)
    const nodes = [{ ...fixture.nodes[0], detail: { features: [{ kind: "domain", value: "x.test", node_id: "missing" }] } }]
    expect(isEasmResult({ ...fixture, nodes, edges: [] })).toBe(false)
    nodes[0].detail.features[0].node_id = "root|x"
    expect(isEasmResult({ ...fixture, nodes, edges: [] })).toBe(true)
  })

  it("rejects invalid or unsorted dates and repeated component tag types", () => {
    expect(isEasmResult({ ...fixture, heat: [...fixture.heat].reverse() })).toBe(false)
    expect(isEasmResult({ ...fixture, heat: [fixture.heat[0], fixture.heat[0]] })).toBe(false)
    expect(isEasmResult({ ...fixture, heat: [{ date: "2026-02-30", value: 1 }] })).toBe(false)
    expect(isEasmResult({ ...fixture, components: [{ name: "nginx", count: 1, tags: [{ type: "hot" }, { type: "hot" }] }] })).toBe(false)
  })
})
