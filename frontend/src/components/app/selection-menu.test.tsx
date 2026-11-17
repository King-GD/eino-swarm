import { act, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { ApiError, api } from "@/lib/api"
import { defaultSpeechSettings, type SpeechSnapshot } from "@/lib/speech-types"
import * as speechAPI from "@/lib/speech-api"
import { speechPlayback } from "@/lib/speech-media"
import { TERMINAL_SELECTION_EVENT, type TerminalSelection } from "@/lib/terminal"
import type { Meta } from "@/lib/types"
import { useApp } from "@/store/app"
import { SelectionMenu, selectionSpeechKey } from "./selection-menu"

function selectAll(el: HTMLElement) {
  const range = document.createRange()
  range.selectNodeContents(el)
  const sel = window.getSelection()
  sel?.removeAllRanges()
  sel?.addRange(range)
}

function speechReady(): SpeechSnapshot {
  const defaults = defaultSpeechSettings()
  return {
    execution: "pc_local",
    input: { ...defaults.input, enabled: false, ready: false, model: "", label: "" },
    output: { ...defaults.output, enabled: true, ready: true, model: "tts", label: "TTS" },
    limits: defaults.limits,
  }
}

describe("SelectionMenu", () => {
  beforeEach(() => {
    useApp.setState({ locale: "en", meta: undefined })
  })
  afterEach(() => {
    act(() => {
      speechPlayback.stop()
      useApp.setState({ locale: "en", meta: undefined })
    })
    vi.restoreAllMocks()
  })

  function terminalSelection(source: HTMLElement, text: string | null, clear = vi.fn()) {
    act(() => document.dispatchEvent(new CustomEvent<TerminalSelection>(TERMINAL_SELECTION_EVENT, {
      detail: {
        source, clear,
        selection: text === null ? null : {
          text, rect: { top: 200, bottom: 220, left: 100, width: 40, height: 20 },
        },
      },
    })))
    return clear
  }

  it("shares one menu between terminal and transcript selections and clears the correct source", () => {
    const onAdd = vi.fn()
    render(<><div data-quote-source="">transcript</div><div data-testid="terminal" /><SelectionMenu onAdd={onAdd} /></>)
    selectAll(screen.getByText("transcript"))
    fireEvent.mouseUp(document)
    const terminal = screen.getByTestId("terminal")
    const clear = terminalSelection(terminal, "terminal provenance\noutput")
    expect(screen.getAllByRole("menuitem", { name: "Add to chat" })).toHaveLength(1)
    // Another hidden terminal's cleanup cannot dismiss this terminal's menu.
    terminalSelection(document.createElement("div"), null)
    fireEvent.click(screen.getByRole("menuitem", { name: "Add to chat" }))
    expect(onAdd).toHaveBeenCalledTimes(1)
    expect(onAdd).toHaveBeenCalledWith("terminal provenance\noutput")
    expect(clear).toHaveBeenCalledOnce()
    expect(screen.queryByRole("menuitem")).toBeNull()

    terminalSelection(terminal, "old terminal selection")
    selectAll(screen.getByText("transcript"))
    fireEvent.mouseDown(screen.getByText("transcript"))
    fireEvent.mouseUp(screen.getByText("transcript"))
    fireEvent.click(screen.getByRole("menuitem", { name: "Add to chat" }))
    expect(onAdd).toHaveBeenLastCalledWith("transcript")
  })

  it("keeps terminal snapshots on programmatic scroll but dismisses on user movement or escape", () => {
    render(<><div data-testid="terminal" /><SelectionMenu onAdd={vi.fn()} /></>)
    const terminal = screen.getByTestId("terminal")
    fireEvent.mouseDown(terminal)
    terminalSelection(terminal, "output")
    expect(screen.queryByRole("menuitem")).toBeNull()
    fireEvent.mouseUp(terminal)
    expect(screen.getByRole("menuitem")).toBeVisible()
    fireEvent.scroll(terminal)
    expect(screen.getByRole("menuitem")).toBeVisible()
    fireEvent.scroll(document)
    expect(screen.getByRole("menuitem")).toBeVisible()
    fireEvent.keyUp(terminal, { key: "Escape" })
    expect(screen.queryByRole("menuitem")).toBeNull()
    for (const dismiss of [
      () => fireEvent.wheel(document),
      () => fireEvent.touchMove(document),
      () => fireEvent.resize(window),
      () => fireEvent.mouseUp(document),
      () => terminalSelection(terminal, null),
    ]) {
      terminalSelection(terminal, "output")
      expect(screen.getByRole("menuitem")).toBeVisible()
      dismiss()
      expect(screen.queryByRole("menuitem")).toBeNull()
    }
  })
  it("offers Add to chat after a selection inside a quote source", () => {
    const onAdd = vi.fn()
    render(
      <>
        <div data-quote-source="">alpha beta</div>
        <SelectionMenu onAdd={onAdd} />
      </>,
    )
    selectAll(screen.getByText("alpha beta"))
    fireEvent.mouseUp(document)
    expect(screen.getByRole("menuitem", { name: "Add to chat" })).toBeInTheDocument()
    expect(screen.queryByRole("menuitem", { name: "Read aloud" })).toBeNull()
    expect(screen.queryByRole("menuitem", { name: "Translate" })).toBeNull()

    fireEvent.click(screen.getByRole("menuitem", { name: "Add to chat" }))
    expect(onAdd).toHaveBeenCalledWith("alpha beta")
    expect(screen.queryByRole("menuitem", { name: "Add to chat" })).toBeNull()
  })

  it("passes the settled transcript selection only to side chat", () => {
    const onAdd = vi.fn()
    const onAsk = vi.fn()
    render(
      <>
        <div data-quote-source="">progress note</div>
        <div data-testid="terminal" />
        <SelectionMenu onAdd={onAdd} onAsk={onAsk} />
      </>,
    )
    terminalSelection(screen.getByTestId("terminal"), "terminal output")
    expect(screen.queryByRole("menuitem", { name: "Ask in side chat" })).toBeNull()
    const note = screen.getByText("progress note")
    fireEvent.mouseDown(note)
    selectAll(note)
    fireEvent.mouseUp(note)
    window.getSelection()?.removeAllRanges()
    fireEvent(document, new Event("selectionchange"))
    fireEvent.click(screen.getByRole("menuitem", { name: "Ask in side chat" }))
    expect(onAsk.mock.calls).toEqual([["progress note"]])
    expect(onAdd).not.toHaveBeenCalled()
    expect(screen.queryByRole("menu")).toBeNull()
  })

  // Streaming tokens replace text nodes and auto-follow scrolls. Both used
  // to look like "the selection is gone" and flash Add to chat then hide it.
  it("keeps Add to chat when a stream collapses the native selection", () => {
    render(
      <>
        <div data-quote-source="">alpha beta</div>
        <SelectionMenu onAdd={vi.fn()} />
      </>,
    )
    selectAll(screen.getByText("alpha beta"))
    fireEvent.mouseUp(document)
    expect(screen.getByRole("menuitem", { name: "Add to chat" })).toBeInTheDocument()

    window.getSelection()?.removeAllRanges()
    fireEvent(document, new Event("selectionchange"))
    fireEvent.scroll(document)
    expect(screen.getByRole("menuitem", { name: "Add to chat" })).toBeInTheDocument()
  })

  it("dismisses Add to chat on a wheel or a click away", () => {
    render(
      <>
        <div data-quote-source="">alpha beta</div>
        <SelectionMenu onAdd={vi.fn()} />
      </>,
    )
    selectAll(screen.getByText("alpha beta"))
    fireEvent.mouseUp(document)
    fireEvent.wheel(document, { deltaY: 40 })
    expect(screen.queryByRole("menuitem", { name: "Add to chat" })).toBeNull()

    selectAll(screen.getByText("alpha beta"))
    fireEvent.mouseUp(document)
    window.getSelection()?.removeAllRanges()
    fireEvent.mouseUp(document)
    expect(screen.queryByRole("menuitem", { name: "Add to chat" })).toBeNull()
  })

  // Chromium reports the previous range at mouseup when the click lands in
  // the quote source's empty area, and collapses it afterwards. That looks
  // like a stream wiping the native selection, which must not keep the menu.
  it("hides Add to chat when a click leaves the range unchanged", () => {
    render(
      <>
        <div data-quote-source="">alpha beta</div>
        <button type="button">elsewhere</button>
        <SelectionMenu onAdd={vi.fn()} />
      </>,
    )
    const text = screen.getByText("alpha beta")
    fireEvent.mouseDown(text, { clientX: 0, clientY: 0 })
    selectAll(text)
    fireEvent.mouseUp(text, { clientX: 24, clientY: 0, detail: 1 })
    expect(screen.getByRole("menuitem", { name: "Add to chat" })).toBeInTheDocument()

    const away = screen.getByRole("button", { name: "elsewhere" })
    fireEvent.mouseDown(away, { clientX: 8, clientY: 40 })
    fireEvent.mouseUp(away, { clientX: 36, clientY: 48, detail: 1 })
    expect(screen.queryByRole("menuitem", { name: "Add to chat" })).toBeNull()
    fireEvent(document, new Event("selectionchange"))
    expect(screen.queryByRole("menuitem", { name: "Add to chat" })).toBeNull()
    window.getSelection()?.removeAllRanges()
    fireEvent(document, new Event("selectionchange"))
    expect(screen.queryByRole("menuitem", { name: "Add to chat" })).toBeNull()
  })

  it("keeps Add to chat when the gesture that created the selection then loses it to a stream", () => {
    render(
      <>
        <div data-quote-source="">alpha beta</div>
        <SelectionMenu onAdd={vi.fn()} />
      </>,
    )
    const text = screen.getByText("alpha beta")
    fireEvent.mouseDown(text, { clientX: 0, clientY: 0 })
    selectAll(text)
    fireEvent.mouseUp(text, { clientX: 24, clientY: 0, detail: 1 })
    window.getSelection()?.removeAllRanges()
    fireEvent(document, new Event("selectionchange"))
    expect(screen.getByRole("menuitem", { name: "Add to chat" })).toBeInTheDocument()
  })

  it("shows Add to chat after a double click selects a word", () => {
    render(
      <>
        <div data-quote-source="">alpha beta</div>
        <SelectionMenu onAdd={vi.fn()} />
      </>,
    )
    const text = screen.getByText("alpha beta")
    selectAll(text)
    fireEvent.mouseDown(text, { clientX: 4, clientY: 4, detail: 2 })
    fireEvent.mouseUp(text, { clientX: 4, clientY: 4, detail: 2 })
    expect(screen.getByRole("menuitem", { name: "Add to chat" })).toBeInTheDocument()
  })

  it("does not appear for a selection outside a quote source", () => {
    render(
      <>
        <p>composer draft</p>
        <SelectionMenu onAdd={vi.fn()} />
      </>,
    )
    selectAll(screen.getByText("composer draft"))
    fireEvent.mouseUp(document)
    expect(screen.queryByRole("menuitem", { name: "Add to chat" })).toBeNull()
  })

  it("reads the selection with the speech model and keeps the menu open", async () => {
    const play = vi.spyOn(speechPlayback, "play").mockResolvedValue(undefined)
    const synthesize = vi.spyOn(speechAPI, "synthesizeSpeech").mockResolvedValue(new Blob(["audio"], { type: "audio/mpeg" }))
    const onAdd = vi.fn()
    useApp.setState({ meta: { speech: speechReady() } as Meta })
    render(<><div data-quote-source="">alpha beta</div><SelectionMenu onAdd={onAdd} /></>)
    selectAll(screen.getByText("alpha beta"))
    fireEvent.mouseUp(document)
    fireEvent.click(screen.getByRole("menuitem", { name: "Read aloud" }))
    expect(onAdd).not.toHaveBeenCalled()
    expect(play).toHaveBeenCalledWith(selectionSpeechKey, "alpha beta", expect.objectContaining({ chunkChars: 1000 }))
    await play.mock.calls[0][2].synthesize("alpha beta", new AbortController().signal)
    expect(synthesize).toHaveBeenCalledWith("alpha beta", expect.any(AbortSignal), speechReady().limits.output_bytes)
    expect(screen.getByRole("menuitem", { name: "Add to chat" })).toBeVisible()
  })

  it("hides reading when spoken output is switched off or not ready", () => {
    const ready = speechReady()
    ready.output.enabled = false
    useApp.setState({ meta: { speech: ready } as Meta })
    const view = render(<><div data-quote-source="">alpha beta</div><SelectionMenu onAdd={vi.fn()} /></>)
    selectAll(screen.getByText("alpha beta"))
    fireEvent.mouseUp(document)
    expect(screen.queryByRole("menuitem", { name: "Read aloud" })).toBeNull()
    ready.output.enabled = true
    ready.output.ready = false
    useApp.setState({ meta: { speech: ready } as Meta })
    view.rerender(<><div data-quote-source="">alpha beta</div><SelectionMenu onAdd={vi.fn()} /></>)
    expect(screen.queryByRole("menuitem", { name: "Read aloud" })).toBeNull()
  })

  it("stops selection playback when the menu closes", () => {
    vi.spyOn(speechPlayback, "getSnapshot").mockReturnValue({ key: selectionSpeechKey, state: "playing" })
    const stop = vi.spyOn(speechPlayback, "stop").mockImplementation(() => {})
    useApp.setState({ meta: { speech: speechReady() } as Meta })
    render(<><div data-quote-source="">alpha beta</div><SelectionMenu onAdd={vi.fn()} /></>)
    selectAll(screen.getByText("alpha beta"))
    fireEvent.mouseUp(document)
    fireEvent.click(screen.getByRole("menuitem", { name: "Stop reading" }))
    expect(stop).toHaveBeenCalled()
    stop.mockClear()
    fireEvent.wheel(document)
    expect(stop).toHaveBeenCalled()
    expect(screen.queryByRole("menu")).toBeNull()
  })

  it("translates an English selection into Chinese and leaves the quote in place", async () => {
    useApp.setState({ locale: "zh" })
    const translate = vi.spyOn(api, "translateSelection").mockResolvedValue({ text: "不要重启。" })
    const onAdd = vi.fn()
    render(<><div data-quote-source="">Do not restart.</div><SelectionMenu threadId="th_1" onAdd={onAdd} /></>)
    selectAll(screen.getByText("Do not restart."))
    fireEvent.mouseUp(document)
    fireEvent.click(screen.getByRole("menuitem", { name: "翻译" }))
    expect(translate).toHaveBeenCalledWith("th_1", "Do not restart.", expect.any(AbortSignal))
    expect(await screen.findByRole("status", { name: "译文" })).toHaveTextContent("不要重启。")
    expect(onAdd).not.toHaveBeenCalled()
    expect(screen.getByRole("menuitem", { name: "加入对话" })).toBeVisible()
  })

  it("offers translation only for English text while the interface is Chinese", () => {
    useApp.setState({ locale: "zh" })
    render(
      <>
        <div data-quote-source="">这是中文</div>
        <div data-quote-source="">check 这个</div>
        <div data-quote-source="">Do not restart.</div>
        <SelectionMenu threadId="th_1" onAdd={vi.fn()} />
      </>,
    )
    selectAll(screen.getByText("这是中文"))
    fireEvent.mouseUp(document)
    expect(screen.queryByRole("menuitem", { name: "翻译" })).toBeNull()
    selectAll(screen.getByText("check 这个"))
    fireEvent.mouseUp(document)
    expect(screen.queryByRole("menuitem", { name: "翻译" })).toBeNull()
    selectAll(screen.getByText("Do not restart."))
    fireEvent.mouseUp(document)
    expect(screen.getByRole("menuitem", { name: "翻译" })).toBeVisible()

    act(() => useApp.setState({ locale: "en" }))
    expect(screen.queryByRole("menuitem", { name: "Translate" })).toBeNull()
    expect(screen.queryByRole("menuitem", { name: "翻译" })).toBeNull()
  })

  it("shows a translation failure without the provider message and cancels when the menu closes", async () => {
    useApp.setState({ locale: "zh" })
    const translate = vi.spyOn(api, "translateSelection")
    translate.mockRejectedValueOnce(new ApiError("private-upstream-detail", 502, "selection_translation_unavailable"))
    render(<><div data-quote-source="">Do not restart.</div><SelectionMenu threadId="th_1" onAdd={vi.fn()} /></>)
    const text = screen.getByText("Do not restart.")
    selectAll(text)
    fireEvent.mouseUp(document)
    fireEvent.click(screen.getByRole("menuitem", { name: "翻译" }))
    expect(await screen.findByText("翻译失败，请重试。")).toBeVisible()
    expect(screen.queryByText("private-upstream-detail")).toBeNull()

    translate.mockRejectedValueOnce(new ApiError("too long", 400, "selection_translation_too_long"))
    fireEvent.click(screen.getByRole("menuitem", { name: "翻译" }))
    expect(await screen.findByText("选区过长，请缩短后再翻译。")).toBeVisible()

    let signal: AbortSignal | undefined
    translate.mockImplementation((_id, _text, next: AbortSignal) => {
      signal = next
      return new Promise(() => {})
    })
    fireEvent.click(screen.getByRole("menuitem", { name: "翻译" }))
    expect(await screen.findByText("正在翻译…")).toBeVisible()
    fireEvent.wheel(document)
    expect(signal?.aborted).toBe(true)
    expect(screen.queryByRole("menu")).toBeNull()
  })
})
