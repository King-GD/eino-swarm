import { expect, it } from "vitest"
import { audioOverridesWithoutControl, parseAudioDescriptor, playableAudio, resolveAudioParameters, safeAudioOverrides } from "./audio-parameters"
const raw = {version:1,revision:"r",status:"configured",parameters:{voice:{values:["calm","bright"],default:"calm",configurable:true},speed:{min:0.1,max:10,default:2,configurable:true},response_format:{values:["pcm"],default:"pcm",configurable:false},instructions:{max_characters:2,configurable:true},max_input_characters:12}}
it("keeps defaults off the wire and validates explicit Unicode choices without old speed limits",()=>{
 const d=parseAudioDescriptor(raw,"speech")
 expect(resolveAudioParameters(d,{})).toEqual({request:{},effective:{voice:"calm",speed:2,response_format:"pcm"}})
 expect(resolveAudioParameters(d,{voice:"bright",speed:8,instructions:"😀好"}).request).toEqual({voice:"bright",speed:8,instructions:"😀好"})
 for (const invalid of [{voice:"gone"},{speed:11},{speed:NaN},{instructions:"😀好啊"},{prompt:"x"},{response_format:"pcm"}]) expect(()=>resolveAudioParameters(d,invalid)).toThrow("invalid_audio_parameter")
 expect(playableAudio({response_format:"pcm",stream_format:"sse"},"speech")).toBe(true)
 expect(playableAudio({response_format:"wav",stream_format:"sse"},"speech")).toBe(false)
 expect(playableAudio({response_format:"text"},"transcription")).toBe(false)
})
it("does not enable malformed, legacy or unknown declarations",()=>{
 for (const value of [null,{...raw,version:2},{...raw,revision:""},{...raw,parameters:null},{...raw,parameters:{voice:{values:["a","a"],configurable:true}}},{...raw,parameters:{speed:{min:0,max:1,default:1,configurable:true}}},{...raw,parameters:{prompt:{max_characters:1,default:"😀好",configurable:true}}}]) expect(parseAudioDescriptor(value,"speech").status).toBe("unsupported")
 for (const status of ["undeclared","incompatible"]) expect(parseAudioDescriptor({...raw,status},"speech")).toEqual({version:1,revision:"r",status})
 expect(()=>resolveAudioParameters(parseAudioDescriptor({...raw,status:"incompatible"},"speech"),{})).toThrow("audio_configuration_unavailable")
 const required=parseAudioDescriptor({...raw,parameters:{voice:{values:["a"],configurable:true}}},"speech")
 expect(()=>resolveAudioParameters(required,{})).toThrow("invalid_audio_parameter")
})
it("keeps a reset only for values the visible control cannot clear",()=>{
 const parameters=parseAudioDescriptor(raw,"speech").parameters
 expect(audioOverridesWithoutControl(parameters,{voice:"bright",speed:8,instructions:""})).toEqual({})
 expect(audioOverridesWithoutControl(parameters,{voice:"bright",response_format:"pcm"})).toEqual({response_format:"pcm"})
 expect(audioOverridesWithoutControl(undefined,{voice:"gone"})).toEqual({voice:"gone"})
})
it("copies only bounded explicit preferences without trusting a foreign authority",()=>{
 expect(safeAudioOverrides({voice:"gone",speed:8,instructions:"",token:"private",url:"https://untrusted.test",parameters:raw})).toEqual({voice:"gone",speed:8,instructions:""})
 expect(safeAudioOverrides({speed:Infinity,prompt:"a".repeat(400001)})).toEqual({})
})
