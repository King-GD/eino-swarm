
import { SelectItem } from "@/components/ui/select"
import { accountKindTally, accountModelsProblem, officialModelLock, type AccountKindKey } from "@/lib/account-models"
import type { MessageKey } from "@/lib/i18n"
import type { Settings } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { useApp } from "@/store/app"

import { AccountSignIn } from "./account-sign-in"
import {
  SettingsChoice, SettingsRow, SettingsSection, settingsHintClass, settingsMatch,
} from "./settings-field"

/** The signed-in server account, ahead of the person's own model services.
 *  The line is a count by the server's classification. The model names and
 *  the conversation default live on the server row below. */
export function AccountModelsCard({
  settings, onChange, query = "",
}: {
  settings: Settings
  onChange: (s: Settings) => void
  query?: string
}) {
  const t = useT()
  const account = useApp((s) => s.meta?.account_models)
  const fofa = useApp((s) => s.meta?.product_mode) === "fofa"
  const local = useApp((s) => s.meta?.product_mode) === "local"
  const problem = accountModelsProblem(account)
  const ready = account?.state === "ready"
  const tally = accountKindTally(account?.kinds)
  const visibleRoles = (account?.roles ?? []).filter((role) => role.id !== "decisions")
  const ownReady = settings.models.providers.some((p) => p.ready && p.enabled !== false)
  const source = settings.models.default_source ?? "account"
  const officialOnly = officialModelLock(account, settings.model_policy)
  const kindText: Record<Exclude<AccountKindKey, "decisions">, MessageKey> = {
    chat: "accountModels.kind.chat",
    unclassified: "accountModels.kind.unclassified",
    embedding: "accountModels.kind.embedding",
    ocr: "accountModels.kind.ocr",
    transcription: "accountModels.kind.transcription",
    speech: "accountModels.kind.speech",
    other: "accountModels.kind.other",
  }
  const counted = tally.map((part) => t(kindText[part.key], { count: part.count })).join(" · ")
  const summary = !account || account.state === "signed_out"
    ? t("accountModels.signedOut")
    : account.state === "pending"
      ? t("accountModels.pending")
      : ready
        ? counted || (account.username
          ? t("accountModels.readyAs", { count: account.models, name: account.display_name || account.username })
          : t("accountModels.ready", { count: account.models }))
        : t(problem ?? "accountModels.reason.unknown")

  if (settings.model_policy?.mode === "local") return null
  if (!settingsMatch(query, t("accountModels.title"), t("accountModels.desc"), "account server official")) return null
  return (
    <SettingsSection title={t("accountModels.title")} description={t("accountModels.desc")}>
      <div className="flex flex-col gap-3 px-4 py-3" data-settings-row="" data-testid="account-models-card">
        <div role="status" aria-live="polite">
          <p className={settingsHintClass}>{summary}</p>
        </div>
        {ready ? null : <AccountSignIn />}
      </div>
      {ready && fofa && visibleRoles.length > 0 ? (
        <div className="grid gap-2 px-4 py-3" data-testid="account-model-roles">
          <p className="text-(length:--chrome-font-size) font-medium">{t("accountModels.roles")}</p>
          {visibleRoles.map((role) => (
            <label key={role.id} className="flex items-center justify-between gap-3 text-(length:--chrome-font-size)">
              <span>{role.label || role.id}</span>
              <span className="flex items-center gap-2 text-muted-foreground">
                {role.enabled ? t("accountModels.roleOn") : t("accountModels.roleOff")}
                <input type="checkbox" checked={role.enabled} disabled aria-label={role.label || role.id} readOnly />
              </span>
            </label>
          ))}
        </div>
      ) : null}
      {!settings.model_policy && ready && ownReady && !officialOnly ? (
        <SettingsChoice
          label={t("accountModels.defaultTitle")}
          value={source}
          ariaLabel={t("accountModels.defaultTitle")}
          onValueChange={(next) =>
            onChange({
              ...settings,
              models: { ...settings.models, default_source: next === "provider" ? "provider" : "account" },
            })}
        >
          <SelectItem value="account">{t("accountModels.defaultAccount")}</SelectItem>
          <SelectItem value="provider">{t("accountModels.defaultProvider")}</SelectItem>
        </SettingsChoice>
      ) : null}
      {!settings.model_policy && ready && !ownReady && !officialOnly ? (
        <SettingsRow label={t("accountModels.defaultTitle")} hint={t("accountModels.defaultNeedsOwn")} />
      ) : null}
    </SettingsSection>
  )
}
