import { useEffect, useMemo, useState } from "react"
import type { GatewayBootstrap, GatewaySession, GatewayStatus } from "./gateway-account"
import { gatewayModelSources, GATEWAY_MODEL_REFRESH_EVENT } from "./gateway-models"
import type { DirectProvider } from "./direct-provider"
import { discoverModels, ModelHttpError } from "./openai-client"
import type { PhoneMode } from "./phone-mode"

type SourceState = { authority: string; providers: DirectProvider[]; busy: boolean; error: boolean }

/** Catalogs are account-scoped memory, never personal provider settings. Each
 * endpoint can deny its source independently without hiding its sibling. */
export function useLocalModelSources(mode: PhoneMode, status: GatewayStatus, session: GatewaySession | null, bootstrap: GatewayBootstrap | undefined) {
  const enabled = mode === "local" && status === "authorized" && Boolean(session && bootstrap)
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
        const preferred = source.provider.model
        return { source, provider: names.length ? { ...source.provider, catalog: names, model: names.includes(preferred) ? preferred : names[0] } : undefined }
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
              const retained = previous.authority === authority ? previous.providers.find(row => row.id === result.source.provider.id && row.baseURL === result.source.provider.baseURL) : undefined
              if (retained) providers.push({ ...retained, capabilities: result.source.provider.capabilities, locked: result.source.provider.locked })
            }
          } else if (result.provider) providers.push(result.provider)
        }
        return { authority, providers, busy: false, error: failed }
      })
    })
    return () => { current = false }
  }, [authority, sources, revision])
  return {
    providers: state.authority === authority && enabled ? state.providers.filter(row => sources.some(source => source.provider.id === row.id && source.provider.baseURL === row.baseURL && source.provider.catalogURL === row.catalogURL)).map(row => {
      const source = sources.find(source => source.provider.id === row.id)!.provider
      return { ...row, capabilities: source.capabilities, api: source.api, locked: source.locked }
    }) : [],
    busy: enabled && state.authority === authority && state.busy,
    error: enabled && (bootstrap?.capability_status !== "ready" || state.authority === authority && state.error),
    refresh: () => setRevision(value => value + 1),
  }
}
