import { useState } from "react"
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { basisText, indexResult } from "../../../lib/easm/model"
import { easmText, EasmTextContext } from "../../../lib/easm/messages"

const zhText = easmText("zh")
import type { EasmNode, EasmResult } from "../../../lib/easm/types"
import { NodeCard } from "./node-card"
import { KIND_ICONS } from "./kind-style"

afterEach(cleanup)

const domain: EasmNode = { id: "domain", kind: "domain", label: "example.test" }
const company: EasmNode = { id: "company", kind: "company", label: "示例企业" }
const icp: EasmNode = { id: "icp", kind: "feature", featureKind: "icp", label: "京ICP备12345678号", detail: { subject_name: company.label } }

function result(nodes: EasmNode[], edges: EasmResult["edges"] = []): EasmResult {
  return {
    schema_version: 1, title: "EASM", collection: { status: "done", steps: [] },
    nodes: nodes.some((node) => node.kind === "root") ? nodes : [{ id: "root", kind: "root", label: "示例主体" }, ...nodes],
    edges, heat: [], components: [],
  }
}

function Card({ graph, start }: { graph: EasmResult; start: string }) {
  const [selected, setSelected] = useState(start)
  const index = indexResult(graph)
  return <EasmTextContext value={zhText}><NodeCard node={index.byId.get(selected)!} index={index} onSelect={setSelected} onClose={vi.fn()} /></EasmTextContext>
}

describe("EASM node details", () => {
  it("shows the saved company logo once in the root header and keeps its introduction", () => {
    const root: EasmNode = {
      id: "root", kind: "root", label: "Fixture company",
      detail: { logo: "https://example.test/logo.svg", description: "A sourced company introduction" },
    }
    render(<Card graph={result([root])} start={root.id} />)
    const logo = screen.getByRole("img", { name: `${root.label}标识` })
    expect(logo.closest("header")).not.toBeNull()
    expect(logo).toHaveAttribute("src", root.detail!.logo)
    expect(logo).toHaveClass("object-contain")
    expect(screen.getAllByRole("img", { name: `${root.label}标识` })).toHaveLength(1)
    expect(screen.getByText(root.detail!.description!)).toBeInTheDocument()
  })

  it("falls back for a missing or broken logo and can display an updated saved logo", () => {
    const root: EasmNode = { id: "root", kind: "root", label: "Fixture company" }
    const { rerender } = render(<Card graph={result([root])} start={root.id} />)
    expect(screen.getByRole("dialog").querySelector("header img")).toHaveAttribute("src", KIND_ICONS.root)
    const withLogo = { ...root, detail: { logo: "https://example.test/broken.svg" } }
    rerender(<Card graph={result([withLogo])} start={root.id} />)
    fireEvent.error(screen.getByRole("img", { name: `${root.label}标识` }))
    expect(screen.queryByRole("img", { name: `${root.label}标识` })).not.toBeInTheDocument()
    expect(screen.getByRole("dialog").querySelector("header img")).toHaveAttribute("src", KIND_ICONS.root)
    rerender(<Card graph={result([{ ...withLogo, detail: { logo: "https://example.test/updated.svg" } }])} start={root.id} />)
    expect(screen.getByRole("img", { name: `${root.label}标识` })).toHaveAttribute("src", "https://example.test/updated.svg")
  })

  it.each([
    { label: "example.test", value: "example.test" },
    { label: "example.test", value: "different.test", node_id: domain.id },
    { label: "example.test", value: " EXAMPLE.TEST. " },
    { label: "EXAMPLE.TEST.", value: "example.test" },
  ])("hides a saved domain self-reference $label / $value", (saved) => {
    const node: EasmNode = {
      ...domain, label: saved.label,
      detail: { features: [{ kind: "domain", value: saved.value, node_id: saved.node_id }] },
    }
    render(<Card graph={result([node])} start={node.id} />)
    expect(screen.getByRole("heading", { name: node.label })).toBeInTheDocument()
    expect(screen.getByText("暂无已保存的有效特征")).toBeInTheDocument()
    expect(screen.queryByText("域名", { exact: true })).not.toBeInTheDocument()
    expect(node.detail?.features).toHaveLength(1)
  })

  it.each(["root", "company"] as const)("keeps a %s node's real domain references", (kind) => {
    const node: EasmNode = {
      id: "owner", kind, label: domain.label,
      detail: { features: [{ kind: "domain", value: domain.label, node_id: domain.id }] },
    }
    render(<Card graph={result([node, domain], [{ source: node.id, target: domain.id, confirmed: true }])} start={node.id} />)
    const card = screen.getByRole("dialog")
    expect(within(card).getByRole("heading", { name: "有效特征" })).toBeInTheDocument()
    fireEvent.click(within(card).getByRole("button", { name: domain.label }))
    expect(screen.getByText("暂无已保存的有效特征")).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: domain.label })).toBeInTheDocument()
  })

  it("navigates the direct filing chain and keeps the incoming company in the filing evidence", () => {
    const node: EasmNode = {
      ...domain,
      detail: { features: [
        { kind: "domain", value: "EXAMPLE.TEST.", node_id: domain.id },
        { kind: "icp", value: icp.label, node_id: icp.id },
      ] },
    }
    const graph = result([company, icp, node], [
      { source: company.id, target: icp.id, confirmed: true },
      { source: icp.id, target: node.id, confirmed: true },
    ])
    render(<Card graph={graph} start={company.id} />)
    fireEvent.click(screen.getByRole("button", { name: icp.label }))
    const card = screen.getByRole("dialog", { name: icp.label })
    expect(within(card).getByText("备案主体", { exact: true })).toBeInTheDocument()
    expect(within(card).queryByRole("button", { name: company.label })).not.toBeInTheDocument()
    expect(within(card).getByText(`线索来源于 ${company.label} 的备案记录。提取信息：ICP备案 ${icp.label}，域名 ${domain.label}。`)).toBeInTheDocument()
    fireEvent.click(within(card).getByRole("button", { name: domain.label }))
    expect(screen.getByRole("dialog", { name: domain.label })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: icp.label })).not.toBeInTheDocument()
  })

  it("keeps a modern certificate's incoming sources in its judgment only", () => {
    const certificate: EasmNode = {
      id: "certificate", kind: "feature", featureKind: "cert", label: "Certificate evidence",
    }
    const graph = result([domain, company, certificate], [
      { source: company.id, target: domain.id, confirmed: true },
      { source: domain.id, target: certificate.id, confirmed: true },
    ])
    render(<Card graph={graph} start={domain.id} />)
    fireEvent.click(screen.getByRole("button", { name: certificate.label }))
    expect(screen.getByRole("dialog", { name: certificate.label })).toBeInTheDocument()
    expect(screen.getByText("暂无已保存的有效特征")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: company.label })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: domain.label })).not.toBeInTheDocument()
    const judgment = within(screen.getByRole("heading", { name: "资产判断详情" }).parentElement!)
    expect(judgment.getByText(/主域名 example\.test/)).toBeInTheDocument()
  })

  it("lists children at exactly one level", () => {
    const certificate: EasmNode = {
      id: "certificate", kind: "feature", featureKind: "cert", label: "Outgoing certificate",
    }
    const grandchild: EasmNode = { id: "mx", kind: "feature", featureKind: "mx", label: "mail.example.test" }
    const graph = result([domain, icp, certificate, grandchild], [
      { source: icp.id, target: domain.id, confirmed: true },
      { source: domain.id, target: certificate.id, confirmed: true },
      { source: certificate.id, target: grandchild.id, confirmed: true },
    ])
    render(<Card graph={graph} start={domain.id} />)
    expect(screen.getByRole("button", { name: certificate.label })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: icp.label })).not.toBeInTheDocument()
    expect(screen.queryByText(grandchild.label)).not.toBeInTheDocument()
  })

  it("preserves judgment while the feature list waits for a direct child", () => {
    const basis = { source: "filings", via: icp.label }
    const node: EasmNode = {
      ...domain,
      detail: { basis: [basis], features: [
        { kind: "domain", value: domain.label },
        { kind: "domain", value: "related.test" },
        { kind: "cert", value: "existing certificate" },
      ] },
    }
    render(<Card graph={result([node])} start={node.id} />)
    expect(screen.getByText(basisText(basis, zhText))).toBeInTheDocument()
    expect(screen.getByText("暂无已保存的有效特征")).toBeInTheDocument()
    expect(screen.queryByText("related.test")).not.toBeInTheDocument()
    expect(screen.queryByText("existing certificate")).not.toBeInTheDocument()
  })

  it("shows stored asset judgment and confirmed filing domains on a feature card", () => {
    const otherDomain = { ...domain, id: "other-domain", label: "other.test" }
    const pendingDomain = { ...domain, id: "pending-domain", label: "pending.test" }
    const filing: EasmNode = { ...icp, detail: { ...icp.detail, basis: [{ source: "filings", via: icp.label }], features: [
      { kind: "domain", value: domain.label, node_id: domain.id },
      { kind: "domain", value: otherDomain.label, node_id: otherDomain.id },
      { kind: "domain", value: pendingDomain.label, node_id: pendingDomain.id },
    ] } }
    render(<Card graph={result([company, filing, domain, otherDomain, pendingDomain], [
      { source: filing.id, target: domain.id, confirmed: true },
      { source: company.id, target: otherDomain.id, confirmed: true },
      { source: filing.id, target: pendingDomain.id, confirmed: false },
    ])} start={filing.id} />)
    const heading = screen.getByRole("heading", { name: "资产判断详情" })
    const judgment = within(heading.parentElement!)
    expect(judgment.getByText(basisText({ source: "filings", via: filing.label }, zhText))).toBeInTheDocument()
    expect(judgment.getByText(`线索来源于 ${company.label} 的备案记录。提取信息：ICP备案 ${filing.label}，域名 ${domain.label}、${otherDomain.label}。`)).toBeInTheDocument()
    expect(judgment.queryByText(/pending\.test/)).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: otherDomain.label })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: pendingDomain.label }))
    expect(screen.getByRole("dialog", { name: pendingDomain.label })).toBeInTheDocument()
  })

  it("shows a confirmed filing's recorded domain when it has an outgoing source relation", () => {
    render(<Card graph={result([icp, domain], [
      { source: icp.id, target: domain.id, confirmed: true },
    ])} start={icp.id} />)
    expect(screen.getByRole("heading", { name: "资产判断详情" })).toBeInTheDocument()
    expect(screen.getByText(`线索来源于 ${company.label} 的备案记录。提取信息：ICP备案 ${icp.label}，域名 ${domain.label}。`)).toBeInTheDocument()
  })

  it("uses the accepted source in domain judgment", () => {
    const pending = { ...icp, id: "pending", label: "Pending filing" }
    const savedDomain: EasmNode = { ...domain, detail: { features: [{ kind: "icp", value: icp.label, node_id: icp.id }] } }
    render(<Card graph={result([savedDomain, icp, pending, company], [
      { source: company.id, target: savedDomain.id, confirmed: true },
    ])} start={domain.id} />)
    const judgment = within(screen.getByRole("heading", { name: "资产判断详情" }).parentElement!)
    expect(judgment.getByRole("listitem")).toHaveTextContent(`${domain.label} 与关联组织 ${company.label} 的关联已确认。`)
    expect(judgment.queryByText(/Pending filing/)).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: icp.label })).not.toBeInTheDocument()
  })

  it("distinguishes recorded confirmed links from pending or unmarked links", () => {
    const second = { ...company, id: "company2", label: "Second company" }
    const unmarked = { ...domain, id: "unmarked", label: "another.test" }
    render(<Card graph={result([company, second, domain, unmarked], [
      { source: company.id, target: domain.id, confirmed: true },
      { source: company.id, target: second.id, confirmed: false },
      { source: company.id, target: unmarked.id },
    ])} start={company.id} />)
    const judgment = within(screen.getByRole("heading", { name: "资产判断详情" }).parentElement!)
    expect(judgment.getByRole("listitem")).toHaveTextContent(`${company.label} 与主域名 ${domain.label} 的关联已确认。`)
    expect(judgment.queryByText(/Second company/)).not.toBeInTheDocument()
    expect(judgment.queryByText(/another\.test/)).not.toBeInTheDocument()
    cleanup()
    render(<Card graph={result([second, domain], [
      { source: second.id, target: domain.id, confirmed: false },
    ])} start={second.id} />)
    expect(screen.getByText("暂无已保存的判断依据")).toBeInTheDocument()
  })

  // Evidence is stored as structured basis; every saved item must be shown,
  // worded by the renderer, with the stored data left untouched.
  it("words every saved basis item without changing it", () => {
    const basis = [{ source: "relations", via: company.label }, { source: "asset_rules", via: "rule", date: "2026-01-02T03:04:05Z" }]
    const saved = { ...company, detail: { basis } }
    render(<Card graph={result([saved])} start={saved.id} />)
    const judgment = within(screen.getByRole("heading", { name: "资产判断详情" }).parentElement!)
    for (const item of basis) expect(judgment.getByText(basisText(item, zhText))).toBeInTheDocument()
    expect(basisText(basis[1], zhText)).toContain("2026-01-02")
    expect(saved.detail.basis).toEqual(basis)
  })

  // The agent's tool reports the relationship it found; the card shows that
  // value as reported, next to the evidence for the link.
  it("shows the reported relation on a company card", () => {
    const related: EasmNode = { ...company, detail: { relation: "subsidiary", basis: [{ source: "relation_tool", via: "R-1" }] } }
    render(<Card graph={result([related], [{ source: "root", target: related.id, confirmed: true }])} start={related.id} />)
    const judgment = within(screen.getByRole("heading", { name: "资产判断详情" }).parentElement!)
    expect(judgment.getByText("关系：subsidiary")).toBeInTheDocument()
    expect(judgment.getByText(basisText({ source: "relation_tool", via: "R-1" }, zhText))).toBeInTheDocument()
    const plain: EasmNode = { ...company, id: "plain" }
    cleanup()
    render(<Card graph={result([plain], [{ source: "root", target: plain.id, confirmed: true }])} start={plain.id} />)
    expect(screen.queryByText(/关系：/)).not.toBeInTheDocument()
  })

  it("keeps root cards to the profile, organizations and effective features", () => {
    const certificateOrg: EasmNode = { id: "cert-org", kind: "feature", featureKind: "org", label: "Certificate Subject" }
    const root: EasmNode = {
      id: "root", kind: "root", label: "Fixture Root",
      detail: {
        description: "A sourced company introduction", website: "https://example.test", region: "Country",
        industry: ["Industry"], basis: [{ source: "organizations" }],
        organizations: [{ name: company.label }],
      },
    }
    const graph = { ...result([root, company, certificateOrg, domain], [
      { source: root.id, target: company.id, confirmed: true },
      { source: root.id, target: certificateOrg.id, confirmed: false },
      { source: root.id, target: domain.id, confirmed: true },
    ]), summary: { fofa_assets: 42, fofa_ips: 5 } }
    render(<Card graph={graph} start={root.id} />)
    const card = screen.getByRole("dialog")
    expect(within(card).getAllByRole("heading", { level: 3 }).map((el) => el.textContent)).toEqual(["简介", "有效特征"])
    expect(within(card).getByText("A sourced company introduction")).toBeInTheDocument()
    expect(within(card).getByText("组织列表")).toBeInTheDocument()
    expect(within(card).queryByText("节点信息")).not.toBeInTheDocument()
    expect(within(card).queryByText("资产判断详情")).not.toBeInTheDocument()
    expect(within(card).queryByText("Old judgment")).not.toBeInTheDocument()
    expect(within(card).queryByText("Industry")).not.toBeInTheDocument()
    expect(within(card).getByRole("button", { name: domain.label })).toBeInTheDocument()
    const organizations = within(within(card).getByText("组织列表").closest("details")!)
    expect(organizations.getByRole("button", { name: company.label })).toBeInTheDocument()
    expect(within(card).getByRole("button", { name: certificateOrg.label })).toBeInTheDocument()
  })

  it("lists related legal companies without relabeling them as certificate organizations", () => {
    const second: EasmNode = { id: "second", kind: "company", label: "Second legal company" }
    const pending = { ...company, id: "pending", label: "Pending company" }
    const subject: EasmNode = { id: "subject", kind: "feature", featureKind: "org", label: "Certificate subject" }
    const graph = result([company, second, pending, subject], [
      { source: "root", target: company.id, confirmed: true },
      { source: "root", target: second.id, confirmed: true },
      { source: "root", target: pending.id, confirmed: false },
      { source: "root", target: subject.id, confirmed: true },
    ])
    render(<Card graph={graph} start="root" />)
    expect(screen.getByText("暂无已保存的企业简介")).toBeInTheDocument()
    const organizations = within(screen.getByText("组织列表").closest("details")!)
    expect(organizations.getByRole("button", { name: second.label })).toBeInTheDocument()
    expect(organizations.queryByText(pending.label)).not.toBeInTheDocument()
    expect(organizations.queryByText(subject.label)).not.toBeInTheDocument()
    const button = organizations.getByRole("button", { name: company.label })
    expect(button.querySelector("img")).toHaveAttribute("src", KIND_ICONS.org)
    fireEvent.click(button)
    expect(screen.getByRole("dialog", { name: company.label })).toBeInTheDocument()
    expect(screen.getByRole("dialog").querySelector("header img")).toHaveAttribute("src", KIND_ICONS.org)
    expect(screen.getByText("关联组织", { exact: true })).toBeInTheDocument()
    expect(screen.getAllByRole("heading", { level: 3 }).map((el) => el.textContent)).toEqual(["资产判断详情", "有效特征"])
  })

  it("keeps an explicitly empty saved organization list empty", () => {
    const root: EasmNode = { id: "root", kind: "root", label: "Fixture root", detail: { organizations: [] } }
    render(<Card graph={result([root, company], [{ source: root.id, target: company.id, confirmed: true }])} start={root.id} />)
    expect(screen.getByText("暂无已保存的组织")).toBeInTheDocument()
    const organizations = within(screen.getByText("组织列表").closest("details")!)
    expect(organizations.queryByText(company.label)).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: company.label })).toBeInTheDocument()
  })

  it("lists and opens pending and unmarked direct children from the current graph", () => {
    const pending = { ...icp, id: "pending", label: "Pending filing" }
    const sourced = { ...icp, id: "sourced", label: "Sourced filing" }
    const unmarked = { ...icp, id: "unmarked", label: "Unmarked filing" }
    const node: EasmNode = { ...domain, detail: { features: [
      { kind: "icp", value: pending.label, node_id: pending.id },
      { kind: "icp", value: sourced.label, node_id: sourced.id },
      { kind: "icp", value: unmarked.label, node_id: unmarked.id },
    ] } }
    render(<Card graph={result([node, pending, sourced, unmarked], [
      { source: node.id, target: pending.id, confirmed: false },
      { source: node.id, target: unmarked.id },
    ])} start={node.id} />)
    expect(screen.queryByText(sourced.label)).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: unmarked.label })).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: pending.label }))
    expect(screen.getByRole("dialog", { name: pending.label })).toBeInTheDocument()
  })

  it("does not choose an arbitrary node when a saved organization name is ambiguous", () => {
    const other = { ...company, id: "other-company" }
    const root: EasmNode = { id: "root", kind: "root", label: "Fixture root", detail: { organizations: [{ name: company.label }] } }
    render(<Card graph={result([root, company, other])} start={root.id} />)
    expect(screen.getByText(company.label)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: company.label })).not.toBeInTheDocument()
  })

  it.each([
    { label: "关联组织", node: company },
    { label: "证书组织", node: { id: "new-org", kind: "feature", featureKind: "org", label: "Certificate organization" } as EasmNode },
  ])("preserves a saved $label and its confirmed domain link", ({ label, node: organization }) => {
    render(<Card graph={result([organization, domain], [{ source: organization.id, target: domain.id, confirmed: true }])} start={organization.id} />)
    const card = screen.getByRole("dialog")
    expect(card.querySelector("header img")).toHaveAttribute("src", KIND_ICONS.org)
    expect(within(card).getByText(label, { exact: true })).toBeInTheDocument()
    fireEvent.click(within(card).getByRole("button", { name: domain.label }))
    expect(screen.getByRole("dialog", { name: domain.label })).toBeInTheDocument()
  })

  it("shows certificate identity inside effective features without adding service tables or changing saved flags", () => {
    const certificate: EasmNode = {
      id: "certificate", kind: "feature", featureKind: "cert", label: "Shared certificate",
      detail: {
        serial: "shared-serial", issuer: "Source CA", not_before: "2026-01-01", not_after: "2099-12-31",
        names: ["one.example.test", "two.example.test"],
        features: [{ kind: "org", value: "Certificate subject organization" }],
        is_valid: false, is_match: false,
        observations: [
          { host: "https://one.example.test:443", source: "search", is_valid: false, is_match: false, is_expired: false },
          { host: "https://two.example.test:8443", source: "lookup", is_valid: true, is_match: true },
        ],
      },
    }
    const graph = result([certificate, domain], [{ source: domain.id, target: certificate.id, confirmed: true }])
    const before = JSON.stringify(graph)
    render(<Card graph={graph} start={certificate.id} />)
    const card = screen.getByRole("dialog")
    expect(within(card).getAllByRole("heading", { level: 3 }).map((el) => el.textContent)).toEqual(["资产判断详情", "有效特征"])
    expect(within(card).getByText("shared-serial")).toBeInTheDocument()
    expect(within(card).getByText("Source CA")).toBeInTheDocument()
    expect(within(card).getByText("2026-01-01 至 2099-12-31")).toBeInTheDocument()
    expect(within(card).getByText("one.example.test、two.example.test")).toBeInTheDocument()
    expect(within(card).queryByText("Certificate subject organization")).not.toBeInTheDocument()
    expect(within(card).queryByRole("table", { name: "服务观测" })).not.toBeInTheDocument()
    expect(within(card).queryByText("校验状态")).not.toBeInTheDocument()
    expect(within(card).queryByRole("button", { name: domain.label })).not.toBeInTheDocument()
    expect(JSON.stringify(graph)).toBe(before)
  })
})
