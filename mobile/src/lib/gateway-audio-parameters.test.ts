import { beforeEach, expect, it, vi } from "vitest"
import { gatewayAudioBootstrap } from "../test/gateway-audio"
import { saveGatewaySession, type GatewaySession } from "./gateway-account"
import { discoverGatewayVoiceModels, gatewayVoiceSnapshot, type GatewayAudioParameters } from "./gateway-speech"
import { publishGatewayCapabilities, authoritativeGatewayAudio, GATEWAY_CAPABILITY_REFRESH_EVENT } from "./gateway-capabilities"
import { applyGatewayAudio, gatewayAudioView, saveGatewayAudioPreference } from "./gateway-audio-parameters"
import { gatewaySpeechCall } from "./speech-transport"
import { safeSpeechSnapshot } from "./speech-cache"
const session:GatewaySession={origin:"https://voice.test",token:"agm_"+"a".repeat(64),user:{id:"u",username:"u",approval_status:"approved"},device:{id:"p",expires_at:"2099-01-01T00:00:00Z"}}
const descriptor={version:1,revision:"r1",status:"configured",parameters:{voice:{values:["a","b"],default:"a",configurable:true},response_format:{values:["pcm"],default:"pcm",configurable:false},stream_format:{values:["audio"],default:"audio",configurable:false},speed:{min:0.1,max:10,default:2,configurable:true},max_input_characters:4}}
function setup(){const boot=gatewayAudioBootstrap(session);boot.capabilities!.sources.preset!.capabilities.speech!.audio_parameters_version=1;boot.voice_parameters={"gateway:preset":{speech:descriptor}};publishGatewayCapabilities(session,boot);return boot}
beforeEach(()=>{localStorage.clear();saveGatewaySession(session)})
it("fetches private audio declarations even when a fixed public role already has a default",async()=>{
 const boot=setup(), parameters:GatewayAudioParameters={}
 const read=vi.fn(async()=>[{id:"speech",model_type:"speech",audio:descriptor}])
 expect((await discoverGatewayVoiceModels(boot,read,parameters))["gateway:preset"].speech).toBe("speech")
 expect(read).toHaveBeenCalledWith(boot.models.client_catalog_url)
 expect(parameters["gateway:preset"].speech).toEqual(descriptor)
})
it("scopes explicit preferences and preserves invalid choices instead of changing voice",()=>{
 const boot=setup(),base=gatewayVoiceSnapshot(session,boot)
 const view=gatewayAudioView(session,boot,"gateway:preset","speech","speech")!
 saveGatewayAudioPreference(view,{voice:"b",speed:8})
 const active=applyGatewayAudio(base,session,boot)
 expect(active.output).toMatchObject({ready:true,voice:"b",speed:8,format:"pcm"})
 expect(active.limits.chunk_chars).toBe(4)
 const cached=safeSpeechSnapshot(active)!
 expect(cached.output.audio?.overrides).toEqual({voice:"b",speed:8})
 expect(cached.output.audio?.parameters).toBeUndefined()
 const other={...session,user:{...session.user,id:"other"}}
 expect(gatewayAudioView(other,boot,"gateway:preset","speech","speech")?.overrides).toEqual({})
 expect(gatewayAudioView(session,boot,"gateway:preset","different","speech")?.overrides).toEqual({})
 boot.voice_parameters!["gateway:preset"].speech={...descriptor,revision:"r2",parameters:{...descriptor.parameters,voice:{values:["a"],default:"a",configurable:true}}}
 expect(applyGatewayAudio(base,session,boot).output).toMatchObject({ready:false,reason:"invalid_audio_parameter",audio:{overrides:{voice:"b",speed:8}}})
 saveGatewayAudioPreference(view,{})
 expect(applyGatewayAudio(base,session,boot).output.voice).toBe("a")
})
it("uses the phone's declaration and omits defaults in a real direct request",async()=>{
 const boot=setup(),snapshot=gatewayVoiceSnapshot(session,boot)
 const fetcher=vi.fn<typeof fetch>(async()=>new Response(new Uint8Array([0,1]),{headers:{"Content-Type":"audio/pcm"}}))
 const audio=await gatewaySpeechCall(snapshot,session,"speak","😀好",new AbortController().signal,fetcher)
 expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({model:"speech",input:"😀好"})
 if(typeof audio!=="string" && "chunks" in audio){for await(const _ of audio.chunks){void _};audio.cancel()}
 expect(authoritativeGatewayAudio({...session,user:{...session.user,id:"other"}},snapshot)).toBeUndefined()
})
it.each(["invalid_audio_parameter","audio_parameters_changed"])("refreshes on %s without replaying the request",async code=>{
 const boot=setup(),snapshot=gatewayVoiceSnapshot(session,boot),refresh=vi.fn()
 window.addEventListener(GATEWAY_CAPABILITY_REFRESH_EVENT,refresh)
 try {
  const fetcher=vi.fn<typeof fetch>(async()=>new Response(JSON.stringify({error:{code,message:"private"}}),{status:409}))
  await expect(gatewaySpeechCall(snapshot,session,"speak","ok",new AbortController().signal,fetcher)).rejects.toMatchObject({code:"invalid_audio_parameter",message:"invalid_audio_parameter"})
  expect(fetcher).toHaveBeenCalledOnce();expect(refresh).toHaveBeenCalledOnce()
 } finally {window.removeEventListener(GATEWAY_CAPABILITY_REFRESH_EVENT,refresh)}
})

it("does not replace an invalid PC choice with automatic gateway defaults",async()=>{
 const {selectPhoneSpeech}=await import("./phone-speech-selection")
 const boot=setup(),automatic=applyGatewayAudio(gatewayVoiceSnapshot(session,boot),session,boot)
 const preferred={...automatic,output:{...automatic.output,ready:false,reason:"invalid_audio_parameter",audio:{...automatic.output.audio!,overrides:{voice:"gone"}}}}
 const chosen=selectPhoneSpeech(automatic,preferred,false,true,()=>true)
 expect(chosen.snapshot!.output.audio?.overrides).toEqual({voice:"gone"})
 expect(authoritativeGatewayAudio(session,chosen.snapshot!)!.output.ready).toBe(false)
 const view=gatewayAudioView(session,boot,"gateway:preset","speech","speech")!
 saveGatewayAudioPreference(view,{voice:"b"})
 delete boot.voice_parameters
 publishGatewayCapabilities(session,boot)
 expect(authoritativeGatewayAudio(session,automatic)!.output).toMatchObject({ready:false,audio:{overrides:{voice:"b"}}})
})
