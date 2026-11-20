import type { HistoryWork, RemoteEvent, RemoteRequest, TurnView } from "./rpc"
import type { Step } from "./mock-turn"

export function compactHistoryFixture(threadID: string): boolean {
  return threadID === "t-live" && new URLSearchParams(location.search).get("history") === "compact"
}

function compactTurns(): TurnView[] {
  const ordinals = ["First", "Second", "Third"]
  if (new URLSearchParams(location.search).has("many-turns")) ordinals.push("Fourth", "Fifth", "Sixth", "Seventh")
  return ordinals.map((ordinal, index) => ({
    id: `compact-${index + 1}`, status: "done", text: `Response ${index + 1}`, user_text: `${ordinal} prompt`,
  }))
}

export function compactTurnSummaries(): TurnView[] {
  const turns = compactTurns()
  if (!new URLSearchParams(location.search).has("legacy-turn-previews")) return turns
  return turns.map(({ id, status, text }) => ({ id, status, text }))
}

export function compactHistorySteps(): Step[] {
  return compactTurns().flatMap((turn, index) => [
    { kind: "user_message", turnID: turn.id, text: turn.user_text },
    ...Array.from({ length: 90 }, (_, i) => ({
      kind: "reasoning", turnID: turn.id, text: `Deferred thought ${index + 1}.${i + 1}`,
    })),
    { kind: "tool_call", turnID: turn.id, callID: turn.id, text: "inspect({})" },
    { kind: "tool_result", turnID: turn.id, callID: turn.id, text: `Deferred result ${index + 1}` },
    { kind: "agent_message", turnID: turn.id, text: `Response ${index + 1}\n\n${"A paragraph with enough text for the transcript to scroll.\n\n".repeat(22)}` },
    { kind: "done", turnID: turn.id },
  ])
}

export function mockCompactPage(events: RemoteEvent[], req: Partial<RemoteRequest> = {}) {
  if (req.work_from && req.work_through) {
    const work = events.filter((event) => event.seq >= req.work_from! && event.seq <= req.work_through! &&
      event.seq > (req.since ?? 0))
    return { events: work, seq: work.at(-1)?.seq ?? req.since }
  }
  const remaining = events.filter((event) => !req.before || event.seq < req.before)
  const turnId = remaining.at(-1)?.turn_id
  const turn = remaining.filter((event) => event.turn_id === turnId)
  const answer = turn.find((event) => event.kind === "agent_message")
  if (turnId === "compact-5" && answer && new URLSearchParams(location.search).has("split-history")) {
    return { events: turn.filter((event) => event.seq >= answer.seq), seq: answer.seq, focus_seq: answer.seq, more: true }
  }
  const work = turn.filter((event) => event.kind === "reasoning" || event.kind.startsWith("tool_"))
  const historyWork: HistoryWork[] = work.length ? [{ from: work[0].seq, through: work.at(-1)!.seq }] : []
  return {
    events: turn.filter((event) => !work.includes(event)),
    history_work: historyWork,
    seq: turn[0]?.seq,
    focus_seq: turn.find((event) => event.kind === "agent_message")?.seq,
    more: remaining.length > turn.length,
  }
}
