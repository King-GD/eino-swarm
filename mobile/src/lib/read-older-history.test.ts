import { expect, it, vi } from "vitest"
import type { RemoteLink } from "./link"
import { readOlderHistory } from "./read-older-history"
import type { RemoteEvent, RemoteResponse } from "./rpc"
import { emptyView } from "./session"

it("keeps reading past a server focus that paints no assistant text", async () => {
  const event = (seq: number, text: string, agent_id = "manager"): RemoteEvent => ({
    seq, text, agent_id, thread_id: "thread", kind: "agent_message", created_at: "",
  })
  const reply = (page: Partial<RemoteResponse>): RemoteResponse => ({ ok: true, v: 1, id: "log", ...page })
  const rpc = vi.fn()
    .mockResolvedValueOnce(reply({
      seq: 18, more: true, focus_seq: 20,
      events: [event(18, "Worker markdown", "worker"), event(20, '{"agents":[{"status":"done"}]}')],
    }))
    .mockResolvedValueOnce(reply({ seq: 4, focus_seq: 9, events: [event(9, "The previous answer")] }))
  let state = { ...emptyView(), threadId: "thread", hasMore: true, oldestSeq: 25 }
  const setError = vi.fn()
  const focus = await readOlderHistory({
    link: { rpc } as unknown as RemoteLink,
    threadId: "thread",
    stillHere: () => true,
    current: () => state,
    commit: (next) => { state = { ...next, threadId: "thread" } },
    setError,
  })
  expect(focus).toBe(9)
  expect(rpc.mock.calls.map(([request]) => request.before)).toEqual([25, 18])
  expect(state.blocks.filter((block) => block.text === "The previous answer")).toHaveLength(1)
  expect(setError).not.toHaveBeenCalled()
})
