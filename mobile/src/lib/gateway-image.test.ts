import { beforeEach, describe, expect, it, vi } from "vitest"

import type { SourceCapabilities } from "../../../frontend/src/lib/capabilities"
import type { DirectProvider } from "./direct-provider"
import { saveGatewaySession, type GatewayBootstrap, type GatewaySession } from "./gateway-account"
import { publishGatewayCapabilities } from "./gateway-capabilities"
import { gatewayImageTarget, generateGatewayImage, imagePromptFromArguments } from "./gateway-image"
import { discoverGatewayImageModels, gatewayImageModel } from "./gateway-image-models"

const origin = "https://images.invalid"
const session: GatewaySession = {
  origin, token: "agm_" + "b".repeat(64),
  user: { id: "owner", username: "fixture", approval_status: "approved", enabled: true },
  device: { id: "phone", expires_at: "2099-01-01T00:00:00Z" },
}
const provider: DirectProvider = {
  id: "gateway:preset", sourceKind: "gateway", api: "chat", baseURL: origin + "/v1", apiKey: session.token,
  model: "chat", label: "Gateway", catalog: ["chat"], timeoutSeconds: 30,
}
const signal = () => new AbortController().signal
const imageCap = (): SourceCapabilities => ({
  authorized: true, available: true, configurable: true,
  capabilities: { image: { supported: true, available: true, configurable: true, endpoints: ["images"] } },
})

function bootstrap(): GatewayBootstrap {
  return {
    user: session.user, device: session.device,
    models: {
      source_id: provider.id, source_kind: "gateway", label: "Preset", execution: "phone",
      api_base_url: provider.baseURL, catalog_url: origin + "/v1/models",
      client_catalog_url: origin + "/client/models/v1/preset/models", api_access_enabled: true,
    },
    gateway_models: {
      source_id: "gateway:global", source_kind: "gateway", label: "Global", execution: "phone",
      api_base_url: origin + "/client/models/v1/gateway", catalog_url: origin + "/client/models/v1/gateway/models",
      defaults: { image: "global-image" }, api_access_enabled: true,
    },
    capabilities: { version: 1, model_roles_version: 3, sources: { preset: imageCap(), gateway: imageCap() } },
    pairlink: { enabled: false, hub_url: origin, redeem_url: "", websocket_url: "", info_url: "" },
    heartbeat_interval: 30, presence_ttl: 90,
  } as GatewayBootstrap
}

beforeEach(() => {
  localStorage.clear()
  saveGatewaySession(session)
})

describe("phone image source and admission", () => {
  it("picks a stable typed name from the exact source, and keeps fixed roles fixed", async () => {
    const boot = bootstrap()
    const read = vi.fn().mockResolvedValue([{ id: "z", model_type: "image" }, { id: "chat", model_type: "chat" }, { id: "a", model_type: "image" }])
    boot.image_models = await discoverGatewayImageModels(boot, read)
    expect(read).toHaveBeenCalledOnce()
    expect(read).toHaveBeenCalledWith(origin + "/client/models/v1/preset/models")
    expect(gatewayImageModel(boot, provider.id)).toEqual({ model: "a", baseURL: provider.baseURL })
    boot.models = { ...boot.models, configurable: false, roles: [{ id: "image", enabled: true, label: "Image" }] }
    expect(gatewayImageModel(boot, provider.id)).toEqual({ model: "image", baseURL: provider.baseURL })
    boot.capabilities!.sources.preset!.capabilities.image!.available = false
    publishGatewayCapabilities(session, boot)
    expect(gatewayImageTarget(provider)).toBeUndefined()
    expect(gatewayImageTarget({ ...provider, id: "gateway:wrong" })).toBeUndefined()
    expect(gatewayImageTarget({ ...provider, sourceKind: "phone" })).toBeUndefined()
  })

  it("rejects malformed or oversized tool arguments before a paid call", () => {
    expect(imagePromptFromArguments('{"prompt":"  test  "}')).toBe("test")
    for (const args of ['{}', '{"prompt":[]}', '{"prompt":" "}', '{"prompt":"t","n":4}', '{"prompt":"\\u0000"}', JSON.stringify({prompt: "测".repeat(1400)})]) {
      expect(() => imagePromptFromArguments(args)).toThrow("image_prompt_invalid")
    }
  })

  it.each([
    [502, '{"error":{"message":"private backend detail"}}', "image_gateway_failed"],
    [403, '{"error":{"message":"private backend detail"}}', "image_permission_denied"],
    [200, '{"data":[{"url":"https://outside.invalid/generated.png"}]}', "image_invalid_response"],
    [200, '{"data":[{"b64_json":"aGVsbG8="}]}', "image_invalid_response"],
    [200, '{"data":[{"b64_json":"a==="}]}', "image_invalid_response"],
  ] as const)("settles %s %s without retrying the paid request", async (status, body, code) => {
    const boot = bootstrap()
    boot.models.defaults = { image: "preset-image" }
    publishGatewayCapabilities(session, boot)
    const target = gatewayImageTarget(provider)!
    const transport = vi.fn(async () => new Response(body, {status, headers: {"Content-Type": "application/json"}}))
    await expect(generateGatewayImage(target, "prompt", signal(), 1000, transport)).rejects.toMatchObject({ code })
    expect(transport).toHaveBeenCalledTimes(1)
    expect(transport).toHaveBeenCalledWith(origin + "/v1/images/generations", expect.objectContaining({
      body: JSON.stringify({ model: "preset-image", prompt: "prompt", n: 1 }),
      redirect: "error", credentials: "omit", cache: "no-store",
    }))
  })

  it("preserves an ambiguous POST outcome after the transport loses its response", async () => {
    const boot = bootstrap()
    boot.models.defaults = { image: "preset-image" }
    publishGatewayCapabilities(session, boot)
    const transport = vi.fn().mockRejectedValue(new TypeError("connection lost"))
    await expect(generateGatewayImage(gatewayImageTarget(provider)!, "prompt", signal(), 1000, transport))
      .rejects.toMatchObject({ code: "image_result_unknown" })
    expect(transport).toHaveBeenCalledTimes(1)
  })

  it("cancels a pending request when its negotiated capability is revoked", async () => {
    const boot = bootstrap()
    boot.models.defaults = { image: "preset-image" }
    publishGatewayCapabilities(session, boot)
    const transport = vi.fn((_url: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init!.signal!.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true })
    }))
    const pending = generateGatewayImage(gatewayImageTarget(provider)!, "prompt", signal(), 3000, transport)
    const revoked = bootstrap()
    revoked.capabilities!.sources.preset!.capabilities.image!.available = false
    publishGatewayCapabilities(session, revoked)
    await expect(pending).rejects.toMatchObject({ code: "image_permission_denied" })
    expect(transport).toHaveBeenCalledTimes(1)
  })
})
