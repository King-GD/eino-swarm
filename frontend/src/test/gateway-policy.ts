import type { ModelPolicy } from "@/lib/model-policy"

/** A verified gateway receipt, independent of retained login/profile copy. */
export function gatewayVoicePolicy(configurable = false): ModelPolicy {
  return {
    mode: "gateway", configurable, ready: true, scope: "gateway:test",
    capabilities: {
      authorized: true, available: true, configurable,
      capabilities: {
        transcription: { supported: true, available: true, configurable, endpoints: ["audio_transcriptions"] },
        speech: { supported: true, available: true, configurable, endpoints: ["audio_speech"] },
      },
    },
  }
}
