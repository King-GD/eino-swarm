import { act, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { emptyTranscript, type Block } from "@/lib/transcript"
import { useApp } from "@/store/app"
import { useSettingsSheet } from "@/store/settings-sheet"
import { IterationLimitCard } from "./iteration-limit-card"

const before = useApp.getState()
const settingsBefore = useSettingsSheet.getState()
afterEach(() => act(() => {
  useApp.setState(before, true)
  useSettingsSheet.setState(settingsBefore, true)
}))
const block: Block = { id: "pause", kind: "confirm", agentId: "manager", turnId: "turn", seq: 2,
  at: "2026-01-01T00:00:00Z", text: "", confirm: {
    limit: 80, extendBy: 2000, reason: "workers_uncollected", automaticResume: true, pending: true,
  } }

it("explains automatic collection waiting and preserves explicit Continue and Stop", () => {
  const extend = vi.fn()
  useApp.setState({ status: { running: true }, transcript: { ...emptyTranscript(), running: true }, extendTurn: extend })
  const { rerender } = render(<IterationLimitCard block={block} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("resume once")
  expect(screen.getByTestId("iteration-limit")).not.toHaveTextContent(/80|2000|tool-round limit/)
  expect(screen.queryByRole("button", { name: "Manager tool rounds" })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Continue to collect delegated results" }))
  expect(extend).toHaveBeenCalledWith(true)
  fireEvent.click(screen.getByRole("button", { name: "Stop waiting for delegated results" }))
  expect(extend).toHaveBeenCalledWith(false)
  rerender(<IterationLimitCard block={{ ...block, confirm: { ...block.confirm!, pending: false, continued: true } }} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("Continuing to collect")
  expect(screen.queryByText(/resume once/)).not.toBeInTheDocument()
  expect(screen.queryByRole("button")).not.toBeInTheDocument()
})

it("does not promise automatic resumption for an exhausted collection allowance", () => {
  useApp.setState({ status: { running: true }, transcript: { ...emptyTranscript(), running: true } })
  render(<IterationLimitCard block={{ ...block, confirm: { ...block.confirm!, automaticResume: false } }} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("Continue to collect them, or Stop")
  expect(screen.queryByText(/resume once/)).not.toBeInTheDocument()
  act(() => useApp.setState({ status: { running: false }, transcript: emptyTranscript() }))
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("Stopped before collecting")
})

it("keeps a live collection pause actionable when the transcript flag is idle", () => {
  useApp.setState({ status: { running: true }, transcript: emptyTranscript() })
  render(<IterationLimitCard block={{ ...block, confirm: { ...block.confirm!, automaticResume: false } }} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("Continue to collect them, or Stop")
  expect(screen.getByRole("button", { name: "Continue to collect delegated results" })).toBeEnabled()
  expect(screen.getByRole("button", { name: "Stop waiting for delegated results" })).toBeEnabled()
  expect(screen.getByTestId("iteration-limit")).not.toHaveTextContent("Stopped before collecting")
})

it("does not call a refused collection idle while the turn is still running", () => {
  useApp.setState({ status: { running: true }, transcript: { ...emptyTranscript(), running: true } })
  const { rerender } = render(<IterationLimitCard block={{ ...block, confirm: { ...block.confirm!, pending: false, continued: false } }} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("still closing")
  expect(screen.queryByRole("button")).not.toBeInTheDocument()
  expect(screen.getByTestId("iteration-limit")).not.toHaveTextContent("Stopped before collecting")
  act(() => useApp.setState({ status: { running: false } }))
  rerender(<IterationLimitCard block={{ ...block, confirm: { ...block.confirm!, pending: false, continued: false } }} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("Stopped before collecting")
  expect(screen.queryByRole("button", { name: "Manager tool rounds" })).not.toBeInTheDocument()
})

const rounds: Block = {
  ...block,
  confirm: { limit: 80, extendBy: 2000, pending: true },
}

it("opens the manager tool-round setting without changing the offered continuation", () => {
  useApp.setState({ status: { running: true }, transcript: { ...emptyTranscript(), running: true }, locale: "en" })
  const { rerender } = render(<IterationLimitCard block={rounds} />)
  const card = screen.getByTestId("iteration-limit")
  expect(card).toHaveTextContent("Reached the manager tool-round limit (80). Continue for another 2000 rounds?")
  expect(card).toHaveTextContent("This continuation still adds the rounds above")
  fireEvent.click(screen.getByRole("button", { name: "Manager tool rounds" }))
  expect(useSettingsSheet.getState()).toMatchObject({ open: true, section: "swarm", focus: "manager_max_iterations" })
  rerender(<IterationLimitCard block={{ ...rounds, confirm: { ...rounds.confirm!, pending: false, continued: true } }} />)
  expect(screen.queryByRole("button", { name: "Manager tool rounds" })).not.toBeInTheDocument()
})

it("does not call a deferred answer check the manager round cap", () => {
  useApp.setState({ status: { running: true }, transcript: { ...emptyTranscript(), running: true }, locale: "zh" })
  const deferred: Block = { ...rounds, confirm: { limit: 80, extendBy: 2000, pending: true, reason: "classification_deferred" } }
  const { rerender } = render(<IterationLimitCard block={deferred} />)
  const card = screen.getByTestId("iteration-limit")
  expect(card).toHaveTextContent("答复核对已暂缓")
  expect(card).toHaveTextContent("主管工具回合上限仍是 2000")
  expect(card).toHaveTextContent("这一轮已用 80 轮")
  expect(card).not.toHaveTextContent("已到主管工具回合上限")
  expect(screen.queryByRole("button", { name: "主管工具回合" })).not.toBeInTheDocument()
  rerender(<IterationLimitCard block={{ ...deferred, confirm: { ...deferred.confirm!, pending: false } }} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("已停止")
  expect(screen.queryByRole("button", { name: "主管工具回合" })).not.toBeInTheDocument()
})

it("keeps the setting link after stopping at the cap", () => {
  useApp.setState({ status: { running: false }, transcript: emptyTranscript(), locale: "zh" })
  render(<IterationLimitCard block={{ ...rounds, confirm: { ...rounds.confirm!, pending: false } }} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("下次暂停前可在这里改")
  fireEvent.click(screen.getByRole("button", { name: "主管工具回合" }))
  expect(useSettingsSheet.getState()).toMatchObject({ open: true, section: "swarm", focus: "manager_max_iterations" })
})
