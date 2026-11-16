import { useId } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { SelectItem } from "@/components/ui/select"
import type { MessageKey } from "@/lib/i18n"
import { configurableModelRoles, type ModelRef, type ModelRole } from "@/lib/model-policy"
import type { Settings } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { useApp } from "@/store/app"
import { SettingsChoice, SettingsRow, SettingsSection } from "./settings-field"

const labels: Record<Exclude<ModelRole, "decisions">, MessageKey> = {
  conversation: "modelPolicy.conversation",
  title: "modelPolicy.titleRole",
  compact: "modelPolicy.compact",
  embedding: "modelPolicy.embedding",
  transcription: "modelPolicy.transcription",
  speech: "modelPolicy.speech",
  ocr: "modelPolicy.ocr",
}

export function FunctionalModels({ settings, onChange, query = "" }: {
  settings: Settings
  onChange: (value: Settings) => void
  query?: string
}) {
  const t = useT(), id = useId()
  const account = useApp(s => s.meta?.account_models)
  const accountRows = useApp(s => s.models).filter(row => row.source === "account")
  if (!settings.model_policy?.configurable) return null
  const providers = settings.models.providers.map(p => ({
    id: p.id, label: p.label || p.id,
    names: [...new Set([p.model, ...(p.catalog ?? [])].filter(Boolean))],
  }))
  if (settings.model_policy.mode === "gateway" && account?.source_id) {
    providers.unshift({
      id: account.source_id, label: account.label || account.source_id,
      names: [...new Set([
        ...accountRows.map(row => row.model),
        ...Object.values(settings.model_policy.defaults ?? {}).map(ref => ref.model),
        ...(account.kinds?.embedding_models ?? []),
        ...(account.kinds?.transcription_models ?? []),
        ...(account.kinds?.speech_models ?? []),
      ])],
    })
  }
  const update = (role: Exclude<ModelRole, "decisions">, ref?: ModelRef) => {
    const bindings = { ...settings.models.bindings }
    if (ref) bindings[role] = ref
    else delete bindings[role]
    onChange({ ...settings, models: { ...settings.models, bindings } })
  }
  return <SettingsSection title={t("modelPolicy.title")} description={t("modelPolicy.description")}>
    {configurableModelRoles.map(role => {
      const ref = settings.models.bindings?.[role]
      const fallback = settings.model_policy?.defaults?.[role]
      const selection = ref ?? fallback
      const provider = providers.find(p => p.id === selection?.provider_id)
      return <div key={role} data-settings-key={`model-${role}`}>
        <SettingsChoice label={t(labels[role])} query={query} value={ref?.provider_id ?? "__default"}
          ariaLabel={`${t(labels[role])} ${t("modelPolicy.provider")}`}
          onValueChange={provider_id => {
            if (provider_id === "__default") update(role)
            else {
              const p = providers.find(row => row.id === provider_id)
              update(role, { provider_id, model: p?.names[0] ?? selection?.model ?? "" })
            }
          }}>
          <SelectItem value="__default">{t("modelPolicy.default")}{fallback ? ` · ${fallback.model}` : ""}</SelectItem>
          {providers.map(p => <SelectItem key={p.id} value={p.id} disabled={!p.names.length}>{p.label}</SelectItem>)}
        </SettingsChoice>
        {ref ? <SettingsRow label={t("modelPolicy.model")} query={query} search={[t(labels[role])]}>
          <Input aria-label={`${t(labels[role])} ${t("modelPolicy.model")}`} key={`${ref.provider_id}:${ref.model}`}
            defaultValue={ref.model} list={`${id}-${role}`}
            onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); event.currentTarget.blur() } }}
            onBlur={event => {
              const model = event.target.value.trim()
              if (model && model !== ref.model) update(role, { ...ref, model })
              else event.target.value = ref.model
            }} />
          <datalist id={`${id}-${role}`}>{provider?.names.map(name => <option key={name} value={name} />)}</datalist>
          <Button type="button" variant="ghost" size="sm" onClick={() => update(role)}>{t("modelPolicy.default")}</Button>
        </SettingsRow> : null}
      </div>
    })}
  </SettingsSection>
}
