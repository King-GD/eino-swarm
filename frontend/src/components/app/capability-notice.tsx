import { useState } from "react"
import { Button } from "@/components/ui/button"
import { refreshCapabilities } from "@/lib/refresh-capabilities"
import { useT } from "@/lib/use-t"
import { useApp } from "@/store/app"

export function CapabilityNotice() {
  const t = useT(), [busy, setBusy] = useState(false)
  const policy = useApp(s => s.meta?.model_policy)
  if (policy?.capability_status !== "unavailable" && (policy?.mode !== "gateway" || policy.capability_status === "ready")) return null
  return <div role="status" className="flex items-center gap-2 px-3 py-2 text-sm text-muted-foreground">
    <span>{t("modelPolicy.unresolved")}</span>
    <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => {
      setBusy(true); void refreshCapabilities().catch(() => undefined).finally(() => setBusy(false))
    }}>{busy ? "…" : t("modelPolicy.retry")}</Button>
  </div>
}
