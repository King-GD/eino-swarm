import { render, screen, within } from "@testing-library/react"
import { expect, it } from "vitest"
import { easmText, EasmTextContext } from "../../../lib/easm/messages"
import { CloudTable } from "./cloud-table"

const zh = easmText("zh")
const table = (props: Parameters<typeof CloudTable>[0]) => <EasmTextContext value={zh}><CloudTable {...props} /></EasmTextContext>

it("shows progress only while providers can arrive and hides the empty card afterwards", () => {
  const view = render(table({ clouds: [], loading: true }))
  expect(screen.getByRole("progressbar", { name: zh.cloudLoading })).toBeInTheDocument()
  expect(screen.queryByRole("table")).toBeNull()
  view.rerender(table({ clouds: [], loading: false }))
  expect(screen.queryByRole("progressbar")).toBeNull()
  expect(screen.queryByRole("region", { name: zh.cloudTitle })).toBeNull()
  view.rerender(table({ clouds: [{ name: "aws", category: "cloud", assets: 3, ips: 2, services: [] }], loading: true }))
  expect(screen.queryByRole("progressbar")).toBeNull()
  expect(screen.getByRole("table")).toHaveTextContent("aws")
})

// Each row is the company's assets behind that provider: an untranslated name
// keeps the source spelling, and a category the copy does not know falls back to
// a dash instead of an empty cell.
it("shows the assets behind each provider with its category and services", () => {
  render(table({
    clouds: [
      { name: "BaiShanCloud", display_name: "白山云", category: "cdn", assets: 1135, ips: 131, services: [{ name: "NGINX", count: 980 }, { name: "OpenResty", count: 54 }] },
      { name: "Tencent Mail", category: "mail", assets: 56, ips: 40, services: [] },
      { name: "Some New CDN", category: "unknown", assets: 4, ips: 2, services: [] },
    ],
  }))
  const rows = within(screen.getByTestId("easm-clouds")).getAllByRole("row")
  expect(rows).toHaveLength(4)
  expect(rows[0]).toHaveTextContent(zh.cloudProvider)
  expect(rows[0]).toHaveTextContent(zh.cloudServices)
  expect(rows[1]).toHaveTextContent("白山云")
  expect(rows[1]).toHaveTextContent("CDN")
  expect(rows[1]).toHaveTextContent("1,135")
  expect(rows[1]).toHaveTextContent("NGINX 980、OpenResty 54")
  // The counts are text, not a tooltip: a narrow panel wraps the cell, and a
  // title cannot be read by tapping.
  expect(rows[1].lastElementChild).not.toHaveAttribute("title")
  expect(rows[2]).toHaveTextContent("Tencent Mail")
  expect(rows[2]).toHaveTextContent("邮件")
  // No services reported is a dash, not a blank cell.
  expect(rows[2].lastElementChild).toHaveTextContent("—")
  expect(rows[3]).toHaveTextContent("Some New CDN")
  expect(rows[3].children[1]).toHaveTextContent("—")
})

// The maintained names are Chinese. Another language shows the provider as
// FOFA reported it, and the same stored row follows a language switch.
it("shows the maintained name only in Chinese and the source name otherwise", () => {
  const en = easmText("en")
  const clouds = [{ name: "akamai", display_name: "阿卡迈", category: "cdn" as const, assets: 7, ips: 3, services: [] }]
  const view = render(<EasmTextContext value={en}><CloudTable clouds={clouds} /></EasmTextContext>)
  const row = () => within(screen.getByTestId("easm-clouds")).getAllByRole("row")[1]
  expect(row()).toHaveTextContent("akamai")
  expect(row()).not.toHaveTextContent("阿卡迈")
  expect(row().children[1]).toHaveTextContent(en.cloudCategories.cdn)
  expect(row().children[1]).not.toHaveAttribute("title")
  view.rerender(<EasmTextContext value={zh}><CloudTable clouds={clouds} /></EasmTextContext>)
  expect(row()).toHaveTextContent("阿卡迈")
})

// A portrait saved before cloud collection existed can still open.
it("hides the cloud card for a stored portrait with no cloud data", () => {
  render(table({}))
  expect(screen.queryByRole("region", { name: zh.cloudTitle })).toBeNull()
  expect(screen.queryByRole("table")).toBeNull()
})
