import { Clock, MoreHorizontal, Pencil, Pin, PinOff, Trash2 } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { SidebarKindSlot, sidebarRowClass } from "@/components/app/sidebar-slots"
import { StatusDot } from "@/components/app/transcript"
import { AskMark } from "@/components/app/ask-mark"
import { WaitMark } from "@/components/app/wait-mark"
import { UnreadMark } from "@/components/app/unread-mark"
import { chromeTypeClass } from "@/lib/chrome-type"
import { useSortableList } from "@/lib/sortable"
import type { ScheduleSource, Thread } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { cn } from "@/lib/utils"
import { useApp } from "@/store/app"

type DragBind = ReturnType<ReturnType<typeof useSortableList>["bind"]>

/** One conversation in the sidebar: Recents, a project folder, or Pinned.
 *  The leading size-4 slot is the folder column — empty, a progress dot
 *  while a turn is live, a ping question mark while ask_user is blocked,
 *  or a clock while a wait is parked — so titles line up with the
 *  project name. */
export function SidebarThreadRow({
  thread,
  active,
  running,
  waiting,
  asking,
  drag,
  onOpen,
  onRename,
  onDelete,
  onPin,
}: {
  thread: Thread
  active: boolean
  running: boolean
  waiting?: boolean
  asking?: boolean
  drag?: DragBind
  onOpen: (id: string) => void
  onRename: (id: string, title: string) => void
  onDelete: (id: string) => void
  onPin?: (id: string, pinned: boolean) => void
}) {
  const t = useT()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(thread.title)
  const source = thread.schedule_source
  const canPin = Boolean(onPin && thread.project_id)

  if (editing) {
    const commit = () => {
      const title = draft.trim()
      if (title && title !== thread.title) onRename(thread.id, title)
      setEditing(false)
    }
    const field = (
      <Input
        autoFocus
        value={draft}
        className={cn("h-[28px]", chromeTypeClass)}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit()
          if (e.key === "Escape") {
            setDraft(thread.title)
            setEditing(false)
          }
        }}
      />
    )
    return (
      <div className={sidebarRowClass}>
        <SidebarKindSlot />
        <div className="min-w-0 flex-1">{field}</div>
      </div>
    )
  }

  return (
    <div
      {...drag}
      data-testid="thread-row"
      data-id={thread.id}
      aria-current={active ? "true" : undefined}
      className={cn(
        sidebarRowClass,
        source && "h-auto! flex-wrap gap-y-0",
        drag && "data-[dragging=true]:cursor-grabbing",
        "data-[dragging=true]:opacity-60 data-[over=true]:bg-sidebar-accent",
        active
          ? "bg-sidebar-accent text-foreground"
          : "text-sidebar-foreground hover:bg-sidebar-accent/60",
      )}
    >
      <button
        type="button"
        onClick={() => onOpen(thread.id)}
        className={cn(
          "flex min-w-0 flex-1 items-center gap-1 text-left",
          source && "min-h-(--sidebar-row-height)",
        )}
      >
        <SidebarKindSlot>
          {asking ? (
            <AskMark />
          ) : running ? (
            <StatusDot status="running" />
          ) : waiting ? (
            <WaitMark />
          ) : thread.unread ? (
            <UnreadMark />
          ) : source ? (
            <Clock aria-hidden className="size-3.5 text-muted-foreground" />
          ) : null}
        </SidebarKindSlot>
        <span data-testid="row-label" className="truncate">
          {thread.title || t("sidebar.untitled")}
        </span>
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-xs"
            data-no-drag
            className="opacity-0 transition-opacity group-hover:opacity-100 data-[state=open]:opacity-100"
            title={t("sidebar.more")}
            aria-label={t("sidebar.more")}
          >
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {canPin ? (
            <DropdownMenuItem
              onSelect={() => onPin?.(thread.id, !thread.pinned)}
            >
              {thread.pinned ? <PinOff /> : <Pin />}
              {thread.pinned ? t("sidebar.unpin") : t("sidebar.pin")}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem
            onSelect={() => {
              setDraft(thread.title)
              setEditing(true)
            }}
          >
            <Pencil />
            {t("sidebar.rename")}
          </DropdownMenuItem>
          <DropdownMenuItem destructive onSelect={() => onDelete(thread.id)}>
            <Trash2 />
            {t("sidebar.delete")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {source ? <SidebarScheduleSource source={source} onOpen={onOpen} /> : null}
    </div>
  )
}

function SidebarScheduleSource({
  source,
  onOpen,
}: {
  source: ScheduleSource
  onOpen: (id: string) => void
}) {
  const t = useT()
  const openSchedule = useApp((s) => s.openScheduleDetails)
  const origin = source.origin_thread_id
  const originLabel = t("schedule.sourceConversation", { title: source.origin_title || origin || "" })
  const taskLabel = t("schedule.resultSource", { id: source.schedule_id })
  const linkClass = "h-auto max-w-full justify-start px-0 py-0.5 text-xs"

  return (
    <div className="flex min-w-0 basis-full gap-1 pb-1">
      <SidebarKindSlot />
      <div className="flex min-w-0 flex-col items-start">
        <Button type="button" variant="link" data-no-drag title={taskLabel}
          className={linkClass} onClick={() => void openSchedule(source.schedule_id)}>
          <span className="break-all whitespace-normal text-left">{taskLabel}</span>
        </Button>
        {origin && (source.origin_available ? (
          <Button type="button" variant="link" data-no-drag title={originLabel}
            className={linkClass} onClick={() => onOpen(origin)}>
            <span className="truncate">{originLabel}</span>
          </Button>
        ) : (
          <span className="truncate text-xs text-muted-foreground" title={originLabel}>
            {originLabel}
          </span>
        ))}
      </div>
    </div>
  )
}
