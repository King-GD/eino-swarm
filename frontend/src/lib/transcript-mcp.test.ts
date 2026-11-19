import { describe, expect, it } from "vitest"

import { emptyTranscript, MANAGER_ID, reduceEvents } from "./transcript"
import type { SwarmEvent } from "./types"

describe("MCP task failure receipt", () => {
  it("keeps adapter diagnostics in Full log while the chat shows the terminal cause", () => {
    const base = { thread_id: "thread", turn_id: "turn", agent_id: "worker" }
    const text = '{"code":"timeout","server_id":"fixture"}'
    const receipt = { ...base, kind: "mcp_execution_blocked", seq: 1, text, tool_call_id: "original" }
    const errored = { ...base, kind: "error", seq: 2, agent_id: MANAGER_ID, err: "MCP tool failed. Repair the connection and retry." }
    const state = reduceEvents(emptyTranscript(), [receipt, errored] as SwarmEvent[])
    const blocks = state.agents[MANAGER_ID].blocks
    expect(blocks).toContainEqual(expect.objectContaining({ kind: "trace", traceKind: "mcp_execution_blocked", quiet: true, text }))
    expect(blocks).toContainEqual(expect.objectContaining({ text: errored.err }))
    expect(state.agents.worker).toBeUndefined()
  })
})
