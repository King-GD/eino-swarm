import { useEffect, useState } from "react"
import { t } from "./i18n"
import type { RemoteLink } from "./link"
import { OpLog } from "./rpc"
import { dropPackedJson } from "./tool-preview"

const maxPreviewPages = 8

export function pairedPromptPreview(text: string, hasImages?: boolean): string {
  if (text.startsWith("This turn is a scheduled check.")) return ""
  return dropPackedJson(text).trim().replace(/\s+/g, " ").slice(0, 66) || (hasImages ? t("thread.image") : "")
}

export function useOlderTurnPreviews(link: RemoteLink | undefined, threadId: string, before: number | undefined,
  turns: { id: string; label: string }[]) {
  const missing = JSON.stringify(turns.filter((turn) => !turn.label).map((turn) => turn.id))
  const [state, setState] = useState<{ link: RemoteLink; threadId: string; labels: Map<string, string> }>()
  useEffect(() => {
    if (!link || !before || missing === "[]") return
    const owner = new AbortController()
    const targets = new Set<string>(JSON.parse(missing))
    const publish = (labels: Map<string, string>) => {
      setState((previous) => ({
        link, threadId, labels: new Map([
          ...(previous?.link === link && previous.threadId === threadId ? previous.labels : []), ...labels,
        ]),
      }))
    }
    void readOlderTurnPreviews(link, threadId, before, targets, owner.signal, publish).catch(() => {
      // These optional labels have a local fallback. A failed preview read
      // must leave the transcript and the ordinary Earlier action usable.
    })
    return () => owner.abort()
  }, [link, threadId, before, missing])
  return state?.link === link && state?.threadId === threadId ? state.labels : undefined
}

export async function readOlderTurnPreviews(link: Pick<RemoteLink, "rpc">, threadId: string, before: number,
  targets: Set<string>, signal: AbortSignal, publish: (labels: Map<string, string>) => void) {
  for (let page = 0; page < maxPreviewPages && targets.size && !signal.aborted; page++) {
    const reply = await link.rpc({ op: OpLog, thread_id: threadId, before, compact_history: true }, { signal, dropOnTimeout: false })
    if (signal.aborted || !reply.ok || (reply.thread_id && reply.thread_id !== threadId)) return
    const labels = new Map<string, string>()
    for (const event of reply.events ?? []) {
      const id = event.turn_id
      if (!id || !targets.has(id) || event.thread_id !== threadId || event.seq >= before ||
        event.kind !== "user_message" || (event.agent_id && event.agent_id !== "manager")) continue
      const label = pairedPromptPreview(event.text, event.has_images)
      if (label) {
        targets.delete(id)
        labels.set(id, label)
      }
    }
    if (labels.size) publish(labels)
    if (!reply.more || !reply.seq || reply.seq >= before) return
    before = reply.seq
  }
}
