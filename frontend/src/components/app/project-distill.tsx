import { ChevronDown } from "lucide-react"
import { useEffect, useState } from "react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { api } from "@/lib/api"
import type { DistillScope } from "@/lib/distillation"
import { useT } from "@/lib/use-t"
import { useApp } from "@/store/app"
import { useDistillation } from "@/store/distillation"
import { useKnowledge } from "@/store/knowledge"
import { useTraceFocus } from "@/store/trace-focus"

export interface ProjectDistillProps {
  projectId: string
  threadId?: string
  knowledgeEnabled?: boolean
}

const scopes = [
  ["latest_turn", "knowledge.distillLatest"],
  ["thread", "knowledge.distillTask"],
  ["project", "knowledge.distillProject"],
] as const

export function ProjectDistillMenu({ projectId, threadId, knowledgeEnabled }: ProjectDistillProps) {
  const t = useT()
  const slot = useDistillation((s) => s.projects[projectId])
  const busy = slot?.starting || slot?.result?.state === "running"
  const refreshKey = useKnowledge((s) => s.refreshByProject[projectId])
  const [knowledge, setKnowledge] = useState<{ projectId: string; enabled: boolean }>()
  const enabled = knowledgeEnabled ?? (knowledge?.projectId === projectId ? knowledge.enabled : undefined)

  useEffect(() => {
    if (knowledgeEnabled !== undefined) return
    const controller = new AbortController()
    void api.knowledgeStatus(projectId, controller.signal).then((status) => {
      if (!controller.signal.aborted) setKnowledge({ projectId, enabled: status.enabled })
    }, () => undefined)
    return () => controller.abort()
  }, [projectId, knowledgeEnabled, refreshKey])

  const start = (scope: DistillScope, enableKnowledge: boolean) => {
    void useDistillation.getState().start(projectId, {
      scope,
      ...(scope === "project" ? {} : { thread_id: threadId }),
      ...(enableKnowledge ? { enable_knowledge: true } : {}),
    })
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button data-testid="knowledge-distill" variant="outline" size="sm" disabled={busy} aria-busy={busy}>
          {t("knowledge.distill")}<ChevronDown aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {scopes.map(([scope, key]) => (
          <DropdownMenuItem key={scope} disabled={scope !== "project" && !threadId} onSelect={() => start(scope, false)}>{t(key)}</DropdownMenuItem>
        ))}
        {enabled === false ? <>
          <DropdownMenuSeparator />
          <DropdownMenuLabel>{t("knowledge.enableAndDistill")}</DropdownMenuLabel>
          {scopes.map(([scope, key]) => (
            <DropdownMenuItem
              key={scope}
              aria-label={`${t("knowledge.enableAndDistill")} · ${t(key)}`}
              disabled={scope !== "project" && !threadId}
              onSelect={() => start(scope, true)}
            >{t(key)}</DropdownMenuItem>
          ))}
        </> : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export async function openProjectReviewTrace(threadId: string, turnId: string) {
  if (useApp.getState().activeId !== threadId) await useApp.getState().openThread(threadId)
  if (useApp.getState().activeId !== threadId) return
  if (!await useApp.getState().loadUntilTurn(turnId)) return
  if (useApp.getState().activeId === threadId) useTraceFocus.getState().open(turnId)
}

export function ProjectDistillStatus({ projectId }: { projectId: string }) {
  const t = useT()
  const slot = useDistillation((s) => s.projects[projectId])
  const result = slot?.starting ? undefined : slot?.result
  const running = result?.state === "running"
  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      await useDistillation.getState().refresh(projectId, signal)
      if (!signal.aborted && running) timer = setTimeout(() => void poll(), 800)
    }
    void poll()
    return () => { controller.abort(); clearTimeout(timer) }
  }, [projectId, running])

  const state = slot?.starting ? "running" : result?.state
  const changes = result?.changes ?? []
  const pages = result?.knowledge ?? []
  const trace = result?.thread_id && result.turn_id ? { thread: result.thread_id, turn: result.turn_id } : undefined
  const error = slot?.error ?? result?.error
  const historyOmitted = result?.history_omitted
  const hasChanges = changes.length + pages.length > 0
  const status = state === "completed" && !hasChanges
    ? t("knowledge.distillNoChanges")
    : state && state !== "idle" ? t(`knowledge.distillState.${state}`) : undefined
  if ((!state || state === "idle") && !error) return null
  return (
    <div className="space-y-1 text-xs text-muted-foreground" data-testid="project-distill-status">
      <div className="flex flex-wrap items-center gap-x-2">
        <p role="status">{status}</p>
        {trace ? (
          <Button variant="link" size="sm" className="h-auto p-0" onClick={() => void openProjectReviewTrace(trace.thread, trace.turn)}>
            {t("knowledge.trace")}
          </Button>
        ) : null}
      </div>
      {historyOmitted ? <p>{t("knowledge.distillOmitted")}</p> : null}
      {result?.uncertain ? <p>{t("knowledge.distillUncertain")}</p> : null}
      {error ? <p role="alert" className="text-destructive">{error}</p> : null}
      {hasChanges ? (
        <details>
          <summary>{t("knowledge.distillChanges", {
            notes: changes.filter((change) => change.target === "memory").length,
            skills: changes.filter((change) => change.target === "skill_manage").length,
            pages: pages.length,
          })}</summary>
          <ul className="list-disc break-words pl-4">
            {changes.map((change, i) => <li key={i}>{change.name ?? change.text}</li>)}
            {pages.map((page) => <li key={page.revision}>{page.title ?? t("knowledge.distillSchema")}</li>)}
          </ul>
        </details>
      ) : null}
    </div>
  )
}
