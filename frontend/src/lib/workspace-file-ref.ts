/** A fragment, not a custom protocol: the markdown renderer drops unknown
 * schemes before the link component can see them. */
const FILE_HREF = "#zwai-file:"
const TOKEN = /[\p{L}\p{N}._+\-~]*[\p{L}\p{N}_+\-~](?:\/[\p{L}\p{N}._+\-~]*[\p{L}\p{N}_+\-~])*/gu
const PREVIEW_NAME = /\.(md|markdown|mdx|txt|log|csv|tsv|json|jsonl|ndjson|ya?ml|toml|ini|conf|cfg|xml|svg|go|mod|sum|py|rb|rs|[ch]|cc|cpp|hpp|java|kt|swift|mm?|sh|bash|zsh|fish|ps1|bat|sql|tsx?|jsx?|css|scss|sass|less|html?|vue|svelte|png|jpe?g|gif|webp|docx|xlsx|pptx|epub)$/i

type MdNode = {
  type: string
  value?: string
  url?: string
  children?: MdNode[]
}

export function workspaceFileHref(path: string): string {
  return FILE_HREF + encodeURIComponent(path)
}

export function workspaceFileFromHref(href: string | null | undefined): string | null {
  if (!href) return null
  const at = href.indexOf(FILE_HREF)
  if (at < 0) return null
  try {
    return workspaceFilePath(decodeURIComponent(href.slice(at + FILE_HREF.length)))
  } catch {
    return null
  }
}

function previewableFileName(name: string): boolean {
  return PREVIEW_NAME.test(name.slice(name.lastIndexOf("/") + 1))
}

/** A single relative workspace file, or null when the text is not one. */
export function workspaceFilePath(raw: string): string | null {
  let token = raw.trim()
  if (!token || token.length > 512 || /[\s:\\]/.test(token) || token.includes("//")) return null
  while (token.startsWith("./")) token = token.slice(2)
  if (!token || token.startsWith("/") || token.endsWith("/")) return null
  const parts = token.split("/")
  if (parts.length > 32) return null
  for (let i = 0; i < parts.length; i++) {
    const part = parts[i] ?? ""
    if (!part || part === "." || part === ".." || part.length > 255) return null
    if (i < parts.length - 1 && part.includes(".")) return null
  }
  if (!previewableFileName(parts[parts.length - 1] ?? "")) return null
  return token
}

type ListedPath = { path: string; dir?: boolean }

/** One real workspace path, or every path that shares the requested name. */
export type WorkspaceFileMatch =
  | { status: "one"; path: string }
  | { status: "many"; paths: string[] }

/** Prefer the path as written. A tool-relative name is the file under `work/`
 * when that is the only match. Several files with the same name stay a choice,
 * including one under `work/`. */
export function chooseWorkspaceFile(requested: string, hits: readonly ListedPath[]): WorkspaceFileMatch {
  const files = hits.filter((hit) => hit.path && !hit.dir)
  if (files.some((hit) => hit.path === requested)) return { status: "one", path: requested }
  const suffix = `/${requested}`
  const paths = [...new Set(files.filter((hit) => hit.path.endsWith(suffix)).map((hit) => hit.path))]
  paths.sort()
  if (paths.length === 1) return { status: "one", path: paths[0]! }
  if (paths.length > 1) return { status: "many", paths }
  return { status: "one", path: requested }
}

function breaksPath(ch: string | undefined, side: "before" | "after"): boolean {
  if (!ch) return true
  if (ch === "/" || ch === "\\") return false
  if (side === "before" && /[\p{L}\p{N}._+\-~]/u.test(ch)) return false
  if (side === "after" && /[\p{L}\p{N}_+\-~]/u.test(ch)) return false
  return true
}

export function splitWorkspaceFileText(value: string): MdNode[] {
  const out: MdNode[] = []
  let last = 0
  for (const match of value.matchAll(TOKEN)) {
    const token = match[0]
    const index = match.index ?? 0
    const before = index === 0 ? undefined : value[index - 1]
    const after = value[index + token.length]
    if (!breaksPath(before, "before") || !breaksPath(after, "after")) continue
    const path = workspaceFilePath(token)
    if (!path) continue
    if (index > last) out.push({ type: "text", value: value.slice(last, index) })
    out.push({
      type: "link",
      url: workspaceFileHref(path),
      children: [{ type: "text", value: token }],
    })
    last = index + token.length
  }
  if (out.length === 0) return [{ type: "text", value }]
  if (last < value.length) out.push({ type: "text", value: value.slice(last) })
  return out
}

const SKIP = new Set(["link", "linkReference", "definition", "image", "imageReference"])

function rewrite(node: MdNode) {
  const children = node.children
  if (!children || SKIP.has(node.type)) return
  for (let i = 0; i < children.length; i++) {
    const child = children[i]
    if (!child) continue
    if (child.type === "text" && typeof child.value === "string") {
      const next = splitWorkspaceFileText(child.value)
      if (next.length === 1 && next[0]?.type === "text") continue
      children.splice(i, 1, ...next)
      i += next.length - 1
      continue
    }
    if (child.type === "inlineCode" && typeof child.value === "string") {
      const path = workspaceFilePath(child.value)
      if (path) {
        children[i] = { type: "link", url: workspaceFileHref(path), children: [child] }
        continue
      }
    }
    rewrite(child)
  }
}

/** Turn workspace file paths in prose and inline code into preview links.
 * Fenced code and existing links stay as written. */
export function remarkWorkspaceFiles() {
  return (tree: MdNode) => {
    rewrite(tree)
  }
}
