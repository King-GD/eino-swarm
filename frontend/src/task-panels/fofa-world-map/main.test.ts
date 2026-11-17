/// <reference types="vite/client" />
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import html from "./index.html?raw"
import type { PanelCallbacks } from "../sdk"
import type { HostContext } from "../types"

const { create } = vi.hoisted(() => ({ create: vi.fn() }))
vi.mock("../sdk", () => ({ createTaskPanel: create }))

const context: HostContext = { panel_id: "p", thread_id: "t", revision: 1, locale: "en", theme: "light", width: 400, height: 400, visible: true, source_status: "interrupted" }
function state(phase: string) {
  return { collection: { phase, received: 1, discovered: 1, completeness: "partial", reason: "client_failure" }, assets_by_id: { a: { id: "a", ip: "192.0.2.1", host: "fixture.invalid", port: 443, protocol: "https", country: "US", region: "", city: "", status: "pending" } }, results_by_id: {}, probe: { pending: 1, running: 0, responded: 0, no_response: 0, skipped: 0, interrupted: 0 } }
}
beforeEach(async () => {
  vi.resetModules(); create.mockClear()
  document.body.innerHTML = new DOMParser().parseFromString(html, "text/html").body.innerHTML
  await import("./main")
})
afterEach(() => { document.body.replaceChildren() })

it("retains a confirmed collection failure when source cleanup marks unfinished probes interrupted", async () => {
  const callbacks = create.mock.calls[0][0] as PanelCallbacks
  await callbacks.onSnapshot(state("failed"), context)
  expect(document.getElementById("phase")).toHaveTextContent("Collection failed")
  expect(document.getElementById("completeness")).toHaveTextContent("client_failure")
  expect(document.getElementById("details")).toHaveTextContent("interrupted")
  expect(document.getElementById("counters")).toHaveTextContent("1 interrupted")
})

it("shows an unfinished collection as interrupted without rewriting an ended collection", async () => {
  const callbacks = create.mock.calls[0][0] as PanelCallbacks
  await callbacks.onSnapshot(state("collecting"), context)
  expect(document.getElementById("phase")).toHaveTextContent("interrupted")
  await callbacks.onSnapshot(state("finished"), { ...context, revision: 2 })
  expect(document.getElementById("phase")).toHaveTextContent("Collection ended")
})

it("does not color regions without a source ISO code using another country's assets", async () => {
  const callbacks = create.mock.calls[0][0] as PanelCallbacks
  const template = state("finished")
  const next = { ...template, assets_by_id: { a: { ...template.assets_by_id.a, country: "CN" }, b: { ...template.assets_by_id.a, id: "b", country: "SL" } } }
  await callbacks.onSnapshot(next, context)
  const regions = [...document.querySelectorAll(".countries path")].filter((node) => /Somaliland:|Turkish Republic of Northern Cyprus:/.test(node.textContent ?? ""))
  expect(regions).toHaveLength(2)
  for (const region of regions) expect(region).toHaveAttribute("fill", "var(--empty)")
  const keys = regions.map((node) => node.getAttribute("data-geometry"))
  await callbacks.onSnapshot(next, { ...context, revision: 2 })
  for (const [index, key] of keys.entries()) expect(document.querySelector(`[data-geometry="${key}"]`)).toBe(regions[index])
})
