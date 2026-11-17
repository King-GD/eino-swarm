import { policyAllows, policyConfigurable } from "@/lib/model-policy"
import { useEffect, useId, useRef, useState, useSyncExternalStore } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { SelectItem } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useApp } from "@/store/app"
import { toastError } from "@/store/toasts"
import { defaultSpeechSettings, type SpeechInputSettings, type SpeechOutputSettings } from "@/lib/speech-types"
import { speechPlayback, speechRecordingSupported, startSpeechRecording, type SpeechRecording } from "@/lib/speech-media"
import { synthesizeSpeech, transcribeSpeech } from "@/lib/speech-api"
import { speechFailureMessage } from "@/lib/speech-error"
import type { Settings } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { Field, SettingsChoice, SettingsRow, SettingsSection, settingsMatch } from "./settings-field"

type Kind = "input" | "output"
type Connection = SpeechInputSettings | SpeechOutputSettings

export const speechSettingsSearch = "speech voice microphone audio asr tts transcription read aloud 语音 麦克风 录音 识别 朗读 音色"

export function SpeechSettings({ settings, onChange, onFlush, query: searchQuery = "" }: {
  settings: Settings; onChange: (settings: Settings) => void; onFlush: () => Promise<void>; query?: string
}) {
  const t = useT()
  const query = settingsMatch(searchQuery, t("speech.title"), speechSettingsSearch) ? "" : searchQuery
  const local = useApp((store) => store.meta?.model_policy ? store.meta.model_policy.configurable : store.meta?.product_mode === "local")
  const mode = useApp((store) => store.meta?.product_mode)
  // Legacy LOCAL settings have no policy, but do have an explicit product
  // mode. An absent Gateway manifest still grants no voice capability.
  const legacyLocal = !settings.model_policy && mode === "local"
  const inputAllowed = legacyLocal || policyAllows(settings.model_policy, "transcription")
  const outputAllowed = legacyLocal || policyAllows(settings.model_policy, "speech")
  const snapshot = useApp((store) => store.meta?.speech)
  const raw = defaultSpeechSettings(settings.speech)
  // The provider/custom form has no account choice. A model-policy page keeps
  // source "account", which is the signed-in recognition or speech role.
  const legacyEndpoint = local && !settings.model_policy
  const speech = legacyEndpoint ? { ...raw,
    input: raw.input.source === "account" ? { ...raw.input, source: "custom" as const } : raw.input,
    output: raw.output.source === "account" ? { ...raw.output, source: "custom" as const } : raw.output,
  } : raw
  const [models, setModels] = useState<Partial<Record<Kind, string[]>>>({})
  const [discovering, setDiscovering] = useState<Kind>()
  const discovery = useRef<AbortController | undefined>(undefined)
  const id = useId()
  const connectionIdentity = JSON.stringify([speech.input.source, speech.input.provider_id, speech.input.base_url,
    speech.output.source, speech.output.provider_id, speech.output.base_url])
  useEffect(() => {
    discovery.current?.abort(); discovery.current = undefined; setDiscovering(undefined); setModels({})
  }, [connectionIdentity])
  useEffect(() => () => discovery.current?.abort(), [])
  if (!inputAllowed && !outputAllowed) return null
  const update = <K extends Kind>(kind: K, patch: Partial<(typeof speech)[K]>) => onChange({
    ...settings, speech: { ...speech, [kind]: { ...speech[kind], ...patch } },
  })
  const discover = async (kind: Kind) => {
    discovery.current?.abort()
    const owner = new AbortController()
    discovery.current = owner
    setDiscovering(kind)
    try {
      await onFlush()
      const response = await fetch("/api/audio/models", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ kind }), signal: owner.signal, redirect: "error" })
      const body = await response.json()
      if (!response.ok) throw new Error(typeof body.error === "string" ? body.error : t("speech.failed"))
      const names = Array.isArray(body.models) ? body.models.filter((name: unknown): name is string => typeof name === "string") : []
      if (!owner.signal.aborted) setModels((current) => ({ ...current, [kind]: names }))
    } catch (error) { if (!owner.signal.aborted) toastError(error instanceof Error ? error.message : t("speech.failed")) }
    finally { if (discovery.current === owner) { discovery.current = undefined; setDiscovering(undefined) } }
  }
  const connection = (kind: Kind, value: Connection) => settings.model_policy ? (
    policyConfigurable(settings.model_policy, kind === "input" ? "transcription" : "speech") ? <SettingsRow label={t("speech.model")} hint={snapshot?.[kind].ready ? (snapshot[kind].label || t("modelPolicy.default")) : t("speech.notReady")} query={query} /> : null
  ) : <>
    {local ? <>
      <SettingsChoice label={t("speech.source")} query={query} value={value.source} onValueChange={(source) => update(kind, { source: source as Connection["source"] })}>
        <SelectItem value="provider">{t("speech.provider")}</SelectItem><SelectItem value="custom">{t("speech.custom")}</SelectItem>
      </SettingsChoice>
      {value.source === "provider" ? <SettingsChoice label={t("speech.providerName")} query={query} value={value.provider_id || undefined} onValueChange={(provider_id) => update(kind, { provider_id })}>
        {settings.models.providers.map((provider) => <SelectItem value={provider.id} key={provider.id}>{provider.label || provider.id}</SelectItem>)}
      </SettingsChoice> : <>
        <Field label={t("speech.endpoint")} query={query} wide><Input value={value.base_url} onChange={(event) => update(kind, { base_url: event.target.value })} /></Field>
        <Field label={t("speech.key")} hint={value.has_api_key && value.api_key === undefined ? t("speech.keySaved") : undefined} query={query} wide>
          <Input type="password" autoComplete="new-password" value={value.api_key ?? ""} onChange={(event) => update(kind, { api_key: event.target.value })} />
        </Field>
        {value.has_api_key ? <SettingsRow label={t("speech.clearKey")} query={query}><Button type="button" variant="outline" size="sm" onClick={() => update(kind, { api_key: "", has_api_key: false })}>{t("speech.clearKey")}</Button></SettingsRow> : null}
      </>}
      <Field label={t("speech.model")} query={query} wide>
        <Input list={`${id}-${kind}-models`} value={value.model} onChange={(event) => update(kind, { model: event.target.value })} />
      </Field>
      <datalist id={`${id}-${kind}-models`}>{(models[kind] ?? []).map((model) => <option key={model} value={model} />)}</datalist>
      <SettingsRow label={t("speech.discover")} query={query}><Button type="button" variant="outline" size="sm" disabled={Boolean(discovering)} onClick={() => void discover(kind)}>{discovering === kind ? "…" : t("speech.discover")}</Button></SettingsRow>
    </> : <SettingsRow label={t("speech.model")} hint={t("speech.gatewayModel")} query={query}>
      <span className="text-sm text-muted-foreground">{snapshot?.[kind].label || t("speech.gateway")}</span>
    </SettingsRow>}
  </>
  return <>
    {inputAllowed ? <SettingsSection title={t("speech.input")} description={t("speech.description")}>
      <Field label={t("speech.enabled")} query={query}><Switch checked={speech.input.enabled} onCheckedChange={(enabled) => update("input", { enabled })} /></Field>
      {connection("input", speech.input)}
      <Field label={t("speech.language")} query={query}><Input value={speech.input.language} placeholder={t("speech.autoLanguage")} onChange={(event) => update("input", { language: event.target.value })} /></Field>
      <Field label={t("speech.prompt")} query={query} wide><Input value={speech.input.prompt} onChange={(event) => update("input", { prompt: event.target.value })} /></Field>
      <SettingsChoice label={t("speech.recordingFormat")} query={query} value={speech.input.recording_format} onValueChange={(recording_format) => update("input", { recording_format: recording_format as "auto" | "wav" })}>
        <SelectItem value="auto">{t("speech.autoFormat")}</SelectItem><SelectItem value="wav">WAV</SelectItem>
      </SettingsChoice>
      <Field label={t("speech.duration")} query={query}><Input type="number" min={1} value={speech.input.max_duration_seconds} onChange={(event) => update("input", { max_duration_seconds: Number(event.target.value) })} /></Field>
      <Field label={t("speech.timeout")} query={query}><Input type="number" min={1} value={speech.input.timeout_seconds} onChange={(event) => update("input", { timeout_seconds: Number(event.target.value) })} /></Field>
    </SettingsSection> : null}
    {outputAllowed ? <SettingsSection title={t("speech.output")}>
      <Field label={t("speech.enabled")} query={query}><Switch checked={speech.output.enabled} onCheckedChange={(enabled) => update("output", { enabled })} /></Field>
      {connection("output", speech.output)}
      <Field label={t("speech.voice")} query={query}><Input value={speech.output.voice} placeholder={t("speech.modelDefault")} onChange={(event) => update("output", { voice: event.target.value })} /></Field>
      <Field label={t("speech.speed")} query={query}><Input type="number" step={0.1} min={0.25} max={4} value={speech.output.speed} onChange={(event) => update("output", { speed: Number(event.target.value) })} /></Field>
      <SettingsChoice label={t("speech.outputFormat")} query={query} value={speech.output.format} onValueChange={(format) => update("output", { format: format as SpeechOutputSettings["format"] })}>
        <SelectItem value="mp3">MP3</SelectItem><SelectItem value="wav">WAV</SelectItem><SelectItem value="pcm">{t("speech.pcmStreaming")}</SelectItem>
      </SettingsChoice>
      <Field label={t("speech.instructions")} query={query} wide><Input value={speech.output.instructions} onChange={(event) => update("output", { instructions: event.target.value })} /></Field>
      <Field label={t("speech.timeout")} query={query}><Input type="number" min={1} value={speech.output.timeout_seconds} onChange={(event) => update("output", { timeout_seconds: Number(event.target.value) })} /></Field>
    </SettingsSection> : null}
    <SettingsSection title={t("speech.limits")}>
      {([
        ["input_bytes", "speech.inputLimit", 1 << 20], ["output_bytes", "speech.outputLimit", 1 << 20],
        ["chunk_chars", "speech.chunkLimit", 1], ["staging_ttl_seconds", "speech.ttl", 1],
      ] as const).map(([key, label, unit]) => <Field key={key} label={t(label)} query={query}><Input type="number" min={1} value={speech.limits[key] / unit} onChange={(event) => onChange({ ...settings, speech: { ...speech, limits: { ...speech.limits, [key]: Number(event.target.value) * unit } } })} /></Field>)}
    </SettingsSection>
    {!query ? <SpeechSettingsTest settings={{ ...settings, speech }} onFlush={onFlush} /> : null}
  </>
}

function SpeechSettingsTest({ settings, onFlush }: { settings: Settings; onFlush: () => Promise<void> }) {
  const t = useT()
  const speech = defaultSpeechSettings(settings.speech)
  const [recording, setRecording] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState("")
  const [text, setText] = useState("")
  const capture = useRef<SpeechRecording | undefined>(undefined)
  const controller = useRef<AbortController | undefined>(undefined)
  const snapshot = useApp((store) => store.meta?.speech)
  const playback = useSyncExternalStore(speechPlayback.subscribe, speechPlayback.getSnapshot)
  const cancel = () => { controller.current?.abort(); capture.current?.cancel(); capture.current = undefined; setRecording(false); setBusy(false) }
  const cancelRef = useRef(cancel); cancelRef.current = cancel
  useEffect(() => () => { cancelRef.current(); if (speechPlayback.getSnapshot().key === "settings:speech") speechPlayback.stop() }, [])
  const finish = async () => {
    const active = capture.current
    const owner = controller.current
    if (!active || !owner) return
    capture.current = undefined; setRecording(false); setBusy(true)
    try {
      const blob = await active.stop()
      await onFlush()
      const recognized = await transcribeSpeech(blob, owner.signal)
      if (!owner.signal.aborted) setResult(recognized)
    } catch (error) {
      if (owner.signal.aborted) return
      const message = speechFailureMessage(error, t)
      if (message) toastError(message)
    }
    finally { if (controller.current === owner) setBusy(false) }
  }
  const start = async () => {
    const owner = new AbortController(); controller.current = owner; setBusy(true)
    try {
      const active = await startSpeechRecording({ signal: owner.signal, format: speech.input.recording_format,
        maxBytes: speech.limits.input_bytes, maxDurationSeconds: speech.input.max_duration_seconds, onLimit: () => { void finish() },
      })
      if (owner.signal.aborted) { active.cancel(); return }
      capture.current = active; setRecording(true)
    } catch (error) {
      if (owner.signal.aborted) return
      const message = speechFailureMessage(error, t)
      if (message) toastError(message)
    }
    finally { if (controller.current === owner) setBusy(false) }
  }
  return <SettingsSection description={t("speech.testHint")} title={t("speech.testReading")}>
    <SettingsRow label={t("speech.testRecording")}>
      <div className="flex gap-2"><Button type="button" variant="outline" size="sm" disabled={!speech.input.enabled || busy || !speechRecordingSupported()} onClick={() => void (recording ? finish() : start())}>{recording ? t("speech.finishRecording") : t("speech.testRecording")}</Button>
        {recording || busy ? <Button type="button" variant="ghost" size="sm" onClick={cancel}>{t("speech.cancel")}</Button> : null}</div>
    </SettingsRow>
    {result ? <Field label={t("speech.testResult")}><Textarea value={result} readOnly /></Field> : null}
    <Field label={t("speech.testText")}><Textarea value={text} onChange={(event) => setText(event.target.value)} /></Field>
    <SettingsRow label={t("speech.testReading")}>
      <Button type="button" variant="outline" size="sm" disabled={!speech.output.enabled || snapshot?.output.ready === false || !text.trim()} onClick={() => {
        if (playback.key === "settings:speech") { speechPlayback.stop(); return }
        void speechPlayback.play("settings:speech", text, { chunkChars: speech.limits.chunk_chars, synthesize: async (part, signal) => {
          await onFlush(); return synthesizeSpeech(part, signal, speech.limits.output_bytes)
        } }).catch((error) => {
          const message = speechFailureMessage(error, t)
          if (message) toastError(message)
        })
      }}>{playback.key === "settings:speech" ? t("speech.stopReading") : t("speech.testReading")}</Button>
      {playback.key === "settings:speech" ? <span role="status" aria-live="polite" className="text-xs text-muted-foreground">{t(playback.state === "loading" ? "speech.generating" : "speech.playing")}</span> : speech.output.enabled && snapshot?.output.ready === false ? <span role="status" className="text-xs text-muted-foreground">{t("speech.notReady")}</span> : null}
    </SettingsRow>
  </SettingsSection>
}
