import type { ClientCapabilities, SourceCapabilities } from "../../../frontend/src/lib/capabilities"

/** Explicit current protocol grants for fixtures that exercise authorized calls. */
export function fixtureSourceCapabilities(): SourceCapabilities {
  const role = (endpoint: string) => ({ supported: true, available: true, configurable: true, endpoints: [endpoint] })
  return { authorized: true, available: true, configurable: true, capabilities: {
    conversation: role("chat_completions"), translation: role("chat_completions"),
    speech: role("audio_speech"), transcription: role("audio_transcriptions"),
  } }
}
export function fixtureCapabilities(): ClientCapabilities {
  return { version: 1, model_roles_version: 3, sources: { preset: fixtureSourceCapabilities(), gateway: fixtureSourceCapabilities() } }
}
