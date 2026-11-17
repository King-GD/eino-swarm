import { useSyncExternalStore } from "react"
import { selectedFeatureAllows, type ModelPolicy, type ModelRole } from "../../../frontend/src/lib/model-policy"
import type { RemoteLink } from "./link"

const policies = new WeakMap<RemoteLink, ModelPolicy>()
const listeners = new Set<() => void>()
let revision = 0
export function publishPairedCapabilities(link: RemoteLink, policy: ModelPolicy | undefined): void {
  if (policy) policies.set(link, policy)
  else policies.delete(link)
  revision++
  for (const listener of listeners) listener()
}
export function usePairedFeature(link: RemoteLink | undefined, providerID: string | undefined, role: ModelRole): boolean {
  useSyncExternalStore(listener => { listeners.add(listener); return () => { listeners.delete(listener) } }, () => revision)
  return Boolean(link && selectedFeatureAllows(policies.get(link), providerID, role))
}
