import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { DestRail, useVisibleDest } from "./dest-rail"
import { useApp } from "@/store/app"
import { ScheduleListPane } from "./schedule-inbox"

vi.mock("@/lib/api", () => ({
  api: {
    schedules: async () => ({ schedules: [], unread: 0 }),
  },
}))

function Probe() {
  const pane = useVisibleDest()
  return (
    <div>
      <DestRail onSettings={vi.fn()} onToggleTheme={vi.fn()} onToggleLocale={vi.fn()} />
      <div data-testid="pane">{pane}</div>
      {pane === "scheduled" ? <ScheduleListPane /> : null}
    </div>
  )
}

describe("destination rail", () => {
  it("switches between projects, schedules and conversations", async () => {
    render(<Probe />)
    expect(screen.getByTestId("pane")).toHaveTextContent("projects")
    expect(screen.getByTestId("dest-projects")).toHaveAttribute("aria-current", "page")
    expect(screen.queryByTestId("dest-clients")).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId("schedule-inbox"))
    expect(screen.getByTestId("pane")).toHaveTextContent("scheduled")
    expect(screen.getByTestId("schedule-list-pane")).toBeInTheDocument()
    expect(screen.queryByTestId("dest-clients")).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId("dest-chats"))
    await waitFor(() => expect(screen.getByTestId("pane")).toHaveTextContent("chats"))
    expect(screen.queryByTestId("schedule-list-pane")).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId("dest-projects"))
    await waitFor(() => expect(screen.getByTestId("pane")).toHaveTextContent("projects"))
  })

  it("does not paint an unread count on the rail", () => {
    useApp.setState({ scheduleUnread: 726 })
    render(<DestRail onSettings={vi.fn()} onToggleTheme={vi.fn()} onToggleLocale={vi.fn()} />)
    expect(screen.queryByTestId("schedule-unread")).not.toBeInTheDocument()
    expect(screen.getByTestId("schedule-inbox")).toHaveAttribute("aria-label", "Scheduled")
    const rail = screen.getByTestId("dest-rail")
    expect(rail.style.paddingInline).toBe("var(--dest-rail-pad)")
  })
})
