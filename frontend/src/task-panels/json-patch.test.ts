import { describe, expect, it } from "vitest"
import { applyJsonPatch } from "./json-patch"

describe("panel patches", () => {
  it("keeps the old state intact while applying every RFC6902 operation", () => {
    const original = { nodes: { a: { label: "first" } }, list: [1, 2], "a/b": { "~": true } }
    const next = applyJsonPatch(original, [
      { op: "test", path: "/nodes", value: { a: { label: "first" } } },
      { op: "copy", from: "/nodes/a", path: "/nodes/b" },
      { op: "replace", path: "/nodes/b/label", value: "second" },
      { op: "move", from: "/nodes/a", path: "/nodes/c" },
      { op: "add", path: "/list/-", value: 3 },
      { op: "remove", path: "/list/0" },
      { op: "test", path: "/a~1b/~0", value: true },
    ])
    expect(next).toEqual({ nodes: { b: { label: "second" }, c: { label: "first" } }, list: [2, 3], "a/b": { "~": true } })
    expect(original.nodes).toEqual({ a: { label: "first" } })
  })
  it("rejects missing paths and invalid moves atomically", () => {
    const original = { list: [1], node: { child: {} } }
    for (const operations of [
      [{ op: "replace" as const, path: "/absent", value: 1 }],
      [{ op: "add" as const, path: "/list/01", value: 1 }],
      [{ op: "remove" as const, path: "/list/1" }],
      [{ op: "move" as const, from: "/node", path: "/node/child/inside" }],
      [{ op: "test" as const, path: "/list", value: [2] }],
      [{ op: "add" as const, path: "/bad~2", value: 1 }],
    ]) expect(() => applyJsonPatch(original, operations)).toThrow()
    expect(original).toEqual({ list: [1], node: { child: {} } })
  })
  it("replaces a whole state and treats object names as data", () => {
    const next = applyJsonPatch({}, [{ op: "add", path: "/__proto__", value: { polluted: true } }]) as object
    expect(Object.getPrototypeOf(next)).toBe(Object.prototype)
    expect(Object.hasOwn(next, "__proto__")).toBe(true)
    expect(applyJsonPatch(next, [{ op: "replace", path: "", value: { rebuilt: true } }])).toEqual({ rebuilt: true })
    expect(() => applyJsonPatch({}, [{ op: "remove", path: "" }])).toThrow()
  })
})
