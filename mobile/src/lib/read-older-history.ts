import { remoteError } from "./client"
import type { RemoteLink } from "./link"
import { hasPhoneTurnStart } from "./phone-history"
import { OpLog } from "./rpc"
import { prependOlder, type PhoneView } from "./session"
import { dropPackedJson } from "./tool-preview"

export async function readOlderHistory({ link, threadId, turnId, stillHere, current, commit, setError }: {
  link: RemoteLink
  threadId: string
  turnId?: string
  stillHere: () => boolean
  current: () => PhoneView
  commit: (state: PhoneView) => void
  setError: (error: string) => void
}): Promise<number | undefined> {
  while (stillHere() && current().hasMore) {
    const before = current().oldestSeq || current().lastSeq
    const reply = await link.rpc({ op: OpLog, thread_id: threadId, before, compact_history: true })
    if (!stillHere()) return
    if (!reply.ok) {
      setError(remoteError(reply.error || reply.code || ""))
      return
    }
    const latest = current()
    const cursor = latest.oldestSeq || latest.lastSeq
    if (!latest.hasMore) return
    // Automatic opening history may already have applied this page.
    if (before > 0 && cursor > 0 && cursor < before) {
      if (turnId) continue
      return
    }
    const rows = reply.events ?? []
    const next = prependOlder(latest, rows, Boolean(reply.more), reply.seq, reply.history_work)
    commit(next)
    if (!turnId) {
      const answers = rows.filter((event) =>
        event.kind === "agent_message" && (!event.agent_id || event.agent_id === "manager") && dropPackedJson(event.text),
      )
      const focus = answers.find((event) => event.seq === reply.focus_seq)?.seq ?? answers.at(-1)?.seq
      if (focus) return focus
    } else if (hasPhoneTurnStart(next.blocks, turnId, next.hasMore)) {
      return
    }
    if (!next.oldestSeq || (before > 0 && next.oldestSeq >= before)) return
  }
}
