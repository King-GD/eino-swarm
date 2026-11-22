import { fireEvent, render, screen } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import type { AudioParameterView } from "@/lib/audio-parameters"
import { GatewayAudioSettings } from "./settings-audio-parameters"

const labels = {
  voice: "Voice", response_format: "Format", stream_format: "Stream", language: "Language",
  speed: "Speed", instructions: "Instructions", prompt: "Prompt",
  follow: "Follow gateway", invalid: "Invalid saved choice", unavailable: "Parameters unavailable",
  max_input_characters: "Characters", max_file_bytes: "MiB",
}
const view: AudioParameterView = {
  version: 1, scope: "test", status: "configured", overrides: {}, parameters: {
    voice: { values: ["calm", "bright"], default: "calm", configurable: true },
    response_format: { values: ["pcm", "unsupported"], default: "pcm", configurable: true },
    stream_format: { values: ["audio", "sse"], default: "audio", configurable: true },
    speed: { min: 0.1, max: 10, default: 2, configurable: true },
    instructions: { max_characters: 100, configurable: true },
  },
}

it("retains invalid preferences, filters unsupported formats and resets only the chosen parameter", () => {
  const change = vi.fn()
  const props = { view, role: "speech" as const, value: { voice: "removed", instructions: "" }, labels, query: "", onChange: change }
  const { rerender } = render(<GatewayAudioSettings {...props} />)
  expect(screen.getByRole("alert")).toHaveTextContent(labels.invalid)
  expect(screen.getByRole("combobox", { name: "Voice" })).toHaveTextContent("removed")
  fireEvent.click(screen.getByRole("combobox", { name: "Format" }))
  expect(screen.queryByRole("option", { name: "unsupported" })).toBeNull()
  fireEvent.click(screen.getByRole("option", { name: /^pcm$/ }))
  // Default and explicit values remain distinct on the request boundary.
  expect(change).toHaveBeenLastCalledWith({ voice: "removed", instructions: "", response_format: "pcm" })
  fireEvent.click(screen.getByRole("combobox", { name: "Stream" }))
  fireEvent.click(screen.getByRole("option", { name: "sse" }))
  expect(change).toHaveBeenLastCalledWith({ voice: "removed", instructions: "", stream_format: "sse" })
  fireEvent.change(screen.getByLabelText("Speed"), { target: { value: "8" } })
  expect(change).toHaveBeenLastCalledWith({ voice: "removed", instructions: "", speed: 8 })
  rerender(<GatewayAudioSettings {...props} value={{ ...props.value, speed: 8 }} />)
  fireEvent.change(screen.getByLabelText("Speed"), { target: { value: "" } })
  expect(change).toHaveBeenLastCalledWith(props.value)
  rerender(<GatewayAudioSettings {...props} />)
  fireEvent.change(screen.getByLabelText("Instructions"), { target: { value: "new instruction" } })
  expect(change).toHaveBeenLastCalledWith({ voice: "removed", instructions: "new instruction" })
  expect(screen.getAllByRole("button", { name: "Follow gateway" })).toHaveLength(1)
  fireEvent.click(screen.getByRole("button", { name: "Follow gateway" }))
  expect(change).toHaveBeenLastCalledWith({ voice: "removed" })
  fireEvent.click(screen.getByRole("combobox", { name: "Voice" }))
  fireEvent.click(screen.getByRole("option", { name: "Follow gateway (calm)" }))
  expect(change).toHaveBeenLastCalledWith({ instructions: "" })
  rerender(<GatewayAudioSettings {...props} view={{ ...view, status: "incompatible", parameters: undefined }} />)
  expect(screen.getByText(labels.unavailable)).toBeVisible()
  expect(screen.getByText("Voice: removed · Instructions:")).toBeVisible()
  expect(screen.queryByRole("combobox")).toBeNull()
  fireEvent.click(screen.getByRole("button", { name: "Follow gateway" }))
  expect(change).toHaveBeenLastCalledWith({})
})

it("does not add a reset row when each chosen value can follow the gateway default itself", () => {
  const props = { view, role: "speech" as const, labels, query: "", onChange: vi.fn() }
  const { rerender } = render(<GatewayAudioSettings {...props} value={{}} />)
  expect(screen.queryByRole("button", { name: "Follow gateway" })).toBeNull()
  rerender(<GatewayAudioSettings {...props} value={{ voice: "bright", stream_format: "sse", speed: 1.5, instructions: "" }} />)
  // Instructions keep their own follow action. Voice, stream and speed do not grow another row.
  expect(screen.getAllByRole("button", { name: "Follow gateway" })).toHaveLength(1)
  expect(screen.getByRole("combobox", { name: "Voice" })).toHaveTextContent("bright")
})

it("clears only a saved value that no longer has a control", () => {
  const onChange = vi.fn()
  const parameters = { ...view.parameters, speed: { min: 1, max: 1, default: 1, configurable: false } }
  render(<GatewayAudioSettings view={{ ...view, parameters }} role="speech" value={{ voice: "bright", speed: 1.2 }} labels={labels} query="" onChange={onChange} />)
  fireEvent.click(screen.getByRole("button", { name: "Follow gateway" }))
  expect(onChange).toHaveBeenCalledWith({ voice: "bright" })
})

it("hides gateway-fixed parameters and limits while permitting recovery from a stale override", () => {
  const parameters = {
    instructions: { max_characters: 100, default: "fixed instructions", configurable: false },
    stream_format: { values: ["audio"], default: "audio", configurable: false },
    speed: { min: 1, max: 1, default: 1, configurable: false },
    max_input_characters: 0, max_file_bytes: 0,
  }
  const onChange = vi.fn()
  const props = { view: { ...view, parameters }, role: "speech" as const, onChange, labels, query: "" }
  const { container, rerender } = render(<GatewayAudioSettings {...props} value={{}} />)
  expect(container).toBeEmptyDOMElement()
  rerender(<GatewayAudioSettings {...props} value={{ speed: 1.2 }} />)
  expect(screen.getByRole("alert")).toHaveTextContent(labels.invalid)
  expect(screen.queryByText("Speed")).toBeNull()
  fireEvent.click(screen.getByRole("button", { name: "Follow gateway" }))
  expect(onChange).toHaveBeenCalledWith({})
})
