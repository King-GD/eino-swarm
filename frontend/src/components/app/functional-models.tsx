import { useId } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { SelectGroup, SelectItem, SelectLabel } from "@/components/ui/select"
import { functionalChoiceLabel, functionalGroupHeadings, functionalModelChoices, groupFunctionalChoices, type FunctionalSourceGroup } from "@/lib/functional-model-choices"
import type { MessageKey } from "@/lib/i18n"
import { configurableModelRoles, policyAllows, policyConfigurable, type ModelRef, type ModelRole } from "@/lib/model-policy"
import { modelChoiceId, parseModelChoiceId } from "@/lib/models"
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
  translation: "modelPolicy.translation",
  image: "modelPolicy.image",
}

const groupLabels: Record<FunctionalSourceGroup, MessageKey> = {
  gateway: "modelPolicy.group.gateway",
  third_party: "modelPolicy.group.thirdParty",
  local: "modelPolicy.group.local",
}

export function FunctionalModels({ settings, onChange, query = "" }: {
  settings: Settings
  onChange: (value: Settings) => void
  query?: string
}) {
  const t = useT(), id = useId()
  const account = useApp(s => s.meta?.account_models)
  const models = useApp(s => s.models)
  if (!settings.model_policy?.configurable) return null
  const update = (role: Exclude<ModelRole, "decisions">, ref?: ModelRef) => {
    const bindings = { ...settings.model_policy?.bindings, ...settings.models.bindings }
    if (ref) bindings[role] = ref
    else delete bindings[role]
    onChange({ ...settings, models: { ...settings.models, bindings },
      ...(role === "embedding" ? { search: { embedding: false, embedding_provider: "", embedding_model: "" } } : {}),
    })
  }
  return <SettingsSection title={t("modelPolicy.title")} description={t("modelPolicy.description")}>
    {configurableModelRoles.filter(role => role === "image" || role === "translation" || role === "embedding" || policyConfigurable(settings.model_policy, role)).map(role => {
      if (!policyConfigurable(settings.model_policy, role)) {
        const managed = policyAllows(settings.model_policy, role)
        return <SettingsRow key={role} query={query} label={t(labels[role])}
          hint={t(managed ? "modelPolicy.roleManagedHint" : "modelPolicy.roleUnavailable")}>
          <span className="text-muted-foreground">{t(managed ? "modelPolicy.roleManaged" : "modelPolicy.unavailable")}</span>
          {role === "image" && (settings.models.bindings?.image ?? settings.model_policy?.bindings?.image)?.model
            ? <Button type="button" variant="ghost" size="sm" onClick={() => update(role)}>{t("modelPolicy.default")}</Button> : null}
        </SettingsRow>
      }
      const fallback = settings.model_policy?.defaults?.[role]
      const legacy = role === "embedding" && !fallback && settings.search?.embedding_model?.trim()
        ? { provider_id: settings.search.embedding_provider || settings.models.default, model: settings.search.embedding_model } : undefined
      const ref = settings.models.bindings?.[role] ?? settings.model_policy?.bindings?.[role] ?? legacy
      const off = role === "embedding" && ref?.provider_id === "" && ref.model === ""
      const choices = functionalModelChoices({
        role, policy: settings.model_policy, models, providers: settings.models.providers, account,
        selected: ref?.provider_id && ref.model ? ref : undefined,
      })
      const groups = groupFunctionalChoices(choices)
      const sections = groups.flatMap(group => functionalGroupHeadings(group.group, group.choices, t(groupLabels[group.group])))
      const names = choices.filter(choice => choice.providerId === ref?.provider_id).map(choice => choice.model)
      const value = off ? "__disabled" : ref?.provider_id && ref.model ? modelChoiceId(ref.provider_id, ref.model) : "__default"
      return <div key={role} data-settings-key={`model-${role}`}>
        <SettingsChoice label={t(labels[role])}
          hint={role === "image" ? t("modelPolicy.imageHint") : role === "embedding" ? t("modelPolicy.embeddingHint") : undefined}
          query={query} value={value}
          ariaLabel={role === "image" ? t(labels[role]) : `${t(labels[role])} ${t("modelPolicy.provider")}`}
          onValueChange={next => {
            // Radix reports an empty value when the current id is not mounted yet.
            // That is not a request to clear the saved model.
            if (!next) return
            if (next === "__disabled") update(role, { provider_id: "", model: "" })
            else if (next === "__default") update(role)
            else {
              const choice = parseModelChoiceId(next)
              if (!choice.providerId || !choice.model) return
              update(role, { provider_id: choice.providerId, model: choice.model })
            }
          }}>
          {role === "embedding" ? <SelectItem value="__disabled">{t("modelPolicy.off")}</SelectItem> : null}
          <SelectItem value="__default">{t(role === "embedding" && !fallback ? "modelPolicy.defaultOff" : "modelPolicy.default")}{fallback ? ` · ${fallback.model}` : ""}</SelectItem>
          {sections.map(section => <SelectGroup key={section.key}>
            {sections.length > 1 ? <SelectLabel>{section.title}</SelectLabel> : null}
            {section.choices.map(choice => {
              const choiceId = modelChoiceId(choice.providerId, choice.model)
              return <SelectItem key={choiceId} value={choiceId}>{functionalChoiceLabel(choice, section.choices)}</SelectItem>
            })}
          </SelectGroup>)}
        </SettingsChoice>
        {ref?.provider_id && ref.model && role !== "image" ? <SettingsRow label={t("modelPolicy.model")} query={query} search={[t(labels[role])]}>
          <Input aria-label={`${t(labels[role])} ${t("modelPolicy.model")}`} key={`${ref.provider_id}:${ref.model}`}
            defaultValue={ref.model} list={`${id}-${role}`}
            onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); event.currentTarget.blur() } }}
            onBlur={event => {
              const model = event.target.value.trim()
              if (!model && role === "embedding") update(role, { provider_id: "", model: "" })
              else if (model && model !== ref.model) update(role, { ...ref, model })
              else event.target.value = ref.model
            }} />
          <datalist id={`${id}-${role}`}>{names.map(name => <option key={name} value={name} />)}</datalist>
          <Button type="button" variant="ghost" size="sm" onClick={() => update(role)}>{t("modelPolicy.default")}</Button>
        </SettingsRow> : null}
      </div>
    })}
  </SettingsSection>
}
