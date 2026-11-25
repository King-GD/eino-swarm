import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setLocale } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { mockOutputFiles } from "@/lib/mock-files"
import type { RemoteEvent, RemoteResponse } from "@/lib/rpc"
import { applyEvent } from "@/lib/transcript"
import { ThreadLog } from "./thread-blocks"

const imageID = "img_018a"
const { bytes, mime } = mockOutputFiles("")["outputs/landscape.png"]
const chunk: RemoteResponse = {
  v: 1, id: "", ok: true, file_chunk: {
    path: imageID, name: "", size: bytes.length, mime, offset: 0, next_offset: bytes.length,
    data: btoa(String.fromCharCode(...bytes)), more: false,
  },
}

function link(rpc: RemoteLink["rpc"]): RemoteLink {
  return { path: "relay", alive: () => true, close: vi.fn(), announceDevice: vi.fn(), rpc }
}

function blocks(fields: Partial<RemoteEvent> = {}) {
  return applyEvent([], {
    thread_id: "thread", seq: 1, kind: "user_message", text: "Caption from PC", created_at: "",
    has_images: true, image_ids: [imageID], ...fields,
  })
}

function objectURLs() {
  const create = vi.fn(() => "blob:original")
  const revoke = vi.fn()
  vi.stubGlobal("URL", class extends URL {
    static createObjectURL = create
    static revokeObjectURL = revoke
  })
  return { create, revoke }
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("paired transcript input images", () => {
  it("waits for an explicit click on one attachment each time the transcript opens", async () => {
    setLocale("en")
    objectURLs()
    const rpc = vi.fn<RemoteLink["rpc"]>().mockResolvedValue(chunk)
    const owner = { link: link(rpc), threadId: "thread" }
    const rows = blocks({ image_ids: ["img_028b", imageID] })
    const log = <ThreadLog blocks={rows} imageOwner={owner} />
    const view = render(log)
    expect(rpc).not.toHaveBeenCalled()
    expect(screen.queryByRole("img")).not.toBeInTheDocument()
    const actions = screen.getAllByRole("button", { name: "View image" })
    expect(actions).toHaveLength(2)
    fireEvent.click(actions[1])
    await screen.findByRole("img", { name: "Image" })
    expect(rpc).toHaveBeenCalledOnce()
    expect(rpc).toHaveBeenCalledWith({
      op: "input_image_chunk", thread_id: "thread", image_id: imageID, before: 0,
    }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(screen.getAllByRole("button", { name: "View image" })).toHaveLength(1)

    view.unmount()
    render(log)
    expect(screen.getAllByRole("button", { name: "View image" })).toHaveLength(2)
    expect(screen.queryByRole("img")).not.toBeInTheDocument()
    expect(rpc).toHaveBeenCalledOnce()
  })

  it("keeps the caption and allows retry when the PC cannot provide the image", async () => {
    setLocale("en")
    const { create, revoke } = objectURLs()
    const rpc = vi.fn<RemoteLink["rpc"]>()
      .mockResolvedValueOnce({ v: 1, id: "", ok: false, code: "not_found" })
      .mockResolvedValueOnce(chunk)
    const view = render(<ThreadLog blocks={blocks()} imageOwner={{ link: link(rpc), threadId: "thread" }} />)
    fireEvent.click(screen.getByRole("button", { name: "View image" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the image.")
    expect(screen.getByText("Caption from PC")).toBeVisible()
    fireEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByRole("img", { name: "Image" })).toHaveAttribute("src", "blob:original")
    expect(create).toHaveBeenCalledOnce()
    expect(rpc.mock.calls.map(([req]) => req)).toEqual(Array(2).fill({
      op: "input_image_chunk", thread_id: "thread", image_id: imageID, before: 0,
    }))
    view.unmount()
    expect(revoke).toHaveBeenCalledWith("blob:original")
  })

  it("cancels a pending image when switching PCs with the same transcript identities", async () => {
    setLocale("en")
    const { create } = objectURLs()
    let completeOld!: (response: RemoteResponse) => void
    const old = vi.fn<RemoteLink["rpc"]>().mockImplementation(() => new Promise(resolve => { completeOld = resolve }))
    const next = vi.fn<RemoteLink["rpc"]>().mockResolvedValue(chunk)
    const rows = blocks()
    const oldOwner = { link: link(old), threadId: "thread" }
    const nextOwner = { link: link(next), threadId: "thread" }
    const view = render(<ThreadLog blocks={rows} imageOwner={oldOwner} />)
    fireEvent.click(screen.getByRole("button", { name: "View image" }))
    await waitFor(() => expect(old).toHaveBeenCalledOnce())
    expect(screen.getByRole("status")).toHaveTextContent("Loading image")
    view.rerender(<ThreadLog blocks={rows} imageOwner={nextOwner} />)
    expect(old.mock.calls[0][1]?.signal?.aborted).toBe(true)
    await act(async () => completeOld(chunk))
    expect(next).not.toHaveBeenCalled()
    expect(create).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: "View image" })).toBeVisible()

    fireEvent.click(screen.getByRole("button", { name: "View image" }))
    await screen.findByRole("img", { name: "Image" })
    expect(next).toHaveBeenCalledOnce()
    view.rerender(<ThreadLog blocks={rows} imageOwner={oldOwner} />)
    expect(screen.getByRole("button", { name: "View image" })).toBeVisible()
    expect(old).toHaveBeenCalledOnce()
  })

  it("retains the image-only placeholder from legacy PCs", () => {
    setLocale("en")
    const rpc = vi.fn<RemoteLink["rpc"]>()
    render(<ThreadLog blocks={blocks({ text: "", image_ids: undefined })}
      imageOwner={{ link: link(rpc), threadId: "thread" }} />)
    expect(screen.getByText("Image")).toBeVisible()
    expect(rpc).not.toHaveBeenCalled()
  })
})
