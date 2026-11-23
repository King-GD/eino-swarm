import { createContext, useContext, type ReactNode } from "react"

import { splitKnowledgeLinks } from "@/lib/knowledge-link-ref"
import { cn } from "@/lib/utils"

interface KnowledgeLinkTarget {
  open: (id: string) => void
  unresolved?: readonly string[]
}

const KnowledgeLinkContext = createContext<KnowledgeLinkTarget | null>(null)

export function KnowledgeLinkProvider({ open, unresolved, children }: KnowledgeLinkTarget & { children: ReactNode }) {
  return (
    <KnowledgeLinkContext.Provider value={{ open, unresolved }}>
      {children}
    </KnowledgeLinkContext.Provider>
  )
}

export function useKnowledgeLinks() {
  return useContext(KnowledgeLinkContext)
}

export function KnowledgePlainText({ text }: { text: string }) {
  const target = useKnowledgeLinks()
  if (!target || !text.includes("[[")) return <>{text}</>
  return splitKnowledgeLinks(text).map((part, index) => part.type === "text"
    ? part.value
    : <KnowledgeWikiLink key={`${part.id}:${index}`} id={part.id}>{part.value}</KnowledgeWikiLink>,
  )
}

export function KnowledgeWikiLink({ id, children }: { id: string; children: ReactNode }) {
  const target = useKnowledgeLinks()
  if (!target) return <>{children}</>
  const broken = target.unresolved?.includes(id)
  return (
    <button
      type="button"
      className={cn(
        "inline cursor-pointer whitespace-normal border-0 bg-transparent p-0 text-left align-baseline font-[inherit] underline underline-offset-2 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring",
        broken ? "text-destructive" : "text-foreground hover:text-primary",
      )}
      data-testid="knowledge-wikilink"
      data-page={id}
      onClick={() => target.open(id)}
    >
      {children}
    </button>
  )
}
