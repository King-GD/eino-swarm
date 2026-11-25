import { fireEvent, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { renderComposer } from "@/test/composer"
import type { Attachment, Meta } from "@/lib/types"
import { useApp } from "@/store/app"

beforeEach(() => useApp.setState({
  meta: {
    product_mode: "fofa",
    model_policy: {
      mode: "gateway", configurable: true, ready: true, scope: "gateway",
      capabilities: {
        authorized: true, available: true,
        capabilities: { conversation: { supported: true, available: true, endpoints: ["chat_completions"] } },
      },
    },
  } as Meta,
}))

describe("Composer desktop image input", () => {
  it.each([
    { source: "drop", type: "application/octet-stream" },
    { source: "paste", type: "public.jpeg" },
  ])("sends a $source photo labeled $type as vision bytes, not a workspace path", async ({ source, type }) => {
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: () => "blob:preview" })
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: () => {} })
    const onSend = vi.fn()
    const onUpload = vi.fn(async (): Promise<Attachment[]> => [])
    renderComposer({ models: [], provider: "", model: "", reasoningLevels: [], onSend, onUpload })
    const photo = new File([new Uint8Array([1, 2, 3])], "photo.jpg", { type })
    const transfer = {
      types: ["Files"], files: [photo],
      items: [{ kind: "file", type: photo.type, getAsFile: () => photo }],
    }
    if (source === "drop") {
      fireEvent.drop(screen.getByTestId("composer-drop"), { dataTransfer: transfer })
    } else {
      fireEvent.paste(screen.getByTestId("composer-input"), { clipboardData: transfer })
    }
    expect(screen.getByAltText("photo.jpg")).toHaveAttribute("src", "blob:preview")
    expect(screen.queryByTestId("composer-attachments")).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1))
    expect(onUpload).not.toHaveBeenCalled()
    const [, images, opts] = onSend.mock.calls[0]
    expect(images).toEqual([{ name: "photo.jpg", mime: "image/jpeg", data: "AQID" }])
    expect(opts.files).toBeUndefined()
  })
})
