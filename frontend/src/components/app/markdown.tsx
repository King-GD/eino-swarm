import {
  Children,
  createContext,
  lazy,
  memo,
  Suspense,
  useContext,
  useRef,
  type ComponentPropsWithoutRef,
  type ReactNode,
} from "react"
import { ChevronRight, Files } from "lucide-react"
import Markdown from "react-markdown"
import type { Components } from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkMath from "remark-math"
import { useT } from "@/lib/use-t"

import { ChartPending } from "@/components/app/chart-pending"
import { KnowledgeWikiLink, useKnowledgeLinks } from "@/components/app/knowledge-link-open"
import { MarkdownCodeBlock, readFence } from "@/components/app/markdown-code"
import { useWorkspaceFileOpen, WorkspaceFileLink } from "@/components/app/workspace-file-open"
import { classifyHref } from "@/lib/external-links"
import { knowledgePageFromHref, remarkKnowledgeLinks } from "@/lib/knowledge-link-ref"
import { remarkAnswerFiles, remarkWorkspaceFiles, workspaceFileFromHref } from "@/lib/workspace-file-ref"
import { isMathFence } from "@/lib/math-fence"
import { chartSpecsEqual, parseChartSpec, type ChartSpec } from "@/lib/chart-spec"
import { closeIncompleteMarkdown } from "@/lib/stream-markdown"
import { mermaidFenceIsClosed } from "@/lib/markdown-mermaid"

const TranscriptChart = lazy(async () => {
  const mod = await import("@/components/app/transcript-chart")
  return { default: mod.TranscriptChart }
})

const MarkdownMath = lazy(async () => {
  const mod = await import("@/components/app/markdown-math")
  return { default: mod.MarkdownMath }
})

const MarkdownMermaid = lazy(async () => {
  const mod = await import("@/components/app/markdown-mermaid")
  return { default: mod.MarkdownMermaid }
})

/** remark plugins are module-level so a memoised markdown block is not
 *  invalidated by a new array on every parent render. */
const PLUGINS = [remarkGfm, remarkMath, remarkWorkspaceFiles]
const KNOWLEDGE_PLUGINS = [remarkGfm, remarkMath, remarkKnowledgeLinks, remarkWorkspaceFiles]

const RESULT_PLUGINS = [...PLUGINS, remarkAnswerFiles]
const KNOWLEDGE_RESULT_PLUGINS = [...KNOWLEDGE_PLUGINS, remarkAnswerFiles]

const MarkdownStreaming = createContext(false)
const MarkdownSource = createContext("")

/** Completed markdown is immutable. Without this memo, every streamed token
 *  would re-parse every finished answer in the conversation. The open
 *  streaming block is a different text each time, so it still re-parses —
 *  that is the live render. */
export const MemoMarkdown = memo(function MemoMarkdown({
  text,
  streaming = false,
  indexFiles = false,
}: {
  text: string
  streaming?: boolean
  indexFiles?: boolean
}) {
  const knowledge = useKnowledgeLinks()
  const plugins = indexFiles && !streaming
    ? (knowledge ? KNOWLEDGE_RESULT_PLUGINS : RESULT_PLUGINS)
    : (knowledge ? KNOWLEDGE_PLUGINS : PLUGINS)
  const source = streaming ? closeIncompleteMarkdown(text) : text
  return (
    <MarkdownStreaming.Provider value={streaming}>
      <MarkdownSource.Provider value={source}>
        <Markdown remarkPlugins={plugins} components={COMPONENTS}>
          {source}
        </Markdown>
        {streaming ? (
          <span
            aria-hidden
            className="ml-0.5 inline-block h-4 w-1.5 translate-y-0.5 animate-breathe bg-foreground/70"
          />
        ) : null}
      </MarkdownSource.Provider>
    </MarkdownStreaming.Provider>
  )
})

function MarkdownPre({
  children,
  node,
  ...props
}: ComponentPropsWithoutRef<"pre"> & { node?: unknown }) {
  const streaming = useContext(MarkdownStreaming)
  const source = useContext(MarkdownSource)
  const specRef = useRef<ChartSpec | null>(null)
  const fence = readFence(children)
  if (fence?.lang.toLowerCase() === "mermaid" &&
    (!streaming || mermaidFenceIsClosed(source, node))) {
    return (
      <Suspense fallback={<MarkdownCodeBlock fence={fence} />}>
        <MarkdownMermaid source={fence.text} />
      </Suspense>
    )
  }
  if (fence !== null && fence.lang === "chart") {
    const parsed = parseChartSpec(fence.text)
    if (parsed.ok) {
      if (!specRef.current || !chartSpecsEqual(specRef.current, parsed.spec)) {
        specRef.current = parsed.spec
      }
      return (
        <Suspense fallback={<ChartPending />}>
          <TranscriptChart spec={specRef.current} />
        </Suspense>
      )
    }
    specRef.current = null
    if (streaming || parsed.incomplete) return <ChartPending />
  } else {
    specRef.current = null
  }
  if (fence !== null && isMathFence(fence.lang)) {
    return (
      <Suspense fallback={<code>{fence.text}</code>}>
        <MarkdownMath display tex={fence.text} />
      </Suspense>
    )
  }
  if (fence !== null) {
    return <MarkdownCodeBlock fence={fence} {...props} />
  }
  return <pre {...props}>{children}</pre>
}

function MarkdownInlineCode({
  className,
  children,
  node: _node,
  ...props
}: ComponentPropsWithoutRef<"code"> & { node?: unknown }) {
  if (className?.includes("math-display")) {
    const tex = codeText(children)
    return (
      <Suspense fallback={<code>{tex}</code>}>
        <MarkdownMath display tex={tex} />
      </Suspense>
    )
  }
  if (
    className?.includes("math-inline") ||
    /(^|\s)language-math(\s|$)/.test(className ?? "")
  ) {
    const tex = codeText(children)
    return (
      <Suspense fallback={<code>{tex}</code>}>
        <MarkdownMath tex={tex} />
      </Suspense>
    )
  }
  return (
    <code className={className} {...props}>
      {children}
    </code>
  )
}

function codeText(children: ReactNode): string {
  if (children == null || typeof children === "boolean") return ""
  if (typeof children === "string" || typeof children === "number") {
    return String(children).replace(/\n$/, "")
  }
  if (Array.isArray(children)) {
    return children.map((c) => codeText(c as ReactNode)).join("").replace(/\n$/, "")
  }
  return String(children ?? "").replace(/\n$/, "")
}

/** The scrollport contains readable-width columns when they cannot all fit
 *  in the conversation, without widening it under the side panel. */
function MarkdownTable({
  node: _node,
  ...props
}: ComponentPropsWithoutRef<"table"> & { node?: unknown }) {
  return (
    <div className="md-table thin-scrollbar min-w-0 max-w-full overflow-x-auto" data-testid="markdown-table">
      <table {...props} />
    </div>
  )
}

function MarkdownLink({
  href,
  children,
  node: _node,
  ...props
}: ComponentPropsWithoutRef<"a"> & { node?: unknown }) {
  const file = workspaceFileFromHref(href)
  const page = knowledgePageFromHref(href)
  const open = useWorkspaceFileOpen()
  const streaming = useContext(MarkdownStreaming)
  if (page) {
    if (streaming) return <>{children}</>
    return <KnowledgeWikiLink id={page}>{children}</KnowledgeWikiLink>
  }
  if (file) {
    if (!open || streaming) return <>{children}</>
    return <WorkspaceFileLink path={file}>{children}</WorkspaceFileLink>
  }
  const origin =
    typeof window === "undefined" ? "http://127.0.0.1" : window.location.origin
  const action = classifyHref(href, origin)
  if (action === "block") {
    return <span>{children}</span>
  }
  if (action === "leave") {
    return (
      <a {...props} href={href} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    )
  }
  return (
    <a {...props} href={href}>
      {children}
    </a>
  )
}

/** react-markdown does createElement(components.pre). A new function here
 *  is a new component type, so every later token unmounted a finished
 *  chart and Recharts painted from an empty box again. */
function AnswerFiles({ children }: { children?: ReactNode }) {
  const open = useWorkspaceFileOpen()
  const t = useT()
  if (!open) return null
  const count = Children.count(children)
  return (
    <details className="wb-answer-files" open={count <= 4} data-find-ignore="">
      <summary><Files aria-hidden size={15} />{t("transcript.answerFiles")}<span>{count}</span><ChevronRight aria-hidden size={14} /></summary>
      <div>{children}</div>
    </details>
  )
}

const COMPONENTS: Components = {
  aside: AnswerFiles,
  a: MarkdownLink,
  pre: MarkdownPre,
  code: MarkdownInlineCode,
  table: MarkdownTable,
}
