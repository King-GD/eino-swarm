import { createContext, useContext } from "react"
import type { DisplayKind, TagType } from "./types"

/** Panel copy. Data values (names, domains, filing numbers) are shown as the
 *  source returned them; only the surrounding words are translated here. */
const en = {
  kinds: {
    root: "Target organization", company: "Related organization", org: "Certificate organization", domain: "Primary domain",
    cert: "Domain certificate", asn: "ASN", icon_hash: "Site icon", ipc: "IP range", fid: "FID", icp: "ICP filing", mx: "MX record",
  } satisfies Record<DisplayKind, string>,
  featureGroups: {
    cert: "Domain certificates", org: "Certificate organizations", asn: "ASN", icon_hash: "Icon fingerprints", domain: "Domains",
    ipc: "IP ranges", fid: "FID", icp: "ICP filings", mx: "MX records",
  },
  tags: { hot: "Trending", kev: "KEV", cve: "CVE" } satisfies Record<TagType, string>,
  basisSources: {
    organizations: "organization lookup", relations: "investment relations", branches: "branch registry", filings: "ICP filing",
    asset_rules: "asset rule", certificate_search: "certificate search", asset_lookup: "TLS asset record",
  } as Record<string, string>,
  basis: (source: string, via?: string, date?: string) => `From ${source}${via ? `: ${via}` : ""}${date ? ` (${date})` : ""}`,
  certificateLabel: (tail: string) => `Cert · ${tail}`,
  stats: { clues: "clues", fofa_assets: "FOFA assets", fofa_ips: "asset IPs" },
  validity: (from: string, to: string) => `${from} to ${to}`,
  expand: "Expand EASM", expandGraph: "Expand asset graph", graphTitle: (subject: string) => `${subject} · Asset graph`,
  graphDescription: "Asset relationship graph, attention trend and supply chain. Select a node for details.",
  search: "Search assets or features", searchPlaceholder: "Organization, domain, certificate or filing", graph: "Asset relationship graph", graphNetwork: "Asset and feature network",
  waiting: "Waiting for asset data", graphActions: "Graph actions", zoomIn: "Zoom in", zoomInGraph: "Zoom in the graph",
  zoomOut: "Zoom out", zoomOutGraph: "Zoom out the graph", fit: "Fit view", kindFilter: "Node type filter; select to hide or show",
  heatTitle: "Asset attention trend", heatChart: "Asset attention trend curve", heatLoading: "Loading asset attention trend", heatEmpty: "No attention trend data",
  noIntel: "No related intelligence.", trending: "Trending", trendingDays: (days: number) => `Trending on ${days} of the last 30 days.`,
  supplyTitle: "Supply chain / third-party components", supplyCount: (count: string) => `${count} items`,
  supplyRegion: "All supply chain components, scrollable",
  supplyCaption: (count: number) => `Supply chain components, ordered by trending, KEV and CVE; ${count} items`,
  component: "Component", count: "Aggregate count", tagsColumn: "Attention tags", tagEvidence: (name: string) => `View tag evidence for ${name}`,
  tagDescription: "Trending and vulnerability tag evidence for this component",
  logo: (name: string) => `${name} logo`,
  filingSubject: (subject: string) => ` ${subject}'s filing record`, filingSaved: "the saved filing record",
  filing: (source: string, filing: string, domains: string) => `From${source}. Extracted: ICP filing ${filing}, domains ${domains}.`,
  judgment: "Asset judgment", confirmedLink: (node: string, kind: string, other: string) => `${node} is confirmed to relate to ${kind} ${other}.`,
  more: (count: number) => `${count} more`, noJudgment: "No saved judgment yet",
  profile: "Profile", noDescription: "No saved company introduction", organizations: "Organizations", noOrganizations: "No saved organizations",
  serial: "Serial number", issuer: "Issuer", validityLabel: "Validity", names: "Covered names", filingSubjectLabel: "Filing subject",
  features: "Effective features", noFeatures: "No saved effective features", closeCard: "Close node card",
  listSeparator: ", ",
}

export type EasmText = typeof en

const zh: EasmText = {
  kinds: {
    root: "目标企业", company: "关联组织", org: "证书组织", domain: "主域名",
    cert: "域名证书", asn: "ASN", icon_hash: "网站图标", ipc: "IP 段", fid: "FID", icp: "ICP 备案", mx: "MX 记录",
  },
  featureGroups: {
    cert: "域名证书", org: "证书组织", asn: "ASN", icon_hash: "图标指纹", domain: "域名", ipc: "IP 段", fid: "FID", icp: "ICP 备案", mx: "MX 记录",
  },
  tags: { hot: "热搜", kev: "KEV", cve: "CVE" },
  basisSources: {
    organizations: "组织查询记录", relations: "投资关系记录", branches: "分支机构记录", filings: "ICP 备案记录",
    asset_rules: "资产规则", certificate_search: "证书搜索", asset_lookup: "TLS 资产记录",
  },
  basis: (source, via, date) => `线索来源于${source}${via ? `：${via}` : ""}${date ? `（${date}）` : ""}`,
  certificateLabel: (tail) => `证书 · ${tail}`,
  stats: { clues: "个线索", fofa_assets: "个 FOFA 资产", fofa_ips: "个资产 IP" },
  validity: (from, to) => `${from} 至 ${to}`,
  expand: "展开 EASM", expandGraph: "展开资产图", graphTitle: (subject) => `${subject} · 资产图谱`,
  graphDescription: "资产关系图、热力趋势和供应链，点击节点查看详情。",
  search: "搜索资产或特征", searchPlaceholder: "企业、域名、证书或备案号", graph: "资产关联知识库", graphNetwork: "资产与具体特征网络",
  waiting: "等待资产数据", graphActions: "关联网络操作", zoomIn: "放大", zoomInGraph: "放大关联网络",
  zoomOut: "缩小", zoomOutGraph: "缩小关联网络", fit: "适配视图", kindFilter: "节点类型筛选，点击隐藏或显示",
  heatTitle: "资产热力趋势", heatChart: "资产热力趋势曲线", heatLoading: "正在加载资产热力趋势", heatEmpty: "暂无热力趋势数据",
  noIntel: "暂无相关情报。", trending: "热搜情况", trendingDays: (days) => `近 30 天有 ${days} 天上榜。`,
  supplyTitle: "供应链 / 第三方组件", supplyCount: (count) => `${count}项`,
  supplyRegion: "全部供应链组件，可滚动查看",
  supplyCaption: (count) => `供应链组件，按热搜、KEV、CVE 优先级排序；共 ${count} 项`,
  component: "组件", count: "聚合计数", tagsColumn: "关注标签", tagEvidence: (name) => `查看${name}标签依据`,
  tagDescription: "该组件的热搜和漏洞标签依据",
  logo: (name) => `${name}标识`,
  filingSubject: (subject) => ` ${subject} 的备案记录`, filingSaved: "已保存的备案记录",
  filing: (source, filing, domains) => `线索来源于${source}。提取信息：ICP备案 ${filing}，域名 ${domains}。`,
  judgment: "资产判断详情", confirmedLink: (node, kind, other) => `${node} 与${kind} ${other} 的关联已确认。`,
  more: (count) => `另有 ${count} 项`, noJudgment: "暂无已保存的判断依据",
  profile: "简介", noDescription: "暂无已保存的企业简介", organizations: "组织列表", noOrganizations: "暂无已保存的组织",
  serial: "序列号", issuer: "签发机构", validityLabel: "有效期", names: "覆盖名称", filingSubjectLabel: "备案主体",
  features: "有效特征", noFeatures: "暂无已保存的有效特征", closeCard: "关闭节点卡片",
  listSeparator: "、",
}

export function easmText(locale: string): EasmText {
  return locale.startsWith("zh") ? zh : en
}

/** The panel's locale-bound copy; EasmTaskPanel provides the user's locale. */
export const EasmTextContext = createContext<EasmText>(en)

export function useEasmText() {
  return useContext(EasmTextContext)
}

/** Count formatting follows the same locale as the copy. */
export function formatCount(n: number, text: EasmText) {
  return n.toLocaleString(text === zh ? "zh-CN" : "en-US")
}
