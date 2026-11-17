import type { RemoteLink } from "./link"
import { sameLinkAuthority } from "./link-authority"
import type { SavedLink } from "./store"

/** Inactive PCs keep their sockets; credentials still identify each connection. */
export class RetainedPhoneLinks {
  private readonly links = new Map<string, { saved: SavedLink; link: RemoteLink }>()
  private readonly opening = new Map<string, { saved: SavedLink; promise: Promise<RemoteLink> }>()

  connect(saved: SavedLink, open: (saved: SavedLink) => Promise<RemoteLink>, valid: () => boolean): Promise<RemoteLink> {
    const cached = this.links.get(saved.fingerprint)
    if (cached && cached.link.alive() && sameLinkAuthority(cached.saved, saved)) return Promise.resolve(cached.link)
    if (cached) { this.links.delete(saved.fingerprint); cached.link.close() }
    const pending = this.opening.get(saved.fingerprint)
    if (pending && sameLinkAuthority(pending.saved, saved)) return pending.promise
    const operation = { saved: { ...saved }, promise: Promise.resolve(null as unknown as RemoteLink) }
    operation.promise = Promise.resolve().then(() => {
      if (this.opening.get(saved.fingerprint) !== operation || !valid()) throw new Error("offline")
      return open(saved)
    }).then(link => {
      if (this.opening.get(saved.fingerprint) !== operation || !valid()) {
        link.close()
        throw new Error("offline")
      }
      this.retain(saved, link)
      return link
    }).finally(() => {
      if (this.opening.get(saved.fingerprint) === operation) this.opening.delete(saved.fingerprint)
    })
    this.opening.set(saved.fingerprint, operation)
    return operation.promise
  }

  retain(saved: SavedLink, link: RemoteLink) {
    const previous = this.links.get(saved.fingerprint)
    if (previous && previous.link !== link) previous.link.close()
    if (link.alive()) this.links.set(saved.fingerprint, { saved: { ...saved }, link })
    else { this.links.delete(saved.fingerprint); link.close() }
  }

  take(saved: SavedLink): RemoteLink | undefined {
    const entry = this.links.get(saved.fingerprint)
    this.links.delete(saved.fingerprint)
    if (!entry) return
    if (entry.link.alive() && sameLinkAuthority(entry.saved, saved)) return entry.link
    entry.link.close()
  }

  reconcile(saved: SavedLink[], allows?: (link: SavedLink) => boolean) {
    for (const [fp, entry] of this.opening) {
      if (!saved.some(row => sameLinkAuthority(row, entry.saved) && allows?.(row) !== false)) this.opening.delete(fp)
    }
    for (const [fp, entry] of this.links) {
      const current = saved.find(row => row.fingerprint === fp)
      if (current && allows?.(current) !== false && sameLinkAuthority(entry.saved, current)) continue
      this.links.delete(fp)
      entry.link.close()
    }
  }

  close() {
    const entries = [...this.links.values()]
    this.links.clear()
    this.opening.clear()
    for (const entry of entries) entry.link.close()
  }
}
