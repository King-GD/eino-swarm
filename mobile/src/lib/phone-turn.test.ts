import { describe, expect, it, vi } from "vitest"

import type { RemoteLink } from "./link"
import { answerPhoneQuestion, deliverPhoneFollowup } from "./phone-turn"
import { OpAnswer, OpDeliver, type RemoteResponse } from "./rpc"
import { emptyView } from "./session"

describe("answerPhoneQuestion", () => {
  it("shows a rejected structured answer instead of silently leaving the question waiting", async () => {
    const rpc = vi.fn(async (): Promise<RemoteResponse> => ({
      v: 1, id: "a", ok: false, code: "bad_request", error: 'ask_user: missing an answer for "test_window"',
    }))
    const setError = vi.fn()
    const setPending = vi.fn()
    await expect(answerPhoneQuestion({
      link: () => ({ alive: () => true, rpc }) as unknown as RemoteLink,
      setError,
      setPending,
    }, "thread", { call_id: "call", answers: { test_window: { answers: ["After review"] } } }))
      .rejects.toThrow("test_window")
    expect(rpc).toHaveBeenCalledWith({
      op: OpAnswer, thread_id: "thread", call_id: "call",
      answers: { test_window: { answers: ["After review"] } },
    })
    expect(setError).not.toHaveBeenCalled()
    expect(setPending.mock.calls.map(([pending]) => pending)).toEqual([true, false])
  })

  it("reports a rejected composer answer because it has no question-card alert", async () => {
    const rpc = vi.fn(async (): Promise<RemoteResponse> => ({
      v: 1, id: "a", ok: false, error: "answer no longer awaited",
    }))
    const setError = vi.fn()
    await expect(answerPhoneQuestion({
      link: () => ({ alive: () => true, rpc }) as unknown as RemoteLink,
      setError,
      setPending: vi.fn(),
    }, "thread", { text: "After review" })).rejects.toThrow("answer no longer awaited")
    expect(setError).toHaveBeenCalledWith(expect.stringContaining("answer no longer awaited"))
  })
})

describe("deliverPhoneFollowup", () => {
  function fixture(rpc: RemoteLink["rpc"]) {
    const link = { alive: () => true, rpc } as unknown as RemoteLink
    let view = { ...emptyView(), threadId: "thread" as string | null, followups: [
      { id: "first", seq: 1, text: "later" }, { id: "second", seq: 2, text: "change course" },
    ] }
    return {
      link: () => link, view: () => view,
      commit: vi.fn((next: typeof view) => { view = next }),
      fail: vi.fn(), setError: vi.fn(), setPending: vi.fn(),
    }
  }

  for (const mode of ["steer", "interrupt"] as const) {
    it(`sends the selected queue row and exact turn in a single ${mode} request`, async () => {
      const first = { id: "first", seq: 1, text: "later" }
      const rpc = vi.fn(async (): Promise<RemoteResponse> => ({ v: 1, id: "1", ok: true, followups: [first] }))
      const deps = fixture(rpc)
      await deliverPhoneFollowup(deps, "thread", "second", mode, "turn-a")
      expect(rpc).toHaveBeenCalledTimes(1)
      expect(rpc).toHaveBeenCalledWith({
        op: OpDeliver, thread_id: "thread", followup_id: "second", mode, expected_turn_id: "turn-a",
      })
      expect(deps.view().followups).toEqual([first])
    })
  }

  it("keeps the row when the target finishes before the request arrives", async () => {
    const deps = fixture(vi.fn(async () => ({ v: 1, id: "1", ok: false, code: "delivery_turn_changed" })))
    await deliverPhoneFollowup(deps, "thread", "second", "interrupt", "turn-a")
    expect(deps.commit).not.toHaveBeenCalled()
    expect(deps.setError).toHaveBeenCalledOnce()
  })

  it("ignores a queue acknowledgement after navigation", async () => {
    const deps = fixture(vi.fn(async () => ({ v: 1, id: "1", ok: true, followups: [] })))
    await deliverPhoneFollowup({ ...deps, view: emptyView }, "thread", "second", "steer", "turn-a")
    expect(deps.commit).not.toHaveBeenCalled()
  })
})
