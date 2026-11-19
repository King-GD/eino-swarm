import { X } from "lucide-react"
import { useId, useState, type ReactNode, type Ref } from "react"
import { Badge } from "../../ui/badge"
import { Button } from "../../ui/button"
import { FEATURE_GROUPS, displayKind, formatCount, kindLabel, validity, type EasmIndex } from "../../../lib/easm/model"
import type { DisplayKind, EasmNode } from "../../../lib/easm/types"
import { cn } from "@/lib/cn"
import { KIND_ICONS, kindColor } from "./kind-style"

export function KindIcon({ kind, className }: { kind: DisplayKind; className?: string }) {
  return (
    <span className={cn("inline-flex size-5 shrink-0 items-center justify-center rounded-full", className)} style={{ background: kindColor(kind) }} aria-hidden>
      <img src={KIND_ICONS[kind]} alt="" className="size-[60%]" />
    </span>
  )
}

function NodeIcon({ node }: { node: EasmNode }) {
  const logo = node.kind === "root" ? node.detail?.logo : undefined
  const [failedLogo, setFailedLogo] = useState<string>()
  return logo && logo !== failedLogo
    ? <img src={logo} alt={`${node.label}标识`} className="h-10 w-24 shrink-0 rounded-md bg-white object-contain p-1" onError={() => setFailedLogo(logo)} />
    : <KindIcon kind={displayKind(node)} className="size-10" />
}

/** A clickable reference to another node in the graph. */
function NodeLink({ node, onSelect, children }: { node?: EasmNode; onSelect: (id: string) => void; children: ReactNode }) {
  if (!node) return <span className="font-medium">{children}</span>
  return (
    <button type="button" className="flex min-h-9 w-full items-center gap-2.5 rounded-md border bg-muted/40 px-3 py-1.5 text-left hover:border-primary hover:bg-accent" onClick={() => onSelect(node.id)}>
      <KindIcon kind={displayKind(node)} className="size-5" />
      <span className="min-w-0 flex-1 break-all">{children}</span>
    </button>
  )
}

function Fields({ rows }: { rows: [string, ReactNode][] }) {
  const shown = rows.filter(([, value]) => value !== "" && value != null)
  if (!shown.length) return null
  return (
    <dl className="divide-y overflow-hidden rounded-md border bg-muted/40">
      {shown.map(([label, value]) => (
        <div key={label} className="grid grid-cols-[88px_1fr] gap-3 px-3 py-2">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="m-0 break-all">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function Group({ kind, title, count, open, children }: { kind?: DisplayKind; title: string; count: number; open?: boolean; children: ReactNode }) {
  return (
    <details open={open} className="rounded-md border bg-muted/40 [&_summary::-webkit-details-marker]:hidden">
      <summary className="flex min-h-10 cursor-pointer list-none items-center gap-2.5 px-3 font-medium">
        {kind ? <KindIcon kind={kind} /> : null}
        {title}
        <Badge className="tabular-nums">{count}</Badge>
      </summary>
      <div className="space-y-1.5 border-t px-3 py-2">{children}</div>
    </details>
  )
}

function SectionTitle({ children }: { children: ReactNode }) {
  return <h3 className="mt-4 mb-1.5 flex items-center gap-2 text-xs font-semibold text-muted-foreground after:flex-1 after:border-t after:content-['']">{children}</h3>
}

const domainName = (value: string) => value.trim().toLowerCase().replace(/\.$/, "")

function AssetJudgment({ node, index }: { node: EasmNode; index: EasmIndex }) {
  const reason = node.detail?.reason?.trim()
  const filingDomains = [...new Set([...(index.filingDomains.get(node.id) ?? [])].map((id) => index.byId.get(id)!.label))]
  const linked = [...(index.confirmedLinks.get(node.id) ?? [])].map((id) => index.byId.get(id)!)
  const filings = linked.filter((other) => index.filingDomains.get(other.id)?.has(node.id))
  const evidence = filings.length ? filings : linked
  const hasJudgment = Boolean(reason || filingDomains.length)
  const limit = 6
  return (
    <section className="space-y-1 rounded-r-md border-l-2 border-primary bg-muted/40 px-3 py-2">
      <h3 className="text-xs font-semibold">资产判断详情</h3>
      {reason ? <p className="m-0 whitespace-pre-line break-words text-muted-foreground">{reason}</p> : null}
      {filingDomains.length ? <p className="m-0 break-words text-muted-foreground">已确认关联的备案域名：{filingDomains.join("、")}</p> : null}
      {!hasJudgment && evidence.length ? (
        <div className="text-muted-foreground">
          <p className="m-0">{filings.length ? "关联的 ICP 备案" : "图谱中已确认的关联"}</p>
          <ul className="m-0 list-disc pl-4">
            {evidence.slice(0, limit).map((other) => (
              <li key={other.id} className="break-words">
                {other.label}{filings.length && other.detail?.subject_name ? `（备案主体 ${other.detail.subject_name}）` : ""}
              </li>
            ))}
          </ul>
          {evidence.length > limit ? <p className="m-0">另有 {evidence.length - limit} 项</p> : null}
        </div>
      ) : null}
      {!hasJudgment && !evidence.length ? <p className="m-0 text-muted-foreground">暂无已保存的判断依据</p> : null}
    </section>
  )
}

function RootProfile({ node, index, onSelect }: { node: EasmNode; index: EasmIndex; onSelect: (id: string) => void }) {
  const detail = node.detail ?? {}
  const organizations = [...index.byId.values()].filter((other) => other.kind === "company" || displayKind(other) === "org")
  const saved = detail.organizations
  const rows = saved?.length
    ? saved.map((entry) => {
      const matches = organizations.filter((other) => other.label === entry.name)
      return { ...entry, node: matches.length === 1 ? matches[0] : undefined }
    })
    : organizations.filter((other) => index.related.get(node.id)?.has(other.id)).map((other) => ({ name: other.label, node: other, count: undefined }))
  return (
    <>
      <SectionTitle>简介</SectionTitle>
      <p className="m-0 whitespace-pre-line leading-relaxed text-muted-foreground">{detail.description || "暂无已保存的企业简介"}</p>
      <Group title="组织列表" count={rows.length} open>
        {rows.length ? rows.map((org, i) => (
          <NodeLink key={org.node?.id ?? `${org.name}:${i}`} node={org.node} onSelect={onSelect}>
            {org.name}{org.count != null ? <span className="ml-2 text-xs text-muted-foreground">{formatCount(org.count)}</span> : null}
          </NodeLink>
        )) : <p className="m-0 text-muted-foreground">暂无已保存的组织</p>}
      </Group>
    </>
  )
}

function EffectiveFeatures({ node, index, onSelect }: { node: EasmNode; index: EasmIndex; onSelect: (id: string) => void }) {
  const detail = node.detail ?? {}
  const features: { kind: DisplayKind; value: string; node_id?: string }[] = (detail.features ?? []).filter((feature) => feature.node_id !== node.id && !(
    node.kind === "domain" && feature.kind === "domain" && domainName(feature.value) === domainName(node.label)
  ))
  for (const id of index.related.get(node.id) ?? []) {
    const other = index.byId.get(id)!
    if (other.kind === "root" || other.kind === "company" && node.kind !== "feature" && node.kind !== "org") continue
    const kind = displayKind(other)
    if (features.some((feature) => feature.node_id === id)) continue
    const entry = { kind, value: other.label, node_id: id }
    const unlinked = features.findIndex((feature) => feature.kind === kind && !feature.node_id && feature.value === other.label)
    if (unlinked >= 0) features[unlinked] = entry
    else features.push(entry)
  }
  const sections: [DisplayKind, string][] = [...FEATURE_GROUPS, ["company", "企业组织"]]
  const groups = sections.map(([kind, title]) => [kind, title, features.filter((feature) => feature.kind === kind)] as const).filter(([, , items]) => items.length)
  const certificate = node.featureKind === "cert"
  const rows: [string, ReactNode][] = certificate
    ? [["序列号", detail.serial], ["签发机构", detail.issuer], ["有效期", validity(detail.not_before, detail.not_after)], ["覆盖名称", detail.names?.join("、")]]
    : node.featureKind === "icp" ? [["备案主体", detail.subject_name]] : []
  const hasFields = rows.some(([, value]) => value !== "" && value != null)
  return (
    <>
      <SectionTitle>有效特征</SectionTitle>
      {hasFields ? <Fields rows={rows} /> : null}
      {groups.map(([kind, title, items]) => (
        <Group key={kind} kind={kind} title={title} count={items.length} open={kind === "cert" || items.length <= 3}>
          {items.map((feature) => (
            <NodeLink key={`${feature.value}:${feature.node_id ?? ""}`} node={feature.node_id ? index.byId.get(feature.node_id) : undefined} onSelect={onSelect}>
              <span className="font-mono text-xs">{feature.value}</span>
            </NodeLink>
          ))}
        </Group>
      ))}
      {!hasFields && !groups.length ? <p className="m-0 text-muted-foreground">暂无已保存的有效特征</p> : null}
    </>
  )
}

/** Details for the node the user clicked. */
export function NodeCard({ ref, className, node, index, onSelect, onClose }: {
  ref?: Ref<HTMLElement>
  className?: string
  node: EasmNode
  index: EasmIndex
  onSelect: (id: string) => void
  onClose: () => void
}) {
  const kind = displayKind(node)
  const titleId = useId()
  return (
    <section ref={ref} role="dialog" aria-modal="false" aria-labelledby={titleId} data-testid="easm-node-card"
      className={cn("absolute z-20 flex max-h-[min(560px,calc(100%-24px))] w-[min(400px,calc(100%-24px))] flex-col overflow-hidden rounded-xl border bg-popover text-sm text-popover-foreground shadow-xl", className)}
      onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); onClose() } }}>
      <header className="flex items-center gap-3 border-b bg-muted/40 py-3 pr-2 pl-4">
        <NodeIcon node={node} />
        <div className="min-w-0 flex-1">
          <h2 id={titleId} className="m-0 text-base leading-snug font-semibold break-all">{node.label}</h2>
          <Badge className="mt-1">{kindLabel(kind)}</Badge>
        </div>
        <Button size="icon" variant="ghost" aria-label="关闭节点卡片" className="self-start" onClick={onClose} autoFocus><X className="size-4" /></Button>
      </header>
      <div className="overflow-y-auto px-4 pt-3.5 pb-4 leading-relaxed">
        {node.kind === "root" ? <RootProfile node={node} index={index} onSelect={onSelect} /> : <AssetJudgment node={node} index={index} />}
        <EffectiveFeatures node={node} index={index} onSelect={onSelect} />
      </div>
    </section>
  )
}
