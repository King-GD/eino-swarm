import type { ReactNode } from "react"
import { CalendarClock, FolderKanban, MessageSquare, Settings } from "lucide-react"

import { ChromeMenu } from "@/components/app/chrome-menu"
import { DesktopAccountMenu } from "@/components/app/desktop-account-menu"
import { Button } from "@/components/ui/button"
import {
  scheduledReturnDest,
  setDeskDest,
  useDeskDest,
  visibleDest,
  type DeskDest,
} from "@/lib/desk-nav"
import { useT } from "@/lib/use-t"
import { cn } from "@/lib/utils"
import { useApp } from "@/store/app"
import { useScheduleInboxUI } from "@/store/schedule-inbox-ui"

/** Rail clicks own the inbox flag. The task column only renders the
 *  editor; it must not also be the thing that opens the list. */
export function pickDest(next: DeskDest) {
  if (next === "scheduled") {
    if (!useApp.getState().scheduleInboxOpen) useScheduleInboxUI.getState().reset()
    setDeskDest("scheduled")
    useApp.getState().openScheduleInbox()
    return
  }
  setDeskDest(next)
  useApp.getState().closeScheduleInbox()
}

export function leaveScheduledPage() {
  setDeskDest(scheduledReturnDest())
  useApp.getState().closeScheduleInbox()
}

export function useVisibleDest(): DeskDest {
  const dest = useDeskDest()
  const inbox = useApp((s) => s.scheduleInboxOpen)
  return visibleDest(inbox, dest)
}

function RailButton({
  current,
  label,
  testId,
  onClick,
  children,
}: {
  current: boolean
  label: string
  testId?: string
  onClick: () => void
  children: ReactNode
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      data-testid={testId}
      aria-current={current ? "page" : undefined}
      aria-label={label}
      title={label}
      onClick={onClick}
      className={cn(
        "relative text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground",
        current && "bg-sidebar-accent",
      )}
      style={{
        width: "var(--sidebar-row-height)",
        height: "var(--sidebar-row-height)",
      }}
    >
      {children}
      <span className="wb-nav-label" aria-hidden="true">{label}</span>
    </Button>
  )
}

/** Leftmost destination rail for projects, conversations and waits. The rail stays
 *  when the list column is hidden; `edge` is the hairline against that
 *  column, and the aside draws the outer one when the column is gone.
 *  Unread waits stay on the list rows. A count on the icon covered the
 *  next destination. */
export function DestRail({
  edge = true,
  onSettings,
  onToggleTheme,
  onToggleLocale,
}: {
  edge?: boolean
  onSettings: () => void
  onToggleTheme: () => void
  onToggleLocale: () => void
}) {
  const t = useT()
  const pane = useVisibleDest()

  return (
    <nav
      data-testid="dest-rail"
      data-expanded={edge}
      aria-label={t("nav.destinations")}
      className="wb-rail flex h-full shrink-0 flex-col items-center gap-1 py-2"
      style={{
        width: "var(--dest-rail-width)",
        paddingInline: "var(--dest-rail-pad)",
      }}
    >
      <RailButton
        current={pane === "projects"}
        label={t("nav.projects")}
        testId="dest-projects"
        onClick={() => pickDest("projects")}
      >
        <FolderKanban />
      </RailButton>
      <RailButton
        current={pane === "chats"}
        label={t("nav.chats")}
        testId="dest-chats"
        onClick={() => pickDest("chats")}
      >
        <MessageSquare />
      </RailButton>
      <RailButton
        current={pane === "scheduled"}
        label={t("sidebar.scheduled")}
        testId="schedule-inbox"
        onClick={() => pickDest("scheduled")}
      >
        <CalendarClock />
      </RailButton>
      <div className="wb-nav-tools mt-auto flex flex-col items-center gap-1 pb-1">
        <ChromeMenu onToggleTheme={onToggleTheme} onToggleLocale={onToggleLocale} />
        <RailButton
          current={false}
          label={t("sidebar.settings")}
          onClick={onSettings}
        >
          <Settings />
        </RailButton>
        <DesktopAccountMenu />
      </div>
    </nav>
  )
}
