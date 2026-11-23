import { OpCatalog, OpMeta, OpRefreshCatalog, OpSetProductMode, type RemoteRequest, type RemoteResponse } from "./rpc"

/** Offline fixture state belongs to the PC, so reconnecting its phone does
 * not turn a successfully acknowledged debug switch back into a preset. */
export class MockProductMode {
  private readonly managed = new URLSearchParams(location.search).get("pc-models") === "managed"
  private mode = this.managed ? "fofa" : "local"

  handle(req: Omit<RemoteRequest, "v" | "id">): Partial<RemoteResponse> | undefined {
    if (req.op === OpSetProductMode) {
      if (req.product_mode !== "local" && req.product_mode !== "fofa") return { ok: false, code: "bad_request" }
      const refusal = new URLSearchParams(location.search).get("pc-mode-refusal")
      if (refusal) return { ok: false, code: refusal }
      this.mode = req.product_mode
      return this.policy()
    }
    if (!this.managed) return
    if (req.op === OpMeta) return { ...this.policy(), startup: "ready" }
    if (req.op !== OpCatalog && req.op !== OpRefreshCatalog) return
    if (req.op === OpRefreshCatalog && this.mode !== "local") return { ok: false, code: "custom_models_disabled" }
    const preset = { provider_id: "gateway:preset", provider_label: "Partner", catalog_group: "third_party" as const, model: "shared-model", default: true }
    return { ...this.policy(), models: this.mode === "local" ? [preset,
      { provider_id: "gateway:original", provider_label: "Main", catalog_group: "original", model: "shared-model" },
    ] : [{ ...preset, locked: true }], reasoning_levels: ["none", "low", "medium", "high"] }
  }

  private policy(): Partial<RemoteResponse> {
    const configurable = this.mode === "local"
    const capability = { authorized: true, available: true, configurable, capabilities: {
      conversation: { supported: true, available: true, configurable, endpoints: ["chat_completions"] },
      translation: { supported: true, available: true, configurable, endpoints: ["chat_completions"] },
    } }
    return { product_mode: this.mode as "local" | "fofa", model_policy: {
      mode: this.mode === "local" ? "local" : "gateway", configurable, ready: true, scope: this.mode,
      capability_status: "ready", capabilities: capability,
      account_sources: { "gateway:preset": capability, "gateway:original": capability },
    } }
  }
}
