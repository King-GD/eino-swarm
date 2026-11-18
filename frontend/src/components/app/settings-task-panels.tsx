import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useT } from "@/lib/use-t"
import { panelText } from "@/task-panels/messages"
import { Field, SettingsSection } from "./settings-field"

interface PanelSettings {
  enabled: boolean
  fofa_client: string
  max_assets: number
  batch_size: number
  concurrency: number
  timeout_seconds: number
  flush_interval_ms: number
  max_state_bytes: number
  max_update_bytes: number
}
const numbers = ["max_assets", "batch_size", "concurrency", "timeout_seconds", "flush_interval_ms", "max_state_bytes", "max_update_bytes"] as const

export function TaskPanelSettings({ query }: { query: string }) {
  const t = useT()
  const [settings, setSettings] = useState<PanelSettings>()
  const [baseline, setBaseline] = useState<PanelSettings>()
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  useEffect(() => {
    const abort = new AbortController()
    void fetch("/api/task-panels/settings", { signal: abort.signal }).then(async (response) => {
      if (response.status === 404) return
      if (!response.ok) throw new Error(response.statusText)
      const next = await response.json() as PanelSettings
      if (!abort.signal.aborted) { setSettings(next); setBaseline(next) }
    }).catch((reason: unknown) => { if (!abort.signal.aborted) setError(String(reason)) })
    return () => abort.abort()
  }, [])
  const update = (next: PanelSettings) => { setSettings(next); setSaved(false) }
  const edited = settings && baseline ? Object.fromEntries(Object.entries(settings).filter(([key, value]) => value !== baseline[key as keyof PanelSettings])) : {}
  const save = async () => {
    if (!settings || saving || !Object.keys(edited).length) return
    setSaving(true); setError(undefined); setSaved(false)
    try {
      const response = await fetch("/api/task-panels/settings", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(edited) })
      if (!response.ok) {
        const body = await response.json().catch(() => ({})) as { error?: string }
        throw new Error(body.error ?? response.statusText)
      }
      const next = await response.json() as PanelSettings
      setSettings(next); setBaseline(next); setSaved(true)
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
    finally { setSaving(false) }
  }
  if (!settings && !error) return null
  return <SettingsSection title={panelText(t.locale, "settings")}>
    <p className="mb-3 text-xs text-muted-foreground">{panelText(t.locale, "hint")}</p>
    {settings ? <>
      <Field query={query} label={panelText(t.locale, "enabled")}><Switch disabled={saving} aria-label={panelText(t.locale, "enabled")} checked={settings.enabled} onCheckedChange={(enabled) => update({ ...settings, enabled })} /></Field>
      <Field query={query} label={panelText(t.locale, "client")} wide><Input disabled={saving} value={settings.fofa_client} onChange={(event) => update({ ...settings, fofa_client: event.target.value })} /></Field>
      {numbers.map((key) => <Field key={key} query={query} label={panelText(t.locale, key)}><Input disabled={saving} type="number" min={1} value={settings[key]} onChange={(event) => update({ ...settings, [key]: Number(event.target.value) })} /></Field>)}
      <Button type="button" size="sm" disabled={saving || !Object.keys(edited).length} onClick={() => void save()}>{panelText(t.locale, "save")}</Button>
    </> : null}
    {saved ? <p role="status" className="mt-2 text-xs">{panelText(t.locale, "saved")}</p> : null}
    {error ? <p role="alert" className="mt-2 text-xs text-destructive">{error}</p> : null}
  </SettingsSection>
}
