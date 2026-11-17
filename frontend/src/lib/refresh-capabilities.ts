import { refreshMetaAndModels } from "@/store/app-models"

let pending: Promise<void> | undefined
/** Reuse one request across surfaces; discovery never rewrites saved choices. */
export function refreshCapabilities(): Promise<void> {
  if (pending) return pending
  pending = (async () => {
    try { await fetch("/api/account/capabilities/refresh", { method: "POST", redirect: "error", cache: "no-store" }) }
    finally { await refreshMetaAndModels() }
  })().finally(() => { pending = undefined })
  return pending
}
