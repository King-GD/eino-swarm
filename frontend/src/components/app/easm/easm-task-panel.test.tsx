import { render } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import catalog from "../../../task-panels/catalog.json"
import type { TaskPanel } from "../../../task-panels/types"
import type { EasmResult } from "../../../lib/easm/types"
import { EasmTaskPanel } from "./easm-task-panel"

const seen = vi.hoisted(() => vi.fn())
vi.mock("./easm-panel", () => ({ EasmPanel: (props: unknown) => { seen(props); return null } }))

// Heat loading follows the saved step status and the host's source receipt, so
// a reload or a phone that joins mid-run shows the same spinner as the PC, and
// a finished or interrupted run none.
it("derives heat loading from the persisted collection steps", () => {
  const definition = catalog.find((item) => item.id === "easm-asset-portrait")!
  const state: EasmResult = {
    schema_version: 1, title: "Subject", nodes: [{ id: "root", kind: "root", label: "Subject" }], edges: [],
    collection: { status: "running", steps: [{ id: "heat", status: "running" }] }, heat: [], components: [],
  }
  const panel = { id: "p", thread_id: "t", plugin_id: definition.id, plugin_version: definition.version, revision: 1, definition, state, source_status: "running" } as TaskPanel
  const view = render(<EasmTaskPanel panel={panel} visible locale="zh" />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ heatLoading: true, componentsLoading: false }))
  // A restart interrupts the source but cannot rewrite the state it published:
  // the stored step still says running, while the host reports the source
  // interrupted and no task is active, so every spinner must stop.
  view.rerender(<EasmTaskPanel panel={{ ...panel, source_status: "interrupted", revision: 2 }} visible locale="zh" />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ heatLoading: false, graphLoading: false }))
  // A first pass without a query skips metrics; while the task still runs the
  // agent can supply one, so the sections keep spinning instead of "no data".
  const skipped = { ...state, collection: { status: "done" as const, steps: [{ id: "heat" as const, status: "skipped" as const, code: "no_query" }] } }
  view.rerender(<EasmTaskPanel panel={{ ...panel, state: skipped }} visible locale="zh" active />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ heatLoading: true, componentsLoading: true }))
  const done = { ...state, collection: { status: "done" as const, steps: [{ id: "heat" as const, status: "done" as const }] } }
  view.rerender(<EasmTaskPanel panel={{ ...panel, state: done, revision: 3, source_status: "finished" }} visible locale="zh" />)
  expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ result: done, heatLoading: false }))
  view.rerender(<EasmTaskPanel panel={{ ...panel, state: { title: "" }, revision: 4 }} visible locale="zh" />)
  expect(view.getByRole("alert")).toBeTruthy()
  view.unmount()
})
