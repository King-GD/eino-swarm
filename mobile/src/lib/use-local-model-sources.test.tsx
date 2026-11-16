import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { clearGatewaySession, loadGatewaySession, saveGatewaySession, type GatewayBootstrap } from "./gateway-account"
import { useLocalModelSources } from "./use-local-model-sources"
import { setLocale } from "./i18n"
import { streamCompletion } from "./openai-client"

const origin="https://sources.invalid"
const session={origin,token:"agm_"+"a".repeat(64),user:{id:"owner",username:"Owner",approval_status:"approved"},device:{id:"phone",expires_at:"2099-01-01T00:00:00Z"}}
const bootstrap:GatewayBootstrap={user:session.user,device:session.device,models:{source_id:"gateway:preset",source_kind:"gateway",label:"Partner",execution:"phone",api_base_url:origin+"/v1",catalog_url:origin+"/v1/models",client_catalog_url:origin+"/client/models/v1/preset/models",api_access_enabled:true,configurable:false,roles:[{id:"conversation",enabled:true,label:"Partner chat"}]},gateway_models:{source_id:"gateway:global",source_kind:"gateway",label:"Main",execution:"phone",api_base_url:origin+"/client/models/v1/gateway",catalog_url:origin+"/client/models/v1/gateway/models",api_access_enabled:true},pairlink:{enabled:false,hub_url:origin,redeem_url:origin+"/pairlink/v1/pairings/redeem",websocket_url:origin.replace("https:","wss:")+"/pairlink/v1/ws",info_url:origin+"/pairlink/v1/info"},heartbeat_interval:30,presence_ttl:90}
const response=(body:unknown,status=200)=>new Response(JSON.stringify(body),{status,headers:{"Content-Type":"application/json"}})
beforeEach(()=>{setLocale("en");saveGatewaySession(session)})

describe("LOCAL account model sources",()=>{
 it("aggregates identical model names without merging their routes or groups",async()=>{
  const calls:{url:string;body?:unknown}[]=[]
  vi.stubGlobal("fetch",vi.fn(async(input,init)=>{
   const url=String(input);calls.push({url,body:init?.body?JSON.parse(String(init.body)):undefined})
   expect((init?.headers as Record<string,string>).Authorization).toBe("Bearer "+session.token)
   return url.endsWith("/models")?response({data:[{id:"conversation"}]}):response({choices:[{message:{content:"routed reply"}}]})
  }))
  const hook=renderHook(()=>useLocalModelSources("local","authorized",session,bootstrap))
  await waitFor(()=>expect(hook.result.current.providers).toHaveLength(2))
  expect(hook.result.current.providers.map(row=>[row.id,row.label])).toEqual([["gateway:preset","Third-party · Partner"],["gateway:global","Gateway · Main"]])
  for(const provider of hook.result.current.providers){
   await streamCompletion({provider,model:"conversation",reasoning:"",turns:[{role:"user",parts:[{kind:"text",text:"routing fixture"}]}],signal:new AbortController().signal,onDelta:()=>undefined})
  }
  expect(calls.filter(row=>row.body).map(row=>row.url)).toEqual([origin+"/v1/chat/completions",origin+"/client/models/v1/gateway/chat/completions"])
  expect(localStorage.getItem("zwai.phone.providers")).toBeNull()
 })
 it("hides an independently forbidden source without expiring the login or retrying forever",async()=>{
  const fetcher=vi.fn(async(input:RequestInfo|URL)=>String(input).includes("/gateway/")?response({error:{message:"denied"}},403):response({data:[{id:"conversation"}]}))
  vi.stubGlobal("fetch",fetcher)
  const hook=renderHook(()=>useLocalModelSources("local","authorized",session,bootstrap))
  await waitFor(()=>expect(hook.result.current.busy).toBe(false))
  expect(hook.result.current.providers.map(row=>row.id)).toEqual(["gateway:preset"])
  expect(hook.result.current.error).toBe(false)
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(loadGatewaySession()?.token).toBe(session.token)
 })
 it("refreshes only model sources after Gateway inference loses its grant",async()=>{
  let denied=false
  vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>String(input).includes("/gateway/")&&denied?response({error:{message:"denied"}},403):response({data:[{id:"conversation"}]})))
  const hook=renderHook(()=>useLocalModelSources("local","authorized",session,bootstrap))
  await waitFor(()=>expect(hook.result.current.providers).toHaveLength(2))
  const provider=hook.result.current.providers[1];denied=true
  await act(async()=>{await expect(streamCompletion({provider,model:"conversation",reasoning:"",turns:[{role:"user",parts:[{kind:"text",text:"revocation fixture"}]}],signal:new AbortController().signal,onDelta:()=>undefined})).rejects.toMatchObject({ status: 403 })})
  await waitFor(()=>expect(hook.result.current.providers.map(row=>row.id)).toEqual(["gateway:preset"]))
  expect(loadGatewaySession()?.token).toBe(session.token)
 })
 it("retains same-account models through a transient discovery failure and accepts an empty source",async()=>{
  let code=200
  vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>String(input).includes("/gateway/")?response(code===200?{data:[{id:"global-model"}]}:{error:{message:"unavailable"}},code):response({data:[]})))
  const hook=renderHook(()=>useLocalModelSources("local","authorized",session,bootstrap))
  await waitFor(()=>expect(hook.result.current.providers).toHaveLength(1))
  code=503;act(()=>hook.result.current.refresh())
  await waitFor(()=>expect(hook.result.current.error).toBe(true))
  expect(hook.result.current.providers[0].catalog).toEqual(["global-model"])
 })
 it("drops granted models immediately on mode change, revocation and logout",async()=>{
  const fetcher=vi.fn(async()=>response({data:[{id:"conversation"}]}));vi.stubGlobal("fetch",fetcher)
  const hook=renderHook(({mode,boot,account})=>useLocalModelSources(mode,"authorized",account,boot),{initialProps:{mode:"local" as "local"|"account",boot:bootstrap,account:session as typeof session|null}})
  await waitFor(()=>expect(hook.result.current.providers).toHaveLength(2))
  hook.rerender({mode:"local",boot:{...bootstrap,gateway_models:{...bootstrap.gateway_models!,api_access_enabled:false}},account:session})
  expect(hook.result.current.providers.map(row=>row.id)).toEqual(["gateway:preset"])
  hook.rerender({mode:"account",boot:bootstrap,account:session})
  expect(hook.result.current.providers).toEqual([])
  clearGatewaySession();hook.rerender({mode:"local",boot:bootstrap,account:null})
  expect(hook.result.current.providers).toEqual([])
 })
 it("keeps the Gateway catalog when the preset is denied and never retries the refusal",async()=>{
  const fetcher=vi.fn(async(input:RequestInfo|URL)=>String(input).includes("/preset/")?response({},403):response({data:[{id:"global-model"}]}))
  vi.stubGlobal("fetch",fetcher)
  const hook=renderHook(()=>useLocalModelSources("local","authorized",session,bootstrap))
  await waitFor(()=>expect(hook.result.current.providers.map(row=>row.id)).toEqual(["gateway:global"]))
  expect(hook.result.current.error).toBe(false)
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(loadGatewaySession()?.token).toBe(session.token)
 })
 it("fetches an authorized Gateway source even when the primary preset has no API access",async()=>{
  const fetcher=vi.fn(async()=>response({data:[{id:"global-model"}]}));vi.stubGlobal("fetch",fetcher)
  const deniedPreset={...bootstrap,models:{...bootstrap.models,api_access_enabled:false}}
  const hook=renderHook(()=>useLocalModelSources("local","authorized",session,deniedPreset))
  await waitFor(()=>expect(hook.result.current.providers.map(row=>row.id)).toEqual(["gateway:global"]))
  expect(fetcher).toHaveBeenCalledTimes(1)
 })

})
