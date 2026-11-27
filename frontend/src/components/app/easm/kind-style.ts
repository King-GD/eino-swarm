import asn from "../../../assets/easm/asn.svg"
import cert from "../../../assets/easm/cert.svg"
import certOrg from "../../../assets/easm/cert_org.svg"
import company from "../../../assets/easm/company.svg"
import domain from "../../../assets/easm/domain.svg"
import fid from "../../../assets/easm/fid.svg"
import iconHash from "../../../assets/easm/iconHash.svg"
import icp from "../../../assets/easm/icp.svg"
import ipc from "../../../assets/easm/ipc.svg"
import mx from "../../../assets/easm/mx.svg"
import address from "../../../assets/easm/address.svg"
import other from "../../../assets/easm/other.svg"
import person from "../../../assets/easm/person.svg"
import url from "../../../assets/easm/url.svg"
import vuln from "../../../assets/easm/vuln.svg"
import type { DisplayKind } from "../../../lib/easm/types"

/** FOFA AI type glyphs: white artwork drawn on the type colour. As in FOFA AI,
 *  a related organization is a building and a certificate organization keeps
 *  the certificate-organization glyph; the target shares the related one. */
export const KIND_ICONS: Record<DisplayKind, string> = {
  root: company, company, org: certOrg, domain, cert, icp, asn, fid, icon_hash: iconHash, ipc, mx,
  vuln, url, person, address, other,
}

/** Tailwind token per type, so legend, card and canvas share one colour. */
export const KIND_TOKENS: Record<DisplayKind, string> = {
  root: "--easm-root", company: "--easm-company", org: "--easm-org", domain: "--easm-domain", cert: "--easm-cert",
  icp: "--easm-icp", asn: "--easm-asn", fid: "--easm-fid", icon_hash: "--easm-icon-hash", ipc: "--easm-ipc", mx: "--easm-mx",
  vuln: "--easm-vuln", url: "--easm-url", person: "--easm-person", address: "--easm-address", other: "--easm-other",
}

export const kindColor = (kind: DisplayKind) => `hsl(var(${KIND_TOKENS[kind]}))`

/** Resolves a token for canvas drawing, which cannot read CSS variables itself. */
export function tokenColor(element: Element, token: string) {
  const value = getComputedStyle(element).getPropertyValue(token).trim()
  return value ? `hsl(${value})` : "gray"
}
