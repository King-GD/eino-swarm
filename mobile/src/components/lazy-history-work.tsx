import { useEffect, useRef, useState, type ReactNode } from "react"
import { ChevronRight } from "lucide-react"

import { cn } from "@/lib/cn"
import { linkError, remoteError } from "@/lib/client"
import { t } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { OpLog, type HistoryWork } from "@/lib/rpc"
import { dropPackedJson } from "@/lib/tool-preview"
import { applyEvent, isPhoneOmitted, type CompactBlock } from "@/lib/transcript"

export function LazyHistoryWork({ owner, range, agentId, renderTool }: {
  owner: { link: RemoteLink; threadId: string }
  range: HistoryWork
  agentId: string
  renderTool: (block: CompactBlock) => ReactNode
}) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [page, setPage] = useState<{ cursor: number; more: boolean; blocks: CompactBlock[] }>()
  const reading = useRef<object | null>(null)
  const { link, threadId } = owner
  useEffect(() => {
    setPage(undefined)
    setError("")
    setBusy(false)
    setOpen(false)
    return () => { reading.current = null }
  }, [link, threadId, range.from, range.through, agentId])

  const read = async () => {
    if (reading.current) return
    const request = {}
    reading.current = request
    setBusy(true)
    setError("")
    const cursor = page?.cursor ?? range.from - 1
    try {
      const result = await link.rpc({
        op: OpLog, thread_id: threadId, since: cursor, work_from: range.from, work_through: range.through,
      })
      if (reading.current !== request) return
      if (!result.ok) {
        setError(remoteError(result.error || result.code || ""))
        return
      }
      const next = result.seq ?? 0
      const events = result.events ?? []
      if (result.thread_id !== threadId || next < cursor || next > range.through ||
        (result.more && next <= cursor) || events.some((event, index) =>
          event.thread_id !== threadId || event.seq <= (events[index - 1]?.seq ?? cursor) || event.seq > next)) {
        setError(t("err.open"))
        return
      }
      const owned = events.filter((event) => (event.agent_id || "manager") === agentId)
      setPage({ cursor: next, more: Boolean(result.more), blocks: owned.reduce(applyEvent, page?.blocks ?? []) })
    } catch (cause) {
      if (reading.current === request) setError(linkError(cause))
    } finally {
      if (reading.current === request) {
        reading.current = null
        setBusy(false)
      }
    }
  }

  return (
    <div className="min-w-0 max-w-full">
      <button type="button" aria-expanded={open}
        className="flex min-h-8 items-center gap-1 text-left text-xs text-muted-foreground"
        onClick={() => {
          setOpen(!open)
          if (!open && !page) void read()
        }}>
        <ChevronRight aria-hidden className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")} />
        {t("thread.loadWork")}
      </button>
      {open ? (
        <div className="flex min-w-0 flex-col gap-1 pl-4" aria-busy={busy}>
          {page?.blocks.map((block) => {
            if (isPhoneOmitted(block)) return null
            if (block.kind === "reasoning") {
              const text = dropPackedJson(block.text)
              return text ? <p key={block.id} data-quote-text="" className="whitespace-pre-wrap break-words text-xs text-muted-foreground">{text}</p> : null
            }
            return block.kind === "tool" ? <div key={block.id} className="min-w-0">{renderTool(block)}</div> : null
          })}
          {busy ? <span role="status" className="text-xs text-muted-foreground">{t("thread.loading")}</span> : null}
          {error ? <span role="alert" className="text-xs text-destructive">{error}</span> : null}
          {!busy && (error || page?.more) ? <button type="button" className="min-h-8 self-start text-xs text-primary"
            onClick={() => void read()}>{t(error ? "account.retry" : "thread.moreWork")}</button> : null}
        </div>
      ) : null}
    </div>
  )
}
