import { act, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setLocale } from "@/lib/i18n"
import { Composer } from "./composer"

afterEach(() => {
  delete window.__zwaiAndroidBack
})

function box() {
  return screen.getByLabelText("Message")
}

describe("phone slash commands", () => {
  it("keeps direct model chat free of command suggestions", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" onSubmit={onSubmit} />)
    fireEvent.change(box(), { target: { value: "/" } })
    expect(screen.queryByTestId("slash-menu")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Add" }))
    expect(screen.getByRole("menuitem", { name: "Attach files" })).toBeInTheDocument()
    expect(screen.queryByTestId("composer-command-goal")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith("/")
  })

  it("lists goal and plan from Add and from a typed slash", () => {
    setLocale("en")
    render(<Composer label="Message" sendLabel="Send" slashCommands onSubmit={vi.fn()} />)
    fireEvent.click(screen.getByRole("button", { name: "Add" }))
    expect(screen.getByTestId("composer-command-goal")).toHaveTextContent("Set a standing objective")
    expect(screen.getByTestId("composer-command-plan")).toBeInTheDocument()
    fireEvent.pointerDown(screen.getByTestId("composer-add-scrim"))
    expect(screen.queryByTestId("composer-add-menu")).not.toBeInTheDocument()

    fireEvent.change(box(), { target: { value: "/" } })
    expect(box()).toHaveValue("/")
    expect(screen.getByTestId("slash-command-goal")).toBeInTheDocument()
    expect(screen.getByTestId("slash-command-plan")).toBeInTheDocument()
    fireEvent.change(box(), { target: { value: "/pl" } })
    expect(screen.getByTestId("slash-command-plan")).toBeInTheDocument()
    expect(screen.queryByTestId("slash-command-goal")).not.toBeInTheDocument()
    fireEvent.change(box(), { target: { value: "foo/bar" } })
    expect(screen.queryByTestId("slash-menu")).not.toBeInTheDocument()
    fireEvent.change(box(), { target: { value: "https://example.invalid/goal" } })
    expect(screen.queryByTestId("slash-menu")).not.toBeInTheDocument()
  })

  it("inserts the picked command and does not send it until it has an argument", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" slashCommands onSubmit={onSubmit} />)
    fireEvent.change(box(), { target: { value: "／" } })
    expect(box()).toHaveValue("/")
    fireEvent.click(screen.getByTestId("slash-command-goal"))
    expect(box()).toHaveValue("/goal ")
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
    fireEvent.keyDown(box(), { key: "Enter" })
    expect(onSubmit).not.toHaveBeenCalled()

    fireEvent.change(box(), { target: { value: "/plan inspect" } })
    expect(screen.queryByTestId("slash-menu")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).toHaveBeenCalledWith("/plan inspect")
  })

  it("completes the highlighted command with Enter and keeps an existing sentence", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" slashCommands onSubmit={onSubmit} />)
    fireEvent.change(box(), { target: { value: "/" } })
    fireEvent.keyDown(box(), { key: "ArrowDown" })
    fireEvent.keyDown(box(), { key: "Enter" })
    expect(onSubmit).not.toHaveBeenCalled()
    expect(box()).toHaveValue("/plan ")

    fireEvent.change(box(), { target: { value: "keep going" } })
    fireEvent.click(screen.getByRole("button", { name: "Add" }))
    expect(screen.queryByTestId("composer-command-plan")).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId("composer-command-goal"))
    expect(box()).toHaveValue("keep going /goal ")
  })

  it("closes Add on Android Back and removes only the unfinished slash token", () => {
    setLocale("en")
    render(<Composer label="Message" sendLabel="Send" slashCommands onSubmit={vi.fn()} />)
    fireEvent.change(box(), { target: { value: "keep going" } })
    fireEvent.click(screen.getByRole("button", { name: "Add" }))
    act(() => {
      expect(window.__zwaiAndroidBack?.()).toBe(true)
    })
    expect(screen.queryByTestId("composer-add-menu")).not.toBeInTheDocument()
    expect(box()).toHaveValue("keep going")

    fireEvent.change(box(), { target: { value: "keep going /go" } })
    act(() => {
      expect(window.__zwaiAndroidBack?.()).toBe(true)
    })
    expect(screen.queryByTestId("slash-menu")).not.toBeInTheDocument()
    expect(box()).toHaveValue("keep going ")
    expect(window.__zwaiAndroidBack).toBeUndefined()
  })

  it("still refuses a quoted goal after the command was picked", () => {
    setLocale("en")
    const onSubmit = vi.fn()
    render(<Composer label="Message" sendLabel="Send" slashCommands quotes={["selected context"]} onSubmit={onSubmit} />)
    fireEvent.change(box(), { target: { value: "/goal inspect" } })
    fireEvent.click(screen.getByRole("button", { name: "Send" }))
    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole("alert")).toHaveTextContent("text only")
    expect(box()).toHaveValue("/goal inspect")
  })
})
