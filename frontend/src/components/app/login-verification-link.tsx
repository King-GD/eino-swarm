import { Button } from "@/components/ui/button"
import type { RemoteLoginStatus } from "@/lib/types"
import { useT } from "@/lib/use-t"

import { settingsHintClass } from "./settings-field"

/** Only pending, server-validated public authorization URLs reach this view. */
export function LoginVerificationLink({ login }: { login?: RemoteLoginStatus }) {
  const t = useT()
  if (!login?.verification_uri || !["authorization_pending", "account_pending", "account_rejected"].includes(login.state)) return null
  return (
    <div className="flex flex-col items-start gap-2">
      <p className={settingsHintClass}>{t("settings.remote.login.manualHint")}</p>
      <Button asChild variant="outline" size="sm">
        <a href={login.verification_uri} target="_blank" rel="noopener noreferrer">
          {t("settings.remote.login.openVerification")}
        </a>
      </Button>
      <a className="select-all break-all text-sm text-primary underline" href={login.verification_uri} target="_blank" rel="noopener noreferrer">
        {login.verification_uri}
      </a>
    </div>
  )
}
