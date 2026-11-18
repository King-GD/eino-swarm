import { expect, it, vi } from "vitest"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"
import { discoverGatewayVoiceModels, gatewayVoiceSnapshot } from "./gateway-speech"
import { selectPhoneSpeech } from "./phone-speech-selection"

const session = {origin:"https://gateway.test",token:"fixture",user:{id:"u",username:"u"},device:{id:"phone",expires_at:"2099-01-01T00:00:00Z"}} satisfies GatewaySession
function bootstrap(): GatewayBootstrap {
  const role = (endpoint:string)=>({supported:true,available:true,configurable:true,endpoints:[endpoint]})
  return {
    user: session.user, device: session.device, heartbeat_interval: 30, presence_ttl: 90,
    pairlink: { enabled: false, hub_url: session.origin, redeem_url: session.origin + "/pairlink/v1/pairings/redeem", websocket_url: "wss://gateway.test/pairlink/v1/ws", info_url: session.origin + "/pairlink/v1/info" },
    models: { source_id: "gateway:preset", source_kind: "gateway", execution: "phone", label: "Preset", api_access_enabled: true, api_base_url: session.origin + "/v1", defaults: {}, catalog_url: session.origin + "/v1/models", client_catalog_url: session.origin + "/client/models/v1/preset/models" },
    gateway_models: { source_id: "gateway:native", source_kind: "gateway", execution: "phone", label: "Gateway", api_access_enabled: true, api_base_url: session.origin + "/client/models/v1/gateway", defaults: { speech: "native-voice" }, catalog_url: session.origin + "/client/models/v1/gateway/models" },
    capabilities: { version: 1, model_roles_version: 3, sources: {
      preset: { authorized: true, available: true, configurable: true, capabilities: { transcription: role("audio_transcriptions"), speech: role("audio_speech") } },
      gateway: { authorized: false, available: false, configurable: false, capabilities: { speech: role("audio_speech") } },
    } },
  }
}

it("discovers typed voice models without requiring PC switches or guessing from model names",async()=>{
  const boot=bootstrap()
  const read=vi.fn(async()=>[{id:"speech-in-its-name",model_type:"chat"},{id:"recognized",model_type:"transcription"},{id:"spoken",model_type:"speech"}])
  boot.voice_models=await discoverGatewayVoiceModels(boot,read)
  const speech=gatewayVoiceSnapshot(session,boot)
  expect(read).toHaveBeenCalledOnce()
  expect(read).toHaveBeenCalledWith(session.origin+"/client/models/v1/preset/models")
  expect(speech.input).toMatchObject({enabled:true,ready:true,model:"recognized",source_id:"gateway:preset"})
  expect(speech.output).toMatchObject({enabled:true,ready:true,model:"spoken",source_id:"gateway:preset",format:"pcm"})
  const configured={...speech,input:{...speech.input,execution:"pc_local" as const,gateway_origin:undefined,enabled:true,model:"personal-asr",ready:true}}
  const selected=selectPhoneSpeech(speech,configured,false,true,()=>true)
  expect(selected.owners).toEqual({input:"pc",output:"gateway"})
  expect(selected.snapshot!.input.gateway_origin).toBeUndefined()
  expect(selected.snapshot!.output.model).toBe("spoken")
  const offline=selectPhoneSpeech(speech,configured,false,false,()=>true)
  expect(offline.snapshot!.input.ready).toBe(false)
  expect(offline.snapshot!.output.ready).toBe(true)
})

it("uses incremental audio for automatic Gateway voice while retaining explicit PC output settings",()=>{
  const boot=bootstrap()
  boot.models.defaults={speech:"spoken"}
  const automatic=gatewayVoiceSnapshot(session,boot)
  expect(automatic.output.format).toBe("pcm")
  expect(automatic.limits.chunk_chars).toBe(200)
  const preferred={...automatic,output:{...automatic.output,format:"mp3" as const},limits:{...automatic.limits,chunk_chars:800}}
  const selected=selectPhoneSpeech(automatic,preferred,false,true,()=>true)
  expect(selected.snapshot!.output.format).toBe("mp3")
  expect(selected.snapshot!.limits.chunk_chars).toBe(800)
  expect(selectPhoneSpeech(automatic,undefined,false,false,()=>true).snapshot!.output.format).toBe("pcm")
})

it("keeps source grants independent and refuses cached voice after source authorization is removed",async()=>{
  const boot=bootstrap()
  boot.models.defaults={transcription:"recognition"}
  boot.capabilities!.sources.preset!.capabilities.speech!.available=false
  boot.capabilities!.sources.gateway!.authorized=true
  boot.capabilities!.sources.gateway!.available=true
  const read=vi.fn(async()=>[])
  boot.voice_models=await discoverGatewayVoiceModels(boot,read)
  expect(read).not.toHaveBeenCalled()
  expect(gatewayVoiceSnapshot(session,boot).output).toMatchObject({ready:true,source_id:"gateway:native",model:"native-voice"})
  boot.capabilities!.sources.gateway!.authorized=false
  expect(gatewayVoiceSnapshot(session,boot).output.ready).toBe(false)
  expect(gatewayVoiceSnapshot(session,boot).input.ready).toBe(true)
})

it("retains discovered names only during a temporary catalog outage, never a credential or grant denial",async()=>{
  const boot=bootstrap()
  boot.voice_models={"gateway:preset":{speech:"retained"}}
  expect((await discoverGatewayVoiceModels(boot,async()=>{throw new TypeError("network")}))["gateway:preset"].speech).toBe("retained")
  expect((await discoverGatewayVoiceModels(boot,async()=>{throw {status:403}}))["gateway:preset"].speech).toBeUndefined()
  await expect(discoverGatewayVoiceModels(boot,async()=>{throw {status:401}})).rejects.toMatchObject({status:401})
})
