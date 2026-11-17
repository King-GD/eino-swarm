import { fromMarkdown } from "mdast-util-from-markdown"

/** Extract public prose from the Markdown source, never UI/DOM labels. */
export function speechText(markdown: string): string {
  type Node = { type: string; value?: string; children?: Node[] }
  const read = (node: Node): string => {
    if (["code", "html", "image", "imageReference", "definition", "yaml"].includes(node.type)) return ""
    if (node.type === "text" || node.type === "inlineCode") return node.value ?? ""
    if (node.type === "break") return " "
    const text = (node.children ?? []).map(read).join("")
    return ["paragraph", "heading", "listItem", "blockquote"].includes(node.type) ? text.trim() + "\n\n" : text
  }
  return read(fromMarkdown(markdown)).replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim()
}

/** Bounded Unicode segments, preferring a sentence or paragraph boundary. */
export function speechSegments(text: string, maxChars: number): string[] {
  const limit = Math.max(1, Math.floor(maxChars) || 1)
  const result: string[] = []
  let remaining = [...text.trim()]
  while (remaining.length) {
    let end = Math.min(limit, remaining.length)
    if (end < remaining.length) {
      for (let i = end - 1; i >= Math.floor(end / 2); i--) {
        if (/[。！？.!?\n]/u.test(remaining[i])) { end = i + 1; break }
      }
    }
    const part = remaining.slice(0, end).join("").trim()
    if (part) result.push(part)
    remaining = remaining.slice(end)
  }
  return result
}

/** A late transcription must never replace edits made while recording. */
export function insertSpeechText(current: string, original: string, caret: number, spoken: string): { text: string; caret: number } {
  const value = spoken.trim()
  if (!value) return { text: current, caret: current.length }
  if (current !== original) {
    const text = current + (current && !/\s$/u.test(current) ? " " : "") + value
    return { text, caret: text.length }
  }
  const at = Math.max(0, Math.min(caret, current.length))
  return { text: current.slice(0, at) + value + current.slice(at), caret: at + value.length }
}
