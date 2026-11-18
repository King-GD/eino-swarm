import { useEffect, useMemo, useState } from "react"
import type { GatewayBootstrap, GatewaySession, GatewayStatus } from "./gateway-account"
import { gatewayModelSources, GATEWAY_MODEL_REFRESH_EVENT, type GatewayModelSource } from "./gateway-models"
import type { DirectProvider } from "./direct-provider"
import { discoverModels, ModelHttpError } from "./openai-client"

type SourceState = { authority: string; providers: DirectProvider[]; busy: boolean; error: boolean }

function conversationProvider(source: GatewayModelSource, names: string[]): DirectProvider | undefined {
  const catalog = source.conversationModel ? names.filter(name => name === source.conversationModel) : names
  if (!catalog.length) return
  const provider = source.provider
  return { ...provider, catalog, model: catalog.includes(provider.model) ? provider.model : catalog[0] }
}

function sameCatalog(left: DirectProvider, right: DirectProvider): boolean {
  return left.id === right.id && left.baseURL === right.baseURL && left.catalogURL === right.catalogURL
}

/** Catalogs are account-scoped memory, never personal provider settings. Each
 * endpoint can deny its source independently without hiding its sibling. */
export function useGatewayModelSources(status: GatewayStatus, session: GatewaySession | null, bootstrap: GatewayBootstrap | undefined) {
  const enabled = status === "authorized" && Boolean(session && bootstrap)
  const sources = useMemo(() => enabled ? gatewayModelSources(session!, bootstrap!) : [], [enabled, session, bootstrap])
  const authority = enabled ? JSON.stringify([session!.origin, session!.user.id, session!.device.id, session!.token]) : ""
  const [revision, setRevision] = useState(0)
  const [state, setState] = useState<SourceState>({ authority: "", providers: [], busy: false, error: false })
  useEffect(() => {
    const refresh = () => setRevision(value => value + 1)
    window.addEventListener(GATEWAY_MODEL_REFRESH_EVENT, refresh)
    return () => window.removeEventListener(GATEWAY_MODEL_REFRESH_EVENT, refresh)
  }, [])
  useEffect(() => {
    let current = true
    if (!authority) {
      setState({ authority: "", providers: [], busy: false, error: false })
      return
    }
    setState(previous => ({ authority, providers: previous.authority === authority ? previous.providers : [], busy: sources.length > 0, error: false }))
    void Promise.all(sources.map(async source => {
      try {
        const names = source.names ?? await discoverModels(source.provider)
        return { source, provider: conversationProvider(source, names) }
      } catch (error) {
        return { source, error }
      }
    })).then(results => {
      if (!current) return
      setState(previous => {
        let failed = false
        const providers: DirectProvider[] = []
        for (const result of results) {
          if ("error" in result) {
            const refused = result.error instanceof ModelHttpError && [401, 403].includes(result.error.status)
            if (!refused) {
              failed = true
              const cached = previous.authority === authority ? previous.providers.find(row => sameCatalog(row, result.source.provider)) : undefined
              const retained = cached && conversationProvider(result.source, cached.catalog)
              if (retained) providers.push(retained)
            }
          } else if (result.provider) providers.push(result.provider)
        }
        return { authority, providers, busy: false, error: failed }
      })
    })
    return () => { current = false }
  }, [authority, sources, revision])
  return {
    providers: state.authority === authority && enabled ? sources.flatMap(source => {
      const cached = state.providers.find(row => sameCatalog(row, source.provider))
      const provider = cached && conversationProvider(source, cached.catalog)
      return provider ? [provider] : []
    }) : [],
    busy: enabled && state.authority === authority && state.busy,
    error: enabled && (bootstrap?.capability_status !== "ready" || state.authority === authority && state.error),
    refresh: () => setRevision(value => value + 1),
  }
}
