import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import catalog from "../../../frontend/src/task-panels/catalog.json"
import type { TaskPanel } from "../../../frontend/src/task-panels/types"
import type { RemoteLink } from "@/lib/link"
import { PhoneTaskPanelButton, PhoneTaskPanelSheet, usePhoneTaskPanelView } from "./task-panels"

const { read } = vi.hoisted(() => ({ read: vi.fn() }))
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
