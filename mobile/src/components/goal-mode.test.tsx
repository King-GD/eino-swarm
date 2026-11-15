import { fireEvent, render, screen, within } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"

import { setLocale } from "@/lib/i18n"
import { MockHost, MockLink } from "@/lib/mock-link"
import type { ThreadDetail } from "@/lib/rpc"
import { applyEvent } from "@/lib/transcript"
import { ThreadScreen } from "./thread-screen"

afterEach(() => setLocale("en"))

for (const locale of ["en", "zh"] as const) {
  it(`${locale}: keeps the conversation goal identifiable while browsing workers`, () => {
    setLocale(locale)
    const detail: ThreadDetail = { id: "goal-thread", title: "Work", goal: "Verify the assigned work.", goal_on: true }
    const blocks = applyEvent([], { thread_id: detail.id, seq: 1, kind: "spawned", agent_id: "worker-1", role: "reader", text: "", created_at: "" })
    const handlers = { onBack: vi.fn(), onSend: vi.fn(), onSteer: vi.fn(), onStop: vi.fn(), onAnswer: vi.fn(), onAnswerStructured: vi.fn() }
    const fileLink = new MockLink(new MockHost(0))
    const view = render(<ThreadScreen detail={detail} fileLink={fileLink} blocks={blocks} {...handlers} />)
    const check = () => {
      const banner = screen.getByTestId("goal-banner")
      expect(banner).toHaveTextContent(locale === "zh" ? "目标模式" : "Goal mode")
      expect(banner).toHaveTextContent(detail.goal!)
      expect(banner).toHaveTextContent(locale === "zh" ? "进行中" : "Pursuing")
    }
    check()
    fireEvent.click(screen.getByRole("button", { name: locale === "zh" ? "产出物" : "Outputs" }))
    expect(screen.getByTestId("phone-file-browser")).toBeVisible()
    check()
    fireEvent.click(screen.getByRole("button", { name: locale === "zh" ? "返回" : "Back" }))
    fireEvent.click(screen.getByRole("button", { name: /\(1\)/ }))
    check()
    fireEvent.click(within(screen.getByTestId("agent-roster")).getByRole("button"))
    check()
    view.rerender(<ThreadScreen detail={{ ...detail, goal_complete: true }} blocks={blocks} {...handlers} />)
    expect(screen.queryByTestId("goal-banner")).not.toBeInTheDocument()
    view.rerender(<ThreadScreen detail={{ ...detail, goal: "" }} blocks={blocks} {...handlers} />)
    expect(screen.queryByTestId("goal-banner")).not.toBeInTheDocument()
  })
}
