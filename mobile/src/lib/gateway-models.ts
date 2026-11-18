import { capabilityAllows, capabilityConfigurable } from "../../../frontend/src/lib/capabilities"
import { defaultTimeoutSeconds, type DirectProvider } from "./direct-provider"
import type { GatewayBootstrap, GatewaySession } from "./gateway-account"
import { t } from "./i18n"

/** Each ephemeral source carries only its own negotiated permission. */
export function gatewayProvider(session: GatewaySession | null, bootstrap: GatewayBootstrap | null): DirectProvider | null {
  const capability = bootstrap?.capabilities?.sources.preset
  if (!session || !bootstrap || !capabilityAllows(capability, "conversation") || !bootstrap.models.source_id.startsWith("gateway:")) return null
  return {
    id: bootstrap.models.source_id, sourceKind: "gateway", capabilities: capability,
    label: t("account.serverSource", { name: bootstrap.models.label }),
    baseURL: bootstrap.models.api_base_url, apiKey: session.token,
    api: capability?.capabilities.conversation?.endpoints.includes("chat_completions") ? "chat" : "responses",
    model: bootstrap.models.defaults?.conversation ?? "", catalog: [],
    locked: !capabilityConfigurable(capability, "conversation"),
    timeoutSeconds: defaultTimeoutSeconds, enabled: true,
  }
}

export const GATEWAY_MODEL_REFRESH_EVENT = "zwai.gateway.model-sources.refresh"
export type GatewayModelSource = { provider: DirectProvider; names?: string[]; conversationModel?: string }

export function gatewayModelSources(session: GatewaySession, bootstrap: GatewayBootstrap): GatewayModelSource[] {
  const result: GatewayModelSource[] = []
  const models = bootstrap.models
  const source = gatewayProvider(session, bootstrap)
  if (source) {
    const published = (models.profile?.label ?? models.label).trim()
    source.label = published || source.id
    source.catalogURL = models.client_catalog_url ?? models.catalog_url
    if (models.profile) {
      source.displayLabel = models.profile.label
      source.model = "account"
      result.push({ provider: source, names: [source.model] })
    } else if (models.configurable === false || models.roles?.length) {
      // Functional aliases can use the chat protocol too. Use this source's
      // conversation binding, and still check the private catalog when present.
      const role = models.roles?.find(item => item.id === "conversation")
      if (!models.roles?.length || role?.enabled) {
        const conversationModel = source.model || role?.id || "conversation"
        if (role?.label) source.modelLabels = { [conversationModel]: role.label }
        result.push({
          provider: source,
          conversationModel,
          names: !models.client_catalog_url && role?.enabled ? [conversationModel] : undefined,
        })
      }
    } else result.push({ provider: source })
  }
  const global = bootstrap.gateway_models
  const capability = bootstrap.capabilities?.sources.gateway
  if (global && capabilityAllows(capability, "conversation")) {
    result.push({ provider: {
      id: global.source_id, sourceKind: "gateway", gatewayScope: "gateway", capabilities: capability,
      label: t("account.gatewayGroup", { name: global.label }),
      baseURL: global.api_base_url, catalogURL: global.catalog_url, apiKey: session.token,
      api: capability?.capabilities.conversation?.endpoints.includes("chat_completions") ? "chat" : "responses",
      model: global.defaults?.conversation ?? "", catalog: [], locked: !capabilityConfigurable(capability, "conversation"),
      timeoutSeconds: defaultTimeoutSeconds, enabled: true,
    } })
  }
  return result
}
