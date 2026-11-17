import { fireEvent, render, screen } from "@testing-library/react"
import { beforeEach, expect, it, vi } from "vitest"
import type { Settings } from "@/lib/types"
import { useApp } from "@/store/app"
import { FunctionalModels } from "./functional-models"

const settings = (): Settings => ({
  model_policy: { mode: "gateway", configurable: true, ready: true, scope: "gateway:owner", defaults: { title: { provider_id: "gateway:owner", model: "server-title" } } },
  models: { providers: [{ id: "own", label: "Personal", model: "chat", catalog: ["small"] }], bindings: { title: { provider_id: "own", model: "chosen" } } },
} as Settings)
beforeEach(() => useApp.setState({ meta: undefined, models: [] }))

it("keeps explicit role choices and restores following the gateway default", () => {
  const onChange = vi.fn()
  const { rerender } = render(<FunctionalModels settings={settings()} onChange={onChange} />)
  const model = screen.getByLabelText("Conversation title Model")
  fireEvent.change(model, { target: { value: " replacement " } })
  fireEvent.blur(model)
  expect(onChange.mock.lastCall?.[0].models.bindings.title).toEqual({ provider_id: "own", model: "replacement" })
  fireEvent.change(model, { target: { value: " " } })
  fireEvent.blur(model)
  expect(model).toHaveValue("chosen")
  fireEvent.click(screen.getByRole("button", { name: /^Use default$/ }))
  const restored = onChange.mock.lastCall![0] as Settings
  expect(restored.models.bindings).toEqual({})
  rerender(<FunctionalModels settings={restored} onChange={onChange} />)
  expect(screen.queryByLabelText("Conversation title Model")).toBeNull()
  expect(screen.getByText(/server-title/)).toBeTruthy()
})

it("does not offer the background decisions role", () => {
  const value = settings()
  value.model_policy!.defaults = {
    ...value.model_policy!.defaults,
    decisions: { provider_id: "gateway:owner", model: "hidden-decider" },
  }
  render(<FunctionalModels settings={value} onChange={vi.fn()} />)
  expect(screen.queryByLabelText(/Decision assistant/)).toBeNull()
  expect(screen.queryByText("hidden-decider")).toBeNull()
  expect(screen.getByLabelText("Conversation title Provider")).toBeTruthy()
})

it("has no model controls for a managed preset", () => {
  const value = settings()
  value.model_policy!.configurable = false
  const { container } = render(<FunctionalModels settings={value} onChange={vi.fn()} />)
  expect(container).toBeEmptyDOMElement()
})
