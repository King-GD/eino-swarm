import type { EasmNode, EasmResult } from "../src/lib/easm/types"

const root: EasmNode = { id: "root", kind: "root", label: "Fixture Holdings" }
export const firstDomain: EasmNode = { id: "domain:origin.test", kind: "domain", label: "origin.test" }
const serial = "9a7b"
export const firstCertificate = {
  id: `feature:cert:${serial}@Fixture issuer`, kind: "feature", featureKind: "cert", label: "Fixture certificate",
  detail: { serial, issuer: "Fixture issuer" },
} satisfies EasmNode
export const laterDomain: EasmNode = {
  id: "domain:later.test", kind: "domain", label: "later.test",
  detail: { features: [{ kind: "cert", value: firstCertificate.label, node_id: firstCertificate.id }] },
}
const laterCertificate: EasmNode = {
  ...firstCertificate, id: `feature:cert:${serial}@Other issuer`, label: "Duplicate certificate", detail: { serial, issuer: "Other issuer" },
}
const detachedCompany: EasmNode = { id: "company:detached", kind: "company", label: "Detached company" }
const detachedDomain: EasmNode = { id: "domain:detached.test", kind: "domain", label: "detached.test" }
const isolatedDomain: EasmNode = { id: "domain:isolated.test", kind: "domain", label: "isolated.test" }
export const nextDomain: EasmNode = { id: "domain:batch.test", kind: "domain", label: "batch.test" }

// This saved portrait predates serial-only certificate identity and includes
// observations whose sources do not lead to the confirmed subject.
export const savedEasmGraph: EasmResult = {
  schema_version: 1, title: root.label, collection: { status: "done", steps: [] }, heat: [], components: [],
  nodes: [root, firstDomain, firstCertificate, laterDomain, laterCertificate, detachedCompany, detachedDomain, isolatedDomain],
  edges: [
    { source: root.id, target: firstDomain.id, confirmed: true },
    { source: firstDomain.id, target: firstCertificate.id, confirmed: true },
    { source: root.id, target: laterDomain.id, confirmed: true },
    { source: laterDomain.id, target: laterCertificate.id, confirmed: true },
    { source: detachedCompany.id, target: detachedDomain.id },
    { source: detachedDomain.id, target: detachedCompany.id },
    { source: "company:missing", target: isolatedDomain.id, confirmed: true },
  ],
}

export const updatedEasmGraph: EasmResult = {
  ...savedEasmGraph,
  nodes: [...savedEasmGraph.nodes, nextDomain],
  edges: [...savedEasmGraph.edges, { source: root.id, target: nextDomain.id, confirmed: true }],
}

export const rootedEasmNodeIds = [root.id, firstDomain.id, firstCertificate.id, laterDomain.id]
