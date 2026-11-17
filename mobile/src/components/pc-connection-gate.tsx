import type { ReactNode } from "react"
import { AccountButton } from "./account-button"
import { usePhoneAccount } from "./gateway-account-shell"
import { InboxSkeleton } from "./inbox-skeleton"
import { PcReadyGate } from "./pc-ready-gate"
import { t } from "@/lib/i18n"
import { linkLabel, type SavedLink } from "@/lib/store"

/** Connection transitions stay on the selected PC instead of opening User center. */
export function PcConnectionGate({ hosts, activeFingerprint, connecting, connected, ready, error, onRetry, children }: {
  hosts: SavedLink[]
  activeFingerprint: string
  connecting: boolean
  connected: boolean
  ready: boolean
  error?: string
  onRetry: () => void
  children: ReactNode
}) {
  const account = usePhoneAccount()
  if (account.mode !== "account") return children
  const selected = hosts.find(host => host.fingerprint === activeFingerprint)
  const notice = selected && (connecting || connected) ? (
    <div data-testid="pc-connection" className="flex h-full min-h-0 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
        <h1 className="min-w-0 flex-1 truncate text-sm font-medium">{linkLabel(selected, hosts)}</h1>
        {account.openAccount ? <AccountButton onClick={() => account.openAccount?.()} /> : null}
      </header>
      <p role="status" className="px-4 pt-4 text-sm text-muted-foreground">{t(connected ? "pcWait.restoring" : "scan.connecting")}</p>
      <InboxSkeleton pending={connecting || connected} error={error} onRetry={onRetry} />
    </div>
  ) : <div className="flex h-full min-h-0 flex-col">
    {error ? <p role="alert" className="px-4 text-sm text-destructive">{error}</p> : null}
    {account.empty}
  </div>
  return <PcReadyGate waiting={!connected || !ready} notice={notice}>{children}</PcReadyGate>
}
