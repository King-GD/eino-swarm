import { act, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { setLocale } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { OpFileChunk, OpFiles, OpTaskPanels, type RemoteResponse } from "@/lib/rpc"
import { ThreadScreen } from "./thread-screen"

describe("transcript file links", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("previews a file named in the transcript and returns to the same draft", async () => {
    setLocale("en")
    vi.stubGlobal("URL", class extends URL {
      static createObjectURL() { return "blob:paired-preview" }
      static revokeObjectURL() {}
    })
    const path = "work/draft.txt"
    const name = "draft.txt"
    const content = "File contents from the paired PC"
    const view = { root: "task-root", isolated: true, has_project: true }
    const rpc = vi.fn<RemoteLink["rpc"]>(async (req) => ({ v: 1, id: "", ok: true,
      ...(req.op === OpFiles ? { files: [{ path, name, size: content.length, dir: false, modified: "", uploaded: false }], file_view: view } : {}),
      ...(req.op === OpFileChunk ? { file_view: view, file_chunk: {
        path, name, mime: "text/plain", offset: 0, size: content.length, next_offset: content.length,
        data: btoa(content), more: false,
      } } : {}),
    }))
    const fileLink: RemoteLink = { rpc, path: "paired", alive: () => true, close: vi.fn(), announceDevice: vi.fn() }
    const onBack = vi.fn()
    render(<ThreadScreen detail={{ id: "t1", title: "talk" }} fileLink={fileLink}
      blocks={[{ id: "answer", kind: "answer", text: "The draft is `draft.txt`." }]}
      onBack={onBack} onSend={vi.fn()} onStop={vi.fn()} onAnswer={vi.fn()} onAnswerStructured={vi.fn()} />)

    const composer = screen.getByRole("textbox", { name: "Message" })
    fireEvent.change(composer, { target: { value: "Unsent draft" } })
    const link = screen.getByRole("button", { name: "draft.txt" })
    fireEvent.click(link)
    expect(await screen.findByText(content)).toBeVisible()
    expect(rpc.mock.calls.map(([req]) => req)).toEqual([
      expect.objectContaining({ op: OpFiles, thread_id: "t1", text: "draft.txt" }),
      expect.objectContaining({ op: OpFileChunk, thread_id: "t1", file_path: path, file_root: "task-root" }),
    ])
    act(() => { expect(window.__zwaiAndroidBack?.()).toBe(true) })
    expect(screen.queryByTestId("phone-file-preview")).toBeNull()
    expect(composer).toHaveValue("Unsent draft")
    expect(link).toHaveFocus()
    expect(onBack).not.toHaveBeenCalled()
  })

  it("drops a pending transcript file lookup when switching paired PCs", async () => {
    setLocale("en")
    let resolveListing!: (response: RemoteResponse) => void
    const unsupported = { v: 1, id: "", ok: false, code: "unknown_op" }
    const rpc = vi.fn<RemoteLink["rpc"]>(async (req) => req.op === OpTaskPanels ? unsupported
      : new Promise((resolve) => { resolveListing = resolve }))
    const oldLink: RemoteLink = { rpc, path: "old-pc", alive: () => true, close: vi.fn(), announceDevice: vi.fn() }
    const nextRpc = vi.fn<RemoteLink["rpc"]>(async () => unsupported)
    const nextLink: RemoteLink = { ...oldLink, rpc: nextRpc, path: "next-pc" }
    const props = {
      detail: { id: "same-thread-id", title: "talk" },
      blocks: [{ id: "answer", kind: "answer" as const, text: "Open report.txt." }],
      onBack: vi.fn(), onSend: vi.fn(), onStop: vi.fn(), onAnswer: vi.fn(), onAnswerStructured: vi.fn(),
    }
    const view = render(<ThreadScreen {...props} fileLink={oldLink} panelOwnerKey="old-pc" />)
    fireEvent.click(screen.getByRole("button", { name: "report.txt" }))
    expect(screen.getByRole("status")).toHaveTextContent("Searching")
    const signal = rpc.mock.calls.find(([req]) => req.op === OpFiles)?.[1]?.signal

    view.rerender(<ThreadScreen {...props} fileLink={nextLink} panelOwnerKey="next-pc" />)
    expect(signal?.aborted).toBe(true)
    await act(async () => resolveListing({ v: 1, id: "", ok: true, more: true, next: "2" }))
    expect(rpc.mock.calls.map(([req]) => req.op)).toEqual([OpTaskPanels, OpFiles])
    expect(nextRpc.mock.calls.map(([req]) => req.op)).toEqual([OpTaskPanels])
    expect(screen.queryByTestId("phone-file-browser")).toBeNull()
  })
})
