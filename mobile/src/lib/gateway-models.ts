import { defaultTimeoutSeconds, type DirectProvider } from "./direct-provider"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"
import { t } from "./i18n"

/** Account source identity belongs to the gateway/account, never a model name.
 * The credential lives only in this ephemeral projection. */
export function gatewayProvider(session: GatewaySession | null, bootstrap: GatewayBootstrap | null): DirectProvider | null {
  if (!session || !bootstrap?.models.api_access_enabled || !bootstrap.models.source_id.startsWith("gateway:")) return null
  return {
    id: bootstrap.models.source_id,
    sourceKind: "gateway",
    label: t("account.serverSource", { name: bootstrap.models.label }),
    baseURL: bootstrap.models.api_base_url,
    apiKey: session.token,
    api: "chat",
    model: "",
    catalog: [],
    timeoutSeconds: defaultTimeoutSeconds,
    enabled: true,
  }
}

export const GATEWAY_MODEL_REFRESH_EVENT = "zwai.gateway.model-sources.refresh"
export type GatewayModelSource = { provider: DirectProvider; names?: string[] }

/** Preset and Gateway catalogs keep separate identities even when names match.
 * Older servers may expose only the ordinary catalog or fixed-role metadata. */
export function gatewayModelSources(session: GatewaySession, bootstrap: GatewayBootstrap): GatewayModelSource[] {
  const result: GatewayModelSource[] = []
  const models = bootstrap.models
  const managed = models.configurable === false || Boolean(models.roles?.length || models.profile)
  const source = gatewayProvider(session, bootstrap)
  if (managed && source) {
    source.label = t("account.thirdPartyGroup", { name: models.profile?.label ?? models.label })
    source.catalogURL = models.client_catalog_url ?? models.catalog_url
    source.modelLabels = Object.fromEntries((models.roles ?? []).map(role => [role.id, role.label]))
    source.model = models.defaults?.conversation ?? models.roles?.find(role => role.id === "conversation" && role.enabled)?.id ?? ""
    if (models.profile) {
      source.displayLabel = models.profile.label
      source.model = "account"
      source.locked = false
      result.push({ provider: source, names: [source.model] })
    } else if (!models.client_catalog_url && models.roles) {
      result.push({ provider: source, names: models.roles.filter(role => role.enabled).map(role => role.id) })
    } else result.push({ provider: source })
  }
  const global = bootstrap.gateway_models
  if (global?.api_access_enabled) {
    result.push({ provider: {
      id: global.source_id, sourceKind: "gateway", gatewayScope: "gateway",
      label: t("account.gatewayGroup", { name: global.label }),
      baseURL: global.api_base_url, catalogURL: global.catalog_url, apiKey: session.token,
      api: "chat", model: "", catalog: [], timeoutSeconds: defaultTimeoutSeconds, enabled: true,
    } })
  } else if (!global && !managed && source) {
    source.label = t("account.gatewayGroup", { name: models.label })
    source.catalogURL = models.client_catalog_url ?? models.catalog_url
    source.model = models.defaults?.conversation ?? ""
    result.push({ provider: source })
  }
  return result
}
