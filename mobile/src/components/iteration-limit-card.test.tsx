import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { IterationLimitCard } from "./iteration-limit-card"
import { setLocale } from "@/lib/i18n"
import { ThreadLog } from "./thread-blocks"
import { ThreadScreen } from "./thread-screen"
import type { CompactBlock } from "@/lib/transcript"

afterEach(() => { cleanup(); setLocale("en") })
const block: CompactBlock = { id: "cap", kind: "notice", text: "", turnId: "turn", confirm: { limit: 7, extendBy: 30, pending: true } }

it("coalesces clicks until the PC confirms continuation", async () => {
  setLocale("en")
  let finish!: () => void
  const onContinue = vi.fn(() => new Promise<void>((resolve) => { finish = resolve }))
  render(<IterationLimitCard pause={block.confirm} onContinue={onContinue} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("Settings → Swarm → Manager tool rounds")
  fireEvent.click(screen.getByRole("button", { name: "Continue for more tool rounds" }))
  fireEvent.click(screen.getByRole("button", { name: "Stop at the tool-round limit" }))
  expect(onContinue.mock.calls).toEqual([[true]])
  expect(screen.getByRole("button", { name: "Continue for more tool rounds" })).toBeDisabled()
  await act(async () => finish())
  expect(screen.getByRole("button", { name: "Continue for more tool rounds" })).toBeDisabled()
})

it("does not send a deferred answer check to the manager round setting", () => {
  setLocale("zh")
  render(<IterationLimitCard pause={{ limit: 80, extendBy: 2000, pending: true, reason: "classification_deferred" }} onContinue={vi.fn()} />)
  const card = screen.getByTestId("iteration-limit")
  expect(card).toHaveTextContent("答复核对已暂缓")
  expect(card).toHaveTextContent("主管工具回合上限仍是 2000")
  expect(card).not.toHaveTextContent("已到主管工具回合上限")
  expect(card).not.toHaveTextContent("主管工具回合即可修改")
})

it("retains a refused pause with a visible error and permits another decision", async () => {
  setLocale("en")
  const onContinue = vi.fn().mockRejectedValueOnce(new Error("PC refused")).mockResolvedValueOnce(undefined)
  render(<IterationLimitCard pause={block.confirm} onContinue={onContinue} />)
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "Continue for more tool rounds" })))
  expect(screen.getByRole("alert")).toHaveTextContent("PC refused")
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "Stop at the tool-round limit" })))
  expect(onContinue.mock.calls).toEqual([[true], [false]])
})

it("never offers old, stopped or restoring turns an active limit control", () => {
  setLocale("en")
  const onContinue = vi.fn()
  const { rerender } = render(<ThreadLog blocks={[block]} running onContinue={onContinue} activeTurnId="other" />)
  expect(screen.queryByRole("button", { name: "Continue for more tool rounds" })).toBeNull()
  rerender(<ThreadLog blocks={[block]} onContinue={onContinue} activeTurnId="turn" />)
  expect(screen.queryByRole("button", { name: "Continue for more tool rounds" })).toBeNull()
  rerender(<ThreadLog blocks={[block]} running onContinue={onContinue} activeTurnId="turn" continueDisabled />)
  expect(screen.getByRole("button", { name: "Continue for more tool rounds" })).toBeDisabled()
})

it("offers a snapshot-only pause even before its historical cap is loaded", async () => {
  setLocale("en")
  const onContinue = vi.fn().mockResolvedValue(undefined)
  render(<ThreadScreen detail={{ id: "thread", title: "Task", running: {
    thread_id: "thread", title: "Task", turn_id: "turn", awaiting_continue: true,
  } }} blocks={[]} onBack={vi.fn()} onSend={vi.fn()} onInsert={vi.fn()} onStop={vi.fn()}
    onAnswer={vi.fn()} onAnswerStructured={vi.fn()} onContinue={onContinue} />)
  expect(screen.getByTestId("iteration-limit")).toHaveTextContent("The manager is paused")
  expect(screen.getByTestId("iteration-limit")).not.toHaveTextContent("Manager tool rounds")
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "Continue this turn" })))
  expect(onContinue).toHaveBeenCalledWith(true)
})
