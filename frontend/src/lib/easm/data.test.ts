import { afterEach, describe, expect, it, vi } from "vitest"
import { isEasmResult, loadEasmData, resetEasmData } from "./data"
import type { EasmResult } from "./types"

const fixture: EasmResult = {
  schema_version: 1, title: "示例资产画像",
  nodes: [{ id: "root|x", kind: "root", label: "x" }], edges: [], heat: [], components: [],
}

const response = (body: unknown, ok = true) => Promise.resolve({ ok, json: () => Promise.resolve(body) } as Response)

afterEach(() => resetEasmData())

describe("EASM data", () => {
  it("accepts only a schema_version 1 result with the four collections", () => {
    expect(isEasmResult(fixture)).toBe(true)
    // The tab is hidden rather than drawn from a document this build cannot read.
    expect(isEasmResult({ ...fixture, schema_version: 2 })).toBe(false)
    expect(isEasmResult({ ...fixture, schema_version: "1" })).toBe(false)
    expect(isEasmResult({ ...fixture, title: "" })).toBe(false)
    expect(isEasmResult({ ...fixture, edges: undefined })).toBe(false)
    expect(isEasmResult(null)).toBe(false)
  })

  it("loads the fixed dataset once and shares it with every caller", async () => {
    const fetcher = vi.fn(() => response(fixture))
    const [first, second] = await Promise.all([loadEasmData(fetcher), loadEasmData(fetcher)])
    expect(first).toEqual(fixture)
    expect(second).toEqual(fixture)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher).toHaveBeenCalledWith("/easm-data.json")
  })

  it("stays hidden on a missing, unreadable or malformed file", async () => {
    await expect(loadEasmData(() => response({}, false))).resolves.toBeNull()
    resetEasmData()
    await expect(loadEasmData(() => response({ schema_version: 1 }))).resolves.toBeNull()
    resetEasmData()
    await expect(loadEasmData(() => Promise.reject(new Error("offline")))).resolves.toBeNull()
  })
})
