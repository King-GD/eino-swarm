import { render, screen } from "@testing-library/react"
import { expect, it } from "vitest"
import { easmText, EasmTextContext } from "../../../lib/easm/messages"
import { SupplyTable } from "./supply-table"

const zh = easmText("zh")
const table = (props: Parameters<typeof SupplyTable>[0]) => <EasmTextContext value={zh}><SupplyTable {...props} /></EasmTextContext>

// An empty table while components may still arrive reads as "none found";
// the user should see progress, then either rows or an explicit empty state.
it("shows a spinner while components can arrive, then rows or an empty state", () => {
  const view = render(table({ components: [], loading: true }))
  expect(screen.getByRole("progressbar", { name: zh.supplyLoading })).toBeInTheDocument()
  expect(screen.queryByRole("table")).toBeNull()
  view.rerender(table({ components: [], loading: false }))
  expect(screen.queryByRole("progressbar")).toBeNull()
  expect(screen.getByText(zh.supplyEmpty)).toBeInTheDocument()
  view.rerender(table({ components: [{ name: "nginx", count: 3 }], loading: true }))
  expect(screen.queryByRole("progressbar")).toBeNull()
  expect(screen.getByRole("table")).toHaveTextContent("nginx")
})
