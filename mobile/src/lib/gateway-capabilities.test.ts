import { expect, it } from "vitest"
import type { SourceCapabilities } from "../../../frontend/src/lib/capabilities"
import { gatewayCapabilityAllows, gatewayCapabilityTarget, invalidateGatewayCapabilities, publishGatewayCapabilities } from "./gateway-capabilities"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"

it("binds direct voice to this phone's device, source and negotiated endpoint", () => {
  const session = { origin: "https://gateway.example.test", token: "private-fixture", user: { id: "user", username: "fixture" }, device: { id: "phone", expires_at: "2099-01-01T00:00:00Z" } } satisfies GatewaySession
  const source: SourceCapabilities = { authorized: true, available: true, configurable: false, capabilities: { speech: { supported: true, available: true, configurable: false, endpoints: ["audio_speech"] } } }
  const bootstrap = { models: { source_id: "gateway:preset", api_base_url: session.origin + "/v1" }, gateway_models: { source_id: "gateway:native", api_base_url: session.origin + "/client/models/v1/gateway" }, capabilities: { version: 1, model_roles_version: 3, sources: { preset: source, gateway: { ...source, authorized: false } } } } as GatewayBootstrap
  publishGatewayCapabilities(session, bootstrap)
  expect(gatewayCapabilityAllows(session, "gateway:preset", "speech")).toBe(true)
  expect(gatewayCapabilityTarget(session, "gateway:preset", "speech")).toBe(session.origin + "/v1")
  expect(gatewayCapabilityAllows(session, "gateway:native", "speech")).toBe(false)
  expect(gatewayCapabilityAllows(session, undefined, "speech")).toBe(false)
  expect(gatewayCapabilityAllows({ ...session, device: { ...session.device, id: "another" } }, "gateway:preset", "speech")).toBe(false)
  expect(gatewayCapabilityAllows({ ...session, token: "another" }, "gateway:preset", "speech")).toBe(false)
  invalidateGatewayCapabilities(session)
  expect(gatewayCapabilityAllows(session, "gateway:preset", "speech")).toBe(false)
  publishGatewayCapabilities(session, { ...bootstrap, capabilities: { ...bootstrap.capabilities!, sources: { preset: source, gateway: source } } })
  expect(gatewayCapabilityTarget(session, "gateway:native", "speech")).toBe(session.origin + "/client/models/v1/gateway")
})
