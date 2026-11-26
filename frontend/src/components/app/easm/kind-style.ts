import asn from "../../../assets/easm/asn.svg"
import cert from "../../../assets/easm/cert.svg"
import certOrg from "../../../assets/easm/cert_org.svg"
import domain from "../../../assets/easm/domain.svg"
import fid from "../../../assets/easm/fid.svg"
import iconHash from "../../../assets/easm/iconHash.svg"
import icp from "../../../assets/easm/icp.svg"
import ipc from "../../../assets/easm/ipc.svg"
import mx from "../../../assets/easm/mx.svg"
import type { DisplayKind } from "../../../lib/easm/types"

/** FOFA AI type glyphs: white artwork drawn on the type colour. */
export const KIND_ICONS: Record<DisplayKind, string> = {
  root: certOrg, company: certOrg, org: certOrg, domain, cert, icp, asn, fid, icon_hash: iconHash, ipc, mx,
}

/** Tailwind token per type, so legend, card and canvas share one colour. */
export const KIND_TOKENS: Record<DisplayKind, string> = {
  root: "--easm-root", company: "--easm-company", org: "--easm-org", domain: "--easm-domain", cert: "--easm-cert",
  icp: "--easm-icp", asn: "--easm-asn", fid: "--easm-fid", icon_hash: "--easm-icon-hash", ipc: "--easm-ipc", mx: "--easm-mx",
}

export const kindColor = (kind: DisplayKind) => `hsl(var(${KIND_TOKENS[kind]}))`

/** Resolves a token for canvas drawing, which cannot read CSS variables itself. */
export function tokenColor(element: Element, token: string) {
  const value = getComputedStyle(element).getPropertyValue(token).trim()
  return value ? `hsl(${value})` : "gray"
}
