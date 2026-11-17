import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { defaultSpeechSettings, type SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { setLocale } from "@/lib/i18n"
import { usePhoneSpeech } from "@/lib/phone-speech"
import { SpeechStatus } from "./speech-status"

vi.mock("@/lib/phone-speech", () => ({ usePhoneSpeech: vi.fn() }))
const settings = defaultSpeechSettings()
const config: SpeechSnapshot = {
  execution: "pc_local",
  input: { ...settings.input, enabled: true, ready: true, model: "recognize", label: "ASR" },
  output: { ...settings.output, enabled: true, ready: true, model: "read", label: "TTS", execution: "gateway", gateway_origin: "https://gateway.test" },
  limits: settings.limits,
}
afterEach(() => { cleanup(); vi.clearAllMocks() })
const show = (speech: SpeechSnapshot, connected = true, standalone = false) => {
  setLocale("en")
  vi.mocked(usePhoneSpeech).mockReturnValue({ epoch: 1, connected, standalone, snapshot: speech, inputReady: connected, outputReady: connected })
  render(<SpeechStatus />)
}

describe("speech status displays each operation's actual execution route", () => {
  it("shows PC recognition and direct Gateway read aloud for a configurable account", () => {
    show(config)
    expect(screen.getByText(/^Voice input:/)).toHaveTextContent("This PC runs the local speech models")
    expect(screen.getByText(/^Read aloud:/)).toHaveTextContent("Phone calls the Gateway directly")
  })

  it("shows Gateway recognition and personal PC read aloud independently", () => {
    show({ ...config, execution: "gateway", gateway_origin: "https://gateway.test", output: { ...config.output, execution: "pc_local", gateway_origin: undefined } })
    expect(screen.getByText(/^Voice input:/)).toHaveTextContent("Phone calls the Gateway directly")
    expect(screen.getByText(/^Read aloud:/)).toHaveTextContent("This PC runs the local speech models")
  })

  it("keeps both standalone operations on the phone's own service", () => {
    show({ ...config, output: { ...config.output, execution: "pc_local", gateway_origin: undefined } }, true, true)
    for (const row of [screen.getByText(/^Voice input:/), screen.getByText(/^Read aloud:/)]) expect(row).toHaveTextContent("This phone uses its own model service")
  })

  it("explains the disconnected personal PC route without disabling Gateway playback", () => {
    show(config, false)
    expect(screen.getByRole("status")).toHaveTextContent("Connect to this PC to use its local speech models.")
  })

  it("retains the legacy global route for an old host's output", () => {
    show({ ...config, execution: "gateway", gateway_origin: "https://gateway.test", output: { ...config.output, execution: undefined, gateway_origin: undefined } })
    for (const row of [screen.getByText(/^Voice input:/), screen.getByText(/^Read aloud:/)]) expect(row).toHaveTextContent("Phone calls the Gateway directly")
  })
})
