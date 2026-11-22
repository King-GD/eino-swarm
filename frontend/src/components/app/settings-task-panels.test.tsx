import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { TaskPanelSettings } from "./settings-task-panels"

const settings = { enabled: true, fofa_client: "fofa", max_assets: 1000, batch_size: 100, concurrency: 16, timeout_seconds: 8, flush_interval_ms: 100, max_state_bytes: 8388608, max_update_bytes: 1048576, easm: { server_id: "", tools: { organizations: "org_tool", heat: "" }, max_domains: 20, max_components: 20 } }
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it("saves only plugin settings and retains edits on failure", async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(settings)))
    .mockResolvedValueOnce(new Response(JSON.stringify({ error: "Invalid concurrency" }), { status: 400 }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...settings, concurrency: 4, fofa_client: "other-client" })))
  vi.stubGlobal("fetch", fetcher)
  render(<TaskPanelSettings query="" />)
  const field = await screen.findByLabelText("Concurrent HTTP checks")
  fireEvent.change(field, { target: { value: "4" } })
  fireEvent.click(screen.getByRole("button", { name: "Save panel settings" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("Invalid concurrency")
  expect(field).toHaveValue(4)
  fireEvent.click(screen.getByRole("button", { name: "Save panel settings" }))
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Panel settings saved"))
  expect(fetcher.mock.calls[1][0]).toBe("/api/task-panels/settings")
  expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({ concurrency: 4 })
  expect(JSON.parse(fetcher.mock.calls[2][1].body)).toEqual({ concurrency: 4 })
  expect(screen.getByLabelText("FOFA executable")).toHaveValue("other-client")
})

// Clearing one lookup's tool must save the whole EASM object, so the server
// keeps the user's other role bindings instead of resetting them to defaults.
it("saves EASM tool bindings as one object", async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(settings)))
    .mockResolvedValueOnce(new Response(JSON.stringify(settings)))
  vi.stubGlobal("fetch", fetcher)
  render(<TaskPanelSettings query="" />)
  const field = await screen.findByLabelText("Organization lookup tool")
  fireEvent.change(field, { target: { value: "" } })
  fireEvent.click(screen.getByRole("button", { name: "Save panel settings" }))
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2))
  expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({ easm: { ...settings.easm, tools: { organizations: "", heat: "" } } })
})
