import { currentGatewayImageModel, GATEWAY_CAPABILITIES_EVENT, GATEWAY_CAPABILITY_REFRESH_EVENT, invalidateGatewayImageCapability } from "./gateway-capabilities"
import { gatewayAuthorityCurrent, notifyGatewayRefusal, watchGatewayAuthority } from "./gateway-authority"
import { loadGatewaySession, type GatewaySession } from "./gateway-account"
import { gatewayFetch } from "./gateway-fetch"
import type { DirectProvider } from "./direct-provider"
import { ModelCallError, type ApiStyle } from "./openai-wire"

export const imageToolName = "generate_image"
export const maxGeneratedImageBytes = 8 << 20
const maxJSONBytes = Math.ceil(maxGeneratedImageBytes / 3) * 4 + (64 << 10)
const maxPromptBytes = 4096
const maxPixels = 16 * 1024 * 1024
const maxSide = 8192

const imageExtensions = { "image/png": "png", "image/jpeg": "jpg", "image/webp": "webp" } as const
export type GeneratedImageMIME = keyof typeof imageExtensions
export type GeneratedImage = { blob: Blob; mime: GeneratedImageMIME; width: number; height: number }
export type GatewayImageTarget = { provider: DirectProvider; session: GatewaySession; model: string; baseURL: string }

export class ImageGenerationError extends ModelCallError {
  constructor(readonly code: string) {
    super(code, code === "image_timeout" ? "timeout" : code === "image_cancelled" ? "abort" : "http")
  }
}

export function gatewayImageTarget(provider: DirectProvider): GatewayImageTarget | undefined {
  const session = loadGatewaySession()
  if (provider.sourceKind !== "gateway" || !session || provider.apiKey !== session.token || !gatewayAuthorityCurrent(session)) return
  const source = currentGatewayImageModel(session, provider.id)
  const base = session.origin + (provider.gatewayScope === "gateway" ? "/client/models/v1/gateway" : "/v1")
  if (!source || source.baseURL !== base || provider.baseURL !== base) return
  return { provider, session, ...source }
}

export function imageToolDefinition(style: ApiStyle): Record<string, unknown> {
  const definition = {
    name: imageToolName,
    description: "Generate one image when the current user message explicitly asks to create or draw one. Describe the intended image in the prompt. The finished image appears in this conversation.",
    parameters: { type: "object", properties: { prompt: { type: "string" } }, required: ["prompt"], additionalProperties: false },
  }
  return style === "chat" ? { type: "function", function: definition } : { type: "function", ...definition }
}

export function validateImagePrompt(prompt: unknown): string {
  const text = typeof prompt === "string" ? prompt.trim() : ""
  if (!text || new TextEncoder().encode(text).length > maxPromptBytes || text.includes("\0")) {
    throw new ImageGenerationError("image_prompt_invalid")
  }
  return text
}

export function imagePromptFromArguments(args: string): string {
  try {
    const value: unknown = JSON.parse(args)
    if (value && typeof value === "object" && Object.keys(value).length === 1 && "prompt" in value) return validateImagePrompt(value.prompt)
  } catch { /* Malformed arguments never reach the paid request. */ }
  throw new ImageGenerationError("image_prompt_invalid")
}

export function generatedImageFilename(id: string, mime: GeneratedImageMIME): string {
  return id + "." + imageExtensions[mime]
}

export function isGeneratedImageMIME(value: unknown): value is GeneratedImageMIME {
  return typeof value === "string" && Object.prototype.hasOwnProperty.call(imageExtensions, value)
}

export function assertGatewayImageTarget(target: GatewayImageTarget) {
  const source = currentGatewayImageModel(target.session, target.provider.id)
  if (!gatewayAuthorityCurrent(target.session) || source?.model !== target.model || source.baseURL !== target.baseURL) {
    throw new ImageGenerationError("image_permission_denied")
  }
}

export async function generateGatewayImage(target: GatewayImageTarget, rawPrompt: string, signal: AbortSignal, timeoutMs: number, transport: typeof fetch = gatewayFetch): Promise<GeneratedImage> {
  const prompt = validateImagePrompt(rawPrompt)
  const controller = new AbortController()
  const stop = () => controller.abort("stop")
  const revoke = () => controller.abort("revoked")
  const check = () => {
    try { assertGatewayImageTarget(target) } catch { revoke() }
  }
  signal.addEventListener("abort", stop, { once: true })
  if (signal.aborted) stop()
  const unwatch = watchGatewayAuthority(target.session, revoke)
  window.addEventListener(GATEWAY_CAPABILITIES_EVENT, check)
  const timer = setTimeout(() => controller.abort("timeout"), timeoutMs)
  let response: Response | undefined
  let submitted = false
  try {
    requireActive(controller.signal)
    assertGatewayImageTarget(target)
    const endpoint = target.baseURL + "/images/generations"
    submitted = true
    response = await transport(endpoint, {
      method: "POST",
      headers: { Authorization: "Bearer " + target.session.token, "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ model: target.model, prompt, n: 1 }),
      signal: controller.signal, redirect: "error", credentials: "omit", cache: "no-store",
    })
    requireActive(controller.signal)
    if (response.redirected || response.url && response.url !== endpoint) throw new ImageGenerationError("image_invalid_response")
    if (response.status === 401) notifyGatewayRefusal(target.session, response.status)
    if (response.status === 403 && gatewayAuthorityCurrent(target.session)) {
      invalidateGatewayImageCapability(target.session, target.provider.id)
      window.dispatchEvent(new Event(GATEWAY_CAPABILITY_REFRESH_EVENT))
    }
    if (response.status === 401 || response.status === 403) throw new ImageGenerationError("image_permission_denied")
    if (!response.ok) throw new ImageGenerationError("image_gateway_failed")
    const payload = await boundedImageJSON(response, controller.signal)
    const result = await decodeImage(payload, controller.signal)
    requireActive(controller.signal)
    assertGatewayImageTarget(target)
    return result
  } catch (error) {
    if (error instanceof ImageGenerationError) throw error
    requireActive(controller.signal)
    throw new ImageGenerationError(submitted ? "image_result_unknown" : "image_gateway_failed")
  } finally {
    clearTimeout(timer)
    unwatch()
    signal.removeEventListener("abort", stop)
    window.removeEventListener(GATEWAY_CAPABILITIES_EVENT, check)
    await response?.body?.cancel().catch(() => undefined)
  }
}

function requireActive(signal: AbortSignal) {
  if (signal.aborted) throw new ImageGenerationError(signal.reason === "timeout" ? "image_timeout"
    : signal.reason === "revoked" ? "image_permission_denied" : "image_cancelled")
}

async function boundedImageJSON(response: Response, signal: AbortSignal): Promise<unknown> {
  const type = response.headers.get("Content-Type")?.split(";", 1)[0].trim().toLowerCase()
  if (type !== "application/json" || !response.body || Number(response.headers.get("Content-Length")) > maxJSONBytes) {
    throw new ImageGenerationError("image_invalid_response")
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder("utf-8", { fatal: true })
  let total = 0
  const text: string[] = []
  const stop = () => { void reader.cancel().catch(() => undefined) }
  signal.addEventListener("abort", stop, { once: true })
  try {
    for (;;) {
      requireActive(signal)
      const next = await reader.read()
      requireActive(signal)
      if (next.done) break
      total += next.value.byteLength
      if (total > maxJSONBytes) throw new ImageGenerationError("image_invalid_response")
      text.push(decoder.decode(next.value, { stream: true }))
    }
    text.push(decoder.decode())
    return JSON.parse(text.join(""))
  } catch {
    requireActive(signal)
    throw new ImageGenerationError("image_invalid_response")
  } finally {
    signal.removeEventListener("abort", stop)
    await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
}

async function decodeImage(payload: unknown, signal: AbortSignal): Promise<GeneratedImage> {
  if (!payload || typeof payload !== "object" || !("data" in payload) || !Array.isArray(payload.data) || payload.data.length !== 1 ||
      typeof payload.data[0]?.b64_json !== "string" || payload.data[0].b64_json.length > Math.ceil(maxGeneratedImageBytes / 3) * 4) {
    throw new ImageGenerationError("image_invalid_response")
  }
  let decoded: string
  try { decoded = atob(payload.data[0].b64_json) } catch { throw new ImageGenerationError("image_invalid_response") }
  if (!decoded.length || decoded.length > maxGeneratedImageBytes) throw new ImageGenerationError("image_invalid_response")
  const bytes = Uint8Array.from(decoded, char => char.charCodeAt(0))
  const mime = sniffImageMIME(decoded)
  if (!mime) throw new ImageGenerationError("image_invalid_response")
  const blob = new Blob([bytes], { type: mime })
  const url = URL.createObjectURL(blob)
  const img = new Image()
  const cancel = () => { img.removeAttribute("src") }
  signal.addEventListener("abort", cancel, { once: true })
  try {
    requireActive(signal)
    img.src = url
    await img.decode()
    requireActive(signal)
    const width = img.naturalWidth
    const height = img.naturalHeight
    if (!validGeneratedImageSize(width, height)) throw new ImageGenerationError("image_invalid_response")
    return { mime, blob, width, height }
  } catch {
    requireActive(signal)
    throw new ImageGenerationError("image_invalid_response")
  } finally {
    signal.removeEventListener("abort", cancel)
    URL.revokeObjectURL(url)
  }
}

function sniffImageMIME(decoded: string): GeneratedImageMIME | undefined {
  if (decoded.startsWith("\x89PNG\r\n\x1a\n")) return "image/png"
  if (decoded.startsWith("\xff\xd8\xff")) return "image/jpeg"
  if (decoded.startsWith("RIFF") && decoded.slice(8, 12) === "WEBP") return "image/webp"
}

export function validGeneratedImageSize(width: number, height: number): boolean {
  return Number.isInteger(width) && Number.isInteger(height) && width >= 1 && height >= 1 &&
    width <= maxSide && height <= maxSide && width * height <= maxPixels
}
