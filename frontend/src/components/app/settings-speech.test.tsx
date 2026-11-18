import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { useState } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { SpeechSettings } from "./settings-speech"
import { speechPlayback } from "@/lib/speech-media"
import { defaultSpeechSettings } from "@/lib/speech-types"
import type { Settings, Meta } from "@/lib/types"
import { useApp } from "@/store/app"
import { gatewayVoicePolicy } from "@/test/gateway-policy"

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

function setup(mode: "local" | "fofa", onChange = vi.fn(), onFlush = vi.fn(async () => {}), model_policy?: Settings["model_policy"]) {
  useApp.setState({ meta: { product_mode: mode, model_policy } as Meta })
  const speech = defaultSpeechSettings()
  speech.input.has_api_key = true
  function Host() {
    const [settings, setSettings] = useState({ speech, models: { default: "", providers: [] }, model_policy } as unknown as Settings)
    return <SpeechSettings settings={settings} onChange={(next) => { setSettings(next); onChange(next) }} onFlush={onFlush} />
  }
  render(<Host />)
  return { onChange, onFlush }
}

describe("Speech settings", () => {
  it("distinguishes generating from playing in the reading test", () => {
    let snapshot: ReturnType<typeof speechPlayback.getSnapshot> = { key: "settings:speech", state: "loading" }
    let changed: (() => void) | undefined
    vi.spyOn(speechPlayback, "getSnapshot").mockImplementation(() => snapshot)
    vi.spyOn(speechPlayback, "subscribe").mockImplementation((listener) => { changed = listener; return () => {} })
    setup("local")
    expect(screen.getByRole("status")).toHaveTextContent("Generating speech…")
    act(() => { snapshot = { key: "settings:speech", state: "playing" }; changed?.() })
    expect(screen.getByRole("status")).toHaveTextContent("Playing audio")
    act(() => { snapshot = { state: "idle" }; changed?.() })
    expect(screen.queryByRole("status")).toBeNull()
  })

  it("configures local speech independently and preserves a saved secret until explicitly cleared", () => {
    const { onChange } = setup("local")
    fireEvent.change(screen.getAllByLabelText("API base URL")[0], { target: { value: "http://speech.invalid/v1" } })
    fireEvent.change(screen.getAllByLabelText("Model")[0], { target: { value: "recognizer" } })
    fireEvent.click(screen.getAllByLabelText("Enabled")[0])
    const current = onChange.mock.lastCall![0] as Settings
    expect(current.speech?.input).toMatchObject({ enabled: true, source: "custom", base_url: "http://speech.invalid/v1", model: "recognizer" })
    expect(current.speech?.input.api_key).toBeUndefined()
    expect(current.speech?.output.enabled).toBe(false)
    fireEvent.click(screen.getByRole("button", { name: "Clear saved key" }))
    expect(onChange.mock.lastCall![0].speech.input.api_key).toBe("")
  })

  it("keeps the signed-in account voice role when local settings already have a model policy", () => {
    const { onChange } = setup("local", vi.fn(), vi.fn(async () => {}), { ...gatewayVoicePolicy(true), mode: "local", scope: "local" })
    expect(screen.queryByLabelText("API base URL")).toBeNull()
    expect(screen.getAllByText("Not configured or unavailable").length).toBeGreaterThan(0)
    fireEvent.change(screen.getByLabelText("Speech speed"), { target: { value: "1.2" } })
    expect(onChange.mock.lastCall![0].speech.output).toMatchObject({ source: "account", speed: 1.2 })
  })

  it("uses gateway-managed roles without exposing endpoint or credential configuration", () => {
    const { onChange } = setup("fofa", vi.fn(), vi.fn(async () => {}), gatewayVoicePolicy())
    expect(screen.queryByLabelText("API base URL")).toBeNull()
    expect(screen.queryByLabelText("API key")).toBeNull()
    expect(screen.getByRole("heading", { name: "Voice input" })).toBeVisible()
    fireEvent.click(screen.getAllByLabelText("Enabled")[1])
    expect(onChange.mock.lastCall![0].speech.output).toMatchObject({ enabled: true, source: "account" })
    expect(onChange.mock.lastCall![0].speech.input.enabled).toBe(false)
  })

  it("does not infer Gateway voice permission from product mode alone", () => {
    const { onChange } = setup("fofa")
    expect(screen.queryByRole("heading", { name: "Voice input" })).toBeNull()
    expect(screen.queryByLabelText("Enabled")).toBeNull()
    expect(onChange).not.toHaveBeenCalled()
  })

  it("discovers models only after pending configuration writes finish", async () => {
    const order: string[] = []
    const onFlush = vi.fn(async () => { order.push("saved") })
    const fetcher = vi.fn(async (_path: RequestInfo | URL, _init?: RequestInit) => { order.push("discovered"); return new Response(JSON.stringify({ models: ["recognizer", 42] }), { status: 200 }) })
    vi.stubGlobal("fetch", fetcher)
    setup("local", vi.fn(), onFlush)
    fireEvent.click(screen.getAllByRole("button", { name: "Discover models" })[0])
    await waitFor(() => expect(fetcher).toHaveBeenCalledOnce())
    expect(order).toEqual(["saved", "discovered"])
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/audio/models")
    expect(document.querySelectorAll("datalist option")).toHaveLength(1)
  })

  it("cancels discovery when its endpoint changes and ignores the old list", async () => {
    let resolve!: (response: Response) => void
    let signal!: AbortSignal
    vi.stubGlobal("fetch", vi.fn((_path: RequestInfo | URL, init?: RequestInit) => {
      signal = init!.signal as AbortSignal
      return new Promise<Response>((done) => { resolve = done })
    }))
    setup("local")
    fireEvent.click(screen.getAllByRole("button", { name: "Discover models" })[0])
    await waitFor(() => expect(signal).toBeDefined())
    fireEvent.change(screen.getAllByLabelText("API base URL")[0], { target: { value: "http://replacement.invalid/v1" } })
    expect(signal.aborted).toBe(true)
    resolve(new Response(JSON.stringify({ models: ["old model"] }), { status: 200 }))
    await waitFor(() => expect(screen.getAllByRole("button", { name: "Discover models" })[0]).toBeEnabled())
    expect(document.querySelectorAll("datalist option")).toHaveLength(0)
  })
})
