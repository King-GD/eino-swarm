import { useCallback, useEffect, useState } from "react"

import { TaskPanelSettings } from "./settings-task-panels"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { SelectItem } from "@/components/ui/select"
import { chromeTypeClass } from "@/lib/chrome-type"
import { catalogToolOn, withCatalogTool } from "@/lib/tool-toggle"
import type { Settings, ToolDescriptor } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useT, type Translate } from "@/lib/use-t"
import { MCPTab, mcpSettingsQueryMatches } from "./settings-mcp"

import {
  Field,
  SettingsPage,
  SettingsChoice,
  SettingsSection,
  settingsHintClass,
  settingsLabelClass,
  settingsMatch,
  settingsRowClass,
} from "./settings-field"

export function ToolsTab({
  settings,
  catalog,
  onChange,
  query = "",
  embedded = false,
}: {
  settings: Settings
  catalog: ToolDescriptor[]
  onChange: (s: Settings) => void
  query?: string
  /** The tools page supplies the title and the kind tabs. */
  embedded?: boolean
}) {
  const tr = useT()
  // The config records exceptions rather than the whole list, so a tool added
  // in a later release keeps its own default instead of silently arriving off.
  const isOn = (t: ToolDescriptor) => catalogToolOn(t, settings.tools)

  const toggle = (t: ToolDescriptor, on: boolean) => {
    onChange({ ...settings, tools: withCatalogTool(t, on, settings.tools) })
  }

  const groups = [...new Set(catalog.map((t) => t.group))]

  const page = (
    <>
      <SettingsSection title={tr("settings.tools.execution")}>
        <SettingsChoice
          query={query}
          label={tr("settings.tools.executionLocation")}
          hint={tr("settings.tools.executionHint")}
          search={["web_search", "web_fetch", "execution", "cloud", "local"]}
          value={settings.tools.web_execution ?? "auto"}
          onValueChange={(value) => onChange({
            ...settings,
            tools: { ...settings.tools, web_execution: value === "local" || value === "cloud" ? value : "auto" },
          })}
        >
          <SelectItem value="auto">{tr("settings.tools.executionAuto")}</SelectItem>
          <SelectItem value="local">{tr("settings.tools.executionLocal")}</SelectItem>
          <SelectItem value="cloud">{tr("settings.tools.executionCloud")}</SelectItem>
        </SettingsChoice>
      </SettingsSection>
      {groups.map((group) => (
        <SettingsSection key={group} title={group.charAt(0).toUpperCase() + group.slice(1)}>
          {catalog
            .filter((t) => t.group === group)
            .filter((t) =>
              settingsMatch(query, t.name, t.title, t.summary, t.group),
            )
            .map((t) => (
              <label
                key={t.name}
                data-settings-row=""
                className={cn(
                  settingsRowClass,
                  "cursor-pointer hover:bg-accent/40",
                )}
              >
                <div className="min-w-0 flex-1">
                  <p className={settingsLabelClass}>
                    <span className="font-mono">{t.name}</span>
                    <span className="text-muted-foreground"> — {t.title}</span>
                  </p>
                  <p className={settingsHintClass}>{t.summary}</p>
                </div>
                <Switch
                  checked={isOn(t)}
                  onCheckedChange={(on) => toggle(t, on)}
                  className="mt-0.5"
                />
              </label>
            ))}
        </SettingsSection>
      ))}

      {settings.tools.web_execution === "cloud" ? null : (
      <SettingsSection
        title={tr("settings.tools.proxy")}
        description={tr("settings.tools.proxyDesc")}
      >
        <Field query={query} label={tr("settings.tools.http")} wide>
          <Input
            value={settings.tools.proxy.http}
            placeholder="http://127.0.0.1:7890"
            onChange={(e) =>
              onChange({
                ...settings,
                tools: {
                  ...settings.tools,
                  proxy: { ...settings.tools.proxy, http: e.target.value },
                },
              })
            }
          />
        </Field>
        <Field query={query} label={tr("settings.tools.https")} wide>
          <Input
            value={settings.tools.proxy.https}
            onChange={(e) =>
              onChange({
                ...settings,
                tools: {
                  ...settings.tools,
                  proxy: { ...settings.tools.proxy, https: e.target.value },
                },
              })
            }
          />
        </Field>
        <Field
          query={query}
          label={tr("settings.tools.noProxy")}
          hint={tr("settings.tools.noProxyHint")}
          wide
        >
          <Input
            value={settings.tools.proxy.no_proxy}
            placeholder="localhost,127.0.0.1"
            onChange={(e) =>
              onChange({
                ...settings,
                tools: {
                  ...settings.tools,
                  proxy: { ...settings.tools.proxy, no_proxy: e.target.value },
                },
              })
            }
          />
        </Field>
      </SettingsSection>
      )}

      <TaskPanelSettings query={query} />
      <SettingsSection title={tr("settings.tools.limits")}>
        <Field
          query={query}
          label={tr("settings.tools.resultMax")}
          hint={tr("settings.tools.resultMaxHint")}
        >
          <Input
            type="number"
            min={1000}
            value={settings.tools.result_max_chars ?? 40000}
            onChange={(e) =>
              onChange({
                ...settings,
                tools: { ...settings.tools, result_max_chars: Number(e.target.value) },
              })
            }
          />
        </Field>
      </SettingsSection>
    </>
  )
  if (embedded) return <div className="flex flex-col gap-8">{page}</div>
  return (
    <SettingsPage
      title={tr("settings.tools.title")}
      description={tr("settings.tools.desc")}
    >
      {page}
    </SettingsPage>
  )
}

function builtinToolsMatch(query: string, settings: Settings, catalog: ToolDescriptor[], t: Translate) {
  if (!query.trim()) return true
  const parts = [
    t("settings.tools.title"),
    t("settings.tools.desc"),
    t("settings.tools.builtin"),
    t("settings.tools.execution"),
    t("settings.tools.executionLocation"),
    t("settings.tools.executionHint"),
    t("settings.tools.executionAuto"),
    t("settings.tools.executionLocal"),
    t("settings.tools.executionCloud"),
    "web_search",
    "web_fetch",
    "execution",
    "cloud",
    "local",
    t("settings.tools.limits"),
    t("settings.tools.resultMax"),
    t("settings.tools.resultMaxHint"),
  ]
  if (settings.tools.web_execution !== "cloud") {
    parts.push(
      t("settings.tools.proxy"),
      t("settings.tools.proxyDesc"),
      t("settings.tools.http"),
      t("settings.tools.https"),
      t("settings.tools.noProxy"),
      t("settings.tools.noProxyHint"),
    )
  }
  if (settingsMatch(query, ...parts)) return true
  return catalog.some((tool) => settingsMatch(query, tool.name, tool.title, tool.summary, tool.group))
}

/** Built-in tools and MCP connections stay on one settings page, on separate
 *  tabs. Stacking both lists made the page a single long scroll. */
export function ToolsSettings({
  settings,
  catalog,
  onChange,
  query = "",
}: {
  settings: Settings
  catalog: ToolDescriptor[]
  onChange: (s: Settings) => void
  query?: string
}) {
  const t = useT()
  const [picked, setPicked] = useState<"builtin" | "mcp">("builtin")
  const [namedMCP, setNamedMCP] = useState(false)
  const reportMCP = useCallback((visible: boolean) => {
    setNamedMCP((current) => current === visible ? current : visible)
  }, [])
  const queryOn = query.trim() !== ""
  const builtinHit = builtinToolsMatch(query, settings, catalog, t)
  const mcpHit = mcpSettingsQueryMatches(query, t) || namedMCP
  const showBuiltin = !queryOn || builtinHit || !mcpHit
  const showMCP = !queryOn || mcpHit
  let tab = picked
  if (!showBuiltin && showMCP) tab = "mcp"
  else if (!showMCP && showBuiltin) tab = "builtin"

  useEffect(() => {
    if (showMCP && !showBuiltin) setPicked("mcp")
    else if (showBuiltin && !showMCP) setPicked("builtin")
  }, [showBuiltin, showMCP])

  const kinds = [
    showBuiltin ? { id: "builtin" as const, label: t("settings.tools.builtin") } : null,
    showMCP ? { id: "mcp" as const, label: t("settings.tools.mcp") } : null,
  ].filter((kind): kind is { id: "builtin" | "mcp"; label: string } => kind !== null)

  return (
    <MCPTab query={query} onMatch={reportMCP}>
      {({ connections, json }) => json ?? (
        <div className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-12 py-8">
          <div className="flex flex-col gap-3">
            <h2 className="text-2xl font-semibold tracking-tight text-foreground">
              {t("settings.tools.title")}
            </h2>
            <div
              role="tablist"
              aria-label={t("settings.tools.title")}
              className="flex flex-wrap items-center gap-1"
              onKeyDown={(event) => {
                if (event.key !== "ArrowRight" && event.key !== "ArrowLeft") return
                if (kinds.length < 2) return
                event.preventDefault()
                const index = Math.max(0, kinds.findIndex((kind) => kind.id === tab))
                const step = event.key === "ArrowRight" ? 1 : -1
                setPicked(kinds[(index + step + kinds.length) % kinds.length].id)
              }}
            >
              {kinds.map((kind) => {
                const selected = tab === kind.id
                return (
                  <button
                    key={kind.id}
                    type="button"
                    role="tab"
                    aria-selected={selected}
                    className={cn(
                      chromeTypeClass,
                      "rounded-lg px-3 py-1.5",
                      selected
                        ? "bg-muted font-medium text-foreground"
                        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                    )}
                    onClick={() => setPicked(kind.id)}
                  >
                    {kind.label}
                  </button>
                )
              })}
            </div>
            <p className={cn(chromeTypeClass, "text-muted-foreground")}>
              {tab === "mcp" ? t("mcp.description") : t("settings.tools.desc")}
            </p>
          </div>
          {tab === "mcp" ? connections : (
            <ToolsTab settings={settings} catalog={catalog} onChange={onChange} query={query} embedded />
          )}
        </div>
      )}
    </MCPTab>
  )
}
