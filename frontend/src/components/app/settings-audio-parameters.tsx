import { useId } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { SelectItem } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { audioOverridesWithoutControl, playableAudio, resolveAudioParameters, type AudioOverrides, type AudioParameterView, type AudioRole } from "@/lib/audio-parameters"
import { chromeTypeClass } from "@/lib/chrome-type"
import { Field, SettingsChoice, SettingsRow } from "./settings-field"

/** Desktop settings use the same rows as local speech; phone controls stay compact. */
export function GatewayAudioSettings({ view, role, value, onChange, labels, query }: {
  view?: AudioParameterView
  role: AudioRole
  value: AudioOverrides
  onChange: (value: AudioOverrides) => void
  labels: Record<string, string>
  query: string
}) {
  const id = useId()
  const parameters = view?.version === 1 && view.status === "configured" ? view.parameters : undefined
  const stuck = audioOverridesWithoutControl(parameters, value)
  const reset = Object.keys(stuck).length > 0 ? <SettingsRow label={labels.follow} query={query}>
    <Button type="button" variant="outline" size="sm" onClick={() => {
      const next: AudioOverrides = { ...value }
      for (const key of Object.keys(stuck) as (keyof AudioOverrides)[]) delete next[key]
      onChange(next)
    }}>{labels.follow}</Button>
  </SettingsRow> : null
  if (!parameters) return Object.keys(stuck).length > 0 ? <>
    <SettingsRow label={labels.unavailable} query={query} />
    <SettingsRow label={labels.invalid} hint={Object.entries(value).map(([key, v]) => `${labels[key] ?? key}: ${String(v).slice(0, 128)}`).join(" · ")} query={query} />
    {reset}
  </> : null
  let valid = true
  try { valid = playableAudio(resolveAudioParameters(view!, value).effective, role) } catch { valid = false }
  const change = (key: keyof AudioOverrides, nextValue: string | number | undefined) => {
    const next = { ...value, [key]: nextValue }
    if (nextValue === undefined) delete next[key]
    onChange(next)
  }
  return <>
    {!valid ? <div role="alert" data-settings-row="" className={`${chromeTypeClass} px-4 py-3 text-destructive`}>{labels.invalid}</div> : null}
    {(["voice", "response_format", "stream_format", "language"] as const).map((key) => {
      const option = parameters[key]
      if (!option?.configurable) return null
      const values = option.values.filter(v => key !== "response_format" || (role === "speech" ? ["mp3", "wav", "pcm"] : ["json", "verbose_json"]).includes(v))
      const invalid = value[key] !== undefined && !values.includes(value[key]!)
      return <SettingsChoice key={key} label={labels[key]} query={query}
        value={value[key] === undefined ? "default:" : `value:${value[key]}`}
        onValueChange={v => change(key, v === "default:" ? undefined : v.slice(6))}>
        <SelectItem value="default:">{labels.follow}{option.default !== undefined ? ` (${option.default})` : ""}</SelectItem>
        {invalid ? <SelectItem value={`value:${value[key]}`}>{value[key]} · {labels.invalid}</SelectItem> : null}
        {values.map(v => <SelectItem key={v} value={`value:${v}`}>{v}</SelectItem>)}
      </SettingsChoice>
    })}
    {parameters.speed?.configurable ? <Field label={labels.speed}
      hint={`${parameters.speed.min}–${parameters.speed.max} · ${labels.follow} (${parameters.speed.default})`} query={query}>
      <Input type="number" step="any" min={parameters.speed.min} max={parameters.speed.max}
        value={value.speed ?? ""} placeholder={String(parameters.speed.default)}
        onChange={e => change("speed", e.target.value === "" ? undefined : Number(e.target.value))} />
    </Field> : null}
    {(["instructions", "prompt"] as const).map(key => {
      const option = parameters[key]
      if (!option?.configurable) return null
      return <SettingsRow key={key} label={labels[key]} htmlFor={`${id}-${key}`} hint={`≤${option.max_characters}`} query={query}
        controlClassName="w-72 max-w-[55%] flex-col items-stretch gap-2">
        <div>
          <Textarea id={`${id}-${key}`} value={value[key] ?? ""} placeholder={`${labels.follow}${option.default ? ` (${option.default})` : ""}`} onChange={e => change(key, e.target.value)} />
          {value[key] !== undefined ? <Button type="button" variant="ghost" size="sm" onClick={() => change(key, undefined)}>{labels.follow}</Button> : null}
        </div>
      </SettingsRow>
    })}
    {reset}
  </>
}
