import { describe, expect, it } from "vitest"
import { functionalChoiceLabel, functionalModelChoices, groupFunctionalChoices } from "./functional-model-choices"
import type { ModelPolicy } from "./model-policy"
import type { ModelInfo } from "./types"

const granted = (endpoints: string[]) => ({
  authorized: true, available: true, configurable: true,
  capabilities: { translation: { supported: true, available: true, configurable: true, endpoints }, embedding: { supported: true, available: true, configurable: true, endpoints: ["embeddings"] } },
})

const policy = (): ModelPolicy => ({
  mode: "local", configurable: true, ready: true, scope: "local",
  account_sources: {
    "gateway:origin": granted(["chat_completions"]),
    "gateway:partner": granted(["chat_completions"]),
  },
})

const models: ModelInfo[] = [
  { id: "o", provider_id: "gateway:origin", provider_label: "Origin", label: "origin-chat", model: "origin-chat", ready: true, source: "account", catalog_group: "original" },
  { id: "p", provider_id: "gateway:partner", provider_label: "Partner", label: "partner-chat", model: "partner-chat", ready: true, source: "account", catalog_group: "third_party" },
  { id: "l", provider_id: "own", provider_label: "Personal", label: "local-chat", model: "local-chat", ready: true, source: "provider" },
]

describe("functional model choices", () => {
  it("groups an authorized gateway, third-party catalog and local provider", () => {
    const choices = functionalModelChoices({ role: "translation", policy: policy(), models, providers: [{ id: "own", label: "Personal", model: "local-chat", catalog: ["local-small"] }], account: undefined })
    expect(groupFunctionalChoices(choices).map(group => [group.group, group.choices.map(choice => choice.model)])).toEqual([
      ["gateway", ["origin-chat"]],
      ["third_party", ["partner-chat"]],
      ["local", ["local-chat", "local-small"]],
    ])
  })

  it("uses the model row when the policy has not copied that source", () => {
    const next = policy()
    delete next.account_sources!["gateway:origin"]
    const withRow = models.map(row => row.provider_id === "gateway:origin" ? { ...row, capabilities: granted(["chat_completions"]) } : row)
    const choices = functionalModelChoices({ role: "translation", policy: next, models: withRow, providers: [], account: undefined })
    expect(choices.map(choice => choice.model)).toContain("origin-chat")
  })

  it("keeps a source out of a role it has not authorized", () => {
    const next = policy()
    next.account_sources!["gateway:origin"] = { authorized: true, available: true, configurable: true, capabilities: {} }
    const choices = functionalModelChoices({ role: "translation", policy: next, models, providers: [], account: undefined })
    expect(choices.map(choice => choice.model)).toEqual(["partner-chat", "local-chat"])
  })

  it("lists embedding kind names instead of account chat models", () => {
    const choices = functionalModelChoices({
      role: "embedding", policy: policy(), models, providers: [{ id: "own", label: "Personal", model: "local-embed" }],
      account: { state: "ready", models: 1, source_id: "gateway:partner", label: "Partner", kinds: { chat: 1, embedding: 1, ocr: 0, decisions: 0, other: 0, unclassified: 0, embedding_models: ["partner-embed"] } },
    })
    expect(choices.map(choice => [choice.group, choice.model])).toEqual([
      ["third_party", "partner-embed"],
      ["local", "local-embed"],
    ])
    expect(choices.some(choice => choice.model === "origin-chat" || choice.model === "local-chat")).toBe(false)
  })

  it("lists typed voice names from the catalog that published them", () => {
    const next = policy()
    next.account_sources!["gateway:origin"] = {
      authorized: true, available: true, configurable: true,
      capabilities: {
        transcription: { supported: true, available: true, configurable: true, endpoints: ["audio_transcriptions"] },
        speech: { supported: true, available: true, configurable: true, endpoints: ["audio_speech"] },
      },
    }
    next.functional_catalogs = [
      { provider_id: "gateway:partner", label: "Partner", catalog_group: "third_party", transcription_models: ["preset-recognizer"] },
      { provider_id: "gateway:origin", label: "Origin", catalog_group: "original", transcription_models: ["recognizer"], speech_models: ["speaker"] },
    ]
    const choices = functionalModelChoices({
      role: "transcription", policy: next, models, providers: [{ id: "own", label: "Personal", model: "local-chat" }],
      account: { state: "ready", models: 1, source_id: "gateway:partner", label: "Partner", kinds: { chat: 1, embedding: 0, ocr: 0, decisions: 0, other: 0, unclassified: 0, transcription_models: ["stale-recognizer"] } },
    })
    expect(choices.map(choice => [choice.group, choice.model])).toEqual([
      ["gateway", "recognizer"],
      ["local", "local-chat"],
    ])
    expect(functionalModelChoices({ role: "speech", policy: next, models, providers: [] }).map(choice => choice.model)).toEqual(["speaker"])
  })

  it("does not borrow the single account kind list once catalogs are published", () => {
    const next = policy()
    next.account_sources!["gateway:partner"] = {
      authorized: true, available: true, configurable: true,
      capabilities: { transcription: { supported: true, available: true, configurable: true, endpoints: ["audio_transcriptions"] } },
    }
    next.functional_catalogs = []
    const choices = functionalModelChoices({
      role: "transcription", policy: next, models, providers: [],
      account: { state: "ready", models: 1, source_id: "gateway:partner", label: "Partner", kinds: { chat: 1, embedding: 0, ocr: 0, decisions: 0, other: 0, unclassified: 0, transcription_models: ["stale-recognizer"] } },
    })
    expect(choices.map(choice => choice.model)).toEqual([])
  })

  it("offers image the same authorized chat catalogs and keeps a saved name", () => {
    const choices = functionalModelChoices({
      role: "image", policy: policy(), models, providers: [],
      selected: { provider_id: "own", model: "kept" },
    })
    expect(choices.map(choice => choice.model)).toEqual(["origin-chat", "partner-chat", "local-chat", "kept"])
    const local = groupFunctionalChoices(choices).find(group => group.group === "local")!.choices
    expect(functionalChoiceLabel(local[0], local)).toBe("local-chat")
  })
})
