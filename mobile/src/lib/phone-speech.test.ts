import { act, renderHook, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { saveGatewaySession, clearGatewaySession, loadGatewaySession } from "./gateway-account"
import { acceptPhoneSpeechConfig, setPhoneSpeechSource, transcribePhoneSpeech, synthesizePhoneSpeech, usePhoneSpeech, usePhoneSpeechSync } from "./phone-speech"
import type { SavedLink } from "./store"
import type { RemoteLink } from "./link"
import type { RemoteResponse } from "./rpc"
import { loadSpeechCache } from "./speech-cache"
import { notifyGatewayRefusal } from "./gateway-authority"

const pc: SavedLink = { fingerprint: "pc-a", hostPub: "host", hubURL: "https://hub.test", ticket: "ticket", sessionID: "session", gatewayOwnerID: "owner", gatewayMobileID: "mobile", gatewayOrigin: "https://gateway.test" }
const speech: SpeechSnapshot = {
  execution: "gateway", gateway_origin: "https://gateway.test",
  input: { enabled: true, ready: true, model: "transcription", label: "ASR", language: "", prompt: "", recording_format: "auto", max_duration_seconds: 120, timeout_seconds: 120 },
  output: { enabled: false, ready: true, model: "speech", label: "TTS", voice: "", speed: 1, format: "mp3", instructions: "", timeout_seconds: 120 },
  limits: { input_bytes: 1024, output_bytes: 1024, chunk_chars: 1000, staging_ttl_seconds: 300 },
}
const signIn = (owner = "owner") => saveGatewaySession({ origin: speech.gateway_origin!, token: "agm_" + "a".repeat(64), user: { id: owner, username: owner, approval_status: "approved" }, device: { id: "mobile", expires_at: "2099-01-01T00:00:00Z" } })
afterEach(() => { act(() => setPhoneSpeechSource(undefined, null)); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe("phone speech capability follows the selected PC", () => {
  it.each(["transcribe", "speak"] as const)("keeps an ongoing Gateway %s request and recording epoch through a same-PC disconnect/reconnect", async (operation) => {
    signIn()
    const config = { ...speech, output: { ...speech.output, enabled: true } }
    const rpc = vi.fn<RemoteLink["rpc"]>().mockResolvedValue({ ok: true, speech: config } as RemoteResponse)
    let alive = true
    const original = { alive: () => alive, rpc } as unknown as RemoteLink
    const view = renderHook(({ link }: { link: RemoteLink | null }) => { usePhoneSpeechSync(link, pc, [pc]); return usePhoneSpeech() }, { initialProps: { link: original as RemoteLink | null } })
    await waitFor(() => expect(view.result.current.inputReady).toBe(true))
    const epoch = view.result.current.epoch
    let release!: (response: Response) => void
    let requestSignal!: AbortSignal
    const fetcher = vi.fn<typeof fetch>((_url, init) => { requestSignal = init!.signal as AbortSignal; return new Promise((resolve) => { release = resolve }) })
    vi.stubGlobal("fetch", fetcher)
    const cancelled = vi.fn()
    window.addEventListener("zwai.phone.speech.cancel", cancelled)
    try {
      const work = (operation === "transcribe" ? transcribePhoneSpeech(new Blob(["recording"], { type: "audio/wav" }), new AbortController().signal)
        : synthesizePhoneSpeech("public reply", new AbortController().signal)).then((value) => ({ value, error: undefined }), (error: unknown) => ({ value: undefined, error }))
      await waitFor(() => expect(fetcher).toHaveBeenCalledOnce())
      alive = false
      view.rerender({ link: null })
      const offline = { ...view.result.current }
      const replacement = { alive: () => true, rpc } as unknown as RemoteLink
      view.rerender({ link: replacement })
      await waitFor(() => expect(rpc).toHaveBeenCalledTimes(2))
      const abortedOnReconnect = requestSignal.aborted
      const cancelCount = cancelled.mock.calls.length
      const reconnectedEpoch = view.result.current.epoch
      release(operation === "transcribe" ? new Response(JSON.stringify({ text: "recognized" }), { headers: { "Content-Type": "application/json" } })
        : new Response(new Uint8Array([82, 73, 70, 70]), { headers: { "Content-Type": "audio/wav" } }))
      const outcome = await work
      expect(offline).toMatchObject({ epoch, connected: false, inputReady: true, outputReady: true })
      expect(reconnectedEpoch).toBe(epoch)
      expect(abortedOnReconnect).toBe(false)
      expect(cancelCount).toBe(0)
      expect(outcome.error).toBeUndefined()
      if (operation === "transcribe") expect(outcome.value).toBe("recognized")
      else expect(outcome.value).toMatchObject({ type: "audio/wav", size: 4 })
      expect(fetcher).toHaveBeenCalledOnce()
      expect(fetcher.mock.calls[0][1]?.headers).toMatchObject({ Authorization: "Bearer agm_" + "a".repeat(64) })
      expect(rpc.mock.calls.every(([request]) => request.op === "speech_config")).toBe(true)
    } finally { window.removeEventListener("zwai.phone.speech.cancel", cancelled); view.unmount() }
  })

  it("keeps recording and the Gateway request alive during an ordinary account health refresh", async () => {
    signIn()
    const rpc = vi.fn<RemoteLink["rpc"]>().mockResolvedValue({ ok: true, speech } as RemoteResponse)
    const link = { alive: () => true, rpc } as unknown as RemoteLink
    const view = renderHook(() => { usePhoneSpeechSync(link, pc, [pc]); return usePhoneSpeech() })
    await waitFor(() => expect(view.result.current.inputReady).toBe(true))
    const epoch = view.result.current.epoch
    let release!: (response: Response) => void
    let requestSignal!: AbortSignal
    const fetcher = vi.fn<typeof fetch>((_url, init) => { requestSignal = init!.signal as AbortSignal; return new Promise((resolve) => { release = resolve }) })
    vi.stubGlobal("fetch", fetcher)
    const work = transcribePhoneSpeech(new Blob(["recording"], { type: "audio/wav" }), new AbortController().signal)
      .then((value) => ({ value, error: undefined }), (error: unknown) => ({ value: undefined, error }))
    await waitFor(() => expect(requestSignal).toBeDefined())
    act(() => {
      const current = loadGatewaySession()!
      // The real 30-second account refresh republishes this same authority.
      saveGatewaySession({ ...current, user: { ...current.user, avatar_url: "https://avatar.invalid/updated" }, device: { ...current.device, expires_at: "2099-02-01T00:00:00Z" } })
    })
    const cancelled = requestSignal.aborted
    const refreshedEpoch = view.result.current.epoch
    release(new Response(JSON.stringify({ text: "recognized" }), { headers: { "Content-Type": "application/json" } }))
    const outcome = await work
    expect(cancelled).toBe(false)
    expect(refreshedEpoch).toBe(epoch)
    expect(outcome.error).toBeUndefined()
    expect(outcome.value).toBe("recognized")
    expect(fetcher).toHaveBeenCalledOnce()
    view.unmount()
  })

  it.each(["configuration", "pairing", "account", "credential", "approval", "refusal", "PC"] as const)("still cancels an ongoing Gateway request when its %s changes", async (change) => {
    signIn()
    const rpc = vi.fn<RemoteLink["rpc"]>().mockResolvedValue({ ok: true, speech } as RemoteResponse)
    const link = { alive: () => true, rpc } as unknown as RemoteLink
    const view = renderHook(({ selected }: { selected: SavedLink }) => { usePhoneSpeechSync(link, selected, [selected]); return usePhoneSpeech() }, { initialProps: { selected: pc } })
    await waitFor(() => expect(view.result.current.inputReady).toBe(true))
    let release!: (response: Response) => void
    let requestSignal!: AbortSignal
    vi.stubGlobal("fetch", vi.fn<typeof fetch>((_url, init) => { requestSignal = init!.signal as AbortSignal; return new Promise((resolve) => { release = resolve }) }))
    const work = transcribePhoneSpeech(new Blob(["recording"], { type: "audio/wav" }), new AbortController().signal)
      .then((value) => ({ value, error: undefined }), (error: unknown) => ({ value: undefined, error }))
    await waitFor(() => expect(requestSignal).toBeDefined())
    await act(async () => {
      if (change === "configuration") acceptPhoneSpeechConfig(pc, { ...speech, input: { ...speech.input, enabled: false } })
      else if (change === "account") clearGatewaySession()
      else if (change === "credential") saveGatewaySession({ ...loadGatewaySession()!, token: "agm_" + "b".repeat(64) })
      else if (change === "approval") { const current = loadGatewaySession()!; saveGatewaySession({ ...current, user: { ...current.user, approval_status: undefined } }) }
      else if (change === "refusal") notifyGatewayRefusal(loadGatewaySession()!, 403)
      else view.rerender({ selected: change === "pairing" ? { ...pc, ticket: "new-grant" } : { ...pc, fingerprint: "different-PC" } })
    })
    const cancelled = requestSignal.aborted
    release(new Response(JSON.stringify({ text: "late response" }), { headers: { "Content-Type": "application/json" } }))
    const outcome = await work
    expect(cancelled).toBe(true)
    expect(outcome.error).toMatchObject({ name: "AbortError" })
    expect(outcome.value).toBeUndefined()
    view.unmount()
  })

  it("cancels local model execution when the selected PC link is replaced", async () => {
    const config = { ...speech, execution: "pc_local" as const }
    let finish!: (response: RemoteResponse) => void
    let requestSignal!: AbortSignal
    let alive = true
    const rpc = vi.fn<RemoteLink["rpc"]>(async (request, options) => {
      if (request.op === "speech_config") return { ok: true, speech: config } as RemoteResponse
      if (request.op === "audio_transcribe") {
        requestSignal = options!.signal!
        return await new Promise<RemoteResponse>((resolve) => { finish = resolve })
      }
      return { ok: true } as RemoteResponse
    })
    const link = { alive: () => alive, rpc } as unknown as RemoteLink
    const view = renderHook(({ current }: { current: RemoteLink | null }) => { usePhoneSpeechSync(current, pc, [pc]); return usePhoneSpeech() }, { initialProps: { current: link as RemoteLink | null } })
    await waitFor(() => expect(view.result.current.inputReady).toBe(true))
    const bytes = new Uint8Array([82, 73, 70, 70])
    const recording = Object.assign(new Blob([bytes], { type: "audio/wav" }), { arrayBuffer: async () => bytes.buffer })
    const work = transcribePhoneSpeech(recording, new AbortController().signal).then((value) => ({ value, error: undefined }), (error: unknown) => ({ value: undefined, error }))
    await waitFor(() => expect(finish).toBeTypeOf("function"))
    alive = false
    view.rerender({ current: null })
    const cancelled = requestSignal.aborted
    const request = rpc.mock.calls.find(([request]) => request.op === "audio_transcribe")![0]
    finish({ ok: true, audio: { audio_id: request.audio_id!, text: "late local text" } } as RemoteResponse)
    const outcome = await work
    expect(cancelled).toBe(true)
    expect(outcome.error).toMatchObject({ name: "AbortError" })
    expect(view.result.current.inputReady).toBe(false)
    view.unmount()
  })

  it("keeps synced Gateway speech available with the PC offline, but never another account's credential", () => {
    signIn()
    setPhoneSpeechSource(pc, null)
    acceptPhoneSpeechConfig(pc, speech)
    const view = renderHook(() => usePhoneSpeech())
    expect(view.result.current).toMatchObject({ inputReady: true, outputReady: false, connected: false })
    act(() => setPhoneSpeechSource(undefined, null))
    act(() => setPhoneSpeechSource(pc, null))
    expect(view.result.current.inputReady).toBe(true)
    act(() => { clearGatewaySession(); signIn("different-owner"); setPhoneSpeechSource(pc, null) })
    expect(view.result.current.inputReady).toBe(false)
  })

  it("requires the selected PC's live link for local execution and never falls back to a cached Gateway", () => {
    signIn()
    setPhoneSpeechSource(pc, null)
    acceptPhoneSpeechConfig(pc, { ...speech, execution: "pc_local" })
    const view = renderHook(() => usePhoneSpeech())
    expect(view.result.current.inputReady).toBe(false)
    act(() => setPhoneSpeechSource(pc, { alive: () => true } as RemoteLink))
    expect(view.result.current.inputReady).toBe(true)
    act(() => setPhoneSpeechSource({ ...pc, fingerprint: "pc-b" }, null))
    expect(view.result.current.snapshot).toBeUndefined()
    expect(view.result.current.inputReady).toBe(false)
  })

  it("reads current configuration over the real link interface and removes executable cache for an old host", async () => {
    signIn()
    const rpc = vi.fn<RemoteLink["rpc"]>().mockResolvedValue({ ok: true, speech } as RemoteResponse)
    const link = { alive: () => true, rpc } as unknown as RemoteLink
    const view = renderHook(() => { usePhoneSpeechSync(link, pc, [pc]); return usePhoneSpeech() })
    await waitFor(() => expect(view.result.current.inputReady).toBe(true))
    expect(rpc).toHaveBeenCalledWith({ op: "speech_config" }, expect.objectContaining({ dropOnTimeout: false }))
    view.unmount()
    const old = { alive: () => true, rpc: vi.fn().mockResolvedValue({ ok: false, code: "unknown_op" }) } as unknown as RemoteLink
    const legacy = renderHook(() => { usePhoneSpeechSync(old, pc, [pc]); return usePhoneSpeech() })
    await waitFor(() => expect(legacy.result.current.snapshot).toBeUndefined())
  })

  it("revokes a cached executable snapshot when the live PC publishes invalid configuration", () => {
    signIn()
    setPhoneSpeechSource(pc, null)
    acceptPhoneSpeechConfig(pc, speech)
    const view = renderHook(() => usePhoneSpeech())
    expect(view.result.current.inputReady).toBe(true)
    act(() => { expect(acceptPhoneSpeechConfig(pc, { ...speech, input: { ...speech.input, timeout_seconds: -1 } })).toBe(false) })
    expect(view.result.current.inputReady).toBe(false)
    expect(view.result.current.snapshot).toBeUndefined()
    expect(loadSpeechCache(pc)).toBeUndefined()
  })

  it("does not keep an old enabled cache after the live PC refuses an oversized configuration", async () => {
    signIn()
    setPhoneSpeechSource(pc, null)
    acceptPhoneSpeechConfig(pc, speech)
    const rpc = vi.fn<RemoteLink["rpc"]>().mockResolvedValue({ ok: false, code: "audio_config_limit" } as RemoteResponse)
    const link = { alive: () => true, rpc } as unknown as RemoteLink
    const view = renderHook(() => { usePhoneSpeechSync(link, pc, [pc]); return usePhoneSpeech() })
    await waitFor(() => expect(rpc).toHaveBeenCalled())
    await waitFor(() => expect(view.result.current.snapshot).toBeUndefined())
    expect(loadSpeechCache(pc)).toBeUndefined()
  })
})
