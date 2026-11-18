import { useId, type ReactNode } from "react"
import { playableAudio, resolveAudioParameters, type AudioOverrides, type AudioParameterView, type AudioRole } from "../../lib/audio-parameters"

export function AudioParameterControls({ title, view, role, value, onChange, labels }: {
  title: string
  view?: AudioParameterView; role: AudioRole; value: AudioOverrides; onChange: (value: AudioOverrides) => void
  labels: Record<string, string>
}) {
  const id = useId()
  const p = view?.version === 1 && view.status === "configured" ? view.parameters : undefined
  const hasOverrides = Object.keys(value).length > 0
  const details = (children: ReactNode) => <details><summary className="text-xs">{title}</summary>{children}</details>
  if (!p) return hasOverrides ? details(<div className="flex flex-col gap-2">
    <p role="status" className="text-xs text-muted-foreground">{labels.unavailable}</p>
    <p className="text-xs">{labels.invalid}</p><p className="text-xs">{Object.entries(value).map(([key,v]) => `${labels[key] ?? key}: ${String(v).slice(0,128)}`).join(" · ")}</p><button type="button" className="self-start text-xs underline" onClick={() => onChange({})}>{labels.follow}</button>
  </div>) : null
  let valid = true
  try { valid = playableAudio(resolveAudioParameters(view!, value).effective, role) } catch { valid = false }
  if (valid && !hasOverrides && !Object.values(p).some(option => typeof option === "object" && option.configurable)) return null
  const change = (key: keyof AudioOverrides, v: string | number | undefined) => {
    const next = { ...value, [key]: v }; if (v === undefined) delete next[key]; onChange(next)
  }
  return details(<div className="flex flex-col gap-3" data-testid={`audio-parameters-${role}`}>
    {!valid ? <p role="alert" className="text-sm text-destructive">{labels.invalid}</p> : null}
    {(["voice", "response_format", "stream_format", "language"] as const).map(key => {
      const option = p[key]; if (!option?.configurable) return null
      const values = option.values.filter(v => key !== "response_format" || (role === "speech" ? ["mp3", "wav", "pcm"] : ["json", "verbose_json"]).includes(v))
      const invalid = value[key] !== undefined && !values.includes(value[key]!)
      return <label key={key} htmlFor={`${id}-${key}`} className="flex flex-col gap-1 text-sm">{labels[key]}
        <select id={`${id}-${key}`} className="rounded-md border border-input bg-background p-2" value={value[key] === undefined ? "default:" : `value:${value[key]}`} onChange={e => change(key, e.target.value === "default:" ? undefined : e.target.value.slice(6))}>
          <option value="default:">{labels.follow}{option.default !== undefined ? ` (${option.default})` : ""}</option>
          {invalid ? <option value={`value:${value[key]}`}>{value[key]} · {labels.invalid}</option> : null}
          {values.map(v => <option key={v} value={`value:${v}`}>{v}</option>)}
        </select>
      </label>
    })}
    {p.speed?.configurable ? <label htmlFor={`${id}-speed`} className="flex flex-col gap-1 text-sm">{labels.speed} ({p.speed.min}–{p.speed.max})
      <input id={`${id}-speed`} className="rounded-md border border-input bg-background p-2" type="number" step="any" min={p.speed.min} max={p.speed.max} value={value.speed ?? ""} placeholder={`${labels.follow} (${p.speed.default})`} onChange={e => change("speed", e.target.value === "" ? undefined : Number(e.target.value))} />
    </label> : null}
    {(["instructions", "prompt"] as const).map(key => {
      const option = p[key]; if (!option?.configurable) return null
      return <label key={key} htmlFor={`${id}-${key}`} className="flex flex-col gap-1 text-sm">{labels[key]} (≤{option.max_characters})
        <textarea id={`${id}-${key}`} className="rounded-md border border-input bg-background p-2" value={value[key] ?? ""} placeholder={`${labels.follow}${option.default ? ` (${option.default})` : ""}`} onChange={e => change(key, e.target.value)} />
        {value[key] !== undefined ? <button type="button" className="text-left text-xs underline" onClick={() => change(key, undefined)}>{labels.follow}</button> : null}
      </label>
    })}
    {hasOverrides ? <button type="button" className="self-start text-xs underline" onClick={() => onChange({})}>{labels.follow}</button> : null}
  </div>)
}
