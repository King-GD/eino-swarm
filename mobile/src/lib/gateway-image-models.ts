import { capabilityAllows } from "../../../frontend/src/lib/capabilities"
import type { GatewayBootstrap } from "./gateway-account"

export type GatewayImageModels = Record<string, string>

function validName(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 256 &&
    value === value.trim() && !/[\u0000-\u001f\u007f]/.test(value)
}

function imageSource(bootstrap: GatewayBootstrap, sourceID: string) {
  const kind = sourceID === bootstrap.models.source_id ? "preset"
    : sourceID === bootstrap.gateway_models?.source_id ? "gateway" : undefined
  if (!kind || !capabilityAllows(bootstrap.capabilities?.sources[kind], "image")) return
  const descriptor = kind === "preset" ? bootstrap.models : bootstrap.gateway_models
  if (!descriptor?.api_access_enabled) return
  const fixed = kind === "preset" && descriptor.configurable === false
  const preset = fixed && bootstrap.models.roles?.some(row => row.id === "image" && row.enabled) ? "image" : descriptor.defaults?.image
  const defaultModel = validName(preset) && (!fixed || preset === "image") ? preset : undefined
  return { descriptor, defaultModel, fixed, catalogURL: kind === "preset" ? bootstrap.models.client_catalog_url ?? descriptor.catalog_url : descriptor.catalog_url }
}

export function gatewayImageModel(bootstrap: GatewayBootstrap, sourceID: string): { model: string; baseURL: string } | undefined {
  const source = imageSource(bootstrap, sourceID)
  if (!source) return
  const model = source.defaultModel ?? (source.fixed ? undefined : bootstrap.image_models?.[sourceID])
  if (validName(model)) return { model, baseURL: source.descriptor.api_base_url }
}

export async function discoverGatewayImageModels(bootstrap: GatewayBootstrap, read: (url: string) => Promise<unknown>): Promise<GatewayImageModels> {
  const result: GatewayImageModels = {}
  const ids = [bootstrap.models.source_id, bootstrap.gateway_models?.source_id]
  await Promise.all(ids.map(async id => {
    const source = id && imageSource(bootstrap, id)
    if (!source || !id || source.defaultModel || source.fixed) return
    try {
      const rows = await read(source.catalogURL)
      if (!Array.isArray(rows)) return
      const names = rows.slice(0, 1000).flatMap((row: unknown) => {
        if (row && typeof row === "object" && "id" in row && "model_type" in row && row.model_type === "image" && validName(row.id)) return [row.id]
        return []
      }).sort()
      if (names[0]) result[id] = names[0]
    } catch (error) {
      if (error && typeof error === "object" && "status" in error && error.status === 401) throw error
      // A failed or refused catalog discovery does not confer an image grant.
    }
  }))
  return result
}
