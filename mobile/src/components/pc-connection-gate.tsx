import type { ReactNode } from "react"
import { usePhoneAccount } from "./gateway-account-shell"
import { InboxSkeleton } from "./inbox-skeleton"
import { PcReadyGate } from "./pc-ready-gate"
import { HostChrome } from "./host-chrome"
import { t } from "@/lib/i18n"
import { linkLabel, type SavedLink } from "@/lib/store"

/** Connection transitions stay on the selected PC instead of opening User center. */
export function PcConnectionGate({ hosts, activeFingerprint, connecting, connected, ready, error, onRetry, onSelectHost, onOpenChat, children }: {
  hosts: SavedLink[]
  activeFingerprint: string
  connecting: boolean
  connected: boolean
  ready: boolean
  error?: string
  onRetry: () => void
  onSelectHost: (fingerprint: string) => void
  onOpenChat: () => void
  children: ReactNode
}) {
  const account = usePhoneAccount()
  if (account.mode !== "account") return children
  const selected = hosts.find(host => host.fingerprint === activeFingerprint)
  const notice = selected && (connecting || connected) ? (
    <div data-testid="pc-connection" className="flex h-full min-h-0 flex-col">
      <h2 className="sr-only">{linkLabel(selected, hosts)}</h2>
      <p role="status" className="px-4 pt-4 text-sm text-muted-foreground">{t(connected ? "pcWait.restoring" : "scan.connecting")}</p>
      <InboxSkeleton pending={connecting || connected} error={error} onRetry={onRetry} />
    </div>
  ) : <div className="flex h-full min-h-0 flex-col">
    {error ? <p role="alert" className="px-4 text-sm text-destructive">{error}</p> : null}
    {account.empty}
  </div>
  return <PcReadyGate waiting={!connected || !ready} notice={
    <div className="mx-auto flex h-full max-w-lg flex-col">
      <HostChrome hosts={hosts} activeFingerprint={activeFingerprint} path=""
        connected={connected} reconnecting={connecting && !connected} showChat
        onSelect={onSelectHost} onSelectChat={onOpenChat} />
      <div className="min-h-0 flex-1">{notice}</div>
    </div>
  }>{children}</PcReadyGate>
}
