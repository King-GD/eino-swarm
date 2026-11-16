import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { Composer } from "./composer"
import { SpeechReadButton } from "./speech-read-button"
import { TooltipProvider } from "@/components/ui/tooltip"
import { useApp } from "@/store/app"
import type { Meta } from "@/lib/types"
import { defaultSpeechSettings, type SpeechSnapshot } from "@/lib/speech-types"
import * as media from "@/lib/speech-media"
import * as speechAPI from "@/lib/speech-api"
import * as drafts from "@/lib/use-composer-draft"

function snapshot(): SpeechSnapshot {
  const defaults = defaultSpeechSettings()
  return { execution: "pc_local", input: { ...defaults.input, enabled: true, ready: true, model: "asr", label: "ASR" },
    output: { ...defaults.output, enabled: true, ready: true, model: "tts", label: "TTS" }, limits: defaults.limits }
}
function element(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  return <TooltipProvider><Composer running={false} models={[]} onModelChange={vi.fn()} reasoningLevels={[]}
    onReasoningChange={vi.fn()} onSend={vi.fn()} onStop={vi.fn()} onUpload={vi.fn(async () => [])} focusSignal={0} {...props} /></TooltipProvider>
}
function enabled() {
  act(() => useApp.setState({ meta: { speech: snapshot() } as Meta }))
  vi.spyOn(media, "speechRecordingSupported").mockReturnValue(true)
}
afterEach(() => { media.speechPlayback.stop(); vi.restoreAllMocks() })

describe("Composer speech", () => {
  it("hides unconfigured input and keeps the input and reading switches independent", () => {
    vi.spyOn(media, "speechRecordingSupported").mockReturnValue(true)
    const view = render(element())
    expect(screen.queryByLabelText("Start voice input")).toBeNull()
    const speech = snapshot(); speech.input.enabled = false
    act(() => useApp.setState({ meta: { speech } as Meta }))
    expect(screen.queryByLabelText("Start voice input")).toBeNull()
    view.rerender(<SpeechReadButton id="reply" text="Public reply." />)
    expect(screen.getByLabelText("Read aloud")).toBeVisible()
  })

  it("keeps edits made during recognition and never sends automatically", async () => {
    enabled()
    const stop = vi.fn(async () => new Blob(["captured"], { type: "audio/wav" }))
    vi.spyOn(media, "startSpeechRecording").mockResolvedValue({ stop, cancel: vi.fn() })
    let recognize!: (text: string) => void
    vi.spyOn(speechAPI, "transcribeSpeech").mockImplementation(() => new Promise((resolve) => { recognize = resolve }))
    const send = vi.fn()
    render(element({ onSend: send }))
    const input = screen.getByTestId("composer-input")
    fireEvent.change(input, { target: { value: "before" } })
    fireEvent.click(screen.getByLabelText("Start voice input"))
    await screen.findByLabelText("Finish and transcribe")
    expect(screen.getByLabelText("Send")).toBeDisabled()
    fireEvent.click(screen.getByLabelText("Finish and transcribe"))
    await waitFor(() => expect(speechAPI.transcribeSpeech).toHaveBeenCalledOnce())
    fireEvent.change(input, { target: { value: "edited while waiting" } })
    fireEvent.keyDown(input, { key: "Enter", keyCode: 13 })
    expect(send).not.toHaveBeenCalled()
    await act(async () => { recognize("recognized words") })
    await waitFor(() => expect(input).toHaveValue("edited while waiting recognized words"))
    expect(send).not.toHaveBeenCalled()
    expect(stop).toHaveBeenCalledOnce()
  })

  it("cancels the old draft's request and ignores its late result after navigation", async () => {
    enabled()
    vi.spyOn(drafts, "useComposerDraft").mockReturnValue({ session: undefined, snapshot: undefined })
    vi.spyOn(media, "startSpeechRecording").mockResolvedValue({ stop: async () => new Blob(["audio"], { type: "audio/wav" }), cancel: vi.fn() })
    let resolve!: (text: string) => void
    let signal!: AbortSignal
    vi.spyOn(speechAPI, "transcribeSpeech").mockImplementation((_blob, passed) => { signal = passed; return new Promise((done) => { resolve = done }) })
    const view = render(element({ draftKey: "thread:first" }))
    fireEvent.click(screen.getByLabelText("Start voice input"))
    fireEvent.click(await screen.findByLabelText("Finish and transcribe"))
    await waitFor(() => expect(signal).toBeDefined())
    view.rerender(element({ draftKey: "thread:second" }))
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "second draft" } })
    expect(signal.aborted).toBe(true)
    await act(async () => { resolve("late words") })
    expect(screen.getByTestId("composer-input")).toHaveValue("second draft")
  })

  it("uses Escape to discard recording without calling ASR", async () => {
    enabled()
    const cancel = vi.fn()
    vi.spyOn(media, "startSpeechRecording").mockResolvedValue({ stop: vi.fn(), cancel })
    const recognize = vi.spyOn(speechAPI, "transcribeSpeech")
    render(element())
    fireEvent.click(screen.getByLabelText("Start voice input"))
    await screen.findByLabelText("Finish and transcribe")
    fireEvent.keyDown(window, { key: "Escape" })
    expect(cancel).toHaveBeenCalledOnce()
    expect(recognize).not.toHaveBeenCalled()
    expect(screen.getByLabelText("Start voice input")).toBeVisible()
  })
})
