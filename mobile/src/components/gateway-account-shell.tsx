import { createContext, useContext, useEffect, useState, type ReactNode } from "react"
import { GatewayAccountScreen } from "@/components/gateway-account-screen"
import type { PcWebSnapshot } from "@/components/pc-web-execution-status"
import { loadPhoneMode, savePhoneMode, type PhoneMode } from "@/lib/phone-mode"
import { installAndroidBack } from "@/lib/android-back"
import { type AccountPresentation } from "@/lib/account-presentation"
import type { DirectProvider } from "@/lib/direct-provider"
import { gatewayUserDisplayName } from "@/lib/gateway-names"
import { t } from "@/lib/i18n"
import type { GatewayHost } from "@/lib/gateway-account"
import { autoConnectTarget } from "@/lib/gateway-auto-connect"
import { loadActiveFingerprint, loadSavedLinks, type SavedLink } from "@/lib/store"
import { useLocalModelSources } from "@/lib/use-local-model-sources"
import { useGatewayAccount } from "@/lib/use-gateway-account"

type Account = ReturnType<typeof useGatewayAccount>
type PhoneAccount = {
  allowsLink?: (link: SavedLink) => boolean
  mode?: PhoneMode
  user?: { name: string; avatar?: string }
  providers: DirectProvider[]
  openAccount?: (pcWeb?: PcWebSnapshot) => void
  gatewaySourceID?: string
  gatewaySourceIDs?: string[]
  refreshModels?: () => void | Promise<void>
  modelsBusy?: boolean
  modelsError?: boolean
  empty?: ReactNode
  /** The signed-in account's computers, for the Add a PC sheet to list first. */
  pcs?: { hosts: GatewayHost[]; connect: (id: string) => Promise<void> }
  hasProfile?: boolean
  officialOnly?: boolean
  presentation?: AccountPresentation
  setPresentation?: (value: AccountPresentation) => void
}
export const PhoneAccountContext = createContext<PhoneAccount>({ providers: [], presentation: "official" })
export const usePhoneAccount = () => useContext(PhoneAccountContext)

export function GatewayAccountShell({ children }: { children: ReactNode }) {
  const account = useGatewayAccount()
  const [mode, setMode] = useState(loadPhoneMode)
  const toggleMode = () => {
    const next = mode === "account" ? "local" : "account"
    savePhoneMode(next)
    setMode(next)
    setDialog(undefined)
  }
  const [dialog, setDialog] = useState<{ pcWeb?: PcWebSnapshot }>()
  const open = Boolean(dialog)
  const localModels = useLocalModelSources(mode, account.status, account.session, account.bootstrap)
  const providers = localModels.providers
  const modelBusy = localModels.busy, modelError = localModels.error
  const presentation = "official" as const
  const officialOnly = mode === "account"
  const restricted = mode === "account" && account.status !== "authorized"
  useEffect(() => {
    if (!open || restricted) return
    return installAndroidBack(() => { setDialog(undefined); return true })
  }, [open, restricted])
  const refresh = async () => { await account.refresh(); localModels.refresh() }
  const connect = async (id: string) => {
    const saved = await account.connectHost(id)
    setDialog(undefined)
    window.dispatchEvent(new CustomEvent("zwai.gateway.connect", { detail: saved.fingerprint }))
  }
  const openChat = mode === "local" ? () => { setDialog(undefined); window.dispatchEvent(new Event("zwai.gateway.chat")) } : undefined
  const otherWays = {
    scan: () => window.dispatchEvent(new Event("zwai.gateway.scan")),
    addModel: () => window.dispatchEvent(new Event("zwai.gateway.models")),
  }
  const { freshLogin, consumeFreshLogin, status, bootstrap, hosts } = account
  // Once per sign-in, and only on a phone with no computer yet: the one online
  // computer opens by itself. Later refreshes, restarts and a deliberate
  // disconnect never reach this, so it cannot pull the person back.
  useEffect(() => {
    if (!freshLogin || status !== "authorized" || !bootstrap) return
    consumeFreshLogin()
    const target = autoConnectTarget(hosts, loadSavedLinks(), loadActiveFingerprint())
    if (target) void connect(target.id).catch(() => undefined)
  }, [freshLogin, status, bootstrap])
  const screen = <GatewayAccountScreen account={account} onToggleMode={toggleMode} providers={providers} modelBusy={modelBusy}
    modelError={modelError} pcRequired={mode === "account"} pcWeb={dialog?.pcWeb} onClose={restricted ? undefined : () => setDialog(undefined)} onConnect={connect} onRefresh={refresh} onOpenChat={openChat} />
  return (
    <PhoneAccountContext.Provider value={{
      allowsLink: (link) => mode === "local" || Boolean(account.session
        && link.gatewayOrigin === account.session.origin
        && link.gatewayOwnerID === account.session.user.id
        && link.gatewayMobileID === account.session.device.id),
      mode,
      user: account.session ? { name: gatewayUserDisplayName(account.session.user), avatar: account.session.user.avatar_url } : undefined,
      providers, hasProfile: false, officialOnly, presentation,
      gatewaySourceID: mode === "local" && account.status === "authorized" ? account.bootstrap?.models.source_id : "",
      gatewaySourceIDs: mode === "local" && account.status === "authorized" ? [account.bootstrap?.models.source_id, account.bootstrap?.gateway_models?.source_id].filter((id): id is string => Boolean(id)) : [],
      refreshModels: refresh, modelsBusy: modelBusy, modelsError: modelError,
      openAccount: (pcWeb) => setDialog({ pcWeb }),
      empty: account.status === "authorized" ? <GatewayAccountScreen account={account} onToggleMode={toggleMode}
        providers={providers} modelBusy={modelBusy} modelError={modelError} pcRequired={mode === "account"} onConnect={connect}
        onRefresh={refresh} onOpenChat={openChat} onOtherWays={mode === "local" ? otherWays : undefined} /> : undefined,
      pcs: account.status === "authorized" ? { hosts: account.hosts, connect } : undefined,
    }}>
      {restricted ? screen : <div key={`${mode}:${account.session?.origin ?? ""}:${account.session?.user.id ?? ""}:${account.session?.device.id ?? ""}`} className="h-full min-h-0" inert={open || undefined} aria-hidden={open || undefined}>{children}</div>}
      {open && !restricted ? <div role="dialog" aria-modal="true" aria-label={t("account.title")} className="fixed inset-0 z-50 bg-background pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[env(safe-area-inset-top)]">{screen}</div> : null}
    </PhoneAccountContext.Provider>
  )
}

export type GatewayAccountController = Account
