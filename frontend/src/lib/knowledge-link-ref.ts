import type { Extension as MarkdownExtension } from "mdast-util-from-markdown"
import type { Extension as SyntaxExtension, Tokenizer } from "micromark-util-types"
import type {} from "remark-parse"
import type { Processor } from "unified"

const KNOWLEDGE_HREF = "#zwai-knowledge:"
const WIKILINK = /\[\[([a-z0-9-]+)(?:\|([^\[\]\r\n]*))?\]\]/g
const SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*(?![\s\S])/
const DEVICE_NAME = /^(?:aux|con|nul|prn|com[1-9]|lpt[1-9])$/

declare module "micromark-util-types" {
  interface TokenTypeMap {
    knowledgeWikiLink: "knowledgeWikiLink"
  }
}

export type KnowledgeTextPart =
  | { type: "text"; value: string }
  | { type: "page"; id: string; value: string }

export function isKnowledgePageID(id: string): boolean {
  return id.length <= 100 && SLUG.test(id) && !DEVICE_NAME.test(id)
}

export function knowledgePageFromHref(href: string | undefined): string | undefined {
  if (!href?.startsWith(KNOWLEDGE_HREF)) return
  const id = href.slice(KNOWLEDGE_HREF.length)
  return isKnowledgePageID(id) ? id : undefined
}

/** User and steer messages are plain text. Only wiki references become controls. */
export function splitKnowledgeLinks(value: string): KnowledgeTextPart[] {
  const nodes: KnowledgeTextPart[] = []
  let end = 0
  for (const match of value.matchAll(WIKILINK)) {
    const [token, id, label] = match
    if (!id || !isKnowledgePageID(id) || value[match.index - 1] === "\\") continue
    if (match.index > end) nodes.push({ type: "text", value: value.slice(end, match.index) })
    nodes.push({ type: "page", id, value: label?.trim() || id })
    end = match.index + token.length
  }
  if (end < value.length || nodes.length === 0) nodes.push({ type: "text", value: value.slice(end) })
  return nodes
}

const left = "[".charCodeAt(0)
const right = "]".charCodeAt(0)
const pipe = "|".charCodeAt(0)
const dash = "-".charCodeAt(0)
const tokenize: Tokenizer = (effects, ok, nok) => {
  let id = ""
  return first

  function first(code: number | null) {
    if (code !== left) return nok(code)
    effects.enter("knowledgeWikiLink")
    effects.consume(code)
    return second
  }
  function second(code: number | null) {
    if (code !== left) return nok(code)
    effects.consume(code)
    return identifier
  }
  function identifier(code: number | null) {
    if (code !== null && (code === dash || (code >= 48 && code <= 57) || (code >= 97 && code <= 122))) {
      if (id.length === 100) return nok(code)
      id += String.fromCharCode(code)
      effects.consume(code)
      return identifier
    }
    if (!isKnowledgePageID(id)) return nok(code)
    if (code !== pipe) return closing(code)
    effects.consume(code)
    return label
  }
  function label(code: number | null) {
    if (code === right) return closing(code)
    // Micromark uses values below -2 for line endings.
    if (code === null || code < -2 || code === left) return nok(code)
    effects.consume(code)
    return label
  }
  function closing(code: number | null) {
    if (code !== right) return nok(code)
    effects.consume(code)
    return close
  }
  function close(code: number | null) {
    if (code !== right) return nok(code)
    effects.consume(code)
    effects.exit("knowledgeWikiLink")
    return ok
  }
}

const syntax: SyntaxExtension = { text: { [left]: { tokenize } } }
const markdown: MarkdownExtension = {
  exit: {
    knowledgeWikiLink(token) {
      const raw = this.sliceSerialize(token)
      const divider = raw.indexOf("|")
      const id = raw.slice(2, divider === -1 ? -2 : divider)
      const label = divider === -1 ? id : raw.slice(divider + 1, -2).trim() || id
      if (this.stack.some(({ type }) => ["link", "linkReference", "image", "imageReference"].includes(type))) {
        this.enter({ type: "text", value: raw }, token)
      } else {
        this.enter({ type: "link", url: KNOWLEDGE_HREF + id, children: [{ type: "text", value: label }] }, token)
      }
      this.exit(token)
    },
  },
}

/** Parse alongside ordinary Markdown inline syntax so escaped brackets, code,
 *  and link labels keep their original meaning. */
export function remarkKnowledgeLinks(this: Processor) {
  const data = this.data()
  const syntaxExtensions = data.micromarkExtensions ??= []
  const markdownExtensions = data.fromMarkdownExtensions ??= []
  syntaxExtensions.push(syntax)
  markdownExtensions.push(markdown)
}
