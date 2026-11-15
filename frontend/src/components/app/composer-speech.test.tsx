import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { Composer } from "./composer"
import { SpeechReadButton } from "./speech-read-button"
import { TooltipProvider } from "@/components/ui/tooltip"
import { useApp } from "@/store/app"
import { useToasts } from "@/store/toasts"
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
afterEach(() => { media.speechPlayback.stop(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

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

  it.each(["cancel", "finish"] as const)("keeps a new recording when a %s old recorder reports a late error", async (exit) => {
    enabled()
    act(() => useToasts.getState().clear())
    const tracks = [{ stop: vi.fn() }, { stop: vi.fn() }]
    let grant = 0
    // Each stream retains its own track, just as getUserMedia does.
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn(async () => {
      const track = tracks[grant++]
      return { getTracks: () => [track] }
    }) } })
    vi.stubGlobal("AudioContext", undefined)
    const recorders: Recorder[] = []
    class Recorder {
      static isTypeSupported() { return true }
      state = "inactive"
      mimeType = "audio/webm"
      ondataavailable: ((event: { data: Blob }) => void) | null = null
      onstop: (() => void) | null = null
      onerror: (() => void) | null = null
      constructor() { recorders.push(this) }
      start() { this.state = "recording" }
      stop() {
        this.state = "inactive"
        queueMicrotask(() => { this.ondataavailable?.({ data: new Blob(["audio"]) }); this.onstop?.() })
      }
    }
    vi.stubGlobal("MediaRecorder", Recorder)
    const recognize = vi.spyOn(speechAPI, "transcribeSpeech").mockResolvedValue("first words")
    render(element())
    fireEvent.click(screen.getByLabelText("Start voice input"))
    await screen.findByLabelText("Finish and transcribe")
    fireEvent.click(screen.getByLabelText(exit === "cancel" ? "Cancel voice input" : "Finish and transcribe"))
    await waitFor(() => expect(screen.getByLabelText("Start voice input")).toBeEnabled())
    fireEvent.click(screen.getByLabelText("Start voice input"))
    await screen.findByLabelText("Finish and transcribe")
    expect(recorders).toHaveLength(2)
    // A queued browser event still belongs to its original recorder after a
    // cancel or successful transcription; it cannot discard the new audio.
    act(() => recorders[0].onerror?.())
    expect(screen.getByLabelText("Finish and transcribe")).toBeVisible()
    expect(tracks[1].stop).not.toHaveBeenCalled()
    expect(useToasts.getState().toasts).toHaveLength(0)
    expect(recognize).toHaveBeenCalledTimes(exit === "cancel" ? 0 : 1)
    // Errors from the active recorder still release its track and reach the
    // user's error state; the ownership guard must not swallow every error.
    act(() => recorders[1].onerror?.())
    await waitFor(() => expect(screen.getByLabelText("Start voice input")).toBeEnabled())
    expect(tracks[1].stop).toHaveBeenCalled()
    expect(useToasts.getState().toasts).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: "error", message: "Microphone recording failed." }),
    ]))
    expect(recognize).toHaveBeenCalledTimes(exit === "cancel" ? 0 : 1)
  })

  it("ignores an old capture error callback after a new recording owns the composer", async () => {
    enabled()
    const attempts: media.SpeechRecordingOptions[] = []
    const cancelNew = vi.fn()
    vi.spyOn(media, "startSpeechRecording").mockImplementation(async (options) => {
      attempts.push(options)
      return { stop: vi.fn(), cancel: attempts.length === 2 ? cancelNew : vi.fn() }
    })
    render(element())
    fireEvent.click(screen.getByLabelText("Start voice input"))
    await screen.findByLabelText("Finish and transcribe")
    fireEvent.click(screen.getByLabelText("Cancel voice input"))
    fireEvent.click(screen.getByLabelText("Start voice input"))
    await screen.findByLabelText("Finish and transcribe")
    act(() => attempts[0].onError?.(new Error("old recording error")))
    expect(screen.getByLabelText("Finish and transcribe")).toBeVisible()
    expect(cancelNew).not.toHaveBeenCalled()
  })
})
