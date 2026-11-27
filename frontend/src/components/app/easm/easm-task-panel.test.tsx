import { render } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import catalog from "../../../task-panels/catalog.json"
import easmV1 from "../../../task-panels/easm-v1.json"
import type { TaskPanel } from "../../../task-panels/types"
import type { EasmResult } from "../../../lib/easm/types"
import { EasmTaskPanel } from "./easm-task-panel"

const seen = vi.hoisted(() => vi.fn())
vi.mock("./easm-panel", () => ({ EasmPanel: (props: unknown) => { seen(props); return null } }))

// A reload or a phone that joins mid-run must distinguish a live collection
// from one whose host reports it interrupted.
it("derives heat status from the saved step and the host source receipt", () => {
  const definition = catalog.find((item) => item.id === "easm-asset-portrait")!
  const state: EasmResult = {
    schema_version: 1, title: "Subject", nodes: [{ id: "root", kind: "root", label: "Subject" }], edges: [],
    collection: { status: "running", steps: [{ id: "heat", status: "running" }, { id: "clouds", status: "running" }] }, heat: [], components: [],
  }
  const panel = { id: "p", thread_id: "t", plugin_id: definition.id, plugin_version: definition.version, revision: 1, definition, state, source_status: "running" } as TaskPanel
  const view = render(<EasmTaskPanel panel={panel} visible locale="zh" />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ heatStatus: "loading", componentsStatus: undefined, cloudsStatus: "loading" }))
  // A restart interrupts the source but cannot rewrite the state it published:
  // the stored step still says running, while the host reports the source
  // interrupted and no task is active, so every spinner must stop.
  view.rerender(<EasmTaskPanel panel={{ ...panel, source_status: "interrupted", revision: 2 }} visible locale="zh" />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ heatStatus: "interrupted", cloudsStatus: "interrupted", graphLoading: false }))
  // A first pass without a query skips metrics; while the task still runs the
  // agent can supply one, so the sections keep spinning instead of "no data".
  const skipped = { ...state, collection: { status: "done" as const, steps: [{ id: "heat" as const, status: "skipped" as const, code: "no_query" }] } }
  view.rerender(<EasmTaskPanel panel={{ ...panel, state: skipped }} visible locale="zh" active />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ heatStatus: "loading", componentsStatus: "loading", cloudsStatus: undefined }))
  const done = { ...state, collection: { status: "done" as const, steps: [{ id: "heat" as const, status: "done" as const }] } }
  view.rerender(<EasmTaskPanel panel={{ ...panel, state: done, revision: 3, source_status: "finished" }} visible locale="zh" />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ result: done, heatStatus: undefined }))
  view.rerender(<EasmTaskPanel panel={{ ...panel, state: { title: "" }, revision: 4 }} visible locale="zh" />)
  expect(view.getByRole("alert")).toBeTruthy()
  view.unmount()
})

it("keeps cloud collection hidden while an older host is working", () => {
  const panel: TaskPanel = {
    id: "p", thread_id: "t", plugin_id: easmV1.id, plugin_version: easmV1.version,
    definition: easmV1, revision: 1, state: easmV1.initial_state,
  }
  render(<EasmTaskPanel panel={panel} visible locale="zh" active />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ heatStatus: "loading", cloudsStatus: undefined }))
})

// The panel's subject row carries the host's close control, so no second
// control band sits above the panel. A stored definition can outlive this
// bundle; the control must survive that, or the panel cannot be closed.
it("keeps the host's close control in the header when the definition is stale", () => {
  const definition = catalog.find((item) => item.id === "easm-asset-portrait")!
  const state: EasmResult = {
    schema_version: 1, title: "Subject", nodes: [{ id: "root", kind: "root", label: "Subject" }], edges: [],
    collection: { status: "done", steps: [] }, heat: [], components: [],
  }
  const panel = { id: "p", thread_id: "t", plugin_id: definition.id, plugin_version: definition.version, revision: 1, definition, state } as TaskPanel
  const close = <button type="button">close-it</button>
  const view = render(<EasmTaskPanel panel={panel} visible locale="en" headerActions={close} />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ headerActions: close }))
  view.rerender(<EasmTaskPanel panel={{ ...panel, state: { title: "" }, revision: 2 }} visible locale="en" headerActions={close} />)
  expect(view.getByRole("button", { name: "close-it" })).toBeTruthy()
  view.unmount()
})
