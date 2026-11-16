import { defaultSpeechSettings, type SpeechSettings, type SpeechSnapshot } from "../../../frontend/src/lib/speech-types"
import { isPersonalProvider, loadProviders } from "./direct-provider"
import { loadPhoneMode } from "./phone-mode"
import { endpointSpeechCall, SpeechCallError } from "./speech-transport"

const key = "zwai.phone.localSpeech"
export const LOCAL_SPEECH_EVENT = "zwai.phone.localSpeech.changed"

/** Only preferences and provider references are saved here. Credentials remain
 * in this phone's existing personal-provider store. */
export function loadLocalSpeech(): SpeechSettings {
  const value = defaultSpeechSettings()
  try {
    const saved = JSON.parse(localStorage.getItem(key) || "{}") as Partial<SpeechSettings>
    for (const kind of ["input", "output"] as const) {
      const item = saved[kind]
      if (!item || typeof item !== "object") continue
      value[kind].enabled = item.enabled === true
      value[kind].source = "provider"
      value[kind].provider_id = typeof item.provider_id === "string" && !item.provider_id.startsWith("gateway:") ? item.provider_id : ""
      value[kind].model = typeof item.model === "string" ? item.model : ""
    }
    if (typeof saved.input?.language === "string") value.input.language = saved.input.language
    if (typeof saved.output?.voice === "string") value.output.voice = saved.output.voice
  } catch { /* Missing or damaged preferences start disabled. */ }
  return value
}

export function saveLocalSpeech(settings: SpeechSettings): void {
  if (loadPhoneMode() !== "local") throw new SpeechCallError("unavailable")
  const clean = Object.fromEntries((["input", "output"] as const).map(kind => [kind, {
    enabled: settings[kind].enabled, provider_id: settings[kind].provider_id, model: settings[kind].model,
    ...(kind === "input" ? { language: settings.input.language } : { voice: settings.output.voice }),
  }]))
  localStorage.setItem(key, JSON.stringify(clean))
  window.dispatchEvent(new Event(LOCAL_SPEECH_EVENT))
}

export function localSpeechSnapshot(): SpeechSnapshot {
  const settings = loadLocalSpeech(), providers = loadProviders()
  const ready = (kind: "input" | "output") => providers.some(p => isPersonalProvider(p) && p.id === settings[kind].provider_id) && Boolean(settings[kind].model.trim())
  return {
    execution: "pc_local", limits: settings.limits,
    input: { ...settings.input, label: settings.input.model, ready: ready("input") },
    output: { ...settings.output, label: settings.output.model, ready: ready("output") },
  }
}

export async function localPhoneSpeechCall(operation: "transcribe" | "speak", input: Blob | string, signal: AbortSignal): Promise<string | Blob> {
  if (loadPhoneMode() !== "local") throw new SpeechCallError("unavailable")
  const settings = loadLocalSpeech(), kind = operation === "transcribe" ? "input" : "output"
  const endpoint = loadProviders().find(p => p.id === settings[kind].provider_id && isPersonalProvider(p))
  if (!endpoint) throw new SpeechCallError("unavailable")
  return endpointSpeechCall(localSpeechSnapshot(), endpoint.baseURL, endpoint.apiKey, operation, input, signal)
}
