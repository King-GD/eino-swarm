// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { KnowledgeLinkProvider } from "./knowledge-link-open"
import { MemoMarkdown } from "./markdown"
import { QuotedMessageBody } from "./quoted-message"

describe("Knowledge links in Markdown", () => {
  it("opens an ID from prose without turning its label into a workspace link", () => {
    const open = vi.fn()
    const text = "See [[page-one|report.md]] and **[[missing|Upcoming page]]**. Example: `[[literal]]`."
    render(
      <KnowledgeLinkProvider open={open} unresolved={["missing"]}>
        <MemoMarkdown text={text} />
      </KnowledgeLinkProvider>,
    )
    const report = screen.getByRole("button", { name: "report.md" })
    const missing = screen.getByRole("button", { name: "Upcoming page" })
    expect(report.getAttribute("data-page")).toBe("page-one")
    expect(missing.classList.contains("text-destructive")).toBe(true)
    fireEvent.click(report)
    expect(open.mock.calls).toEqual([["page-one"]])
    expect(screen.getByText("[[literal]]").tagName).toBe("CODE")
  })

  it("renders literal notation in contexts without knowledge navigation", () => {
    render(<MemoMarkdown text="Plain text [[example]]" />)
    expect(screen.getByText("Plain text [[example]]")).toBeTruthy()
    expect(screen.queryByTestId("knowledge-wikilink")).toBeNull()
  })

  it("respects Markdown escapes and other links while keeping wiki labels literal", () => {
    const text = String.raw`Escaped \[[literal]] [outer [[nested]]](https://example.invalid) [[one|**literal stars**]]`
    render(<KnowledgeLinkProvider open={vi.fn()}><MemoMarkdown text={text} /></KnowledgeLinkProvider>)
    expect(screen.getByRole("link", { name: "outer [[nested]]" }).getAttribute("href")).toBe("https://example.invalid")
    expect(screen.getByText(/Escaped \[\[literal\]\]/)).toBeTruthy()
    expect(screen.getAllByTestId("knowledge-wikilink")).toEqual([screen.getByRole("button", { name: "**literal stars**" })])
  })

  it("opens a wiki reference in a human message while keeping the rest plain", () => {
    const open = vi.fn()
    render(<KnowledgeLinkProvider open={open}><QuotedMessageBody text="**Literal** [[topic|Open topic]]" /></KnowledgeLinkProvider>)
    expect(screen.getByText(/\*\*Literal\*\*/)).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Open topic" }))
    expect(open.mock.calls).toEqual([["topic"]])
  })
})
