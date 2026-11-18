import { useEffect, useState } from "react"
import type { EasmResult } from "./types"

/** The EASM tab draws one fixed local dataset. The file is gitignored (real
 *  enterprise data is never committed); without it the tab stays hidden. */
export const EASM_DATA_URL = "/easm-data.json"

export function isEasmResult(value: unknown): value is EasmResult {
  const v = value as Partial<EasmResult> | null
  return !!v && v.schema_version === 1 && typeof v.title === "string" && !!v.title &&
    Array.isArray(v.nodes) && Array.isArray(v.edges) && Array.isArray(v.heat) && Array.isArray(v.components)
}

let pending: Promise<EasmResult | null> | null = null

export function loadEasmData(fetcher: typeof fetch = fetch): Promise<EasmResult | null> {
  pending ??= fetcher(EASM_DATA_URL)
    .then((response) => (response.ok ? response.json() : null))
    .then((value) => (isEasmResult(value) ? value : null))
    .catch(() => null)
  return pending
}

/** Test seam: forget the cached load. */
export function resetEasmData() { pending = null }

export function useEasmData(): EasmResult | null {
  const [data, setData] = useState<EasmResult | null>(null)
  useEffect(() => {
    let live = true
    void loadEasmData().then((value) => { if (live) setData(value) })
    return () => { live = false }
  }, [])
  return data
}
