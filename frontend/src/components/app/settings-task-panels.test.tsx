import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { TaskPanelSettings } from "./settings-task-panels"

const settings = {
  enabled: true,
  fofa_client: "fofa",
  max_assets: 1000,
  batch_size: 100,
  concurrency: 16,
  timeout_seconds: 8,
  flush_interval_ms: 100,
  max_state_bytes: 8388608,
  max_update_bytes: 1048576,
  easm: { server_id: "", tools: { heat: "" }, max_domains: 20, max_components: 20 },
}
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it("exposes only common task panel controls even when the API sends source settings", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(settings))))
  render(<TaskPanelSettings query="" />)
  const heading = await screen.findByRole("heading", { name: "Task panel plugins" })
  const section = within(heading.closest("section")!)
  expect(section.getByRole("switch", { name: "Enable task panels" })).toBeChecked()
  expect(section.getAllByRole("spinbutton")).toEqual([
    section.getByLabelText("Maximum state bytes"),
    section.getByLabelText("Maximum update bytes"),
  ])
  expect(section.queryByRole("textbox")).toBeNull()
  expect(section.queryByText(/FOFA|EASM|collection|HTTP checks/i)).toBeNull()
  expect(section.queryByRole("button", { name: "Save panel settings" })).toBeNull()
})

it("saves only edited common settings and retains edits on failure", async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(settings)))
    .mockResolvedValueOnce(new Response(JSON.stringify({ error: "Invalid state limit" }), { status: 400 }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...settings, max_state_bytes: 4194304, fofa_client: "other-client" })))
  vi.stubGlobal("fetch", fetcher)
  render(<TaskPanelSettings query="" />)
  const field = await screen.findByLabelText("Maximum state bytes")
  fireEvent.change(field, { target: { value: "4194304" } })
  fireEvent.click(screen.getByRole("button", { name: "Save panel settings" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("Invalid state limit")
  expect(field).toHaveValue(4194304)
  fireEvent.click(screen.getByRole("button", { name: "Save panel settings" }))
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Panel settings saved"))
  expect(fetcher.mock.calls[1][0]).toBe("/api/task-panels/settings")
  expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({ max_state_bytes: 4194304 })
  expect(JSON.parse(fetcher.mock.calls[2][1].body)).toEqual({ max_state_bytes: 4194304 })
  expect(screen.queryByDisplayValue("other-client")).toBeNull()
  expect(screen.queryByRole("button", { name: "Save panel settings" })).toBeNull()
})
