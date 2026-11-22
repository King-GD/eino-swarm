import { expect, it, vi } from "vitest"
import { setLocale } from "./i18n"
import { OpLog, type RemoteEvent, type RemoteResponse } from "./rpc"
import { readOlderTurnPreviews } from "./turn-previews"

const event = (seq: number, turn: string, text: string, extra: Partial<RemoteEvent> = {}): RemoteEvent => ({
  seq, thread_id: "thread", turn_id: turn, text, kind: "user_message", created_at: "", ...extra,
})
const reply = (seq: number, events: RemoteEvent[] = [], more = true): RemoteResponse => ({
  v: 1, id: "", ok: true, thread_id: "thread", seq, events, more,
})

it("recovers only the conversation's human input previews from older pages", async () => {
  setLocale("en")
  const rpc = vi.fn()
    .mockResolvedValueOnce(reply(12, [
      event(15, "picture", "Worker text", { agent_id: "worker" }),
      event(14, "picture", "Another thread's prompt", { thread_id: "other" }),
      event(13, "picture", "Prior reply", { kind: "agent_message" }),
      event(12, "picture", "", { agent_id: "manager", has_images: true }),
    ]))
    .mockResolvedValueOnce(reply(4, [event(4, "older", "Real\n input")]))
  const signal = new AbortController().signal
  const publish = vi.fn()
  await readOlderTurnPreviews({ rpc }, "thread", 21, new Set(["picture", "older"]), signal, publish)
  expect(rpc.mock.calls).toEqual([
    [{ op: OpLog, thread_id: "thread", before: 21, compact_history: true }, { signal, dropOnTimeout: false }],
    [{ op: OpLog, thread_id: "thread", before: 12, compact_history: true }, { signal, dropOnTimeout: false }],
  ])
  expect(publish.mock.calls).toEqual([[new Map([["picture", "Image"]])], [new Map([["older", "Real input"]])]])
})

it("stops at a non-advancing cursor and withholds automated scheduling text", async () => {
  const rpc = vi.fn().mockResolvedValue(reply(10, [
    event(9, "schedule", "This turn is a scheduled check. Internal instructions"),
  ]))
  const publish = vi.fn()
  await readOlderTurnPreviews({ rpc }, "thread", 10, new Set(["schedule"]), new AbortController().signal, publish)
  expect(rpc).toHaveBeenCalledOnce()
  expect(publish).not.toHaveBeenCalled()
})

it("bounds a history scan and leaves missing previews unset", async () => {
  const rpc = vi.fn(({ before = 0 }: { before?: number }) => Promise.resolve(reply(before - 1)))
  const publish = vi.fn()
  await readOlderTurnPreviews({ rpc }, "thread", 100, new Set(["missing"]), new AbortController().signal, publish)
  expect(rpc).toHaveBeenCalledTimes(8)
  expect(publish).not.toHaveBeenCalled()
})

it("cannot publish labels from an earlier visit once it is cancelled", async () => {
  const owner = new AbortController()
  const rpc = vi.fn(async () => {
    owner.abort()
    return reply(4, [event(4, "older", "Late preview")])
  })
  const publish = vi.fn()
  await readOlderTurnPreviews({ rpc }, "thread", 10, new Set(["older"]), owner.signal, publish)
  expect(publish).not.toHaveBeenCalled()
})
