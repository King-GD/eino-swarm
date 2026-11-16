import { useState } from "react"
import { loadLocalSpeech, saveLocalSpeech } from "@/lib/local-speech"
import type { DirectProvider } from "@/lib/direct-provider"
import type { SpeechSettings } from "../../../frontend/src/lib/speech-types"
import { t } from "@/lib/i18n"
import { Input } from "./ui/input"

export function LocalSpeechSettings({ providers }: { providers: DirectProvider[] }) {
  const [settings, setSettings] = useState(loadLocalSpeech)
  const update = (next: SpeechSettings) => { saveLocalSpeech(next); setSettings(next) }
  return <section aria-label={t("speech.title")} className="flex flex-col gap-3 border-t border-border p-4">
    <h3 className="text-sm font-semibold">{t("speech.title")}</h3>
    {(["input", "output"] as const).map(kind => <fieldset key={kind} className="flex flex-col gap-2">
      <legend className="text-sm">{t(kind === "input" ? "speech.input" : "speech.output")}</legend>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={settings[kind].enabled} onChange={e => update({ ...settings, [kind]: { ...settings[kind], enabled: e.target.checked } })} />{t("speech.enabled")}</label>
      <label className="text-sm">{t("speech.provider")}<select className="mt-1 w-full rounded border border-border bg-background p-2" value={settings[kind].provider_id} onChange={e => update({ ...settings, [kind]: { ...settings[kind], provider_id: e.target.value } })}>
        <option value="">{t("speech.chooseProvider")}</option>
        {providers.filter(p => !p.id.startsWith("gateway:")).map(p => <option key={p.id} value={p.id}>{p.label || p.id}</option>)}
      </select></label>
      <label className="text-sm">{t("speech.model")}<Input value={settings[kind].model} onChange={e => update({ ...settings, [kind]: { ...settings[kind], model: e.target.value } })} /></label>
      {kind === "input" ? <label className="text-sm">{t("speech.language")}<Input value={settings.input.language} onChange={e => update({ ...settings, input: { ...settings.input, language: e.target.value } })} /></label>
        : <label className="text-sm">{t("speech.voice")}<Input value={settings.output.voice} onChange={e => update({ ...settings, output: { ...settings.output, voice: e.target.value } })} /></label>}
    </fieldset>)}
  </section>
}
