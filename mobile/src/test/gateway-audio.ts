import type { GatewayBootstrap, GatewaySession } from "../lib/gateway-account"
export function gatewayAudioBootstrap(session: GatewaySession): GatewayBootstrap {
 const capability=(endpoint:string)=>({supported:true,available:true,configurable:false,endpoints:[endpoint]})
 return {
  user:session.user,device:session.device,heartbeat_interval:30,presence_ttl:90,
  models:{source_id:"gateway:preset",source_kind:"gateway",execution:"phone",label:"Gateway",api_access_enabled:true,api_base_url:session.origin+"/v1",catalog_url:session.origin+"/v1/models",client_catalog_url:session.origin+"/client/models/v1/preset/models",defaults:{speech:"speech",transcription:"transcription"}},
  pairlink:{enabled:false,hub_url:session.origin,redeem_url:session.origin+"/pairlink/v1/pairings/redeem",websocket_url:session.origin.replace(/^http/,"ws")+"/pairlink/v1/ws",info_url:session.origin+"/pairlink/v1/info"},
  capabilities:{version:1,model_roles_version:3,sources:{preset:{authorized:true,available:true,configurable:false,capabilities:{speech:capability("audio_speech"),transcription:capability("audio_transcriptions")}},gateway:{authorized:false,available:false,configurable:false,capabilities:{}}}},
 }
}
