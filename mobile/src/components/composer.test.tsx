import { act, fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { setLocale } from "@/lib/i18n"
import { Composer } from "./composer"

function modelButton() {
  fireEvent.focus(screen.getByRole("textbox", { name: "Message" }))
  return screen.getByRole("button", { name: "Model" })
}

describe("Composer", () => {
  it("shows the managed model name without offering a picker", () => {
    setLocale("en")
    render(<Composer label="Message" sendLabel="Send" onSubmit={vi.fn()} hideModel models={[{ provider_id: "p", model: "one", default: true }]} />)
    fireEvent.focus(screen.getByRole("textbox", { name: "Message" }))
    expect(screen.queryByRole("button", { name: "Model" })).toBeNull()
    expect(screen.getByText("one", { exact: true })).toBeVisible()
  })

  it("sends the trimmed text and clears the box", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" onSubmit={onSubmit} />)
    const box = screen.getByLabelText("Message")
    fireEvent.change(box, { target: { value: "  hello  " } })
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith("hello")
    expect(box).toHaveValue("")
  })

  // Nothing to send is the common state; an enabled button there is a lie.
  it("keeps send off until there is something to send", () => {
    setLocale("en")
    render(<Composer label="Message" sendLabel="Send" onSubmit={vi.fn()} />)
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "x" } })
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled()
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "   " } })
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
  })

  it("sends on Enter but keeps Shift+Enter for a new line", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" onSubmit={onSubmit} />)
    const box = screen.getByLabelText("Message")
    fireEvent.change(box, { target: { value: "one" } })
    fireEvent.keyDown(box, { key: "Enter", shiftKey: true })
    expect(onSubmit).not.toHaveBeenCalled()
    fireEvent.keyDown(box, { key: "Enter" })
    expect(onSubmit).toHaveBeenCalledWith("one")
  })

  // A Pinyin or Kana candidate list commits on Enter. Sending there would
  // ship half a word.
  it("does not send while an IME is composing", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" onSubmit={onSubmit} />)
    const box = screen.getByLabelText("Message")
    fireEvent.change(box, { target: { value: "ni" } })
    fireEvent.keyDown(box, { key: "Enter", isComposing: true })
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("offers steer beside send while a turn is live", () => {
    setLocale("en")
    const onInsert = vi.fn()
    const onSubmit = vi.fn()
    render(
      <Composer
        label="Message"
        sendLabel="Follow-up"
        onInsert={onInsert}
        onSubmit={onSubmit}
      />,
    )
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "nudge" } })
    fireEvent.click(screen.getByRole("button", { name: "Send options" }))
    fireEvent.click(screen.getByRole("menuitem", { name: "Insert into this turn" }))
    expect(onInsert).toHaveBeenCalledWith("steer", "nudge", undefined)
    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByLabelText("Message")).toHaveValue("")
  })

  it("has no steer control on an idle thread", () => {
    setLocale("en")
    render(<Composer label="Message" sendLabel="Send" onSubmit={vi.fn()} />)
    expect(screen.queryByRole("button", { name: "Steer" })).not.toBeInTheDocument()
  })

  it("refuses to send while the link is down", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" disabled onSubmit={onSubmit} />)
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "x" } })
    fireEvent.keyDown(screen.getByLabelText("Message"), { key: "Enter" })
    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
  })

  it("grows with the text instead of scrolling a one-line slot", () => {
    setLocale("en")
    render(<Composer label="Message" sendLabel="Send" onSubmit={vi.fn()} />)
    const box = screen.getByLabelText("Message") as HTMLTextAreaElement
    Object.defineProperty(box, "scrollHeight", { value: 68, configurable: true })
    fireEvent.change(box, { target: { value: "a\nb\nc" } })
    expect(box.style.height).toBe("68px")
    // Past the cap the transcript would lose its last answer to the keyboard.
    Object.defineProperty(box, "scrollHeight", { value: 900, configurable: true })
    fireEvent.change(box, { target: { value: "a\n".repeat(40) } })
    expect(box.style.height).toBe("132px")
  })

  it("sends attachment-only and quote-only follow-ups instead of discarding them with Stop", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    const onStop = vi.fn()
    const view = render(<Composer label="Message" sendLabel="Follow-up" stopLabel="Stop" onStop={onStop} onSubmit={onSubmit} />)
    fireEvent.change(screen.getByTestId("file-input"), { target: { files: [new File(["content"], "draft.txt")] } })
    fireEvent.click(screen.getByRole("button", { name: "Follow-up" }))
    expect(onSubmit).toHaveBeenCalledWith("", expect.objectContaining({ files: [expect.objectContaining({ name: "draft.txt" })] }))
    view.rerender(<Composer label="Message" sendLabel="Follow-up" stopLabel="Stop" onStop={onStop} onSubmit={onSubmit} quotes={["quoted text"]} />)
    expect(screen.getByRole("button", { name: "Stop" })).toBeEnabled()
    fireEvent.click(screen.getByRole("button", { name: "Follow-up" }))
    expect(onSubmit).toHaveBeenLastCalledWith(expect.stringContaining("quoted text"))
    expect(onStop).not.toHaveBeenCalled()
  })

  it("keeps Stop available during a pending request even when Send cannot accept the draft", () => {
    const onStop = vi.fn()
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" stopLabel="Stop" onStop={onStop} onSubmit={onSubmit} pending disabled />)
    fireEvent.click(screen.getByRole("button", { name: "Stop" }))
    expect(onStop).toHaveBeenCalledOnce()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("renders the hint and whatever sits above the box", () => {
    setLocale("en")
    render(
      <Composer
        label="Message"
        sendLabel="Send"
        hint="queued"
        above={<p>chips</p>}
        onSubmit={vi.fn()}
      />,
    )
    expect(screen.getByText("queued")).toBeInTheDocument()
    expect(screen.getByText("chips")).toBeInTheDocument()
  })

  it("offers only the levels it was given and sends a file with no text", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(
      <Composer
        label="Message"
        sendLabel="Send"
        models={[
          { provider_id: "a", provider_label: "Alpha", model: "one", default: true },
          { provider_id: "b", provider_label: "Beta", model: "two" },
        ]}
        reasoningLevels={["low"]}
        onSubmit={onSubmit}
      />,
    )
    expect(screen.queryByRole("button", { name: "Model" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Thinking level" })).not.toBeInTheDocument()
    fireEvent.click(modelButton())
    const picker = screen.getByRole("dialog", { name: "Model" })
    expect(picker).toBeInTheDocument()
    expect(screen.getByRole("radio", { name: "one" })).toHaveClass("text-sm")
    expect(screen.getByRole("radio", { name: "two" })).toBeInTheDocument()
    fireEvent.click(screen.getByRole("radio", { name: "two" }))
    expect(screen.queryByRole("dialog", { name: "Model" })).not.toBeInTheDocument()
    expect(modelButton()).toHaveTextContent("two")
    expect(modelButton()).toHaveAccessibleDescription("two")
    fireEvent.click(screen.getByRole("button", { name: "Thinking level" }))
    const thinking = screen.getByRole("dialog", { name: "Thinking level" })
    expect(screen.getByRole("radio", { name: "Default thinking" })).toHaveAttribute("aria-checked", "true")
    expect(screen.queryByRole("radio", { name: "Medium thinking" })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("radio", { name: "Low thinking" }))
    expect(thinking).not.toBeInTheDocument()
    const file = new File(["x"], "notes.txt", { type: "text/plain" })
    fireEvent.change(screen.getByTestId("file-input"), { target: { files: [file] } })
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith(
      "",
      expect.objectContaining({ files: [file], images: [], model: "two", providerId: "b", reasoning: "low" }),
    )
  })

  it("shows the locked official name without offering model choices and sends its alias", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(
      <Composer
        label="Message"
        sendLabel="Send"
        providerId="own"
        model="saved-model"
        models={[{ provider_id: "gateway:owner", provider_label: "Partner model", label: "Partner model", model: "account", locked: true, default: true }]}
        onSubmit={onSubmit}
      />,
    )
    const button = modelButton()
    expect(button).toHaveTextContent("Partner model")
    expect(button).not.toHaveTextContent("saved-model")
    fireEvent.click(button)
    expect(screen.getByRole("dialog", { name: "Model" })).toHaveTextContent("Partner model")
    expect(screen.queryByRole("radio")).not.toBeInTheDocument()
    fireEvent.keyDown(window, { key: "Escape" })
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "hi" } })
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith("hi", expect.objectContaining({ providerId: "gateway:owner", model: "account" }))
  })

  it("keeps a chat on a provider the catalog no longer lists", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(
      <Composer
        label="Message"
        sendLabel="Send"
        providerId="hidden"
        model="probe"
        models={[{ provider_id: "a", provider_label: "Alpha", model: "one", default: true }]}
        onSubmit={onSubmit}
      />,
    )
    expect(modelButton()).toHaveTextContent("probe")
    fireEvent.click(modelButton())
    expect(screen.queryByRole("radio", { name: "probe" })).not.toBeInTheDocument()
    expect(screen.getByRole("radio", { name: "one" })).toBeInTheDocument()
    fireEvent.keyDown(window, { key: "Escape" })
    expect(screen.queryByRole("dialog", { name: "Model" })).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "hi" } })
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith(
      "hi",
      expect.objectContaining({ providerId: "hidden", model: "probe" }),
    )
  })

  it("closes the model list on Android Back before leaving the chat", () => {
    setLocale("en")
    const previous = vi.fn(() => true)
    window.__zwaiAndroidBack = previous
    render(
      <Composer
        label="Message"
        sendLabel="Send"
        models={[{ provider_id: "a", provider_label: "Alpha", model: "one" }]}
        onSubmit={vi.fn()}
      />,
    )
    fireEvent.click(modelButton())
    act(() => {
      expect(window.__zwaiAndroidBack?.()).toBe(true)
    })
    expect(screen.queryByRole("dialog", { name: "Model" })).not.toBeInTheDocument()
    expect(previous).not.toHaveBeenCalled()
    expect(window.__zwaiAndroidBack).toBe(previous)
    delete window.__zwaiAndroidBack
  })

  it("can send a quote alone and remove it before sending", () => {
    const onSubmit = vi.fn()
    const onQuotesChange = vi.fn()
    const { rerender } = render(
      <Composer label="Message" sendLabel="Send" quotes={["alpha"]} onQuotesChange={onQuotesChange} onSubmit={onSubmit} />,
    )
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled()
    fireEvent.click(screen.getByRole("button", { name: "Remove quote 1" }))
    expect(onQuotesChange).toHaveBeenCalledWith([])
    rerender(<Composer label="Message" sendLabel="Send" quotes={[]} onQuotesChange={onQuotesChange} onSubmit={onSubmit} />)
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
    rerender(<Composer label="Message" sendLabel="Send" quotes={["alpha"]} onQuotesChange={onQuotesChange} onSubmit={onSubmit} />)
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith("<selected_text>\nalpha\n</selected_text>")
  })

  it("lets the user read and edit a selected quote before sending", () => {
    const onSubmit = vi.fn()
    const onQuotesChange = vi.fn()
    const { rerender } = render(
      <Composer label="Message" sendLabel="Send" quotes={["long original quote"]} onQuotesChange={onQuotesChange} onSubmit={onSubmit} />,
    )
    fireEvent.change(screen.getByRole("textbox", { name: "Edit quote 1" }), { target: { value: "corrected quote" } })
    expect(onQuotesChange).toHaveBeenCalledWith(["corrected quote"])
    rerender(<Composer label="Message" sendLabel="Send" quotes={["corrected quote"]} onQuotesChange={onQuotesChange} onSubmit={onSubmit} />)
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith("<selected_text>\ncorrected quote\n</selected_text>")
  })

  it("does not enable sending an emptied quote", () => {
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" quotes={["  "]} onQuotesChange={vi.fn()} onSubmit={onSubmit} />)
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
  })

  it("restores the text and quote after a failed send", async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error("offline"))
    const onQuotesChange = vi.fn()
    render(<Composer label="Message" sendLabel="Send" quotes={["alpha"]} onQuotesChange={onQuotesChange} onSubmit={onSubmit} />)
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "explain" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }))
    })
    await vi.waitFor(() => expect(onQuotesChange).toHaveBeenLastCalledWith(["alpha"]))
    expect(screen.getByLabelText("Message")).toHaveValue("explain")
  })
})


describe("catalog changes preserve an explicit conversation model", () => {
  it("does not switch an existing selection to a newly listed default", () => {
    setLocale("en")
    const send = vi.fn()
    const props = { label: "Message", sendLabel: "Send", onSubmit: send, providerId: "a", model: "retained" }
    const { rerender } = render(<Composer {...props} models={[{ provider_id: "a", model: "retained", default: true }]} />)
    rerender(<Composer {...props} models={[{ provider_id: "a", model: "new-model", default: true }]} />)
    expect(modelButton()).toHaveTextContent("retained")
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "continue" } })
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(send).toHaveBeenCalledWith("continue", expect.objectContaining({ providerId: "a", model: "retained" }))
  })
})

it("keeps a shown catalog fallback when the list is reordered", () => {
  setLocale("en")
  const props = { label: "Message", sendLabel: "Send", onSubmit: vi.fn(), providerId: "a", model: "" }
  const { rerender } = render(<Composer {...props} models={[
    { provider_id: "a", model: "first", default: true },
    { provider_id: "a", model: "second" },
  ]} />)
  expect(modelButton()).toHaveTextContent("first")
  rerender(<Composer {...props} models={[
    { provider_id: "a", model: "second", default: true },
    { provider_id: "a", model: "first" },
  ]} />)
  expect(modelButton()).toHaveTextContent("first")
})

it("follows a changed settings default and marks a missing one unavailable", () => {
  setLocale("en")
  const props = { label: "Message", sendLabel: "Send", onSubmit: vi.fn(), providerId: "a", model: "" }
  const { rerender } = render(<Composer {...props} models={[
    { provider_id: "a", model: "saved", default: true, default_follows_setting: true, ready: true },
    { provider_id: "a", model: "other", ready: true },
  ]} />)
  expect(modelButton()).toHaveTextContent("saved")
  rerender(<Composer {...props} models={[
    { provider_id: "a", model: "other", default: true, default_follows_setting: true, ready: true },
    { provider_id: "a", model: "saved", ready: true },
  ]} />)
  expect(modelButton()).toHaveTextContent("other")
  rerender(<Composer {...props} models={[
    { provider_id: "a", model: "removed", default: true, default_follows_setting: true, ready: false },
    { provider_id: "a", model: "other", ready: true },
  ]} />)
  expect(modelButton()).toHaveAttribute("data-unavailable", "true")
  expect(modelButton()).toHaveTextContent("removed")
})

it("keeps a user's local new-chat pick when refreshing the available models", () => {
  setLocale("en")
  const props = { label: "Message", sendLabel: "Send", onSubmit: vi.fn() }
  const { rerender } = render(<Composer {...props} models={[{ provider_id: "p", model: "picked", default: true }]} />)
  fireEvent.click(modelButton())
  fireEvent.click(screen.getByRole("radio", { name: "picked" }))
  rerender(<Composer {...props} models={[{ provider_id: "p", model: "fresh", default: true }]} />)
  expect(modelButton()).toHaveTextContent("picked")
})
