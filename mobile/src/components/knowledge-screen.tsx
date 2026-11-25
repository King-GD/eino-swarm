import * as Dialog from "@radix-ui/react-dialog"
import { BookOpen, ChevronLeft, ChevronRight } from "lucide-react"
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react"
import {
  backKnowledgePage, groupKnowledgeConnections, knowledgeSourceURL, pushKnowledgePage,
  type KnowledgePage, type KnowledgePageRef, type KnowledgeSource,
} from "../../../frontend/src/lib/knowledge"
import { PhoneMarkdown } from "@/components/markdown"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { installAndroidBack } from "@/lib/android-back"
import { t } from "@/lib/i18n"
import type { RemoteLink } from "@/lib/link"
import { KnowledgeReadError, readKnowledgeList, readKnowledgePage } from "@/lib/remote-knowledge"

type ListState = { query: string; enabled: boolean; pages: KnowledgePageRef[]; next?: number }

export function KnowledgeButton({ onClick, project }: { onClick: () => void; project?: string }) {
  const label = project ? t("knowledge.openForProject", { project }) : t("knowledge.title")
  return <Button type="button" variant="ghost" className="size-9 shrink-0 px-0"
    onClick={onClick} aria-label={label} title={label}>
    <BookOpen className="size-4" aria-hidden />
  </Button>
}

export function KnowledgeScreen({ link, projectId, projectName, onClose, onSource }: {
  link: RemoteLink
  projectId: string
  projectName?: string
  onClose: () => void
  onSource: (threadId: string) => void
}) {
  const [query, setQuery] = useState("")
  const [offset, setOffset] = useState(0)
  const [retry, setRetry] = useState(0)
  const [list, setList] = useState<ListState>()
  const [listLoading, setListLoading] = useState(true)
  const [listError, setListError] = useState<unknown>()
  const [stack, setStack] = useState<readonly string[]>([])
  const pageId = stack.at(-1)
  const [page, setPage] = useState<KnowledgePage>()
  const [pageError, setPageError] = useState<{ id: string; error: unknown }>()
  const content = useRef<HTMLDivElement>(null)
  const listTop = useRef(0)
  const backRef = useRef<() => void>(() => undefined)
  const back = () => {
    if (pageId) setStack(backKnowledgePage)
    else onClose()
  }
  backRef.current = back

  useEffect(() => installAndroidBack(() => { backRef.current(); return true }), [])
  useLayoutEffect(() => {
    if (content.current) content.current.scrollTop = pageId ? 0 : listTop.current
  }, [pageId])

  useEffect(() => {
    if (pageId) return
    const controller = new AbortController()
    setListLoading(true)
    setListError(undefined)
    const timer = window.setTimeout(() => {
      void readKnowledgeList(link, projectId, query.trim(), offset, controller.signal).then((result) => {
        if (controller.signal.aborted) return
        setList((previous) => {
          const entries = offset && previous?.query === query ? previous.pages : []
          const pages = new Map([...entries, ...result.pages].map((row) => [row.id, row]))
          return { query, enabled: result.enabled, pages: [...pages.values()], next: result.next_offset }
        })
      }).catch((error: unknown) => {
        if (!controller.signal.aborted) setListError(error)
      }).finally(() => {
        if (!controller.signal.aborted) setListLoading(false)
      })
    }, query && !offset ? 180 : 0)
    return () => { controller.abort(); window.clearTimeout(timer) }
  }, [link, projectId, query, offset, retry, pageId])

  useEffect(() => {
    if (!pageId) return
    const controller = new AbortController()
    setPage(undefined)
    setPageError(undefined)
    void readKnowledgePage(link, projectId, pageId, controller.signal).then((result) => {
      if (!controller.signal.aborted) setPage(result)
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setPageError({ id: pageId, error })
    })
    return () => controller.abort()
  }, [link, projectId, pageId, retry])

  function openPage(id: string) {
    if (!pageId) listTop.current = content.current?.scrollTop ?? 0
    setStack((previous) => pushKnowledgePage(previous, id))
  }

  const currentList = list?.query === query ? list : undefined
  const currentPage = page?.id === pageId ? page : undefined
  const tryAgain = () => setRetry((value) => value + 1)
  return <Dialog.Root open onOpenChange={(open) => { if (!open) onClose() }}>
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-50 bg-background" />
      <Dialog.Content className="fixed inset-0 z-50 mx-auto flex min-w-0 max-w-lg flex-col bg-background text-foreground pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)]"
        onEscapeKeyDown={(event) => { if (pageId) { event.preventDefault(); back() } }}>
        <header className="flex min-h-12 shrink-0 items-center gap-2 border-b border-border px-2">
          <Button type="button" variant="ghost" className="size-10 shrink-0 px-0" onClick={back} aria-label={t("thread.back")}>
            <ChevronLeft className="size-5" aria-hidden />
          </Button>
          <div className="min-w-0 flex-1">
            <Dialog.Title className="truncate text-sm font-semibold">{t("knowledge.title")}</Dialog.Title>
            <Dialog.Description className={projectName ? "truncate text-xs text-muted-foreground" : "sr-only"}>
              {projectName || t("knowledge.title")}
            </Dialog.Description>
          </div>
        </header>
        {!pageId && currentList?.enabled !== false ? <div className="shrink-0 border-b border-border p-3">
          <Input aria-label={t("knowledge.search")} placeholder={t("knowledge.search")} maxLength={120}
            type="search" value={query} onChange={(event) => { setQuery(event.target.value); setOffset(0); listTop.current = 0 }} />
        </div> : null}
        <div ref={content} className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-3">
          {pageId ? (
            pageError?.id === pageId ? <KnowledgeError error={pageError.error} onRetry={tryAgain} />
            : currentPage ? <KnowledgeDocument page={currentPage} onPage={openPage} onSource={onSource} />
            : <p role="status" className="p-3 text-sm text-muted-foreground">{t("knowledge.loadingPage")}</p>
          ) : <>
            {listError !== undefined ? <KnowledgeError error={listError} onRetry={tryAgain} /> : null}
            {listLoading ? <p role="status" className="p-3 text-sm text-muted-foreground">{t("knowledge.loading")}</p> : null}
            {!listLoading && listError === undefined && !currentList?.pages.length ? <p role="status" className="p-3 text-sm text-muted-foreground">
              {currentList?.enabled === false ? t("knowledge.inactive") : query.trim() ? t("knowledge.noMatch") : t("knowledge.empty")}
            </p> : null}
            {currentList?.pages.length ? <ul className="divide-y divide-border rounded-xl border border-border">
              {currentList.pages.map((row) => <li key={row.id}>
                <button type="button" className="flex min-h-12 w-full items-center gap-3 px-3 py-2 text-left"
                  onClick={() => openPage(row.id)}>
                  <span className="min-w-0 flex-1">
                    <span className="block wrap-break-word text-sm font-medium">{row.title}</span>
                    <span className="block text-xs text-muted-foreground">{row.type}</span>
                  </span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                </button>
              </li>)}
            </ul> : null}
            {currentList?.next && listError === undefined ? <Button className="mt-3 w-full" variant="outline" disabled={listLoading}
              onClick={() => setOffset(currentList.next!)}>{t("home.more")}</Button> : null}
          </>}
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}

function KnowledgeError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const code = error instanceof KnowledgeReadError ? error.code : ""
  const message = code === "unknown_op" ? "knowledge.updatePC"
    : code === "knowledge_disabled" ? "knowledge.disabled"
    : code === "knowledge_page_not_found" ? "knowledge.missing"
    : code === "too_large" ? "knowledge.tooLarge" : "knowledge.failed"
  return <div role="alert" className="space-y-2 p-3 text-sm">
    <p>{t(message)}</p>
    {message === "knowledge.failed" ? <Button variant="outline" onClick={onRetry}>{t("files.retry")}</Button> : null}
  </div>
}

function KnowledgeDocument({ page, onPage, onSource }: {
  page: KnowledgePage
  onPage: (id: string) => void
  onSource: (id: string) => void
}) {
  const relations = [...groupKnowledgeConnections(page.outgoing ?? []).values(),
    ...groupKnowledgeConnections(page.incoming ?? []).values()]
  const quotes = new Map<string, Set<string>>()
  for (const observation of page.wiki_observations ?? []) {
    const key = sourceKey(observation.source)
    if (!quotes.has(key)) quotes.set(key, new Set())
    quotes.get(key)?.add(observation.quote)
  }
  const fields = Object.entries(page.fields ?? {})
  return <article className="space-y-4 px-1 pb-4">
    <header>
      <h2 className="wrap-break-word text-lg font-semibold">{page.title}</h2>
      <p className="text-xs text-muted-foreground">{page.type}</p>
    </header>
    {page.wiki?.document_pending ? <p role="status" className="text-xs text-muted-foreground">{t("knowledge.pending")}</p> : null}
    {fields.length ? <dl className="space-y-1 text-sm">
      {fields.map(([name, value]) => <div key={name} className="wrap-break-word">
        <dt className="font-medium">{name}</dt>
        <dd className="text-muted-foreground">{typeof value === "string" ? value : JSON.stringify(value)}</dd>
      </div>)}
    </dl> : null}
    {page.body ? <PhoneMarkdown text={page.body} onKnowledgePage={onPage} /> : null}
    {relations.length ? <Section title={t("knowledge.related")}>
      {relations.map(({ label, links }, i) => <div key={i}>
        <p className="text-xs font-medium text-muted-foreground">{label}</p>
        <ul>{links.map((link, index) => <li key={index}>
          {link.page ? <PageButton page={link.page} onPage={onPage} /> : <span className="text-muted-foreground">{link.target}</span>}
        </li>)}</ul>
      </div>)}
    </Section> : null}
    {page.mentions?.length ? <Section title={t("knowledge.mentions")}>
      <ul>{page.mentions.map((item) => <li key={item.id}><PageButton page={item} onPage={onPage} /></li>)}</ul>
    </Section> : null}
    {page.sources?.length ? <Section title={t("knowledge.sources")}>
      <ul className="divide-y divide-border">
        {page.sources.map((source, index) => <li key={index} className="space-y-1 py-2">
          <SourceRow source={source} excerpts={quotes.get(sourceKey(source))} onSource={onSource} />
        </li>)}
      </ul>
    </Section> : null}
  </article>
}

const sourceKey = (source: KnowledgeSource) => JSON.stringify([source.kind, source.thread_id, source.seq])

function SourceRow({ source, excerpts, onSource }: {
  source: KnowledgeSource; excerpts?: Set<string>; onSource: (id: string) => void
}) {
  const url = knowledgeSourceURL(source.uri)
  const label = source.uri || t(source.kind === "event" ? "knowledge.event" : "knowledge.message", { seq: source.seq ?? 0 })
  const thread = source.thread_id
  return <>
    {source.classification === "primary" || source.classification === "analysis" ?
      <p className="text-xs text-muted-foreground">{t(source.classification === "primary" ? "knowledge.primary" : "knowledge.analysis")}</p> : null}
    {url ? <a className="wrap-break-word text-primary underline" href={url} target="_blank" rel="noopener noreferrer">{label}</a>
      : thread && source.available ? <button type="button" className="min-h-9 text-left text-primary underline" onClick={() => onSource(thread)}>{label}</button>
      : <p className="wrap-break-word text-muted-foreground">{label} · {t("knowledge.unavailable")}</p>}
    {source.available && excerpts?.size ? <details>
      <summary className="cursor-pointer text-primary">{t("knowledge.excerpt")}</summary>
      {[...excerpts].map((quote) => <blockquote key={quote} className="my-1 wrap-break-word whitespace-pre-wrap border-s-2 ps-2">{quote}</blockquote>)}
    </details> : null}
  </>
}

function PageButton({ page, onPage }: { page: KnowledgePageRef; onPage: (id: string) => void }) {
  return <button type="button" className="min-h-9 wrap-break-word text-left text-primary underline" onClick={() => onPage(page.id)}>{page.title}</button>
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return <section className="space-y-1 border-t border-border pt-3 text-sm">
    <h3 className="font-semibold">{title}</h3>
    {children}
  </section>
}
