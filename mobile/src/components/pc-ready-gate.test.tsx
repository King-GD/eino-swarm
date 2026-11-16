import { fireEvent, render, screen } from "@testing-library/react"
import { useState } from "react"
import { expect, it } from "vitest"
import { PcReadyGate } from "./pc-ready-gate"

function Conversation() {
  const [draft, setDraft] = useState("")
  return <><p>Earlier conversation</p><input aria-label="Draft" value={draft} onChange={event => setDraft(event.target.value)} /></>
}

it("retains the conversation and draft while making PC work inaccessible during recovery", () => {
  const page = (waiting: boolean) => <PcReadyGate waiting={waiting} notice={<p role="status">Waiting for computer</p>}><Conversation /></PcReadyGate>
  const { rerender } = render(page(false))
  const draft = screen.getByLabelText("Draft")
  fireEvent.change(draft, { target: { value: "Unsent work" } })
  rerender(page(true))
  expect(screen.getByRole("status")).toHaveTextContent("Waiting for computer")
  expect(draft).not.toBeVisible()
  expect(draft.closest("[inert]")).toHaveAttribute("aria-hidden", "true")
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument()
  rerender(page(false))
  expect(screen.getByRole("textbox")).toBe(draft)
  expect(draft).toBeVisible()
  expect(draft).toHaveValue("Unsent work")
  expect(screen.getByText("Earlier conversation")).toBeVisible()
  expect(screen.queryByRole("status")).not.toBeInTheDocument()
})

it("waits for the first ready computer before mounting its work screens", () => {
  const page = (waiting: boolean) => <PcReadyGate waiting={waiting} notice={<p>Connect a computer</p>}><Conversation /></PcReadyGate>
  const { rerender } = render(page(true))
  expect(screen.queryByLabelText("Draft")).not.toBeInTheDocument()
  expect(screen.getByText("Connect a computer")).toBeVisible()
  rerender(page(false))
  expect(screen.getByRole("textbox")).toBeVisible()
})
