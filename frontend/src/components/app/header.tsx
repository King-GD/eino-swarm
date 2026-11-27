import { Folder, MessageSquare, PanelLeft, PanelRight, SquareTerminal, WifiOff } from "lucide-react"
import { useEffect, useLayoutEffect, useState, type RefObject } from "react"

import { CopyButton } from "@/components/app/copy-button"
import { AskMark } from "@/components/app/ask-mark"
import { WaitMark } from "@/components/app/wait-mark"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_VAR,
} from "@/lib/sidebar-width"
import { chromeTypeClass } from "@/lib/chrome-type"
import { desktopShell } from "@/lib/shell"
import type { Project, Thread, ThreadStatus } from "@/lib/types"
import { cn, formatDuration } from "@/lib/utils"
import { useT } from "@/lib/use-t"
import { useApp } from "@/store/app"

export interface SideChatToggle {
  open: boolean
  buttonRef: RefObject<HTMLButtonElement | null>
  toggle: () => void
}

const actionClass = "size-(--titlebar-control) shrink-0 [&_svg]:size-(--titlebar-icon)"

/** The full-width window title bar. On desktop its blank areas drag the
 *  window and a double-click zooms like Finder. */
export function Header({
  thread,
  project,
  status,
  waiting,
  connected,
  panelOpen,
  sidebarOpen,
  trafficInset,
  onTogglePanel,
  onToggleSidebar,
  onOpenTerminal,
  terminalOpen,
  terminalEnabled,
  sideChat,
}: {
  thread?: Thread
  /** The project the open conversation belongs to, when it has one: its name
   *  prefixes the title on this one line. That is the only clue on screen that
   *  tools are pointed somewhere else. */
  project?: Project
  status: ThreadStatus
  /** True while an active thread wake is parked. Still not `running`. */
  waiting?: boolean
  connected: boolean
  panelOpen: boolean
  sidebarOpen: boolean
  /** Desktop macOS: the native traffic lights sit on this bar. */
  trafficInset?: boolean
  onTogglePanel: () => void
  onToggleSidebar: () => void
  onOpenTerminal: () => void
  terminalOpen: boolean
  terminalEnabled: boolean
  sideChat?: SideChatToggle
}) {
  const t = useT()
  const scheduled = useApp((s) => s.scheduleInboxOpen)
  useLayoutEffect(() => {
    // Sample after the title bar has committed the new corner background.
    if (trafficInset) document.dispatchEvent(new Event("zwai:render-cover-change"))
  }, [sidebarOpen, trafficInset])
  // The elapsed clock ticks here, not in App: a once-a-second setState in
  // the shell used to re-parse every markdown block in the conversation.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!status.running || scheduled) return
    const id = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [status.running, scheduled])
  const elapsedMs =
    status.running && status.started_at
      ? Math.max(0, now - new Date(status.started_at).getTime())
      : 0
  const title = scheduled ? t("schedule.inboxTitle") : thread?.title.trim()
  const listLabel = sidebarOpen
    ? t("header.hideConversations")
    : t("header.showConversations")
  return (
    <header
      data-drag-region
      className={cn(
        "wb-titlebar flex shrink-0 items-center bg-background",
        trafficInset ? "h-(--macos-titlebar-height)" : "h-(--titlebar-height)",
      )}
    >
      <div
        data-testid="titlebar-leading"
        data-expanded={sidebarOpen}
        className={cn(
          "flex h-full min-w-(--dest-rail-width) shrink-0 items-center pr-2 text-muted-foreground",
          trafficInset ? "pl-traffic" : "pl-3",
        )}
        style={{ width: sidebarOpen ? `var(${SIDEBAR_WIDTH_VAR}, ${SIDEBAR_WIDTH_DEFAULT}px)` : undefined }}
      >
        <Button
          variant="ghost"
          size="icon-sm"
          className={actionClass}
          data-sidebar-toggle=""
          aria-controls="workspace-sidebar"
          onClick={onToggleSidebar}
          aria-label={listLabel}
          aria-expanded={sidebarOpen}
          title={`${listLabel} (⌘B)`}
        >
          <PanelLeft />
        </Button>
      </div>
      <div className="flex min-w-0 flex-1 items-center gap-2 px-3">
        <div className="flex min-w-0 flex-1 items-center gap-1.5">
          {!scheduled && project ? (
            <>
              <Folder aria-hidden="true" className="size-(--titlebar-icon) shrink-0 text-muted-foreground" />
              <Tooltip>
                <TooltipTrigger asChild>
                  <span
                    className={cn(
                      chromeTypeClass,
                      "min-w-0 max-w-48 truncate text-muted-foreground",
                    )}
                    data-testid="thread-project"
                  >
                    {project.name}
                  </span>
                </TooltipTrigger>
                <TooltipContent>
                  {project.workdir ? t("header.projectFolder", { path: project.workdir }) : t("header.managedProjectFolder")}
                </TooltipContent>
              </Tooltip>
              {title ? <span className={cn(chromeTypeClass, "shrink-0 text-muted-foreground")}>·</span> : null}
            </>
          ) : null}
          {title ? <p
            className={cn(chromeTypeClass, "min-w-0 truncate font-medium")}
            data-testid="thread-title"
          >
            {title}
          </p> : null}
        </div>

        {scheduled ? null : status.running ? (
          <Badge
            variant={status.awaiting_answer ? "ask" : "warning"}
            data-testid="status-badge"
          >
            {status.awaiting_answer ? (
              <AskMark />
            ) : (
              <span className="size-1.5 animate-breathe rounded-full bg-running" />
            )}
            {/* A running clock counts in seconds; "Working · 0ms" reads like a
                bug even when it is the truth. Missing started_at used to
                clamp to 1s forever — that is a lie, not a clock. */}
            {status.awaiting_answer
              ? t("header.yourTurn")
              : status.awaiting_continue
                ? t("header.waiting")
                : status.compressing
                  ? t("header.compressing")
                  : t("header.working")}
            {status.started_at
              ? ` · ${formatDuration(Math.max(elapsedMs, 1000))}`
              : null}
          </Badge>
        ) : waiting ? (
          <Badge variant="warning" data-testid="status-badge">
            <WaitMark />
            {t("header.waiting")}
          </Badge>
        ) : (
          <Badge variant="outline" data-testid="status-badge">
            {t("header.idle")}
          </Badge>
        )}

        {!scheduled && status.turn_id ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <CopyButton text={status.turn_id} />
            </TooltipTrigger>
            <TooltipContent>
              {t("header.copyTurn")}
            </TooltipContent>
          </Tooltip>
        ) : null}

        {!connected ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <Badge variant="danger">
                <WifiOff className="size-3" />
                {t("header.reconnecting")}
              </Badge>
            </TooltipTrigger>
            <TooltipContent>
              {desktopShell()
                ? t("header.lostStreamDesktop")
                : t("header.lostStreamWeb")}
            </TooltipContent>
          </Tooltip>
        ) : null}

        <div className="flex shrink-0 items-center gap-1" data-no-drag>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onOpenTerminal}
            disabled={!terminalEnabled}
            aria-label={t("header.terminal")}
            title={
              terminalEnabled
                ? `${t("header.terminal")} (⌘J)`
                : t("header.terminalDisabled")
            }
            aria-pressed={terminalOpen}
            className={cn(
              actionClass,
              terminalOpen ? "text-foreground" : "text-muted-foreground",
            )}
          >
            <SquareTerminal />
          </Button>
          {sideChat ? (
            <Button
              ref={sideChat.buttonRef}
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={sideChat.toggle}
              aria-label={t(sideChat.open ? "sideChat.close" : "sideChat.open")}
              title={t(sideChat.open ? "sideChat.close" : "sideChat.open")}
              aria-expanded={sideChat.open}
              aria-controls={sideChat.open ? "side-chat" : undefined}
              className={cn(
                actionClass,
                sideChat.open ? "text-foreground" : "text-muted-foreground",
              )}
            >
              <MessageSquare />
            </Button>
          ) : null}
          {scheduled ? null : (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={onTogglePanel}
              aria-label={t("header.togglePanel")}
              aria-expanded={panelOpen}
              title={`${t("header.togglePanel")} (⌘\\)`}
              className={cn(actionClass, panelOpen ? "text-foreground" : "text-muted-foreground")}
            >
              <PanelRight />
            </Button>
          )}
        </div>
      </div>
    </header>
  )
}
