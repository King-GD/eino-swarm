import { describe, expect, it } from "vitest"
import { capabilityAllows, capabilityConfigurable, parseCapabilities, type SourceCapabilities } from "./capabilities"
import { selectedFeatureAllows } from "./model-policy"

const voice = (): SourceCapabilities => ({ authorized: true, available: true, configurable: false, capabilities: {
  speech: { supported: true, available: true, configurable: false, endpoints: ["audio_speech"] },
} })
describe("negotiated gateway controls", () => {
  it("keeps fixed available features without opening their model selector", () => {
    expect(capabilityAllows(voice(), "speech")).toBe(true)
    expect(capabilityConfigurable(voice(), "speech")).toBe(false)
    expect(capabilityAllows(voice(), "transcription")).toBe(false)
    expect(capabilityAllows(undefined, "speech")).toBe(false)
  })
  it("does not enable an unknown endpoint or an unimplemented image adapter", () => {
    const source = voice()
    source.capabilities.speech!.endpoints = ["private_executor"]
    source.capabilities.image = { supported: true, available: true, configurable: true, endpoints: ["images"] }
    expect(capabilityAllows(source, "speech")).toBe(false)
    expect(capabilityAllows(source, "image")).toBe(false)
  })
  it("refuses unknown versions and malformed permissions while retaining independent LOCAL", () => {
    for (const version of [undefined, 2, "1"]) expect(parseCapabilities({ version, model_roles_version: 3, sources: {} })).toBeUndefined()
    expect(parseCapabilities({ version: 1, model_roles_version: 3, sources: { preset: { ...voice(), authorized: "true" }, gateway: voice() } })).toBeUndefined()
    const manifest = parseCapabilities({ version: 1, model_roles_version: 3, sources: { preset: voice(), gateway: { ...voice(), authorized: false } } })!
    expect(capabilityAllows(manifest.sources.preset, "speech")).toBe(true)
    expect(capabilityAllows(manifest.sources.gateway, "speech")).toBe(false)
    const policy = { mode: "local" as const, configurable: true, ready: true, scope: "local", account_sources: { "gateway:one": manifest.sources.gateway } }
    expect(selectedFeatureAllows(policy, "personal", "speech")).toBe(true)
    expect(selectedFeatureAllows(policy, "gateway:one", "speech")).toBe(false)
  })
})
