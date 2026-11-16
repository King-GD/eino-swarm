import { useEffect, useRef, useState } from "react"
import { X } from "lucide-react"
import { AccountPcList } from "@/components/account-pc-list"
import type { GatewayAccountController } from "@/components/gateway-account-shell"
import { Button } from "@/components/ui/button"
import type { DirectProvider } from "@/lib/direct-provider"
import { AccountPortrait } from "@/components/account-portrait"
import { t } from "@/lib/i18n"
import { gatewayUserDisplayName } from "@/lib/gateway-names"
import { WebExecutionSettings } from "@/components/web-execution-settings"
import type { PcWebSnapshot } from "@/components/pc-web-execution-status"
import { SpeechStatus } from "@/components/speech-status"
import { UpdateNotice } from "@/components/update-notice"

export function GatewayAccountScreen({ account, providers, modelBusy, modelError, pcWeb, onClose, onConnect, onRefresh, onOtherWays, onToggleMode, pcRequired=false }: {
  pcRequired?: boolean
  onToggleMode?: () => void
  account: GatewayAccountController
  providers: DirectProvider[]
  modelBusy: boolean
  modelError: boolean
  pcWeb?: PcWebSnapshot
  onClose?: () => void
  onOpenChat?: () => void
  /** Present on the signed-in start page, where pairing by QR and a model
   *  service of one's own are the secondary ways in. */
  onOtherWays?: { scan: () => void; addModel: () => void }
  onConnect: (id: string) => Promise<void>
  onRefresh: () => Promise<void>
}) {
  const lastTouch = useRef(Number.NEGATIVE_INFINITY)
  const clicks = useRef(0)
  const clickTimer = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(clickTimer.current), [])
  const portraitClick = () => {
    clicks.current++
    window.clearTimeout(clickTimer.current)
    clickTimer.current = window.setTimeout(() => { clicks.current = 0 }, 1200)
    if (clicks.current < 3) return
    clicks.current = 0
    window.clearTimeout(clickTimer.current)
    onToggleMode?.()
  }
  const [otherWays, setOtherWays] = useState(false)
  const authorized = account.status === "authorized"
  const accountName = account.session ? gatewayUserDisplayName(account.session.user) : ""
  const waiting = ["checking", "authorization_pending", "account_pending", "account_rejected"].includes(account.status) || Boolean(account.session && account.status === "access_denied")
  const statusText = account.status === "account_pending" ? t("account.pending")
    : account.status === "account_rejected" ? t("account.rejected")
    : account.status === "authorization_pending" ? t("account.browser")
    : account.status === "checking" ? t("account.checking")
    : account.status === "access_denied" ? t(account.session ? "account.accessDenied" : "account.denied")
    : account.status === "expired" ? t("account.expired") : ""
  return (
    <div className="mx-auto flex h-full max-w-lg flex-col">
      <UpdateNotice />
      <main data-testid="gateway-account" className="flex min-h-0 flex-1 flex-col overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <header className="flex h-12 shrink-0 items-center border-b border-border px-4">
        <h1 className="flex-1 text-base font-semibold">{t("account.title")}</h1>
        {onClose ? <Button variant="ghost" className="size-10 px-0" aria-label={t("home.close")} onClick={onClose}><X className="size-5" aria-hidden /></Button> : null}
      </header>
      <div className="flex flex-col gap-4 p-5">
        <div className="flex items-center gap-3">
          <button type="button" data-testid="account-portrait" aria-label={accountName || t("account.title")} className="touch-manipulation rounded-full"
            onPointerUp={(event) => {
              if (event.pointerType !== "touch" || !event.isPrimary) return
              lastTouch.current = performance.now()
              portraitClick()
            }}
            onClick={() => { if (performance.now() - lastTouch.current > 500) portraitClick() }}>
            <AccountPortrait name={accountName} url={account.session?.user.avatar_url} />
          </button>
          <p className="break-all text-sm font-medium">{accountName}</p>
        </div>
        {statusText ? <p role="status" className="text-sm text-muted-foreground">{statusText}</p> : null}
        {account.error ? <p role="alert" className="text-sm text-destructive">{t(account.error === "browser_unavailable" ? "account.browserFailed" : account.error === "gateway_timeout" ? "account.timeout" : account.error === "pc_upgrade_failed" ? "account.upgradeFailed" : "chat.failed")}</p> : null}
        {!authorized && (!waiting || account.status === "access_denied") ? <>
          <p className="text-sm leading-relaxed text-muted-foreground">{t("account.hint")}</p>
          <Button disabled={account.busy} onClick={() => void account.login()}>{t("account.login")}</Button>
        </> : null}
        {authorized && !pcRequired ? <WebExecutionSettings pcWeb={pcWeb} /> : null}
        {!waiting && !pcRequired ? <SpeechStatus /> : null}
        {authorized ? <>
          {!pcRequired && (providers.length > 0 || modelBusy || modelError) ? <section className="flex flex-col gap-2">
            <h2 className="text-sm font-semibold">{t("account.models")}</h2>
            {modelBusy ? <p role="status" className="text-sm text-muted-foreground">{t("account.modelsLoading")}</p> : null}
            {modelError ? <p role="alert" className="text-sm text-destructive">{t("account.modelsFailed")}</p> : null}
            {providers.map(provider => <div key={provider.id}>
              <h3 className="text-sm font-medium">{provider.label}</h3>
              <p className="break-all text-xs text-muted-foreground">{provider.catalog.map(name => provider.modelLabels?.[name] ?? name).join(" · ")}</p>
            </div>)}
          </section> : null}

          {pcRequired && !onClose ? <section aria-labelledby="pc-wait-title" data-testid="pc-wait">
            <h2 id="pc-wait-title" className="text-base font-semibold">{t("pcWait.title")}</h2>
            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{t("pcWait.hint")}</p>
            {account.hosts.length > 0 ? <p role="status" className="mt-2 text-sm text-muted-foreground">{t(account.hosts.some(host => host.online && host.enabled) ? "pcWait.connect" : "pcWait.offline")}</p> : null}
          </section> : null}
          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-semibold">{t("account.pcs")}</h2>
            {!account.hosts.length ? <p className="text-sm text-muted-foreground">{t("account.empty")}</p> : <AccountPcList hosts={account.hosts} onConnect={onConnect} />}
          </section>
          {onOtherWays ? <section className="flex flex-col gap-2">
            <Button variant="ghost" aria-expanded={otherWays} aria-controls="account-other-ways" onClick={() => setOtherWays((value) => !value)}>{t("welcome.other")}</Button>
            {otherWays ? <div id="account-other-ways" className="flex flex-col gap-2">
              <p className="text-xs text-muted-foreground">{t("welcome.otherHint")}</p>
              <Button variant="outline" onClick={onOtherWays.scan}>{t("home.addHost")}</Button>
              <Button variant="outline" onClick={onOtherWays.addModel}>{t("chat.connect")}</Button>
            </div> : null}
          </section> : null}
        </> : null}
        {(authorized || waiting) ? <Button variant="outline" disabled={account.busy} onClick={() => void onRefresh()}>{t("account.retry")}</Button> : null}
        {(account.session || waiting) ? <Button variant="ghost" onClick={() => void account.logout()}>{t("account.logout")}</Button> : null}
      </div>
      </main>
    </div>
  )
}
