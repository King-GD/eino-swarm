import { render, screen } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import type { TaskPanel } from "@/task-panels/types"
import { isNativeTaskPanel, NativeTaskPanel } from "./native-task-panels"

vi.mock("./easm/easm-task-panel", () => ({ EasmTaskPanel: ({ locale }: { locale: string }) => <p>native portrait {locale}</p> }))

const panel = (pluginId: string, renderer?: string): TaskPanel => ({
  id: "p", thread_id: "t", plugin_id: pluginId, plugin_version: "1", revision: 1, state: {},
  definition: { id: pluginId, version: "1", name: "N", description: "", entry: renderer ? "" : "x/index.html", renderer, schema: {} },
})

// Only a catalog definition marked native selects bundled code, and an
// unregistered native id reports itself instead of loading an empty iframe.
it("renders registered native panels and refuses unknown ones", async () => {
  expect(isNativeTaskPanel(panel("easm-asset-portrait", "native"))).toBe(true)
  expect(isNativeTaskPanel(panel("fofa-world-map"))).toBe(false)
  const view = render(<NativeTaskPanel panel={panel("easm-asset-portrait", "native")} visible locale="en" />)
  expect(await screen.findByText("native portrait en")).toBeInTheDocument()
  view.rerender(<NativeTaskPanel panel={panel("not-registered", "native")} visible locale="en" />)
  expect(screen.getByRole("alert")).toBeInTheDocument()
})
