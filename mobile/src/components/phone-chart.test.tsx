import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { parseChartSpec, type ChartSpec } from "@/lib/chart-spec"

import { PhoneChart } from "./phone-chart"

function spec(body: unknown): ChartSpec {
  const got = parseChartSpec(JSON.stringify(body))
  if (!got.ok) throw new Error("fixture")
  return got.spec
}

describe("PhoneChart", () => {
  it("draws a line and switches to the same rows as a table", () => {
    render(
      <PhoneChart
        spec={spec({
          type: "line",
          title: "Counts",
          unit: "n",
          x: "item",
          y: ["left", "right"],
          data: [
            { item: "a", left: 1, right: 2 },
            { item: "b", left: 3, right: 4 },
          ],
        })}
      />,
    )
    expect(screen.getByTestId("phone-chart-plot").querySelectorAll("polyline")).toHaveLength(2)
    fireEvent.click(screen.getByRole("tab", { name: "Table" }))
    expect(screen.getByRole("columnheader", { name: "left (n)" })).toBeInTheDocument()
    expect(screen.getByText("4")).toBeInTheDocument()
  })

  it("draws area, grouped bars, and stacked bars", () => {
    const { rerender } = render(
      <PhoneChart
        spec={spec({
          type: "area",
          x: "k",
          y: "v",
          data: [
            { k: "p", v: 1 },
            { k: "q", v: 2 },
          ],
        })}
      />,
    )
    expect(screen.getByTestId("phone-chart-plot").querySelector("path")).toBeTruthy()

    rerender(
      <PhoneChart
        spec={spec({
          type: "bar",
          x: "k",
          y: ["a", "b"],
          data: [
            { k: "p", a: 1, b: 2 },
            { k: "q", a: 3, b: 4 },
          ],
        })}
      />,
    )
    expect(screen.getByTestId("phone-chart-plot").querySelectorAll("rect").length).toBeGreaterThan(0)

    rerender(
      <PhoneChart
        spec={spec({
          type: "bar",
          stacked: true,
          x: "k",
          y: ["a", "b"],
          data: [
            { k: "p", a: -2, b: -1 },
            { k: "q", a: -4, b: -1 },
          ],
        })}
      />,
    )
    expect(screen.getByTestId("phone-chart-plot").querySelectorAll("rect").length).toBeGreaterThan(0)
  })

  it("draws a pie, including a slice that is the whole circle", () => {
    const { rerender } = render(
      <PhoneChart
        spec={spec({
          type: "pie",
          x: "label",
          y: "value",
          data: [
            { label: "a", value: 1 },
            { label: "b", value: 3 },
          ],
        })}
      />,
    )
    expect(screen.getByTestId("phone-chart-plot").querySelectorAll("path")).toHaveLength(2)

    rerender(
      <PhoneChart
        spec={spec({
          type: "pie",
          x: "label",
          y: "value",
          data: [
            { label: "a", value: 5 },
            { label: "b", value: 0 },
          ],
        })}
      />,
    )
    expect(screen.getByTestId("phone-chart-plot").querySelector("path")?.getAttribute("d")).toMatch(/^M /)
  })

  it("captions the unit above the axis on the default view", () => {
    render(<PhoneChart spec={spec({
      type: "line", x: "k", y: "v", unit: "req/s",
      data: [{ k: "a", v: 1 }, { k: "b", v: 2 }],
    })} />)
    expect(screen.getByTestId("phone-chart-unit")).toHaveTextContent("req/s")
  })

  it("keeps x labels readable when there are more points than fit", () => {
    const data = Array.from({ length: 8 }, (_, i) => ({ k: `p${i}`, v: i + 1 }))
    render(<PhoneChart spec={spec({ type: "line", x: "k", y: "v", data })} />)
    const labels = screen.getByTestId("phone-chart-plot").querySelectorAll("text")
    const xs = [...labels].map((node) => node.textContent)
    expect(xs).toContain("p0")
    expect(xs).toContain("p7")
    expect(xs.filter((text) => text?.startsWith("p"))).toHaveLength(5)
  })

  it("fills below the line and fades to the zero baseline for mixed signs", () => {
    render(<PhoneChart spec={spec({
      type: "line", x: "k", y: "v",
      data: [{ k: "a", v: -2 }, { k: "b", v: 2 }],
    })} />)
    const plot = screen.getByTestId("phone-chart-plot")
    const gradient = plot.querySelector("linearGradient")
    expect(gradient).not.toBeNull()
    expect(gradient?.querySelector('stop[offset="0.5"]')).toHaveAttribute("stop-opacity", "0")
    expect(plot.querySelector("path")).toHaveAttribute("fill", `url(#${gradient?.id})`)
    expect(plot.querySelector("polyline")).toHaveAttribute("stroke", "hsl(var(--chart-1))")
  })
})
