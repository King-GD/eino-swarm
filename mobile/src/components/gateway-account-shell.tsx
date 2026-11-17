import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react"
import { GatewayAccountScreen } from "@/components/gateway-account-screen"
import type { PcWebSnapshot } from "@/components/pc-web-execution-status"
import { loadPhoneMode, savePhoneMode, type PhoneMode } from "@/lib/phone-mode"
import { installAndroidBack } from "@/lib/android-back"
import { type AccountPresentation } from "@/lib/account-presentation"
import type { DirectProvider } from "@/lib/direct-provider"
import { gatewayUserDisplayName } from "@/lib/gateway-names"
import { t } from "@/lib/i18n"
import type { GatewayHost } from "@/lib/gateway-account"
import { type SavedLink } from "@/lib/store"
import { useAutoGatewayPCs } from "@/lib/use-auto-gateway-pcs"
import { useLocalModelSources } from "@/lib/use-local-model-sources"
import { useGatewayAccount } from "@/lib/use-gateway-account"
import { PcModeSwitchError, type BindPcModeSwitch } from "@/lib/use-pc-mode-switch"

type Account = ReturnType<typeof useGatewayAccount>
type PhoneAccount = {
  bindPcModeSwitch?: BindPcModeSwitch
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
  pcs?: { hosts: GatewayHost[]; origin?: string; connect: (id: string) => Promise<void> }
  hasProfile?: boolean
  officialOnly?: boolean
  presentation?: AccountPresentation
  setPresentation?: (value: AccountPresentation) => void
}
export const PhoneAccountContext = createContext<PhoneAccount>({ providers: [], presentation: "official" })
export const usePhoneAccount = () => useContext(PhoneAccountContext)

export function GatewayAccountShell({ children }: { children: ReactNode }) {
  const account = useGatewayAccount()
  useAutoGatewayPCs(account)
  const [mode, setMode] = useState(loadPhoneMode)
  const pcSwitch = useRef<((mode: PhoneMode) => Promise<void>) | null>(null)
  const bindPcModeSwitch: BindPcModeSwitch = useCallback(switcher => {
    pcSwitch.current = switcher
    return () => { if (pcSwitch.current === switcher) pcSwitch.current = null }
  }, [])
  const switching = useRef(false)
  const [modeBusy, setModeBusy] = useState(false)
  const [modeError, setModeError] = useState("")
  const authority = `${account.session?.origin}:${account.session?.user.id}:${account.session?.device.id}:${account.session?.token}`
  const currentAuthority = useRef(authority)
  currentAuthority.current = authority
  const toggleMode = () => {
    if (switching.current) return
    const next = mode === "account" ? "local" : "account"
    const commit = () => {
      if (currentAuthority.current !== authority) return
      savePhoneMode(next)
      setMode(next)
      setModeError("")
      setDialog(undefined)
    }
    if (!pcSwitch.current) { commit(); return }
    switching.current = true
    setModeBusy(true)
    setModeError("")
    void pcSwitch.current(next).then(commit).catch(error => {
      if (currentAuthority.current === authority) setModeError(t(error instanceof PcModeSwitchError
        ? `account.mode.${error.reason}` : "account.mode.failed"))
    }).finally(() => { switching.current = false; setModeBusy(false) })
  }
  const [dialog, setDialog] = useState<{ pcWeb?: PcWebSnapshot }>()
  useEffect(() => {
    const selected = () => setDialog(undefined)
    window.addEventListener("zwai.gateway.connect", selected)
    return () => window.removeEventListener("zwai.gateway.connect", selected)
  }, [])
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
  useEffect(() => { if (open && !restricted) void account.refresh() }, [open, restricted])
  const openChat = mode === "local" ? () => { setDialog(undefined); window.dispatchEvent(new Event("zwai.gateway.chat")) } : undefined
  const otherWays = {
    scan: () => window.dispatchEvent(new Event("zwai.gateway.scan")),
    addModel: () => window.dispatchEvent(new Event("zwai.gateway.models")),
  }
  useEffect(() => { window.dispatchEvent(new Event("zwai.gateway.account")) }, [account.hosts])
  const screen = <GatewayAccountScreen account={account} onToggleMode={toggleMode} modeBusy={modeBusy} modeError={modeError} providers={providers} modelBusy={modelBusy}
    modelError={modelError} pcRequired={mode === "account"} pcWeb={dialog?.pcWeb} onClose={restricted ? undefined : () => setDialog(undefined)} onConnect={connect} onRefresh={refresh} onOpenChat={openChat} />
  return (
    <PhoneAccountContext.Provider value={{
      bindPcModeSwitch,
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
        modeBusy={modeBusy} modeError={modeError}
        providers={providers} modelBusy={modelBusy} modelError={modelError} pcRequired={mode === "account"} onConnect={connect}
        onRefresh={refresh} onOpenChat={openChat} onOtherWays={mode === "local" ? otherWays : undefined} /> : undefined,
      pcs: account.status === "authorized" ? { hosts: account.hosts, origin: account.session?.origin, connect } : undefined,
    }}>
      {restricted ? screen : <div key={`${mode}:${account.session?.origin ?? ""}:${account.session?.user.id ?? ""}:${account.session?.device.id ?? ""}`} className="h-full min-h-0" inert={open || undefined} aria-hidden={open || undefined}>{children}</div>}
      {open && !restricted ? <div role="dialog" aria-modal="true" aria-label={t("account.title")} className="fixed inset-0 z-50 bg-background pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[env(safe-area-inset-top)]">{screen}</div> : null}
    </PhoneAccountContext.Provider>
  )
}

export type GatewayAccountController = Account
