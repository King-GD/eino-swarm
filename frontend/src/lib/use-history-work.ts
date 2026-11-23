import { useEffect, useState } from "react"

import { api } from "./api"
import type { HistoryWork } from "./thread-log"
import { uniqueStoredEvents } from "./thread-log"
import { emptyTranscript, MANAGER_ID, reduceEvent, type Block } from "./transcript"
import { closePendingTools } from "./transcript-worker-ownership"
import type { SwarmEvent } from "./types"

type WorkDetail = { blocks: Block[] } | { error: true }

async function readWork(threadId: string, bounds: [number, number][], signal: AbortSignal): Promise<Block[]> {
  const events: SwarmEvent[] = []
  for (const [from, through] of bounds) {
    let cursor = from - 1
    for (;;) {
      const page = await api.threadHistoryWork(threadId, from, through, cursor, signal)
      events.push(...page.events)
      if (!page.has_more) break
      if (page.seq <= cursor || page.seq >= through) throw new Error("History work cursor did not advance")
      cursor = page.seq
    }
  }
  const agent = uniqueStoredEvents(events).reduce((state, ev) => reduceEvent(state, ev), emptyTranscript()).agents[MANAGER_ID]
  if (!agent) return []
  closePendingTools(agent)
  return agent.blocks
}

export function useHistoryWork(threadId: string | undefined, ranges: HistoryWork[], open: boolean) {
  const [attempt, setAttempt] = useState(0)
  const [result, setResult] = useState<{ identity: string; detail: WorkDetail }>()
  const key = ranges.length > 0 ? JSON.stringify(ranges.map(({ from, through }) => [from, through])) : ""
  const identity = `${threadId}:${key}:${attempt}`
  const current = result?.identity === identity ? result.detail : undefined

  useEffect(() => {
    if (!open || !threadId || !key || current) return
    const controller = new AbortController()
    const finish = (detail: WorkDetail) => {
      if (!controller.signal.aborted) setResult({ identity, detail })
    }
    const bounds: [number, number][] = JSON.parse(key)
    void readWork(threadId, bounds, controller.signal).then(
      (blocks) => finish({ blocks }),
      () => finish({ error: true }),
    )
    return () => controller.abort()
  }, [current, identity, key, open, threadId])

  return { detail: current, retry: () => setAttempt((value) => value + 1) }
}
