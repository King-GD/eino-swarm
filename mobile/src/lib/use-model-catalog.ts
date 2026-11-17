import { GATEWAY_CAPABILITIES_EVENT, gatewayModelPolicyRevision } from "./gateway-capabilities"
import { capabilityAllows, capabilityConfigurable } from "../../../frontend/src/lib/capabilities"
import { publishPairedCapabilities } from "./paired-capabilities"
import type { ModelPolicy } from "../../../frontend/src/lib/model-policy"
import { useCallback, useEffect, useRef, useState } from "react"
import { t } from "./i18n"
import type { RemoteLink } from "./link"
import { OpCatalog, OpRefreshCatalog, OpRefreshCapabilities, type ModelChoice, type PcWebExecutionStatus, type RemoteResponse } from "./rpc"

// Discovery owns a 15s host budget; allow the response to cross the relay
// without letting this optional operation drop an active conversation.
const refreshTimeoutMs = 30_000
const capabilityJobs = new WeakMap<RemoteLink, Promise<RemoteResponse>>()
// Mode acknowledgement can arrive during initial discovery. The PC admits
// only one refresh at a time; queue the new scope instead of accepting "busy".
function refreshCapabilities(link: RemoteLink): Promise<RemoteResponse> {
  const previous = capabilityJobs.get(link)
  const run = () => link.rpc({ op: OpRefreshCapabilities }, { dropOnTimeout: false, timeoutMs: refreshTimeoutMs })
  const job = previous ? previous.catch(() => undefined).then(run) : run()
  capabilityJobs.set(link, job)
  void job.finally(() => { if (capabilityJobs.get(link) === job) capabilityJobs.delete(link) }).catch(() => undefined)
  return job
}

export function useModelCatalog(link: RemoteLink | null, sourceRevision = "") {
  const [predictionsEnabled, setPredictionsEnabled] = useState(true)
  const [policy, setPolicy] = useState<ModelPolicy>()
  const [models, setModels] = useState<ModelChoice[]>([])
  const [levels, setLevels] = useState<string[]>([])
  const [webExecutionStatus, setWebExecutionStatus] = useState<PcWebExecutionStatus>()
  const [catalogBusy, setCatalogBusy] = useState(false)
  const [refreshBusy, setRefreshBusy] = useState(false)
  const [refreshError, setRefreshError] = useState("")
  const [capabilityRevision, setCapabilityRevision] = useState(gatewayModelPolicyRevision)
  useEffect(() => {
    const refresh = () => setCapabilityRevision(gatewayModelPolicyRevision())
    window.addEventListener(GATEWAY_CAPABILITIES_EVENT, refresh)
    return () => window.removeEventListener(GATEWAY_CAPABILITIES_EVENT, refresh)
  }, [])
  const active = useRef(link)
  const generation = useRef(0)
  const inFlight = useRef<object | null>(null)
  const previous = useRef<{ link: RemoteLink | null; sourceRevision: string } | undefined>(undefined)
  active.current = link

  useEffect(() => {
    const gen = ++generation.current
    inFlight.current = null
    const changed = previous.current?.link !== link || previous.current?.sourceRevision !== sourceRevision
    previous.current = { link, sourceRevision }
    if (changed) {
      setPredictionsEnabled(true)
      setPolicy(undefined)
      if (link) publishPairedCapabilities(link, undefined)
      setModels([])
      setLevels([])
      setWebExecutionStatus(undefined)
    }
    setRefreshError("")
    setRefreshBusy(false)
    setCatalogBusy(Boolean(link?.alive()))
    if (link?.alive()) {
      const current = () => active.current === link && generation.current === gen
      void refreshCapabilities(link).then(r => !r.ok ? link.rpc({ op: OpCatalog }) : r).then((r) => {
        if (!current() || !r.ok) return
        if (typeof r.composer_predictions === "boolean") setPredictionsEnabled(r.composer_predictions)
        setPolicy(r.model_policy)
        publishPairedCapabilities(link, r.model_policy)
        setModels(projectModels(r.models, r.model_policy))
        setLevels(r.reasoning_levels ?? [])
        setWebExecutionStatus(validWebStatus(r.web_execution_status))
        if (missingGatewayCapabilities(r.models, r.model_policy)) setRefreshError(t("composer.gatewayPCUpgrade"))
      }).catch(() => undefined).finally(() => {
        if (current()) setCatalogBusy(false)
      })
    }
    return () => { generation.current += 1 }
  }, [link, sourceRevision, capabilityRevision])

  const refreshModels = useCallback(async () => {
    const target = active.current
    if (!target?.alive() || inFlight.current) return
    const job = {}
    inFlight.current = job
    const gen = ++generation.current
    const current = () => active.current === target && generation.current === gen
    setCatalogBusy(false)
    setRefreshBusy(true)
    setRefreshError("")
    setWebExecutionStatus(undefined)
    try {
      const discovery = await refreshCapabilities(target)
      if (!current()) return
      const r = discovery.ok && discovery.model_policy?.mode === "gateway" ? discovery : await target.rpc({ op: OpRefreshCatalog }, {
        dropOnTimeout: false, timeoutMs: refreshTimeoutMs,
      })
      if (!current()) return
      if (!r.ok) {
        setRefreshError(t(r.code === "unknown_op" ? "composer.refreshUnsupported" : "composer.refreshFailed"))
        return
      }
      if (typeof r.composer_predictions === "boolean") setPredictionsEnabled(r.composer_predictions)
      const nextPolicy = r.model_policy ?? discovery.model_policy
      setPolicy(nextPolicy)
      publishPairedCapabilities(target, nextPolicy)
      setModels(projectModels(r.models, nextPolicy))
      setLevels(r.reasoning_levels ?? [])
      setWebExecutionStatus(validWebStatus(r.web_execution_status))
      if (missingGatewayCapabilities(r.models, nextPolicy)) setRefreshError(t("composer.gatewayPCUpgrade"))
    } catch {
      if (current()) setRefreshError(t("composer.refreshFailed"))
    } finally {
      if (inFlight.current === job) inFlight.current = null
      if (current()) setRefreshBusy(false)
    }
  }, [])

  return { policy, predictionsEnabled, models, levels, catalogBusy, refreshBusy, refreshError, refreshModels, webExecutionStatus }
}

function missingGatewayCapabilities(rows: ModelChoice[] | undefined, policy: ModelPolicy | undefined): boolean {
  return !policy?.capability_status && Boolean(rows?.some(row => row.provider_id.startsWith("gateway:") && !policy?.account_sources?.[row.provider_id] && !row.capabilities))
}

function validWebStatus(status: PcWebExecutionStatus | undefined): PcWebExecutionStatus | undefined {
  return status && ["auto", "local", "cloud"].includes(status.preference)
    && ["local", "cloud"].includes(status.execution)
    && ["signed_in", "sign_in_required", "unavailable"].includes(status.cloud_account) ? status : undefined
}

function projectModels(rows: ModelChoice[] | undefined, policy: ModelPolicy | undefined): ModelChoice[] {
  return (rows ?? []).map(row => ({ ...row, capabilities: policy?.account_sources?.[row.provider_id] ?? row.capabilities }))
    .filter(row => !row.provider_id.startsWith("gateway:") || capabilityAllows(row.capabilities, "conversation"))
    .map(row => ({ ...row, locked: row.locked || row.provider_id.startsWith("gateway:") && !capabilityConfigurable(row.capabilities, "conversation") }))
}
