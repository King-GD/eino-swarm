import { capabilityAllows } from "./capabilities"
import { selectedFeatureAllows, type FunctionalCatalog, type ModelPolicy, type ModelRole } from "./model-policy"
import type { AccountModels, ModelInfo, ProviderSettings } from "./types"

type ChoiceProvider = Pick<ProviderSettings, "id" | "label" | "model" | "catalog" | "enabled">

/** Where a functional-model option comes from in local mode. */
export type FunctionalSourceGroup = "gateway" | "third_party" | "local"
export const functionalGroupOrder: FunctionalSourceGroup[] = ["gateway", "third_party", "local"]

export interface FunctionalModelChoice {
  providerId: string
  providerLabel: string
  model: string
  group: FunctionalSourceGroup
}

export function functionalSourceGroup(row: { source?: string; provider_id: string; catalog_group?: string }): FunctionalSourceGroup {
  if (row.catalog_group === "third_party") return "third_party"
  if (row.source === "account" || row.provider_id.startsWith("gateway:")) return "gateway"
  return "local"
}

/** Image generation has no client adapter yet. Its picker lists the same
 *  authorized chat catalogs translation can use, so the control matches the
 *  other rows before that call exists. */
function admissionRole(role: ModelRole): ModelRole {
  return role === "image" ? "translation" : role
}

function sourceAllows(policy: ModelPolicy | undefined, providerId: string, role: ModelRole, models: ModelInfo[]): boolean {
  if (policy?.mode !== "local") return selectedFeatureAllows(policy, providerId, role)
  if (!providerId.startsWith("gateway:")) return true
  const published = policy.account_sources?.[providerId]
  if (published) return capabilityAllows(published, role)
  return capabilityAllows(models.find(row => row.provider_id === providerId)?.capabilities, role)
}

function typedRole(role: ModelRole): role is "embedding" | "transcription" | "speech" {
  return role === "embedding" || role === "transcription" || role === "speech"
}

function catalogNames(role: "embedding" | "transcription" | "speech", catalog: FunctionalCatalog): string[] {
  if (role === "embedding") return catalog.embedding_models ?? []
  if (role === "transcription") return catalog.transcription_models ?? []
  return catalog.speech_models ?? []
}

function legacyKindNames(role: "embedding" | "transcription" | "speech", account?: AccountModels): string[] {
  if (role === "embedding") return account?.kinds?.embedding_models ?? []
  if (role === "transcription") return account?.kinds?.transcription_models ?? []
  return account?.kinds?.speech_models ?? []
}

function add(out: FunctionalModelChoice[], seen: Set<string>, choice: FunctionalModelChoice) {
  const model = choice.model.trim()
  const providerId = choice.providerId.trim()
  if (!providerId || !model) return
  const key = `${providerId}\t${model}`
  if (seen.has(key)) return
  seen.add(key)
  out.push({ ...choice, providerId, model })
}

function labelFor(providerId: string, models: ModelInfo[], providers: ChoiceProvider[], account?: AccountModels): string {
  return models.find(row => row.provider_id === providerId)?.provider_label?.trim()
    || providers.find(provider => provider.id === providerId)?.label?.trim()
    || (account?.source_id === providerId ? account.label?.trim() : "")
    || providerId
}

function groupFor(providerId: string, models: ModelInfo[]): FunctionalSourceGroup {
  const row = models.find(item => item.provider_id === providerId)
  if (row) return functionalSourceGroup(row)
  return functionalSourceGroup({ provider_id: providerId, source: providerId.startsWith("gateway:") ? "account" : "provider" })
}

/** Named models a functional row can pin. Account chat rows stay out of
 *  embedding, transcription and speech. Those names come from each signed-in
 *  catalog; an older payload still has the single account kind list. */
export function functionalModelChoices(input: {
  role: ModelRole
  policy?: ModelPolicy
  models: ModelInfo[]
  providers: ChoiceProvider[]
  account?: AccountModels
  selected?: { provider_id: string; model: string }
}): FunctionalModelChoice[] {
  const { role, policy, models, providers, account, selected } = input
  const out: FunctionalModelChoice[] = []
  const seen = new Set<string>()
  const roleAllows = (providerId: string) => sourceAllows(policy, providerId, admissionRole(role), models)
  const typed = typedRole(role) ? role : undefined
  const catalogs = typed ? policy?.functional_catalogs : undefined
  for (const row of models) {
    const model = row.model?.trim() ?? ""
    if (!row.ready || !model) continue
    // The composer list is conversational. Embedding, transcription and speech
    // take their account names from the typed catalogs and their personal names
    // from the saved provider catalog below.
    if (typed) continue
    if (!roleAllows(row.provider_id)) continue
    add(out, seen, {
      providerId: row.provider_id,
      providerLabel: row.provider_label?.trim() || row.provider_id,
      model,
      group: functionalSourceGroup(row),
    })
  }
  if (typed && catalogs) {
    for (const catalog of catalogs) {
      const providerId = catalog.provider_id?.trim() ?? ""
      if (!providerId || !roleAllows(providerId)) continue
      const providerLabel = catalog.label?.trim() || labelFor(providerId, models, providers, account)
      const group = functionalSourceGroup({ provider_id: providerId, catalog_group: catalog.catalog_group, source: "account" })
      for (const model of catalogNames(typed, catalog)) {
        add(out, seen, { providerId, providerLabel, model, group })
      }
    }
  } else if (typed && account?.source_id && roleAllows(account.source_id)) {
    for (const model of legacyKindNames(typed, account)) {
      add(out, seen, {
        providerId: account.source_id,
        providerLabel: labelFor(account.source_id, models, providers, account),
        model,
        group: groupFor(account.source_id, models),
      })
    }
  }
  for (const provider of providers) {
    if (provider.enabled === false || !roleAllows(provider.id)) continue
    const providerLabel = provider.label?.trim() || provider.id
    for (const model of [provider.model, ...(provider.catalog ?? [])]) {
      add(out, seen, { providerId: provider.id, providerLabel, model: model ?? "", group: "local" })
    }
  }
  if (selected?.provider_id && selected.model && !seen.has(`${selected.provider_id}\t${selected.model}`)) {
    add(out, seen, {
      providerId: selected.provider_id,
      providerLabel: labelFor(selected.provider_id, models, providers, account),
      model: selected.model,
      group: groupFor(selected.provider_id, models),
    })
  }
  return out.sort((a, b) => functionalGroupOrder.indexOf(a.group) - functionalGroupOrder.indexOf(b.group))
}

export function groupFunctionalChoices(choices: FunctionalModelChoice[]): { group: FunctionalSourceGroup; choices: FunctionalModelChoice[] }[] {
  return functionalGroupOrder
    .map(group => ({ group, choices: choices.filter(choice => choice.group === group) }))
    .filter(group => group.choices.length > 0)
}

/** Two providers in one group can publish the same model name. */
export function functionalChoiceLabel(choice: FunctionalModelChoice, group: FunctionalModelChoice[]): string {
  const shared = group.some(other => other.model === choice.model && other.providerId !== choice.providerId)
  return shared ? `${choice.model} · ${choice.providerLabel}` : choice.model
}
