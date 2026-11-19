import { useState } from "react"
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { indexResult } from "../../../lib/easm/model"
import type { EasmNode, EasmResult } from "../../../lib/easm/types"
import { NodeCard } from "./node-card"
import { KIND_ICONS } from "./kind-style"

afterEach(cleanup)

const domain: EasmNode = { id: "domain", kind: "domain", label: "example.test" }
const company: EasmNode = { id: "company", kind: "company", label: "示例企业" }
const icp: EasmNode = { id: "icp", kind: "feature", featureKind: "icp", label: "京ICP备12345678号", detail: { subject_name: company.label } }

function result(nodes: EasmNode[], edges: EasmResult["edges"] = []): EasmResult {
  return {
    schema_version: 1, title: "EASM",
    nodes: nodes.some((node) => node.kind === "root") ? nodes : [{ id: "root", kind: "root", label: "示例主体" }, ...nodes],
    edges, heat: [], components: [],
  }
}

function Card({ graph, start }: { graph: EasmResult; start: string }) {
  const [selected, setSelected] = useState(start)
  const index = indexResult(graph)
  return <NodeCard node={index.byId.get(selected)!} index={index} onSelect={setSelected} onClose={vi.fn()} />
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
    render(<Card graph={result([node, domain])} start={node.id} />)
    const card = screen.getByRole("dialog")
    expect(within(card).getByRole("heading", { name: "有效特征" })).toBeInTheDocument()
    fireEvent.click(within(card).getByRole("button", { name: domain.label }))
    expect(screen.getByText("暂无已保存的有效特征")).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: domain.label })).toBeInTheDocument()
  })

  it("navigates the sourced ICP feature and its connected company and domain", () => {
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
    render(<Card graph={graph} start={node.id} />)
    expect(screen.queryByText("域名", { exact: true })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: icp.label }))
    const card = screen.getByRole("dialog", { name: icp.label })
    expect(within(card).getByText("备案主体", { exact: true })).toBeInTheDocument()
    expect(within(card).getByRole("button", { name: company.label })).toBeInTheDocument()
    fireEvent.click(within(card).getByRole("button", { name: domain.label }))
    expect(screen.getByRole("dialog", { name: domain.label })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: icp.label })).toBeInTheDocument()
  })

  it("keeps other supplied features and does not infer ICP from a reason", () => {
    const node: EasmNode = {
      ...domain,
      detail: { reason: `归属说明引用备案号${icp.label}`, features: [
        { kind: "domain", value: domain.label },
        { kind: "domain", value: "related.test" },
        { kind: "cert", value: "existing certificate" },
      ] },
    }
    render(<Card graph={result([node])} start={node.id} />)
    expect(screen.getByText("related.test")).toBeInTheDocument()
    expect(screen.getByText("existing certificate")).toBeInTheDocument()
    expect(screen.queryByText("ICP 备案", { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: icp.label })).not.toBeInTheDocument()
  })

  it("shows stored asset judgment and confirmed filing domains on a feature card", () => {
    const filing = { ...icp, detail: { ...icp.detail, reason: "备案查询返回该条记录" } }
    const otherDomain = { ...domain, id: "other-domain", label: "other.test" }
    render(<Card graph={result([filing, domain, otherDomain], [
      { source: filing.id, target: domain.id, confirmed: true },
      { source: filing.id, target: otherDomain.id, confirmed: false },
    ])} start={filing.id} />)
    const heading = screen.getByRole("heading", { name: "资产判断详情" })
    const judgment = within(heading.parentElement!)
    expect(judgment.getByText("备案查询返回该条记录")).toBeInTheDocument()
    expect(judgment.getByText("已确认关联的备案域名：example.test")).toBeInTheDocument()
    expect(judgment.queryByText(/other\.test/)).not.toBeInTheDocument()
  })

  it("shows a legacy filing's recorded domain even when no judgment text was saved", () => {
    render(<Card graph={result([icp, domain], [
      { source: icp.id, target: domain.id, confirmed: true },
    ])} start={icp.id} />)
    expect(screen.getByRole("heading", { name: "资产判断详情" })).toBeInTheDocument()
    expect(screen.getByText("已确认关联的备案域名：example.test")).toBeInTheDocument()
  })

  it("shows the saved filing and registrant on a domain card that has no judgment text", () => {
    const pending = { ...icp, id: "pending", label: "Pending filing" }
    render(<Card graph={result([domain, icp, pending, company], [
      { source: icp.id, target: domain.id, confirmed: true },
      { source: company.id, target: domain.id, confirmed: true },
      { source: pending.id, target: domain.id, confirmed: false },
    ])} start={domain.id} />)
    const judgment = within(screen.getByRole("heading", { name: "资产判断详情" }).parentElement!)
    expect(judgment.getByText("关联的 ICP 备案")).toBeInTheDocument()
    expect(judgment.getByRole("listitem")).toHaveTextContent(`${icp.label}（备案主体 ${company.label}）`)
  })

  it("distinguishes recorded confirmed links from pending or unmarked links", () => {
    const second = { ...company, id: "company2", label: "Second company" }
    render(<Card graph={result([company, second, domain], [
      { source: company.id, target: domain.id, confirmed: true },
      { source: company.id, target: second.id, confirmed: false },
      { source: second.id, target: domain.id },
    ])} start={company.id} />)
    const judgment = within(screen.getByRole("heading", { name: "资产判断详情" }).parentElement!)
    expect(judgment.getByText("图谱中已确认的关联")).toBeInTheDocument()
    expect(judgment.getByRole("listitem")).toHaveTextContent(domain.label)
    cleanup()
    render(<Card graph={result([second, domain], [
      { source: second.id, target: domain.id, confirmed: false },
    ])} start={second.id} />)
    expect(screen.getByText("暂无已保存的判断依据")).toBeInTheDocument()
  })

  it("opens a stored domain certificate even without a redundant feature list", () => {
    const certificate: EasmNode = {
      id: "certificate", kind: "feature", featureKind: "cert", label: "Certificate evidence",
    }
    render(<Card graph={result([domain, certificate], [
      { source: certificate.id, target: domain.id, confirmed: false },
    ])} start={domain.id} />)
    fireEvent.click(screen.getByRole("button", { name: certificate.label }))
    expect(screen.getByRole("dialog", { name: certificate.label })).toBeInTheDocument()
  })

  it("keeps root cards to the profile, organizations and effective features", () => {
    const certificateOrg: EasmNode = { id: "cert-org", kind: "feature", featureKind: "org", label: "Certificate Subject" }
    const root: EasmNode = {
      id: "root", kind: "root", label: "Fixture Root",
      detail: {
        description: "A sourced company introduction", website: "https://example.test", region: "Country",
        industry: ["Industry"], reason: "Old judgment",
        organizations: [{ name: company.label }, { name: certificateOrg.label }],
      },
    }
    const graph = { ...result([root, company, certificateOrg, domain], [{ source: root.id, target: domain.id, confirmed: false }]), summary: { fofa_assets: 42, fofa_ips: 5 } }
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
    const orgButton = within(card).getByRole("button", { name: certificateOrg.label })
    expect(orgButton.querySelector("img")).toHaveAttribute("src", KIND_ICONS.org)
    fireEvent.click(orgButton)
    expect(screen.getByRole("dialog", { name: certificateOrg.label })).toBeInTheDocument()
    expect(screen.getByRole("dialog").querySelector("header img")).toHaveAttribute("src", KIND_ICONS.org)
  })

  it("lists related legal companies without relabeling them as certificate organizations", () => {
    const graph = result([company], [{ source: "root", target: company.id, confirmed: false }])
    render(<Card graph={graph} start="root" />)
    expect(screen.getByText("暂无已保存的企业简介")).toBeInTheDocument()
    const button = screen.getByRole("button", { name: company.label })
    expect(button.querySelector("img")).toHaveAttribute("src", KIND_ICONS.company)
    fireEvent.click(button)
    expect(screen.getByRole("dialog", { name: company.label })).toBeInTheDocument()
    expect(screen.getByText("企业组织", { exact: true })).toBeInTheDocument()
    expect(screen.getAllByRole("heading", { level: 3 }).map((el) => el.textContent)).toEqual(["资产判断详情", "有效特征"])
  })

  it("does not choose an arbitrary node when a saved organization name is ambiguous", () => {
    const other = { ...company, id: "other-company" }
    const root: EasmNode = { id: "root", kind: "root", label: "Fixture root", detail: { organizations: [{ name: company.label }] } }
    render(<Card graph={result([root, company, other])} start={root.id} />)
    expect(screen.getByText(company.label)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: company.label })).not.toBeInTheDocument()
  })

  it.each([
    { id: "old-org", kind: "org", label: "Legacy certificate organization" } as EasmNode,
    { id: "new-org", kind: "feature", featureKind: "org", label: "Certificate organization" } as EasmNode,
  ])("uses the certificate organization glyph and linked domain for $kind", (organization) => {
    render(<Card graph={result([organization, domain], [{ source: organization.id, target: domain.id, confirmed: false }])} start={organization.id} />)
    const card = screen.getByRole("dialog")
    expect(card.querySelector("header img")).toHaveAttribute("src", KIND_ICONS.org)
    expect(within(card).getByText("证书组织", { exact: true })).toBeInTheDocument()
    fireEvent.click(within(card).getByRole("button", { name: domain.label }))
    expect(screen.getByRole("dialog", { name: domain.label })).toBeInTheDocument()
  })

  it("shows certificate identity inside effective features without adding service tables or changing saved flags", () => {
    const certificate: EasmNode = {
      id: "certificate", kind: "feature", featureKind: "cert", label: "Shared certificate",
      detail: {
        serial: "shared-serial", issuer: "Source CA", not_before: "2026-01-01", not_after: "2099-12-31",
        is_valid: false, is_match: false,
        observations: [
          { host: "https://one.example.test:443", source: "search", is_valid: false, is_match: false, is_expired: false },
          { host: "https://two.example.test:8443", source: "lookup", is_valid: true, is_match: true },
        ],
      },
    }
    const graph = result([certificate, domain], [{ source: certificate.id, target: domain.id, confirmed: false }])
    const before = JSON.stringify(graph)
    render(<Card graph={graph} start={certificate.id} />)
    const card = screen.getByRole("dialog")
    expect(within(card).getAllByRole("heading", { level: 3 }).map((el) => el.textContent)).toEqual(["资产判断详情", "有效特征"])
    expect(within(card).getByText("shared-serial")).toBeInTheDocument()
    expect(within(card).getByText("Source CA")).toBeInTheDocument()
    expect(within(card).getByText("2099-12-31", { exact: false })).toBeInTheDocument()
    expect(within(card).queryByRole("table", { name: "服务观测" })).not.toBeInTheDocument()
    expect(within(card).queryByText("校验状态")).not.toBeInTheDocument()
    expect(within(card).getByRole("button", { name: domain.label })).toBeInTheDocument()
    expect(JSON.stringify(graph)).toBe(before)
  })
})
