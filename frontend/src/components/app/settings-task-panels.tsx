import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useT } from "@/lib/use-t"
import { panelText } from "@/task-panels/messages"
import { Field, SettingsSection } from "./settings-field"

interface PanelSettings {
  enabled: boolean
  max_state_bytes: number
  max_update_bytes: number
}
const limits = ["max_state_bytes", "max_update_bytes"] as const
const editableKeys = ["enabled", ...limits] as const

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
  const edited = settings && baseline
    ? Object.fromEntries(editableKeys.filter((key) => settings[key] !== baseline[key]).map((key) => [key, settings[key]]))
    : {}
  const hasEdits = Object.keys(edited).length > 0
  const save = async () => {
    if (saving || !hasEdits) return
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
  return <SettingsSection
    title={panelText(t.locale, "settings")}
    action={hasEdits ? <Button type="button" size="sm" disabled={saving} onClick={() => void save()}>{panelText(t.locale, "save")}</Button> : undefined}
  >
    {settings ? <>
      <Field query={query} label={panelText(t.locale, "enabled")}><Switch disabled={saving} aria-label={panelText(t.locale, "enabled")} checked={settings.enabled} onCheckedChange={(enabled) => update({ ...settings, enabled })} /></Field>
      {limits.map((key) => (
        <Field key={key} query={query} label={panelText(t.locale, key)} controlClassName="[&_input[type=number]]:w-32">
          <Input disabled={saving} type="number" min={1} value={settings[key]} onChange={(event) => update({ ...settings, [key]: Number(event.target.value) })} />
        </Field>
      ))}
    </> : null}
    {saved ? <p role="status" className="px-4 py-3 text-xs">{panelText(t.locale, "saved")}</p> : null}
    {error ? <p role="alert" className="px-4 py-3 text-xs text-destructive">{error}</p> : null}
  </SettingsSection>
}
