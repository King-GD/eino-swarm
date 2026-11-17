import { policyAllows, policyConfigurable } from "@/lib/model-policy"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { chromeTypeClass } from "@/lib/chrome-type"
import { embeddingModelChoices } from "@/lib/account-models"
import {
  groupModels,
  modelChoiceId,
  parseModelChoiceId,
} from "@/lib/models"
import { useApp } from "@/store/app"
import { defaultSearchSettings, type SearchSettings, type Settings } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useT } from "@/lib/use-t"

import {
  SettingsRow,
  SettingsSection,
  settingsSelectTriggerClass,
} from "./settings-field"

/** Legacy settings without role bindings still derive search from the model. */
export function SearchSettingsPanel({
  settings,
  onChange,
  query = "",
}: {
  settings: Settings
  onChange: (s: Settings) => void
  query?: string
}) {
  const t = useT()
  const account = useApp((s) => s.meta?.account_models)
  if (settings.model_policy && !policyAllows(settings.model_policy, "embedding")) return null
  const search = defaultSearchSettings(settings.search)
  const update = (patch: Partial<SearchSettings>) =>
    onChange({
      ...settings,
      search: { ...search, ...patch },
    })
  const choices = embeddingModelChoices(account)
  const groups = groupModels(
    choices.map((c) => ({
      provider_id: c.providerId,
      provider_label: c.providerLabel,
      model: c.model,
      label: c.label,
    })),
  )
  const pinned = choices.find(
    (c) =>
      c.model === search.embedding_model &&
      (!search.embedding_provider || c.providerId === search.embedding_provider),
  )

  return (
    <SettingsSection
      title={t("settings.searchEmbed.title")}
      description={t("settings.searchEmbed.desc")}
    >
      <SettingsRow
        query={query}
        search={[
          t("settings.searchEmbed.model"),
          t("settings.searchEmbed.modelHint"),
          "embedding",
        ]}
        label={t("settings.searchEmbed.model")}
        hint={pinned ? pinned.label : search.embedding_model || t("settings.searchEmbed.modelHint")}
        badge={<Badge variant="default">{t("settings.searchEmbed.badge")}</Badge>}
      >
        {choices.length > 0 && (!settings.model_policy || policyConfigurable(settings.model_policy, "embedding")) ? (
          <Select
            value={
              pinned
                ? modelChoiceId(pinned.providerId, pinned.model)
                : undefined
            }
            onValueChange={(id) => {
              const next = parseModelChoiceId(id)
              update({
                embedding_provider: next.providerId,
                embedding_model: next.model,
                embedding: Boolean(next.model.trim()),
              })
            }}
          >
            <SelectTrigger
              aria-label={t("settings.searchEmbed.modelAria")}
              className={settingsSelectTriggerClass}
            >
              <SelectValue placeholder={t("settings.searchEmbed.pick")} />
            </SelectTrigger>
            <SelectContent>
              {groups.map((g) => (
                <SelectGroup key={g.providerId}>
                  {groups.length > 1 ? <SelectLabel>{g.label}</SelectLabel> : null}
                  {g.models.map((m) => {
                    const id = modelChoiceId(m.provider_id, m.model)
                    return (
                      <SelectItem key={id} value={id}>
                        {m.label || m.model}
                      </SelectItem>
                    )
                  })}
                </SelectGroup>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            value={search.embedding_model}
            onChange={(e) => update({ embedding_model: e.target.value, embedding: Boolean(e.target.value.trim()) })}
            aria-label={t("settings.searchEmbed.modelAria")}
            placeholder={t("settings.searchEmbed.pick")}
            className={cn("h-[32px] w-56", chromeTypeClass)}
          />
        )}
      </SettingsRow>
      <SettingsRow query={query} label={t("settings.searchEmbed.embedding")}>
        <Button type="button" variant="ghost" size="sm" disabled={!search.embedding_model}
          onClick={() => update({ embedding: false, embedding_provider: "", embedding_model: "" })}>
          {t("modelPolicy.off")}
        </Button>
      </SettingsRow>
    </SettingsSection>
  )
}
