import { act, fireEvent, render, screen } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import { setLocale } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { OpThreadWork, type RemoteResponse } from "@/lib/rpc"
import { ThreadScreen } from "./thread-screen"

it("hides work records and discards a late PC reply after the setting is turned off", async () => {
  setLocale("en")
  let finishRead!: (value: RemoteResponse) => void
  let requestSignal: AbortSignal | undefined
  const rpc = vi.fn<RemoteLink["rpc"]>(async (request, options) => {
    if (request.op !== OpThreadWork) return { v: 1, id: "", ok: false, code: "unknown_op" }
    requestSignal = options?.signal
    return new Promise((resolve) => { finishRead = resolve })
  })
  const fileLink: RemoteLink = { rpc, alive: () => true, path: "relay", close: vi.fn(), announceDevice: vi.fn() }
  const props = {
    detail: { id: "thread", title: "Conversation" }, blocks: [], fileLink, panelOwnerKey: "pc-1:1",
    onBack: vi.fn(), onSend: vi.fn(), onStop: vi.fn(), onAnswer: vi.fn(), onAnswerStructured: vi.fn(),
  }
  const view = render(<ThreadScreen {...props} />)
  expect(screen.queryByRole("button", { name: "Work records" })).toBeNull()
  view.rerender(<ThreadScreen {...props} workRecordsGeneration={1} />)
  expect(rpc.mock.calls.some(([request]) => request.op === OpThreadWork)).toBe(false)
  fireEvent.click(screen.getByRole("button", { name: "Work records" }))
  expect(await screen.findByRole("heading", { name: "Work records" })).toBeVisible()
  expect(requestSignal?.aborted).toBe(false)
  view.rerender(<ThreadScreen {...props} />)
  expect(requestSignal?.aborted).toBe(true)
  await act(async () => finishRead({ v: 1, id: "", ok: true, data_generation: 1,
    work_snapshot: { thread_id: "thread", project_id: "", nodes: [] } }))
  expect(screen.queryByRole("heading", { name: "Work records" })).toBeNull()
  expect(screen.queryByRole("button", { name: "Work records" })).toBeNull()
  expect(screen.getByRole("heading", { name: "Conversation" })).toBeVisible()
})
