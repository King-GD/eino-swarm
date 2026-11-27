import { X } from "lucide-react"
import { useId, useState, type ReactNode, type Ref } from "react"
import { Badge } from "../../ui/badge"
import { Button } from "../../ui/button"
import { FEATURE_GROUPS, basisText, displayKind, kindLabel, validity, type EasmIndex } from "../../../lib/easm/model"
import { formatCount, useEasmText, type EasmText } from "../../../lib/easm/messages"
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
  const text = useEasmText()
  return logo && logo !== failedLogo
    ? <img src={logo} alt={text.logo(node.label)} className="h-10 w-24 shrink-0 rounded-md bg-white object-contain p-1" onError={() => setFailedLogo(logo)} />
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
      {shown.map(([label, value], i) => (
        <div key={`${label}:${i}`} className="grid grid-cols-[88px_1fr] gap-3 px-3 py-2">
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

function filingDescription(filing: EasmNode, domains: string[], text: EasmText) {
  const subject = filing.detail?.subject_name?.trim()
  const source = subject ? text.filingSubject(subject) : text.filingSaved
  return text.filing(source, filing.label, domains.join(text.listSeparator))
}

function hasPendingGraphLink(index: EasmIndex, source: string, target: string) {
  return index.related.get(source)?.has(target) && !index.confirmedLinks.get(source)?.has(target)
}

function AssetJudgment({ node, index }: { node: EasmNode; index: EasmIndex }) {
  const text = useEasmText()
  const basis = node.detail?.basis ?? []
  const filingDomains = new Set<string>()
  if (node.featureKind === "icp") {
    for (const feature of node.detail?.features ?? []) {
      const domain = feature.node_id ? index.byId.get(feature.node_id) : undefined
      if (feature.kind === "domain" && domain?.kind === "domain" && !hasPendingGraphLink(index, node.id, domain.id)) {
        filingDomains.add(domain.label)
      }
    }
    for (const id of index.filingDomains.get(node.id) ?? []) filingDomains.add(index.byId.get(id)!.label)
  }
  const evidence = [...(index.confirmedLinks.get(node.id) ?? [])].map((id) => index.byId.get(id)!)
  const hasJudgment = Boolean(basis.length || filingDomains.size)
  const limit = 6
  return (
    <section className="space-y-1 rounded-r-md border-l-2 border-primary bg-muted/40 px-3 py-2">
      <h3 className="text-xs font-semibold">{text.judgment}</h3>
      {node.detail?.relation ? <p className="m-0 break-words text-muted-foreground">{text.relation(node.detail.relation)}</p> : null}
      {basis.length ? <ul className="m-0 list-none space-y-0.5 p-0 text-muted-foreground">{basis.map((item, i) => <li key={i} className="break-words">{basisText(item, text)}</li>)}</ul> : null}
      {filingDomains.size ? <p className="m-0 break-words text-muted-foreground">{filingDescription(node, [...filingDomains], text)}</p> : null}
      {!hasJudgment && evidence.length ? (
        <div className="text-muted-foreground">
          <ul className="m-0 list-disc pl-4">
            {evidence.slice(0, limit).map((other) => (
              <li key={other.id} className="break-words">
                {text.confirmedLink(node.label, kindLabel(displayKind(other), text), other.label)}
              </li>
            ))}
          </ul>
          {evidence.length > limit ? <p className="m-0">{text.more(evidence.length - limit)}</p> : null}
        </div>
      ) : null}
      {!hasJudgment && !evidence.length ? <p className="m-0 text-muted-foreground">{text.noJudgment}</p> : null}
    </section>
  )
}

function RootProfile({ node, index, onSelect }: { node: EasmNode; index: EasmIndex; onSelect: (id: string) => void }) {
  const text = useEasmText()
  const detail = node.detail ?? {}
  const organizations = [...index.byId.values()].filter((other) => displayKind(other) === "company")
  const related = index.related.get(node.id)
  const confirmed = index.confirmedLinks.get(node.id)
  const saved = detail.organizations
  const rows = saved !== undefined
    ? saved.flatMap((entry) => {
      const matches = organizations.filter((other) => other.label === entry.name)
      const match = matches.length === 1 ? matches[0] : undefined
      if (match && related?.has(match.id) && !confirmed?.has(match.id)) return []
      return [{ ...entry, node: match && confirmed?.has(match.id) ? match : undefined }]
    })
    : organizations.filter((other) => confirmed?.has(other.id)).map((other) => ({ name: other.label, node: other, count: undefined }))
  return (
    <>
      <SectionTitle>{text.profile}</SectionTitle>
      <p className="m-0 whitespace-pre-line leading-relaxed text-muted-foreground">{detail.description || text.noDescription}</p>
      <Group title={text.organizations} count={rows.length} open>
        {rows.length ? rows.map((org, i) => (
          <NodeLink key={org.node?.id ?? `${org.name}:${i}`} node={org.node} onSelect={onSelect}>
            {org.name}{org.count != null ? <span className="ml-2 text-xs text-muted-foreground">{formatCount(org.count, text)}</span> : null}
          </NodeLink>
        )) : <p className="m-0 text-muted-foreground">{text.noOrganizations}</p>}
      </Group>
    </>
  )
}

/** Mailboxes the source published for this node; a person's address belongs
 *  to the person, not to a graph node of its own. */
function Contacts({ node }: { node: EasmNode }) {
  const text = useEasmText()
  const emails = node.detail?.emails ?? []
  if (!emails.length) return null
  return (
    <>
      <SectionTitle>{text.contact}</SectionTitle>
      <Fields rows={emails.map((email) => [email.label || text.contactEmail, email.address])} />
    </>
  )
}

function EffectiveFeatures({ node, index, onSelect }: { node: EasmNode; index: EasmIndex; onSelect: (id: string) => void }) {
  const detail = node.detail ?? {}
  const children = index.children.get(node.id) ?? []
  const text = useEasmText()
  const sections: [DisplayKind, string][] = [...FEATURE_GROUPS.map((kind) => [kind, text.featureGroups[kind]] as [DisplayKind, string]), ["company", kindLabel("company", text)]]
  const groups = sections.map(([kind, title]) => [kind, title, children.filter((child) => displayKind(child) === kind)] as const).filter(([, , items]) => items.length)
  const certificate = node.featureKind === "cert"
  const rows: [string, ReactNode][] = certificate
    ? [[text.serial, detail.serial], [text.issuer, detail.issuer], [text.validityLabel, validity(text, detail.not_before, detail.not_after)], [text.names, detail.names?.join(text.listSeparator)]]
    : node.featureKind === "icp" ? [[text.filingSubjectLabel, detail.subject_name]] : []
  const hasFields = rows.some(([, value]) => value !== "" && value != null)
  return (
    <>
      <SectionTitle>{text.features}</SectionTitle>
      {hasFields ? <Fields rows={rows} /> : null}
      {groups.map(([kind, title, items]) => (
        <Group key={kind} kind={kind} title={title} count={items.length} open={kind === "cert" || items.length <= 3}>
          {items.map((child) => (
            <NodeLink key={child.id} node={child} onSelect={onSelect}>
              <span className="font-mono text-xs">{child.label}</span>
            </NodeLink>
          ))}
        </Group>
      ))}
      {!hasFields && !groups.length ? <p className="m-0 text-muted-foreground">{text.noFeatures}</p> : null}
    </>
  )
}

/** Details for the node the user clicked. */
export function NodeCard({ ref, className, node, index, onSelect, onClose, collapse }: {
  ref?: Ref<HTMLElement>
  className?: string
  node: EasmNode
  index: EasmIndex
  onSelect: (id: string) => void
  onClose: () => void
  /** Present only where the canvas has no hover to reach its fold badge,
   *  i.e. the phone; the desktop card leaves folding to the canvas badge. */
  collapse?: { collapsed: boolean; count: number; onToggle: () => void }
}) {
  const kind = displayKind(node)
  const titleId = useId()
  const text = useEasmText()
  return (
    <section ref={ref} role="dialog" aria-modal="false" aria-labelledby={titleId} data-testid="easm-node-card"
      className={cn("absolute z-20 flex max-h-[min(560px,calc(100%-24px))] w-[min(400px,calc(100%-24px))] flex-col overflow-hidden rounded-xl border bg-popover text-sm text-popover-foreground shadow-xl", className)}
      onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); onClose() } }}>
      <header className="flex items-center gap-3 border-b bg-muted/40 py-3 pr-2 pl-4">
        <NodeIcon node={node} />
        <div className="min-w-0 flex-1">
          <h2 id={titleId} className="m-0 text-base leading-snug font-semibold break-all">{node.label}</h2>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <Badge>{kindLabel(kind, text)}</Badge>
            {collapse ? (
              <Button size="sm" variant="ghost" aria-expanded={!collapse.collapsed} onClick={collapse.onToggle}>
                {collapse.collapsed ? text.expandNode(collapse.count) : text.collapseNode(collapse.count)}
              </Button>
            ) : null}
          </div>
        </div>
        <Button size="icon" variant="ghost" aria-label={text.closeCard} className="self-start" onClick={onClose} autoFocus><X className="size-4" /></Button>
      </header>
      <div className="overflow-y-auto px-4 pt-3.5 pb-4 leading-relaxed">
        {node.kind === "root" ? <RootProfile node={node} index={index} onSelect={onSelect} /> : <AssetJudgment node={node} index={index} />}
        <Contacts node={node} />
        <EffectiveFeatures node={node} index={index} onSelect={onSelect} />
      </div>
    </section>
  )
}
