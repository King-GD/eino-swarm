import { X } from "lucide-react"
import { useId, type ReactNode, type Ref } from "react"
import { Badge } from "../../ui/badge"
import { Button } from "../../ui/button"
import { FEATURE_GROUPS, displayKind, formatCount, isSafeLink, kindLabel, summaryStats, validity, type EasmIndex } from "../../../lib/easm/model"
import type { DisplayKind, EasmNode, EasmResult } from "../../../lib/easm/types"
import { cn } from "@/lib/cn"
import { KIND_ICONS, kindColor } from "./kind-style"

export function KindIcon({ kind, className }: { kind: DisplayKind; className?: string }) {
  return (
    <span className={cn("inline-flex size-5 shrink-0 items-center justify-center rounded-full", className)} style={{ background: kindColor(kind) }} aria-hidden>
      <img src={KIND_ICONS[kind]} alt="" className="size-[60%]" />
    </span>
  )
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

function AssetBody({ node, index, summary, onSelect }: { node: EasmNode; index: EasmIndex; summary: EasmResult["summary"]; onSelect: (id: string) => void }) {
  const detail = node.detail ?? {}
  const root = node.kind === "root"
  const corporate = node.kind === "root" || node.kind === "company" || node.kind === "org"
  const orgByName = new Map([...index.byId.values()].filter((n) => n.kind === "org").map((n) => [n.label, n]))
  const groups = FEATURE_GROUPS.map(([kind, title]) => [kind, title, (detail.features ?? []).filter((f) => f.kind === kind)] as const).filter(([, , items]) => items.length)
  return (
    <div className="space-y-3">
      {corporate && (detail.logo || detail.description) ? (
        <div className="flex items-start gap-3">
          {detail.logo ? <img src={detail.logo} alt={`${node.label}标识`} className="size-10 shrink-0 rounded-md border bg-muted p-1" /> : null}
          {detail.description ? <p className="m-0 leading-relaxed text-muted-foreground">{detail.description}</p> : null}
        </div>
      ) : null}
      {root ? <Fields rows={[["官网", isSafeLink(detail.website) ? <a href={detail.website} target="_blank" rel="noreferrer" className="text-primary hover:underline">{detail.website}</a> : detail.website], ["国家 / 地区", detail.region]]} /> : null}
      {root && detail.industry?.length ? (
        <Group title="行业" count={detail.industry.length}>
          {detail.industry.map((item) => <p key={item} className="m-0 py-1 font-medium">{item}</p>)}
        </Group>
      ) : null}
      {root && summaryStats(summary).length ? (
        <>
          <SectionTitle>节点信息</SectionTitle>
          <Fields rows={([["线索数", summary?.clues], ["FOFA 资产数", summary?.fofa_assets], ["FOFA 资产 IP 数", summary?.fofa_ips]] as const)
            .filter(([, count]) => Number.isFinite(count)).map(([label, count]) => [label, formatCount(count as number)])} />
        </>
      ) : null}
      {root && detail.organizations?.length ? (
        <Group kind="org" title="组织列表" count={detail.organizations.length}>
          {detail.organizations.map((org) => (
            <NodeLink key={org.name} node={orgByName.get(org.name)} onSelect={onSelect}>
              {org.name}{org.count != null ? <span className="ml-2 text-xs text-muted-foreground">{formatCount(org.count)}</span> : null}
            </NodeLink>
          ))}
        </Group>
      ) : null}
      {!root && detail.reason ? <p className="m-0 rounded-r-md border-l-2 border-primary bg-muted/40 px-3 py-2 text-muted-foreground">{detail.reason}</p> : null}
      {groups.length ? <SectionTitle>有效特征</SectionTitle> : null}
      {groups.map(([kind, title, items]) => (
        <Group key={kind} kind={kind === "domain" ? "domain" : kind} title={title} count={items.length} open={kind === "cert" || items.length <= 3}>
          {items.map((feature) => (
            <NodeLink key={`${feature.value}:${feature.node_id ?? ""}`} node={feature.node_id && feature.node_id !== node.id ? index.byId.get(feature.node_id) : undefined} onSelect={onSelect}>
              <span className="font-mono text-xs">{feature.value}</span>
            </NodeLink>
          ))}
        </Group>
      ))}
    </div>
  )
}

function FeatureBody({ node, index, onSelect }: { node: EasmNode; index: EasmIndex; onSelect: (id: string) => void }) {
  const detail = node.detail ?? {}
  const rows: [string, ReactNode][] = node.featureKind === "cert"
    ? [["证书名称", node.label], ["序列号", detail.serial], ["签发机构", detail.issuer], ["有效期", validity(detail.not_before, detail.not_after)], ["覆盖名称", detail.names?.join("、")]]
    : [["特征值", node.label], ["备案主体", detail.subject_name]]
  const members = index.members.get(node.id) ?? []
  return (
    <div className="space-y-1.5">
      <Fields rows={rows} />
      <SectionTitle>具有该特征的资产 <Badge className="tabular-nums">{members.length}</Badge></SectionTitle>
      {members.length ? members.map((id) => <NodeLink key={id} node={index.byId.get(id)} onSelect={onSelect}>{index.byId.get(id)?.label ?? id}</NodeLink>) : <p className="m-0 text-muted-foreground">暂无记录</p>}
    </div>
  )
}

/** Details for the node the user clicked. */
export function NodeCard({ ref, className, node, index, summary, onSelect, onClose }: {
  ref?: Ref<HTMLElement>
  className?: string
  node: EasmNode
  index: EasmIndex
  summary: EasmResult["summary"]
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
        <KindIcon kind={kind} className="size-10" />
        <div className="min-w-0 flex-1">
          <h2 id={titleId} className="m-0 text-base leading-snug font-semibold break-all">{node.label}</h2>
          <Badge className="mt-1">{kindLabel(kind)}</Badge>
        </div>
        <Button size="icon" variant="ghost" aria-label="关闭节点卡片" className="self-start" onClick={onClose} autoFocus><X className="size-4" /></Button>
      </header>
      <div className="overflow-y-auto px-4 pt-3.5 pb-4 leading-relaxed">
        {node.kind === "feature" ? <FeatureBody node={node} index={index} onSelect={onSelect} /> : <AssetBody node={node} index={index} summary={summary} onSelect={onSelect} />}
      </div>
    </section>
  )
}
