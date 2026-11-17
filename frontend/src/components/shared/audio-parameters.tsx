import { useId } from "react"
import { playableAudio, resolveAudioParameters, type AudioOverrides, type AudioParameterView, type AudioRole } from "../../lib/audio-parameters"

export function AudioParameterControls({ view, role, value, onChange, labels }: {
  view?: AudioParameterView; role: AudioRole; value: AudioOverrides; onChange: (value: AudioOverrides) => void
  labels: Record<string, string>
}) {
  const id = useId()
  const p = view?.version === 1 && view.status === "configured" ? view.parameters : undefined
  if (!p) return <div className="flex flex-col gap-2">
    <p role="status" className="text-xs text-muted-foreground">{labels.unavailable}</p>
    {Object.keys(value).length ? <><p className="text-xs">{labels.invalid}</p><p className="text-xs">{Object.entries(value).map(([key,v]) => `${labels[key] ?? key}: ${String(v).slice(0,128)}`).join(" · ")}</p><button type="button" className="self-start text-xs underline" onClick={() => onChange({})}>{labels.follow}</button></> : null}
  </div>
  let valid = true
  try { valid = playableAudio(resolveAudioParameters(view!, value).effective, role) } catch { valid = false }
  const change = (key: keyof AudioOverrides, v: string | number | undefined) => {
    const next = { ...value, [key]: v }; if (v === undefined) delete next[key]; onChange(next)
  }
  return <div className="flex flex-col gap-3" data-testid={`audio-parameters-${role}`}>
    {!valid ? <p role="alert" className="text-sm text-destructive">{labels.invalid}</p> : null}
    {(["voice", "response_format", "stream_format", "language"] as const).map(key => {
      const option = p[key]; if (!option) return null
      const values = option.values.filter(v => key !== "response_format" || (role === "speech" ? ["mp3", "wav", "pcm"] : ["json", "verbose_json"]).includes(v))
      const invalid = value[key] !== undefined && !values.includes(value[key]!)
      return <label key={key} htmlFor={`${id}-${key}`} className="flex flex-col gap-1 text-sm">{labels[key]}
        <select id={`${id}-${key}`} className="rounded-md border border-input bg-background p-2" disabled={!option.configurable} value={value[key] === undefined ? "default:" : `value:${value[key]}`} onChange={e => change(key, e.target.value === "default:" ? undefined : e.target.value.slice(6))}>
          <option value="default:">{labels.follow}{option.default !== undefined ? ` (${option.default})` : ""}</option>
          {invalid ? <option value={`value:${value[key]}`}>{value[key]} · {labels.invalid}</option> : null}
          {values.map(v => <option key={v} value={`value:${v}`}>{v}</option>)}
        </select>
      </label>
    })}
    {p.speed ? <label htmlFor={`${id}-speed`} className="flex flex-col gap-1 text-sm">{labels.speed} ({p.speed.min}–{p.speed.max})
      <input id={`${id}-speed`} className="rounded-md border border-input bg-background p-2" type="number" step="any" min={p.speed.min} max={p.speed.max} disabled={!p.speed.configurable} value={value.speed ?? ""} placeholder={`${labels.follow} (${p.speed.default})`} onChange={e => change("speed", e.target.value === "" ? undefined : Number(e.target.value))} />
    </label> : null}
    {(["instructions", "prompt"] as const).map(key => {
      const option = p[key]; if (!option) return null
      return <label key={key} htmlFor={`${id}-${key}`} className="flex flex-col gap-1 text-sm">{labels[key]} (≤{option.max_characters})
        <textarea id={`${id}-${key}`} className="rounded-md border border-input bg-background p-2" disabled={!option.configurable} value={value[key] ?? ""} placeholder={`${labels.follow}${option.default ? ` (${option.default})` : ""}`} onChange={e => change(key, e.target.value)} />
        {value[key] !== undefined && option.configurable ? <button type="button" className="text-left text-xs underline" onClick={() => change(key, undefined)}>{labels.follow}</button> : null}
      </label>
    })}
    {p.max_input_characters ? <p className="text-xs">{labels.max_input_characters}: {p.max_input_characters}</p> : null}
    {p.max_file_bytes ? <p className="text-xs">{labels.max_file_bytes}: {p.max_file_bytes}</p> : null}
    {Object.keys(value).length ? <button type="button" className="self-start text-xs underline" onClick={() => onChange({})}>{labels.follow}</button> : null}
  </div>
}
