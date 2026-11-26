import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { easmText } from "../../../frontend/src/lib/easm/messages"
import catalog from "../../../frontend/src/task-panels/catalog.json"
import easmV1 from "../../../frontend/src/task-panels/easm-v1.json"
import type { TaskPanel } from "../../../frontend/src/task-panels/types"
import type { RemoteLink } from "@/lib/link"
import { PhoneTaskPanelButton, PhoneTaskPanelSheet, usePhoneTaskPanelView } from "./task-panels"
import { ThreadScreen } from "./thread-screen"

const { read } = vi.hoisted(() => ({ read: vi.fn() }))
vi.mock("../../../frontend/src/components/app/easm/asset-graph", () => ({ AssetGraph: () => null }))
vi.mock("@/lib/use-task-panels", () => ({ useTaskPanels: read }))
const link = { alive: () => true, rpc: vi.fn(), path: "relay", close: vi.fn(), announceDevice: vi.fn() } as RemoteLink
const definition = catalog[0]
function panel(id = "p", revision = 1): TaskPanel {
  return { id, thread_id: "thread", plugin_id: definition.id, plugin_version: definition.version, revision, state: definition.initial_state, definition }
}
let owner = 0
let refresh = vi.fn()
function data(panels: TaskPanel[] = []) { return { panels, changes: {}, loading: false, error: undefined as string | undefined, supported: true, connected: true, available: panels.length > 0, refresh } }
function Harness({ authority = `pc-${owner}`, target = link }: { authority?: string; target?: RemoteLink }) {
  const view = usePhoneTaskPanelView(target, "thread", authority)
  return <><PhoneTaskPanelButton view={view} /><PhoneTaskPanelSheet view={view} /></>
}
beforeEach(() => {
  ++owner; refresh = vi.fn(); link.rpc = vi.fn(); read.mockReset().mockReturnValue(data())
  vi.stubGlobal("fetch", vi.fn(async () => new Response("<!doctype html><body>Fixture plugin</body>")))
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} })
})
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it("opens an upstream EASM v1 snapshot on a new phone", async () => {
  const component = "Recorded component"
  const legacy: TaskPanel = { ...panel(), plugin_id: easmV1.id, plugin_version: easmV1.version, definition: easmV1,
    state: { ...easmV1.initial_state, components: [{ name: component, count: 3 }] } }
  read.mockReturnValue(data([legacy]))
  render(<Harness />)
  await screen.findByRole("dialog", { name: "Task panels" })
  await act(async () => { await vi.dynamicImportSettled() })
  expect(await screen.findByRole("button", { name: easmText("en").tagEvidence(component) })).toBeVisible()
})

it("only offers panels for a paired PC with an available panel", () => {
  const view = render(<Harness />)
  expect(screen.queryByRole("button", { name: "Task panels" })).toBeNull()
  read.mockReturnValue(data([panel()]))
  view.rerender(<Harness authority="" />)
  expect(screen.queryByRole("button", { name: "Task panels" })).toBeNull()
})

it("opens once and preserves its iframe when closed and reopened after updates", async () => {
  read.mockReturnValue(data([panel()]))
  const view = render(<Harness />)
  expect(await screen.findByRole("dialog", { name: "Task panels" })).toBeVisible()
  const frame = await screen.findByTestId("task-panel-frame")
  fireEvent.click(screen.getByRole("button", { name: "Close" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  read.mockReturnValue(data([panel("p", 2)]))
  view.rerender(<Harness />)
  expect(screen.queryByRole("dialog")).toBeNull()
  fireEvent.click(screen.getByRole("button", { name: "Task panels" }))
  expect(screen.getByTestId("task-panel-frame")).toBe(frame)
  expect(fetch).toHaveBeenCalledOnce()
})

it("keeps multiple iframe instances across selection and Android Back only closes the panel", async () => {
  read.mockReturnValue(data([panel("first"), panel("second")]))
  render(<Harness />)
  await waitFor(() => expect(screen.getAllByTestId("task-panel-frame")).toHaveLength(2))
  const frames = screen.getAllByTestId("task-panel-frame")
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "second" } })
  expect(screen.getAllByTestId("task-panel-frame")).toEqual(frames)
  let handled = false
  act(() => { handled = window.__zwaiAndroidBack?.() ?? false })
  expect(handled).toBe(true)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(link.rpc).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole("button", { name: "Task panels" }))
  expect(screen.getByRole("combobox")).toHaveValue("second")
})

it("keeps a retry entry for transfer errors before a safe panel body has arrived", () => {
  read.mockReturnValue({ ...data(), available: true, connected: false, error: "Panel transfer failed" })
  render(<Harness />)
  expect(screen.queryByRole("dialog")).toBeNull()
  fireEvent.click(screen.getByRole("button", { name: "Task panels" }))
  expect(screen.getByRole("alert")).toHaveTextContent("Panel transfer failed")
  expect(screen.getByRole("status")).toHaveTextContent("Reconnecting")
  fireEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(refresh).toHaveBeenCalledOnce()
  fireEvent.keyDown(window, { key: "Escape" })
  expect(screen.queryByRole("dialog")).toBeNull()
})

it("refuses a mismatched bundled version before requesting or executing HTML", async () => {
  read.mockReturnValue(data([{ ...panel(), plugin_version: "older" }]))
  render(<Harness />)
  expect(await screen.findByRole("alert")).toHaveTextContent("incompatible")
  expect(fetch).not.toHaveBeenCalled()
  expect(screen.queryByTestId("task-panel-frame")).toBeNull()
})

it("destroys the previous owner's iframe before showing a same-ID panel from another PC", async () => {
  read.mockReturnValue(data([panel()]))
  const view = render(<Harness />)
  const previous = await screen.findByTestId("task-panel-frame")
  view.rerender(<Harness authority={`other-${owner}`} />)
  await waitFor(() => expect(screen.getByTestId("task-panel-frame")).not.toBe(previous))
})

it("replaces a same-PC same-thread iframe when restore changes only the data generation", async () => {
  read.mockReturnValue(data([panel("p", 9)]))
  const pc = `generation-pc-${owner}`
  const view = render(<Harness authority={`${pc}:0`} />)
  const previous = await screen.findByTestId("task-panel-frame")
  read.mockReturnValue(data([panel("p", 1)]))
  view.rerender(<Harness authority={`${pc}:1`} />)
  await waitFor(() => expect(screen.getByTestId("task-panel-frame")).not.toBe(previous))
  expect(read).toHaveBeenLastCalledWith(link, "thread", `${pc}:1`, true)
})

// Use the conversation's Back handler as well as the nested sheet handler.
// A test of the sheet alone cannot see the conversation consume the first Back.
it.each(["automatic open", "manual reopen", "an update over Outputs"])("closes supply detail, then task panels, after %s", async (opening) => {
  const easm = catalog.find((item) => item.id === "easm-asset-portrait")!
  const component = "Fixture component"
  const state = { ...easm.initial_state, components: [{ name: component, count: 3 }] }
  const native: TaskPanel = { id: "n", thread_id: "thread", plugin_id: easm.id, plugin_version: easm.version, revision: 1, state, definition: easm }
  const onBack = vi.fn()
  const aboveOutputs = opening === "an update over Outputs"
  if (aboveOutputs) link.rpc = vi.fn().mockResolvedValue({ ok: true, files: [] })
  else read.mockReturnValue(data([native]))
  const conversation = () => <ThreadScreen detail={{ id: "thread", title: "Conversation" }} blocks={[]} caughtUp
    fileLink={link} panelOwnerKey={`pc-${owner}`} onBack={onBack}
    onSend={vi.fn()} onStop={vi.fn()} onAnswer={vi.fn()} onAnswerStructured={vi.fn()} />
  const view = render(conversation())
  if (aboveOutputs) {
    fireEvent.click(screen.getByRole("button", { name: "Outputs" }))
    expect(screen.getByRole("heading", { name: "Outputs" })).toBeVisible()
    read.mockReturnValue(data([native]))
    view.rerender(conversation())
  }
  await screen.findByRole("dialog", { name: "Task panels" })
  if (opening === "manual reopen") {
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.click(screen.getByRole("button", { name: "Task panels" }))
  }
  // The sheet can appear before its lazy renderer; cold imports can exceed findBy's timeout.
  await act(async () => { await vi.dynamicImportSettled() })
  const sheet = await screen.findByTestId("phone-task-panels")
  fireEvent.click(await screen.findByRole("button", { name: easmText("en").tagEvidence(component) }))
  const inner = await screen.findByRole("dialog", { name: component })
  expect(sheet.contains(inner)).toBe(true)
  if (aboveOutputs) {
    read.mockReturnValue(data([{ ...native, revision: 2 }]))
    view.rerender(conversation())
    expect(screen.getByTestId("easm-task-panel")).toHaveAttribute("data-revision", "2")
  }
  let handled = false
  act(() => { handled = window.__zwaiAndroidBack?.() ?? false })
  expect(handled).toBe(true)
  expect(screen.queryByRole("dialog", { name: component })).toBeNull()
  expect(sheet).toHaveAttribute("aria-hidden", "false")
  act(() => { handled = window.__zwaiAndroidBack?.() ?? false })
  expect(handled).toBe(true)
  expect(screen.queryByRole("dialog", { name: "Task panels" })).toBeNull()
  expect(screen.getByRole("heading", { name: aboveOutputs ? "Outputs" : "Conversation" })).toBeVisible()
  if (aboveOutputs) {
    act(() => { handled = window.__zwaiAndroidBack?.() ?? false })
    expect(handled).toBe(true)
    expect(screen.getByRole("heading", { name: "Conversation" })).toBeVisible()
  }
  expect(onBack).not.toHaveBeenCalled()
})
