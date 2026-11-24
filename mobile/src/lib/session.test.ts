import { describe, expect, it } from "vitest"

import type { RemoteEvent, RemoteResponse, ThreadDetail } from "./rpc"
import { OpEvent, OpReady } from "./rpc"
import { applyOpenDetail, applyPush, emptyView, markRunning, openView, prependOlder } from "./session"
import { managerBlocks, phoneAgents } from "./transcript"

function detail(partial?: Partial<ThreadDetail>): ThreadDetail {
  return { id: "t1", title: "one", ...partial }
}

function ev(partial: Partial<RemoteEvent> & Pick<RemoteEvent, "kind" | "seq">): RemoteEvent {
  return {
    thread_id: "t1",
    text: "",
    created_at: "2026-01-01T00:00:00Z",
    ...partial,
  }
}

function push(event: RemoteEvent, thread_id = "t1"): RemoteResponse {
  return {
    v: 1,
    id: "",
    ok: true,
    op: OpEvent,
    thread_id,
    seq: event.seq,
    event,
  }
}

function ready(partial?: Partial<RemoteResponse>): RemoteResponse {
  return {
    v: 1,
    id: "",
    ok: true,
    op: OpReady,
    thread_id: "t1",
    seq: 0,
    status: { running: false },
    ...partial,
  }
}

describe("phone watch session", () => {
  it("keeps both completion times when an earlier scheduled turn is paged in", () => {
    let view = applyPush(openView(detail()), ready({ seq: 6, more: true, events: [
      ev({ seq: 5, kind: "agent_message", turn_id: "second", text: "newer result" }),
      ev({ seq: 6, kind: "done", turn_id: "second", created_at: "2026-10-08T02:03:04Z" }),
    ] }))
    view = prependOlder(view, [
      ev({ seq: 1, kind: "user_message", turn_id: "first", text: "This turn is a scheduled check." }),
      ev({ seq: 2, kind: "agent_message", turn_id: "first", text: "older result" }),
      ev({ seq: 3, kind: "done", turn_id: "first", created_at: "2026-10-08T01:01:02Z" }),
    ], false)
    expect(managerBlocks(view.blocks).map((b) => [b.kind, b.text])).toEqual([
      ["answer", "older result"], ["turn_end", "2026-10-08T01:01:02Z"],
      ["answer", "newer result"], ["turn_end", "2026-10-08T02:03:04Z"],
    ])
  })

  it("rebuilds worker identity and separate log after older pages load", () => {
    let view = applyPush(openView(detail()), ready({
      seq: 5, more: true,
      events: [
        ev({ seq: 4, kind: "agent_message", agent_id: "manager", text: "manager answer" }),
        ev({ seq: 5, kind: "finished", agent_id: "w1", text: "done" }),
      ],
    }))
    view = prependOlder(view, [
      ev({ seq: 1, kind: "spawned", agent_id: "w1", role: "reader", text: "" }),
      ev({ seq: 2, kind: "reasoning", agent_id: "w1", text: "read evidence" }),
      ev({ seq: 3, kind: "agent_message", agent_id: "w1", text: "worker answer" }),
    ], false)
    expect(managerBlocks(view.blocks).map((b) => b.text)).toEqual(["manager answer"])
    expect(phoneAgents(view.blocks)).toMatchObject([{
      id: "w1", role: "reader", status: "done",
      blocks: [{ kind: "spawn" }, { kind: "reasoning" }, { kind: "answer", text: "worker answer" }],
    }])
  })

  it("keeps simultaneous manager and worker live text while paging older history", () => {
    let view = applyPush(openView(detail()), ready({ seq: 2, more: true,
      events: [ev({ seq: 2, kind: "spawned", agent_id: "w1", role: "reader" })],
    }))
    view = applyPush(view, push(ev({ seq: 0, kind: "delta", agent_id: "manager", text: "manager live" })))
    view = applyPush(view, push(ev({ seq: 0, kind: "delta", agent_id: "w1", text: "worker live" })))
    const liveIds = view.blocks.filter((block) => block.streaming).map((block) => block.id)
    view = prependOlder(view, [ev({ seq: 1, kind: "user_message", text: "request" })], false)
    expect(managerBlocks(view.blocks).find((b) => b.kind === "answer"))
      .toMatchObject({ text: "manager live", streaming: true })
    expect(phoneAgents(view.blocks)[0].blocks.find((b) => b.kind === "answer"))
      .toMatchObject({ text: "worker live", streaming: true })
    expect(view.blocks.filter((block) => block.streaming).map((block) => block.id)).toEqual(liveIds)
    view = applyPush(view, push(ev({ seq: 0, kind: "delta", agent_id: "manager", text: "manager live, continued" })))
    view = applyPush(view, push(ev({ seq: 0, kind: "delta", agent_id: "w1", text: "worker live, continued" })))
    expect(view.blocks.filter((block) => block.streaming).map((block) => block.id)).toEqual(liveIds)
    expect(new Set(view.blocks.map((block) => block.id)).size).toBe(view.blocks.length)
    view = applyPush(view, ready({ seq: 4, events: [
      ev({ seq: 3, kind: "agent_message", agent_id: "manager", text: "manager live, continued" }),
      ev({ seq: 4, kind: "agent_message", agent_id: "w1", text: "worker live, continued" }),
    ] }))
    expect(managerBlocks(view.blocks).filter((block) => block.kind === "answer").map((block) => block.text))
      .toEqual(["manager live, continued"])
    expect(phoneAgents(view.blocks)[0].blocks.filter((block) => block.kind === "answer").map((block) => block.text))
      .toEqual(["worker live, continued"])
    expect(new Set(view.blocks.map((block) => block.id)).size).toBe(view.blocks.length)
  })
  it("does not paint catch-up until ready, then folds it in one shot", () => {
    let view = openView(detail())
    view = applyPush(view, push(ev({ seq: 1, kind: "user_message", text: "hi" })))
    expect(view.blocks).toEqual([])
    expect(view.queued).toHaveLength(1)
    view = applyPush(view, ready({ seq: 1 }))
    expect(view.blocks).toHaveLength(1)
    expect(view.lastSeq).toBe(1)
    expect(view.caughtUp).toBe(true)
    expect(view.queued).toEqual([])
    expect(applyPush(emptyView(), push(ev({ seq: 1, kind: "user_message", text: "hi" }))).blocks).toEqual([])
  })

  it("keeps a listing stub until watch ready so a tap is not a freeze", () => {
    const stub = openView(detail({ title: "parked", waiting: true }))
    expect(stub.caughtUp).toBe(false)
    const opened = applyOpenDetail(stub, detail({ title: "parked", waiting: true, goal: "keep going" }))
    expect(opened.detail?.goal).toBe("keep going")
    expect(opened.caughtUp).toBe(false)
    expect(opened.blocks).toEqual([])
    const other = applyOpenDetail(opened, detail({ id: "t2", title: "other" }))
    expect(other.threadId).toBe("t2")
    expect(other.detail?.title).toBe("other")
    expect(applyOpenDetail(emptyView(), detail()).threadId).toBe("t1")
  })

  it("paints ready.events in one shot without a replay", () => {
    const view = applyPush(
      openView(detail()),
      ready({
        seq: 3,
        more: true,
        events: [
          ev({ seq: 2, kind: "user_message", text: "mid" }),
          ev({ seq: 3, kind: "agent_message", text: "tail" }),
        ],
      }),
    )
    expect(view.blocks.map((b) => b.text)).toEqual(["mid", "tail"])
    expect(view.oldestSeq).toBe(2)
    expect(view.lastSeq).toBe(3)
    expect(view.hasMore).toBe(true)
    expect(view.caughtUp).toBe(true)
  })

  it("pages when the live-edge snapshot is not seq 1 even if ready omits more", () => {
    const view = applyPush(
      openView(detail()),
      ready({
        seq: 24,
        events: [
          ev({ seq: 20, kind: "user_message", text: "mid" }),
          ev({ seq: 24, kind: "agent_message", text: "tail" }),
        ],
      }),
    )
    expect(view.hasMore).toBe(true)
    expect(view.oldestSeq).toBe(20)
  })

  it("ignores another thread and duplicate seq", () => {
    let view = applyPush(openView(detail()), ready({ seq: 0 }))
    view = applyPush(view, push(ev({ seq: 2, kind: "user_message", text: "a" })))
    view = applyPush(view, push(ev({ seq: 2, kind: "user_message", text: "a" })))
    view = applyPush(view, push(ev({ seq: 3, kind: "delta", text: "nope" }), "other"))
    expect(view.blocks).toHaveLength(1)
    expect(view.lastSeq).toBe(2)
  })

  it("tracks running, title, goal and plan from the same kinds as desktop", () => {
    let view = openView(detail())
    view = applyPush(view, {
      v: 1,
      id: "",
      ok: true,
      op: OpReady,
      thread_id: "t1",
      seq: 4,
      status: { running: true, turn_id: "tu", awaiting_answer: false },
    })
    expect(view.detail?.running?.turn_id).toBe("tu")
    view = applyPush(view, push(ev({ seq: 5, kind: "title", text: "named" })))
    expect(view.detail?.title).toBe("named")
    view = applyPush(view, push(ev({ seq: 6, kind: "goal", text: "objective" })))
    expect(view.detail?.goal_on).toBe(true)
    view = applyPush(view, push(ev({ seq: 7, kind: "plan", text: "steps" })))
    expect(view.detail?.plan_on).toBe(true)
    view = applyPush(view, push(ev({ seq: 8, kind: "done" })))
    expect(view.detail?.running).toBeUndefined()
    view = markRunning(view)
    expect(view.detail?.running?.thread_id).toBe("t1")
  })

  it("keeps a parked wait and a standing goal after done so the phone is not idle", () => {
    let view = applyPush(
      openView(detail({ goal: "keep going", goal_on: true })),
      ready({
        seq: 1,
        status: {
          running: false,
          waiting: true,
          wake: { id: "sch_1", title: "wake", next_run_at: "2026-09-21T02:00:00Z" },
        },
      }),
    )
    expect(view.detail?.waiting).toBe(true)
    expect(view.detail?.wake?.id).toBe("sch_1")
    view = applyPush(
      view,
      push(
        ev({
          seq: 2,
          kind: "schedule",
          text: JSON.stringify({
            id: "sch_1",
            kind: "thread",
            status: "active",
            next_run_at: "2026-09-21T04:00:00Z",
          }),
        }),
      ),
    )
    expect(view.detail?.wake?.next_run_at).toBe("2026-09-21T04:00:00Z")
    view = applyPush(view, push(ev({ seq: 3, kind: "goal_complete", text: "done" })))
    expect(view.detail?.goal_on).toBe(true)
    expect(view.detail?.goal_complete).toBe(true)
    view = applyPush(view, push(ev({ seq: 4, kind: "done" })))
    expect(view.detail?.running).toBeUndefined()
    expect(view.detail?.waiting).toBe(true)
    expect(view.detail?.wake?.id).toBe("sch_1")
    view = applyPush(view, {
      v: 1,
      id: "",
      ok: true,
      op: OpEvent,
      thread_id: "t1",
      seq: 6,
      event: ev({ seq: 6, kind: "schedule_fired", text: "sch_1" }),
      status: { running: true, waiting: false, turn_id: "tu2" },
    })
    expect(view.detail?.waiting).toBe(false)
    expect(view.detail?.running?.turn_id).toBe("tu2")
  })

  it("opens hasMore from ready and prepends older events above the viewport", () => {
    let view = applyPush(
      openView(detail()),
      ready({
        seq: 4,
        more: true,
        events: [ev({ seq: 4, kind: "user_message", text: "now" })],
      }),
    )
    expect(view.hasMore).toBe(true)
    expect(view.oldestSeq).toBe(4)
    view = prependOlder(
      view,
      [ev({ seq: 2, kind: "user_message", text: "old" }), ev({ seq: 4, kind: "user_message", text: "dup" })],
      false,
    )
    expect(view.hasMore).toBe(false)
    expect(view.oldestSeq).toBe(2)
    expect(view.blocks.map((b) => b.text)).toEqual(["old", "now"])
    view = prependOlder(view, [ev({ seq: 2, kind: "user_message", text: "old" })], true)
    expect(view.hasMore).toBe(false)
    view = { ...view, hasMore: true, oldestSeq: 8 }
    view = prependOlder(view, [], true, 3)
    expect(view.hasMore).toBe(true)
    expect(view.oldestSeq).toBe(3)
    view = applyPush(view, ready({ seq: 8, status: { running: false } }))
    expect(view.hasMore).toBe(true)
    expect(view.oldestSeq).toBe(2)
  })

  it("does not walk a log cursor forward on a later empty ready", () => {
    let view = applyPush(openView(detail()), ready({ seq: 4, more: true, events: [] }))
    expect(view.oldestSeq).toBe(5)
    view = prependOlder(view, [], true, 3)
    expect(view.oldestSeq).toBe(3)
    view = applyPush(view, ready({ seq: 8, more: true, events: [] }))
    expect(view.hasMore).toBe(true)
    expect(view.oldestSeq).toBe(3)
  })

  it("keeps a log cursor when ready has more but no last-turn rows", () => {
    const view = applyPush(openView(detail()), ready({ seq: 4, more: true, events: [] }))
    expect(view.caughtUp).toBe(true)
    expect(view.hasMore).toBe(true)
    expect(view.oldestSeq).toBe(5)
    expect(view.blocks).toEqual([])
  })

  it("keeps a live streaming answer when older rows are prepended", () => {
    let view = applyPush(openView(detail()), ready({ seq: 0 }))
    view = applyPush(view, push(ev({ seq: 3, kind: "user_message", text: "q" })))
    view = applyPush(view, push(ev({ seq: 0, kind: "delta", text: "partial" })))
    view = prependOlder(view, [ev({ seq: 1, kind: "user_message", text: "prev" })], true)
    expect(view.blocks.map((b) => b.text)).toEqual(["prev", "q", "partial"])
    expect(view.blocks[2]?.streaming).toBe(true)
  })

  it("drops another thread's older page", () => {
    let view = applyPush(
      openView(detail()),
      ready({ seq: 4, events: [ev({ seq: 4, kind: "user_message", text: "now" })] }),
    )
    view = prependOlder(
      view,
      [ev({ seq: 1, kind: "user_message", text: "nope", thread_id: "other" })],
      true,
      1,
    )
    expect(view.blocks.map((b) => b.text)).toEqual(["now"])
    expect(view.hasMore).toBe(true)
    expect(view.oldestSeq).toBe(1)
  })
})

it.each([true, false])("retains a generation receipt through live/history overlap and paging (history first: %s)", (historyFirst) => {
  const thought = ev({ seq: 2, kind: "reasoning", agent_id: "worker", message_id: "old", text: "unfinished" })
  const stop = ev({ seq: 3, kind: "generation_interrupted", agent_id: "worker", message_id: "old", text: JSON.stringify({ reason: "engine_restart", recovery: "pending" }) })
  const resume = ev({ seq: 4, kind: "generation_recovered", agent_id: "worker", message_id: "old", text: JSON.stringify({ reason: "engine_restart", recovery: "recovered" }) })
  let view = applyPush(openView(detail()), ready({ seq: historyFirst ? 4 : 1, more: true, events: historyFirst ? [thought, stop, resume] : [ev({ seq: 1, kind: "spawned", agent_id: "worker" })], status: { running: true } }))
  for (const row of [thought, stop, resume]) view = applyPush(view, push(row))
  view = applyPush(view, push(ev({ seq: 0, kind: "reasoning_delta", agent_id: "worker", message_id: "new", text: "new output" })))
  view = prependOlder(view, [ev({ seq: 1, kind: "spawned", agent_id: "worker" })], false)
  expect(view.blocks.filter((b) => b.kind === "reasoning").map((b) => [b.messageId, Boolean(b.streaming)])).toEqual([["old", false], ["new", true]])
  expect(view.blocks.filter((b) => b.generation)).toHaveLength(1)
  expect(view.blocks.find((b) => b.generation)?.generation?.recovery).toBe("recovered")
  expect(phoneAgents(view.blocks)[0].status).toBe("running")
})

it("applies an unseen lower-sequence interruption once after newer work and never reopens it while paging", () => {
  let view = applyPush(openView(detail()), ready({ seq: 5, events: [ev({ seq: 5, kind: "spawned", agent_id: "worker" })], status: { running: true } }))
  view = applyPush(view, push(ev({ seq: 0, kind: "reasoning_delta", agent_id: "worker", message_id: "old", text: "unfinished" })))
  const stop = ev({ seq: 3, kind: "generation_interrupted", agent_id: "worker", message_id: "old", text: JSON.stringify({ reason: "unknown", recovery: "pending" }) })
  view = applyPush(view, push(stop))
  view = applyPush(view, push(stop))
  view = prependOlder(view, [ev({ seq: 1, kind: "user_message", text: "request" })], false)
  expect(view.blocks.find((b) => b.messageId === "old" && b.kind === "reasoning")?.streaming).toBe(false)
  expect(view.blocks.filter((b) => b.generation)).toHaveLength(1)
})

it("orders an unseen lower-sequence finish before the replacement spawn and live output", () => {
  let view = applyPush(openView(detail()), ready({ seq: 8, events: [ev({ seq: 8, kind: "spawned", agent_id: "worker" })], status: { running: true } }))
  view = applyPush(view, push(ev({ seq: 0, kind: "reasoning_delta", agent_id: "worker", message_id: "replacement", text: "running" })))
  view = applyPush(view, push(ev({ seq: 7, kind: "finished", agent_id: "worker", text: "older finished attempt" })))
  expect(phoneAgents(view.blocks)[0].status).toBe("running")
  expect(view.blocks.find((b) => b.messageId === "replacement")?.streaming).toBe(true)
})

it("keeps the current turn running when an older unseen terminal arrives with its generation history", () => {
  let view = applyPush(openView(detail()), ready({ seq: 8, events: [ev({ seq: 8, kind: "turn", agent_id: "manager", turn_id: "new-turn" })], status: { running: true, turn_id: "new-turn" } }))
  view = applyPush(view, push(ev({ seq: 7, kind: "done", agent_id: "manager", turn_id: "old-turn" })))
  expect(view.detail?.running?.turn_id).toBe("new-turn")
  expect(view.events.filter((row) => row.seq === 7)).toHaveLength(1)
})

it("shows workers that are still running outside the first page without moving Earlier", () => {
  const secret = "launch instruction stays off the phone"
  let view = applyPush(openView(detail()), ready({
    seq: 30,
    more: true,
    history_before: 20,
    events: [
      ev({ seq: 28, kind: "agent_message", text: "latest report" }),
      ev({ seq: 29, kind: "done", created_at: "2026-11-24T02:57:46Z" }),
    ],
    roster: [
      ev({ seq: 4, kind: "spawned", agent_id: "w1", role: "scanner", text: secret }),
      ev({ seq: 5, kind: "spawned", agent_id: "w2", role: "reader", text: secret }),
    ],
  }))
  expect(view.oldestSeq).toBe(20)
  expect(view.lastSeq).toBe(30)
  expect(view.hasMore).toBe(true)
  expect(view.events.map((row) => row.seq)).toEqual([28, 29])
  expect(managerBlocks(view.blocks).map((block) => block.text)).toEqual([
    "latest report",
    "2026-11-24T02:57:46Z",
  ])
  expect(view.blocks.some((block) => block.text.includes(secret))).toBe(false)
  expect(phoneAgents(view.blocks)).toMatchObject([
    { id: "w1", role: "scanner", status: "running" },
    { id: "w2", role: "reader", status: "running" },
  ])

  const cleared = applyPush(view, ready({
    seq: 30, more: true, history_before: 20, roster: [],
    events: [
      ev({ seq: 28, kind: "agent_message", text: "latest report" }),
      ev({ seq: 29, kind: "done", created_at: "2026-11-24T02:57:46Z" }),
    ],
  }))
  expect(phoneAgents(cleared.blocks)).toEqual([])
  expect(cleared.oldestSeq).toBe(20)

  view = applyPush(view, push(ev({ seq: 4, kind: "spawned", agent_id: "w1", role: "scanner", text: secret })))
  expect(view.oldestSeq).toBe(20)
  expect(view.events.some((row) => row.seq === 4)).toBe(false)

  view = applyPush(view, push(ev({ seq: 31, kind: "tool_call", agent_id: "w1", tool_call_id: "c1", text: "read({})" })))
  expect(phoneAgents(view.blocks).find((agent) => agent.id === "w1")?.blocks.some((block) => block.kind === "tool")).toBe(true)
  expect(managerBlocks(view.blocks).some((block) => block.kind === "tool")).toBe(false)
  expect(view.oldestSeq).toBe(20)

  view = applyPush(view, ready({ seq: 31, more: true, history_before: 20 }))
  expect(phoneAgents(view.blocks).map((agent) => agent.status)).toEqual(["running", "running"])

  view = applyPush(view, push(ev({ seq: 32, kind: "finished", agent_id: "w1" })))
  expect(phoneAgents(view.blocks)).toMatchObject([
    { id: "w1", status: "done" },
    { id: "w2", status: "running" },
  ])
  expect(view.oldestSeq).toBe(20)
})

it("keeps a timed-out worker failed without moving Earlier or reviving it", () => {
  const timeout = "swarm: agent w1 exceeded timeout 1s (1 consecutive watchdog failures): context deadline exceeded"
  let view = applyPush(openView(detail()), ready({
    seq: 30, more: true, history_before: 20,
    events: [ev({ seq: 28, kind: "agent_message", text: "latest report" })],
    roster: [
      ev({ seq: 4, kind: "spawned", agent_id: "w1", role: "scanner" }),
      ev({ seq: 5, kind: "spawned", agent_id: "w2", role: "reader" }),
    ],
  }))
  view = applyPush(view, push(ev({ seq: 31, kind: "finished", agent_id: "w1", err: timeout })))
  expect(phoneAgents(view.blocks)).toMatchObject([
    { id: "w1", status: "failed" },
    { id: "w2", status: "running" },
  ])
  expect(view.oldestSeq).toBe(20)
  expect(view.events.map((row) => row.seq)).toEqual([28, 31])

  view = applyPush(view, push(ev({ seq: 32, kind: "error", agent_id: "manager", text: "scheduled check failed" })))
  expect(phoneAgents(view.blocks).map((agent) => agent.status)).toEqual(["failed", "running"])
  expect(view.oldestSeq).toBe(20)

  view = applyPush(view, ready({
    seq: 30, more: true, history_before: 20,
    events: [ev({ seq: 28, kind: "agent_message", text: "latest report" })],
    roster: [
      ev({ seq: 4, kind: "spawned", agent_id: "w1", role: "scanner" }),
      ev({ seq: 5, kind: "spawned", agent_id: "w2", role: "reader" }),
    ],
  }))
  expect(phoneAgents(view.blocks).map((agent) => agent.status)).toEqual(["failed", "running"])
  expect(view.oldestSeq).toBe(20)
  expect(view.roster.map((row) => row.seq)).toEqual([4, 5])

  view = applyPush(view, ready({
    seq: 32, more: true, history_before: 20,
    events: [
      ev({ seq: 28, kind: "agent_message", text: "latest report" }),
      ev({ seq: 31, kind: "finished", agent_id: "w1", err: timeout }),
    ],
    roster: [ev({ seq: 5, kind: "spawned", agent_id: "w2", role: "reader" })],
  }))
  expect(phoneAgents(view.blocks).find((agent) => agent.id === "w1")?.status).toBe("failed")
  expect(phoneAgents(view.blocks).find((agent) => agent.id === "w2")?.status).toBe("running")
  expect(view.roster.map((row) => row.seq)).toEqual([5])
  expect(view.oldestSeq).toBe(20)
})

it("drops a rewound running worker and does not duplicate it when Earlier arrives", () => {
  let view = applyPush(openView(detail()), ready({
    seq: 30, more: true, history_before: 20,
    events: [ev({ seq: 28, kind: "agent_message", text: "latest report" })],
    roster: [
      ev({ seq: 4, kind: "spawned", agent_id: "w1", role: "scanner", text: "secret instruction" }),
      ev({ seq: 8, kind: "spawned", agent_id: "w2", role: "reader" }),
    ],
  }))
  view = applyPush(view, push(ev({ seq: 0, kind: "rewound", text: "6" })))
  expect(phoneAgents(view.blocks)).toMatchObject([{ id: "w1", role: "scanner", status: "running" }])
  expect(view.blocks.some((block) => block.text.includes("secret instruction"))).toBe(false)

  view = prependOlder(view, [
    ev({ seq: 4, kind: "spawned", agent_id: "w1", role: "scanner", text: "secret instruction" }),
  ], true, 4)
  expect(view.oldestSeq).toBe(4)
  expect(view.roster).toEqual([])
  expect(phoneAgents(view.blocks).find((agent) => agent.id === "w1")?.blocks.filter((block) => block.kind === "spawn")).toHaveLength(1)
  expect(view.blocks.some((block) => block.text.includes("secret instruction"))).toBe(false)
})
