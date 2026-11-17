import type { RemoteLink } from "./link"
import { sameLinkAuthority } from "./link-authority"
import type { SavedLink } from "./store"

/** Inactive PCs keep their sockets; credentials still identify each connection. */
export class RetainedPhoneLinks {
  private readonly links = new Map<string, { saved: SavedLink; link: RemoteLink }>()

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
    for (const entry of entries) entry.link.close()
  }
}
