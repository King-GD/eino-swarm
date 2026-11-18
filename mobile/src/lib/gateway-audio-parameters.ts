import { playableAudio, resolveAudioParameters, safeAudioOverrides, type AudioOverrides, type AudioParameterView, type AudioRole } from "../../../frontend/src/lib/audio-parameters"
import { speechRoute, type SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"

export const GATEWAY_AUDIO_PREFERENCES_EVENT = "zwai.gateway.audio.preferences"
const preferenceKey = (scope: string) => "zwai.phone.audio."+scope
export function saveGatewayAudioPreference(view: AudioParameterView, overrides: AudioOverrides): void {
 localStorage.setItem(preferenceKey(view.scope),JSON.stringify(overrides))
 window.dispatchEvent(new Event(GATEWAY_AUDIO_PREFERENCES_EVENT))
}
export function gatewayAudioView(session: GatewaySession, bootstrap: GatewayBootstrap, sourceID: string | undefined, model: string, role: AudioRole, inherited?: AudioOverrides): AudioParameterView | undefined {
 const kind = sourceID === bootstrap.models.source_id ? "preset" : sourceID && sourceID === bootstrap.gateway_models?.source_id ? "gateway" : undefined
 if (!kind) return
 const scope = JSON.stringify([session.origin,session.user.id,sourceID,kind,model,role])
 let overrides = inherited ?? {}
 try {
  const saved = localStorage.getItem(preferenceKey(scope))
  if (saved !== null) { const value:unknown = JSON.parse(saved); if (value && typeof value === "object" && !Array.isArray(value)) overrides=safeAudioOverrides(value) }
 } catch { /* A storage outage must not erase a PC's explicit preference. */ }
 const version = bootstrap.capabilities?.sources[kind]?.capabilities[role]?.audio_parameters_version
 const descriptor = version === 1 ? bootstrap.voice_parameters?.[sourceID!]?.[model] ?? {version:1,status:"unavailable"} : {version:0,status:"unsupported"}
 return {...descriptor,scope,overrides:{...overrides}}
}
export function applyGatewayAudio(snapshot: SpeechSnapshot, session: GatewaySession, bootstrap: GatewayBootstrap): SpeechSnapshot {
 const next:SpeechSnapshot = {...snapshot,input:{...snapshot.input},output:{...snapshot.output},limits:{...snapshot.limits}}
 for (const kind of ["input","output"] as const) {
  const route=speechRoute(next,kind), role=kind === "input" ? "transcription" : "speech"
  if (route?.execution !== "gateway" || route.gateway_origin !== session.origin) continue
  const slot=next[kind], view=gatewayAudioView(session,bootstrap,route.source_id,slot.model,role,slot.audio?.overrides)
  slot.audio=view
  if (!view) continue
  if (view.version !== 1 || view.status === "undeclared") {
   if (Object.keys(view.overrides).length) {slot.ready=false;slot.reason="invalid_audio_parameter"}
   continue
  }
  try {
   const {effective}=resolveAudioParameters(view,view.overrides)
   if (!playableAudio(effective,role)) throw new Error("invalid_audio_parameter")
   if (kind === "input") {
    next.input.language=effective.language ?? ""; next.input.prompt=effective.prompt ?? ""
    const maximum=view.parameters?.max_file_bytes
    if (maximum) next.limits.input_bytes=Math.min(next.limits.input_bytes,maximum)
   } else {
    next.output.voice=effective.voice ?? ""; next.output.speed=effective.speed ?? 1; next.output.instructions=effective.instructions ?? ""
    next.output.format=(effective.response_format ?? "mp3") as SpeechSnapshot["output"]["format"]
    const maximum=view.parameters?.max_input_characters
    if (maximum) next.limits.chunk_chars=Math.min(next.limits.chunk_chars,maximum)
   }
  } catch { slot.ready=false;slot.reason="invalid_audio_parameter" }
 }
 return next
}
