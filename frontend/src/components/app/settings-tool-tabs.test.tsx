import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { api } from "@/lib/api"
import type { MCPServerView } from "@/lib/mcp-settings"
import type { Settings, ToolDescriptor } from "@/lib/types"
import { useApp } from "@/store/app"

import { ToolsSettings } from "./settings-tools"

vi.mock("@/lib/api", () => ({ api: {
  mcpServers: vi.fn(), mcpPolicy: vi.fn(), saveMCPPolicy: vi.fn(), saveMCPServer: vi.fn(),
  deleteMCPServer: vi.fn(), inspectMCPServer: vi.fn(), importMCPServers: vi.fn(),
} }))

const catalog: ToolDescriptor[] = [
  { name: "read", title: "Read file", summary: "Read a file", group: "files", network: false, default_off: false },
]

const saved: MCPServerView = {
  id: "local", name: "Local tools", transport: "stdio", command: "runner", args: ["--serve"],
  url: "", enabled: true, timeout_seconds: 30, env_keys: [], header_keys: [],
}

const base: Settings = {
  server: { addr: "127.0.0.1:1", open_browser: false },
  models: { default: "", providers: [] },
  swarm: {
    max_concurrent: 1, agent_timeout_seconds: 30, max_turns: 1,
    manager_max_iterations: 1, progress_interval_seconds: 1,
    delta_coalesce_ms: 1, auto_title: false,
  },
  tools: {
    disabled: [], enabled: [],
    proxy: { http: "", https: "", no_proxy: "" },
    web_search_max_results: 8,
  },
  memory: {
    enabled: false, auto_review: false, char_limit: 1, entry_max: 1,
    review_max_iterations: 1, skills_index_max: 1, notifications: "off",
  },
  log: { level: "info" },
}

describe("tool settings tabs", () => {
  beforeEach(() => {
    useApp.setState({ locale: "en" })
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })))
    vi.mocked(api.mcpServers).mockReset().mockResolvedValue([])
    vi.mocked(api.mcpPolicy).mockReset().mockResolvedValue(5)
    vi.mocked(api.importMCPServers).mockReset().mockResolvedValue([])
  })

  it("keeps built-in tools and MCP connections on separate tabs", async () => {
    const user = userEvent.setup()
    render(<ToolsSettings settings={base} catalog={catalog} onChange={vi.fn()} />)
    expect(screen.getByRole("heading", { name: "Tools" })).toBeVisible()
    expect(screen.getByRole("tab", { name: "Built-in" })).toHaveAttribute("aria-selected", "true")
    expect(screen.getByRole("tab", { name: "MCP" })).toHaveAttribute("aria-selected", "false")
    expect(screen.getByLabelText("Web tools run on")).toBeVisible()
    expect(screen.getByText("read")).toBeVisible()
    expect(screen.queryByRole("button", { name: "Add server" })).toBeNull()

    await user.click(screen.getByRole("tab", { name: "MCP" }))
    expect(await screen.findByRole("button", { name: "Add server" })).toBeVisible()
    expect(screen.queryByLabelText("Web tools run on")).toBeNull()
    expect(screen.queryByText("read")).toBeNull()

    await user.click(screen.getByRole("tab", { name: "Built-in" }))
    expect(screen.getByLabelText("Web tools run on")).toBeVisible()
    expect(screen.queryByRole("button", { name: "Add server" })).toBeNull()
  })

  it("follows a search onto the only matching tab", async () => {
    vi.mocked(api.mcpServers).mockResolvedValue([saved])
    const view = render(<ToolsSettings settings={base} catalog={catalog} onChange={vi.fn()} query="proxy" />)
    expect(screen.getByRole("tab", { name: "Built-in" })).toHaveAttribute("aria-selected", "true")
    expect(screen.queryByRole("tab", { name: "MCP" })).toBeNull()
    expect(screen.getByRole("heading", { name: "Proxy" })).toBeVisible()

    view.rerender(<ToolsSettings settings={base} catalog={catalog} onChange={vi.fn()} query="stdio" />)
    await waitFor(() => expect(screen.getByRole("tab", { name: "MCP" })).toHaveAttribute("aria-selected", "true"))
    expect(screen.queryByRole("tab", { name: "Built-in" })).toBeNull()
    expect(screen.getByRole("button", { name: "Add server" })).toBeVisible()

    view.rerender(<ToolsSettings settings={base} catalog={catalog} onChange={vi.fn()} query="Local tools" />)
    expect(await screen.findByText("Local tools")).toBeVisible()
    expect(screen.getByRole("tab", { name: "MCP" })).toHaveAttribute("aria-selected", "true")
  })

  it("gives the JSON document the whole pane", async () => {
    const user = userEvent.setup()
    render(<ToolsSettings settings={base} catalog={catalog} onChange={vi.fn()} />)
    await user.click(screen.getByRole("tab", { name: "MCP" }))
    const edit = await screen.findByRole("button", { name: "Edit JSON" })
    await waitFor(() => expect(edit).toBeEnabled())
    await user.click(edit)
    expect(screen.getByRole("heading", { name: "JSON configuration" })).toBeVisible()
    expect(screen.queryByRole("heading", { name: "Tools" })).toBeNull()
    expect(screen.queryByRole("tab", { name: "Built-in" })).toBeNull()
    await user.click(screen.getByRole("button", { name: "Cancel" }))
    expect(screen.getByRole("heading", { name: "Tools" })).toBeVisible()
    expect(screen.getByRole("tab", { name: "MCP" })).toHaveAttribute("aria-selected", "true")
    expect(screen.getByRole("button", { name: "Add server" })).toBeVisible()
  })

  it("moves between the two tabs with the arrow keys", async () => {
    render(<ToolsSettings settings={base} catalog={catalog} onChange={vi.fn()} />)
    await act(async () => {
      await vi.mocked(api.mcpServers).mock.results[0]?.value
      await vi.mocked(api.mcpPolicy).mock.results[0]?.value
    })
    const builtin = screen.getByRole("tab", { name: "Built-in" })
    builtin.focus()
    fireEvent.keyDown(builtin.parentElement!, { key: "ArrowRight" })
    expect(screen.getByRole("tab", { name: "MCP" })).toHaveAttribute("aria-selected", "true")
    fireEvent.keyDown(screen.getByRole("tab", { name: "MCP" }).parentElement!, { key: "ArrowLeft" })
    expect(screen.getByRole("tab", { name: "Built-in" })).toHaveAttribute("aria-selected", "true")
  })
})
