/** The EASM result shape from the task-panel catalog. */

export type NodeKind = "root" | "company" | "domain" | "feature"
export type FeatureKind = "org" | "cert" | "mx" | "asn" | "icp" | "icon_hash" | "ipc" | "fid"
/** Legend / colour / icon key: a feature uses its featureKind. */
export type DisplayKind = Exclude<NodeKind, "feature"> | FeatureKind

export interface EasmDetail {
  description?: string
  logo?: string
  website?: string
  region?: string
  industry?: string[]
  organizations?: { name: string; count?: number }[]
  /** Structured evidence for why the node is on the graph; worded by the renderer. */
  basis?: EasmBasis[]
  status?: string
  share?: string
  registry_id?: string
  registry_name?: string
  organization_id?: string
  features?: { kind: FeatureKind | "domain"; value: string; node_id?: string }[]
  serial?: string
  issuer?: string
  not_before?: string
  not_after?: string
  is_valid?: boolean
  is_match?: boolean
  observations?: {
    host: string
    domain?: string
    source?: string
    is_valid?: boolean
    is_match?: boolean
    is_expired?: boolean
  }[]
  names?: string[]
  subject_name?: string
}

export interface EasmBasis {
  source: string
  via?: string
  date?: string
}

export interface EasmNode {
  id: string
  kind: NodeKind
  featureKind?: FeatureKind
  label: string
  ip_count?: number
  detail?: EasmDetail
}

export interface EasmEdge {
  source: string
  target: string
  confirmed?: boolean
}

export interface EasmTagEvidence {
  title: string
  date?: string
  url?: string
  detail?: string
}

export type TagType = "hot" | "kev" | "cve"

export interface EasmComponent {
  name: string
  display_name?: string
  count: number
  hot_days?: number
  tags?: { type: TagType; summary?: string; evidence?: EasmTagEvidence[] }[]
}

export type EasmStepId =
  | "organizations" | "relations" | "branches" | "filings" | "asset_rules"
  | "certificates" | "summary" | "heat" | "components"
export type EasmStepStatus = "pending" | "running" | "done" | "failed" | "skipped" | "unavailable" | "interrupted"

/** Producer progress. Step status drives loading indicators, so no transient
 *  side channel is needed and a reload shows the same state. */
export interface EasmCollection {
  status: "idle" | "running" | "done" | "interrupted"
  steps: { id: EasmStepId; status: EasmStepStatus; code?: string }[]
}

export interface EasmResult {
  schema_version: 1
  title: string
  query?: string
  collection: EasmCollection
  nodes: EasmNode[]
  edges: EasmEdge[]
  summary?: { clues?: number; fofa_assets?: number; fofa_ips?: number }
  heat: { date: string; value: number }[]
  components: EasmComponent[]
}
