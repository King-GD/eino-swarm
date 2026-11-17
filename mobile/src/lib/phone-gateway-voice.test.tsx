import { PROTOCOL_V } from "./rpc"
import { act, renderHook, waitFor } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { defaultSpeechSettings, type SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { clearGatewaySession, loadGatewaySession, saveGatewaySession, type GatewayBootstrap } from "./gateway-account"
import { publishGatewayCapabilities } from "./gateway-capabilities"
import { acceptPhoneSpeechConfig, setPhoneSpeechSource, synthesizePhoneSpeech, transcribePhoneSpeech, usePhoneSpeech, usePhoneSpeechSync } from "./phone-speech"
import type { RemoteLink } from "./link"
import type { SavedLink } from "./store"

const pc: SavedLink = { fingerprint: "pc", hubURL: "https://gateway.test", hostPub: "host", ticket: "ticket", sessionID: "session" }
function signIn() {
  saveGatewaySession({ origin: "https://gateway.test", token: "agm_"+"a".repeat(64), user: { id: "owner", username: "owner", approval_status: "approved" }, device: { id: "phone", expires_at: "2099-01-01T00:00:00Z" } })
  const role = (endpoint: string) => ({ supported:true, available:true, configurable:true, endpoints:[endpoint] })
  const session = loadGatewaySession()!
  const bootstrap: GatewayBootstrap = {
    user: session.user, device: session.device, heartbeat_interval: 30, presence_ttl: 90,
    pairlink: { enabled: false, hub_url: session.origin, redeem_url: session.origin + "/pairlink/v1/pairings/redeem", websocket_url: "wss://gateway.test/pairlink/v1/ws", info_url: session.origin + "/pairlink/v1/info" },
    models: {
      source_id: "gateway:preset", source_kind: "gateway", execution: "phone", label: "Preset", api_access_enabled: true, catalog_url: session.origin + "/v1/models", api_base_url: "https://gateway.test/v1",
      defaults: { transcription: "recognition-model", speech: "spoken-model" },
    },
    capabilities: {
      version: 1, model_roles_version: 3,
      sources: {
        preset: {
          authorized: true, available: true, configurable: true,
          capabilities: { transcription: role("audio_transcriptions"), speech: role("audio_speech") },
        },
      },
    },
  }
  publishGatewayCapabilities(session, bootstrap)
}
function unconfigured(): SpeechSnapshot {
  const settings = defaultSpeechSettings()
  return { execution:"pc_local", input:{...settings.input, ready:false, label:""}, output:{...settings.output, ready:false, label:""}, limits:settings.limits }
}
afterEach(() => { act(() => { clearGatewaySession(); setPhoneSpeechSource(undefined,null) }); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

it("exposes complete Gateway recognition and playback when the PC has no voice configuration", async () => {
  signIn()
  const rpc = vi.fn<RemoteLink["rpc"]>(async () => { throw new Error("Voice inference cannot go through the PC") })
  setPhoneSpeechSource(pc, { alive:()=>true, rpc } as unknown as RemoteLink)
  acceptPhoneSpeechConfig(pc, unconfigured())
  const view = renderHook(() => usePhoneSpeech())
  expect(view.result.current).toMatchObject({ inputReady:true, outputReady:true })
  const requests: {url:string; body:unknown}[] = []
  vi.stubGlobal("fetch", vi.fn<typeof fetch>(async (url, init) => {
    requests.push({url:String(url),body:init?.body})
    return String(url).endsWith("transcriptions") ? new Response(JSON.stringify({text:"recognized words"}),{headers:{"Content-Type":"application/json"}}) : new Response(new Uint8Array([1,2]),{headers:{"Content-Type":"audio/mpeg"}})
  }))
  expect(await transcribePhoneSpeech(new Blob(["recording"],{type:"audio/wav"}),new AbortController().signal)).toBe("recognized words")
  expect((requests[0].body as FormData).get("model")).toBe("recognition-model")
  await synthesizePhoneSpeech("The complete reply.",new AbortController().signal)
  expect(JSON.parse(String(requests[1].body))).toMatchObject({model:"spoken-model",input:"The complete reply."})
  expect(requests.map(r=>r.url)).toEqual(["https://gateway.test/v1/audio/transcriptions","https://gateway.test/v1/audio/speech"])
  expect(rpc).not.toHaveBeenCalled()
  act(() => setPhoneSpeechSource(pc,null))
  expect(view.result.current).toMatchObject({inputReady:true,outputReady:true,connected:false})
  view.unmount()
})

it("does not interrupt independent Gateway dictation when optional PC metadata refreshes or disconnects", async () => {
  signIn()
  const rpc = vi.fn<RemoteLink["rpc"]>(async req => ({v:PROTOCOL_V,id:req.id??"config",ok:true,speech:unconfigured()}))
  const link = {alive:()=>true,rpc} as unknown as RemoteLink
  const view = renderHook(() => { usePhoneSpeechSync(link,pc,[pc]); return usePhoneSpeech() })
  await waitFor(() => expect(view.result.current.inputReady).toBe(true))
  let complete!: (response: Response)=>void
  let signal!: AbortSignal
  vi.stubGlobal("fetch", vi.fn<typeof fetch>(async (_url, init) => { signal=init!.signal as AbortSignal; return await new Promise<Response>(resolve=>{complete=resolve}) }))
  const work = transcribePhoneSpeech(new Blob(["recording"],{type:"audio/wav"}),new AbortController().signal)
  const epoch = view.result.current.epoch
  act(() => { for(let i=0;i<4;i++) acceptPhoneSpeechConfig(pc,unconfigured()); setPhoneSpeechSource(pc,null) })
  expect(view.result.current.epoch).toBe(epoch)
  expect(signal.aborted).toBe(false)
  complete(new Response(JSON.stringify({text:"complete"}),{headers:{"Content-Type":"application/json"}}))
  expect(await work).toBe("complete")
  view.unmount()
})
