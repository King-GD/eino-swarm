import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { setPhoneSpeechSource, acceptPhoneSpeechConfig, cancelPhoneSpeech } from "@/lib/phone-speech"
import type { RemoteLink } from "@/lib/link"
import type { RemoteResponse } from "@/lib/rpc"
import type { SavedLink } from "@/lib/store"
import { Composer } from "./composer"
import { renderBlock } from "./thread-blocks"

const media = vi.hoisted(() => ({ start: vi.fn(), stop: vi.fn(), cancel: vi.fn(), play: vi.fn(), stopPlaying: vi.fn() }))
vi.mock("../../../frontend/src/lib/speech-media", () => ({
  speechRecordingSupported: () => true,
  startSpeechRecording: media.start,
  speechPlayback: { play: media.play, stop: media.stopPlaying, subscribe: () => () => {}, getSnapshot: () => idle },
}))
const idle = { state: "idle" as const }
const pc: SavedLink = { fingerprint: "pc", hostPub: "key", hubURL: "https://hub.test", ticket: "ticket", sessionID: "session" }
const config: SpeechSnapshot = {
  execution: "pc_local",
  input: { enabled: true, ready: true, model: "local-asr", label: "ASR", language: "", prompt: "", recording_format: "auto", max_duration_seconds: 120, timeout_seconds: 120 },
  output: { enabled: true, ready: true, model: "local-tts", label: "TTS", voice: "", speed: 1, format: "wav", instructions: "", timeout_seconds: 120 },
  limits: { input_bytes: 1024 * 1024, output_bytes: 1024 * 1024, chunk_chars: 1000, staging_ttl_seconds: 300 },
}
const blob = Object.assign(new Blob([new Uint8Array([1, 2])], { type: "audio/wav" }), { arrayBuffer: async () => new Uint8Array([1, 2]).buffer })
let resolveText: ((text: string) => void) | undefined
let rpc: ReturnType<typeof vi.fn<RemoteLink["rpc"]>>
const link = () => ({ alive: () => true, rpc }) as unknown as RemoteLink

beforeEach(() => {
  media.start.mockReset(); media.stop.mockReset(); media.cancel.mockReset(); media.play.mockReset(); media.stopPlaying.mockClear()
  media.start.mockResolvedValue({ stop: media.stop, cancel: media.cancel })
  media.play.mockResolvedValue(undefined)
  media.stop.mockResolvedValue(blob)
  rpc = vi.fn<RemoteLink["rpc"]>(async (request) => {
    if (request.op === "audio_transcribe") return new Promise((resolve) => { resolveText = (text) => resolve({ ok: true, audio: { audio_id: request.audio_id!, text } } as RemoteResponse) })
    return { ok: true, audio: { audio_id: request.audio_id! } } as RemoteResponse
  })
  setPhoneSpeechSource(pc, link())
  acceptPhoneSpeechConfig(pc, config)
})
afterEach(() => { act(() => setPhoneSpeechSource(undefined, null)); resolveText = undefined })

describe("phone voice input and response actions", () => {
  it.each(["```go\nprivateCode()\n```", "```mermaid\ngraph TD; A-->B\n```"])("keeps Copy but hides Read when a completed reply contains only excluded code", (text) => {
    const view = render(<>{renderBlock({ id: "only-code", kind: "answer", text })}</>)
    expect(screen.getByRole("button", { name: "Copy response" })).toBeVisible()
    expect(screen.queryByRole("button", { name: "Read aloud" })).not.toBeInTheDocument()
    view.rerender(<>{renderBlock({ id: "only-code", kind: "answer", text: text + "\n\nReadable explanation." })}</>)
    expect(screen.getByRole("button", { name: "Read aloud" })).toBeVisible()
  })

  it("keeps the idle microphone inside the one-line composer instead of raising its top", () => {
    render(<Composer label="Message" sendLabel="Send" onSubmit={vi.fn()} />)
    const mic = screen.getByRole("button", { name: "Start voice input" })
    expect(mic.className).toContain("size-9")
    expect(mic.className).not.toContain("size-11")
    expect(screen.getByRole("button", { name: "Send" }).className).toContain("size-9")
  })

  it("keeps recording and transcription out of Send, and retains edits made while recognition is pending", async () => {
    const send = vi.fn()
    render(<Composer label="Message" sendLabel="Send" onSubmit={send} />)
    const box = screen.getByLabelText("Message")
    fireEvent.change(box, { target: { value: "original" } })
    fireEvent.click(screen.getByRole("button", { name: "Start voice input" }))
    await screen.findByRole("button", { name: "Finish and transcribe" })
    await waitFor(() => expect(screen.getByRole("button", { name: "Finish and transcribe" })).toBeEnabled())
    const recording = screen.getByRole("group", { name: "Voice input" })
    expect(recording.closest("form")).toBe(box.closest("form"))
    expect(recording).toBeVisible()
    expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Finish and transcribe" }))
    await waitFor(() => expect(resolveText).toBeTypeOf("function"))
    expect(screen.getByRole("status")).toHaveTextContent("Transcribing")
    expect(box).toBeVisible()
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
    fireEvent.change(box, { target: { value: "edited meanwhile" } })
    fireEvent.keyDown(box, { key: "Enter" })
    expect(send).not.toHaveBeenCalled()
    await act(async () => { resolveText?.("spoken text") })
    expect(box).toHaveValue("edited meanwhile spoken text")
    expect(send).not.toHaveBeenCalled()
    expect(rpc.mock.calls.at(-1)?.[0]).toMatchObject({ op: "audio_cancel" })
  })

  it("cancels the actual recording with Android Back and leaves the original draft intact", async () => {
    render(<Composer label="Message" sendLabel="Send" onSubmit={vi.fn()} />)
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "keep this draft" } })
    fireEvent.click(screen.getByRole("button", { name: "Start voice input" }))
    await waitFor(() => expect(screen.getByRole("button", { name: "Finish and transcribe" })).toBeEnabled())
    act(() => expect(window.__zwaiAndroidBack?.()).toBe(true))
    expect(screen.queryByRole("group", { name: "Voice input" })).not.toBeInTheDocument()
    expect(screen.getByLabelText("Message")).toHaveValue("keep this draft")
    expect(media.cancel).toHaveBeenCalled()
    expect(rpc).not.toHaveBeenCalled()
  })

  it("drops a late recognition result after the current PC changes", async () => {
    render(<Composer label="Message" sendLabel="Send" onSubmit={vi.fn()} />)
    fireEvent.click(screen.getByRole("button", { name: "Start voice input" }))
    await waitFor(() => expect(screen.getByRole("button", { name: "Finish and transcribe" })).toBeEnabled())
    fireEvent.click(screen.getByRole("button", { name: "Finish and transcribe" }))
    await waitFor(() => expect(resolveText).toBeTypeOf("function"))
    act(() => setPhoneSpeechSource({ ...pc, fingerprint: "other" }, link()))
    await act(async () => { resolveText?.("must not arrive") })
    expect(screen.getByLabelText("Message")).toHaveValue("")
    expect(screen.queryByRole("button", { name: "Start voice input" })).not.toBeInTheDocument()
  })

  it("offers Copy and Read only on completed answers and gates ASR and TTS separately", () => {
    const view = render(<>{renderBlock({ id: "a", kind: "answer", text: "completed prose", streaming: true })}</>)
    expect(screen.queryByRole("button", { name: "Read aloud" })).not.toBeInTheDocument()
    view.rerender(<>{renderBlock({ id: "a", kind: "answer", text: "completed prose" })}</>)
    expect(screen.getByRole("button", { name: "Copy response" })).toBeVisible()
    fireEvent.click(screen.getByRole("button", { name: "Read aloud" }))
    expect(media.play).toHaveBeenCalledWith("a", "completed prose", expect.objectContaining({ chunkChars: 1000, synthesize: expect.any(Function) }))
    act(() => acceptPhoneSpeechConfig(pc, { ...config, output: { ...config.output, enabled: false } }))
    expect(screen.queryByRole("button", { name: "Read aloud" })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Copy response" })).toBeVisible()
    cancelPhoneSpeech()
  })
})
