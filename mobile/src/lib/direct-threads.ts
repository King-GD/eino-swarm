import { mintID } from "./direct-provider"
import type { CloudToolEvent } from "./cloud-web-tools"
import { removeDirectImage, type DirectImageRef } from "./direct-images"
import { generatedImageFilename, isGeneratedImageMIME, validGeneratedImageSize } from "./gateway-image"

const KEY = "zwai.phone.directThreads"

/** Tickets share this store. A long transcript must not be the thing that
 *  pushes the binding out, and image bytes never belong next to a ticket. */
const maxThreads = 40
const maxMessages = 200

export type StoredAttachment = {
  name: string
  kind: "image" | "file"
  mediaType?: string
  /** Session only. Stripped before localStorage. */
  dataUrl?: string
  /** Decoded text of a file, session only, so the next turn can resend it. */
  text?: string
}

export type DirectImageState = {
  status: "running" | "completed" | "failed" | "cancelled" | "unknown"
  image?: DirectImageRef
  code?: string
}

export type DirectMessage = {
  id: string
  role: "user" | "assistant"
  text: string
  reasoning?: string
  attachments?: StoredAttachment[]
  error?: string
  tools?: CloudToolEvent[]
  image?: DirectImageState
}

export type DirectThread = {
  id: string
  title: string
  providerId: string
  model: string
  reasoning: string
  updatedAt: number
  messages: DirectMessage[]
}

export function loadThreads(): DirectThread[] {
  const raw = read()
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.map(asThread).filter((row): row is DirectThread => Boolean(row))
  } catch {
    return []
  }
}

export function saveThreads(rows: DirectThread[], previous = loadThreads()) {
  const slim = rows
    .map(slimThread)
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .slice(0, maxThreads)
  const retained = imageIDs(slim)
  const removed = [...imageIDs(previous)].filter(id => !retained.has(id))
  try {
    if (slim.length === 0) localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, JSON.stringify(slim))
  } catch { return false }
  for (const id of removed) void removeDirectImage(id).catch(() => undefined)
  return true
}

export function threadTitle(text: string, attachmentName: string): string {
  const line = text.trim().split("\n")[0]?.trim() ?? ""
  const base = line || attachmentName.trim()
  if (!base) return ""
  return base.length > 48 ? base.slice(0, 48) : base
}

export function newThread(pick: {
  providerId: string
  model: string
  reasoning: string
}): DirectThread {
  return {
    id: mintID("c"),
    title: "",
    providerId: pick.providerId,
    model: pick.model,
    reasoning: pick.reasoning,
    updatedAt: Date.now(),
    messages: [],
  }
}

function slimThread(row: DirectThread): DirectThread {
  return {
    ...row,
    messages: row.messages.slice(-maxMessages).map((message) => ({
      ...message,
      attachments: message.attachments?.map((item) => ({
        name: item.name,
        kind: item.kind,
      })),
    })),
  }
}

function asThread(value: unknown): DirectThread | null {
  if (!value || typeof value !== "object") return null
  const row = value as Partial<DirectThread>
  if (typeof row.id !== "string" || !row.id) return null
  const messages = Array.isArray(row.messages)
    ? row.messages.map(asMessage).filter((item): item is DirectMessage => Boolean(item))
    : []
  return {
    id: row.id,
    title: typeof row.title === "string" ? row.title : "",
    providerId: typeof row.providerId === "string" ? row.providerId : "",
    model: typeof row.model === "string" ? row.model : "",
    reasoning: typeof row.reasoning === "string" ? row.reasoning : "",
    updatedAt: typeof row.updatedAt === "number" ? row.updatedAt : 0,
    messages,
  }
}

function asMessage(value: unknown): DirectMessage | null {
  if (!value || typeof value !== "object") return null
  const row = value as Partial<DirectMessage>
  if (row.role !== "user" && row.role !== "assistant") return null
  if (typeof row.id !== "string" || !row.id) return null
  return {
    id: row.id,
    role: row.role,
    text: typeof row.text === "string" ? row.text : "",
    reasoning: typeof row.reasoning === "string" ? row.reasoning : undefined,
    error: typeof row.error === "string" ? row.error : undefined,
    tools: Array.isArray(row.tools) ? row.tools.slice(0, 16).flatMap((tool) => {
      const parsed = asToolEvent(tool)
      return parsed ? [parsed] : []
    }) : undefined,
    image: asImageState(row.image),
    attachments: Array.isArray(row.attachments)
      ? row.attachments.flatMap((item) => {
          const chip = asAttachment(item)
          return chip ? [chip] : []
        })
      : undefined,
  }
}

function imageIDs(threads: DirectThread[]): Set<string> {
  const ids = new Set<string>()
  for (const thread of threads) {
    for (const { image } of thread.messages) {
      if (image?.image) ids.add(image.image.id)
    }
  }
  return ids
}

function asImageState(value: unknown): DirectImageState | undefined {
  if (!value || typeof value !== "object") return
  const row = value as Partial<DirectImageState>
  if (row.status === "running" || row.status === "unknown") return { status: "unknown" }
  if (row.status === "cancelled") return { status: "cancelled" }
  if (row.status === "failed") return { status: "failed", code: typeof row.code === "string" && row.code.length <= 64 ? row.code : undefined }
  if (row.status !== "completed") return
  const ref = row.image
  if (!ref || typeof ref.id !== "string" || !/^img-[a-f0-9]{16}$/.test(ref.id) || !isGeneratedImageMIME(ref.mime) ||
      !validGeneratedImageSize(ref.width, ref.height)) {
    return { status: "failed", code: "image_invalid_response" }
  }
  return {
    status: "completed",
    image: { id: ref.id, mime: ref.mime, name: generatedImageFilename(ref.id, ref.mime), width: ref.width, height: ref.height },
  }
}

function asToolEvent(value: unknown): CloudToolEvent | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null
  const tool = value as Partial<CloudToolEvent>
  if (typeof tool.id !== "string" || tool.id.length > 512 ||
      typeof tool.name !== "string" || !["web_search", "web_fetch"].includes(tool.name) ||
      !["running", "completed", "failed", "cancelled"].includes(tool.status ?? "") ||
      typeof tool.args !== "string" || tool.args.length > 4096 ||
      typeof tool.text !== "string" || tool.text.length > 4096) return null
  return {
    id: tool.id,
    callId: typeof tool.callId === "string" && tool.callId.length <= 256 ? tool.callId : undefined,
    responseId: typeof tool.responseId === "string" && tool.responseId.length <= 256 ? tool.responseId : undefined,
    name: tool.name,
    status: tool.status === "running" ? "cancelled" : tool.status as CloudToolEvent["status"],
    args: tool.args, text: tool.text, truncated: tool.truncated === true,
  }
}

function asAttachment(value: unknown): StoredAttachment | null {
  if (!value || typeof value !== "object") return null
  const row = value as Partial<StoredAttachment>
  if (typeof row.name !== "string" || !row.name) return null
  if (row.kind !== "image" && row.kind !== "file") return null
  return { name: row.name, kind: row.kind }
}

function read(): string | null {
  try {
    return localStorage.getItem(KEY)
  } catch {
    return null
  }
}
