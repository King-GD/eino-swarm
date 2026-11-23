import { describe, expect, it } from "vitest"
import { graphSectionLoading, isEasmResult, sectionLoading } from "./data"
import type { EasmResult } from "./types"

const fixture: EasmResult = {
  schema_version: 1, title: "Fixture portrait",
  collection: { status: "done", steps: [] },
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
    expect(isEasmResult({ ...fixture, collection: undefined })).toBe(false)
  })

  // While the asset work continues a section spins instead of claiming "no
  // data"; once nothing more can arrive it must never leave a spinner behind.
  it("keeps a section loading until its step can no longer deliver", () => {
    const running = { status: "running" as const, steps: [{ id: "heat" as const, status: "pending" as const }, { id: "summary" as const, status: "done" as const }] }
    expect(sectionLoading({ ...fixture, collection: running }, "heat", false, "running")).toBe(true)
    expect(sectionLoading({ ...fixture, collection: running }, "summary", true, "running")).toBe(false)
    const skipped = { status: "done" as const, steps: [{ id: "heat" as const, status: "skipped" as const, code: "no_query" }] }
    expect(sectionLoading({ ...fixture, collection: skipped }, "heat", true)).toBe(true)
    expect(sectionLoading({ ...fixture, collection: skipped }, "heat", false)).toBe(false)
    expect(sectionLoading({ ...fixture, collection: { status: "idle", steps: [] } }, "components", true)).toBe(true)
    expect(sectionLoading({ ...fixture, collection: { ...running, status: "interrupted" } }, "heat", false, "interrupted")).toBe(false)
  })

  // A first batch without a query leaves the collection idle: the section must
  // keep waiting while the task can still push data, and stop once it cannot.
  it("keeps an idle collection waiting only while the task can still push data", () => {
    const idle = { status: "idle" as const, steps: [{ id: "heat" as const, status: "pending" as const }, { id: "components" as const, status: "failed" as const, code: "tool_error" }] }
    expect(sectionLoading({ ...fixture, collection: idle }, "heat", true)).toBe(true)
    expect(sectionLoading({ ...fixture, collection: idle }, "heat", false)).toBe(false)
    expect(sectionLoading({ ...fixture, collection: idle }, "components", false)).toBe(false)
    const done = { status: "done" as const, steps: [{ id: "heat" as const, status: "done" as const }] }
    expect(sectionLoading({ ...fixture, collection: done }, "heat", true)).toBe(false)
  })

  // The agent pushes the graph itself, so no step owns it: it waits while a
  // call is collecting or the conversation can still push another batch.
  it("keeps the graph loading for a running source or an active task", () => {
    expect(graphSectionLoading(false)).toBe(false)
    expect(graphSectionLoading(false, "running")).toBe(true)
    expect(graphSectionLoading(true)).toBe(true)
    expect(graphSectionLoading(false, "interrupted")).toBe(false)
    expect(graphSectionLoading(false, "finished")).toBe(false)
  })

  // A source killed by an engine restart leaves its stored steps saying
  // "running"; only the host's receipt can tell the reload that nothing collects
  // any more, so the sections must not spin on the stale state alone.
  it("stops loading a stale running state once the host reports the source interrupted", () => {
    const stale = { status: "running" as const, steps: [{ id: "heat" as const, status: "running" as const }, { id: "components" as const, status: "pending" as const }] }
    expect(sectionLoading({ ...fixture, collection: stale }, "heat", false, "running")).toBe(true)
    expect(sectionLoading({ ...fixture, collection: stale }, "heat", false, "interrupted")).toBe(false)
    expect(sectionLoading({ ...fixture, collection: stale }, "components", false, "interrupted")).toBe(false)
    expect(sectionLoading({ ...fixture, collection: stale }, "heat", false)).toBe(false)
    expect(graphSectionLoading(false, "interrupted")).toBe(false)
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

  it("rejects a second incoming edge, a self edge or a source for the root", () => {
    const nodes: EasmResult["nodes"] = [...fixture.nodes, { id: "company", kind: "company", label: "Company" }]
    for (const edge of [
      { source: "company", target: "domain|x.test" },
      { source: "company", target: "company" },
      { source: "company", target: "root|x" },
      fixture.edges[0],
    ]) {
      expect(isEasmResult({ ...fixture, nodes, edges: [...fixture.edges, edge] })).toBe(false)
    }
    expect(isEasmResult({ ...fixture, nodes, edges: [...fixture.edges, { source: "root|x", target: "company" }] })).toBe(true)
  })
})
