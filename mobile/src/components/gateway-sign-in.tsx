import { Button } from "@/components/ui/button"
import type { GatewayAccountController } from "@/components/gateway-account-shell"
import { t } from "@/lib/i18n"

export type GatewaySignInState = ReturnType<typeof gatewaySignInState>

export function gatewaySignInState({ status, session, error, busy, login, logout, refresh }: GatewayAccountController) {
  return { status, hasSession: Boolean(session), error, busy, login, logout, refresh }
}

export function gatewaySignInWaiting({ status, hasSession }: GatewaySignInState) {
  return ["checking", "authorization_pending", "account_pending", "account_rejected"].includes(status)
    || (hasSession && status === "access_denied")
}

export function GatewaySignIn({ account, welcome = false }: { account: GatewaySignInState; welcome?: boolean }) {
  const { status, hasSession, error, busy } = account
  const waiting = gatewaySignInWaiting(account)
  const statusText = status === "account_pending" ? t("account.pending")
    : status === "account_rejected" ? t("account.rejected")
    : status === "authorization_pending" ? t("account.browser")
    : status === "checking" ? t("account.checking")
    : status === "access_denied" ? t(hasSession ? "account.accessDenied" : "account.denied")
    : status === "expired" ? t("account.expired") : ""
  const errorText = error ? t(error === "browser_unavailable" ? "account.browserFailed"
    : error === "gateway_timeout" ? "account.timeout"
    : error === "pc_upgrade_failed" ? "account.upgradeFailed" : "chat.failed") : ""
  return <>
    {statusText ? <p role="status" className="text-sm text-muted-foreground">{statusText}</p> : null}
    {errorText ? <p role="alert" className="text-sm text-destructive">{errorText}</p> : null}
    {status !== "authorized" && (!waiting || status === "access_denied") ? <>
      {welcome ? null : <p className="text-sm leading-relaxed text-muted-foreground">{t("account.hint")}</p>}
      <Button disabled={busy} onClick={() => void account.login()}>{t("account.login")}</Button>
    </> : null}
    {welcome && waiting ? <Button variant="outline" disabled={busy} onClick={() => void account.refresh()}>{t("account.retry")}</Button> : null}
    {welcome && (hasSession || waiting) ? <Button variant="ghost" onClick={() => void account.logout()}>{t("account.logout")}</Button> : null}
  </>
}
