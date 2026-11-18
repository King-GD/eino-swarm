import type { ImageRef } from "./types"

export type ImageGenerationStatus = "running" | "completed" | "failed" | "cancelled" | "unknown"

export interface ImageGeneration {
  id: string
  status: ImageGenerationStatus
  code?: string
  media?: ImageRef
  seq: number
}

export function imageGeneration(
  event: { thread_id: string; turn_id?: string; tool_call_id?: string; text?: string; seq: number; err?: string },
  media?: ImageRef,
): ImageGeneration | undefined {
  const status = event.text
  if (!event.turn_id || !event.tool_call_id || !["running", "completed", "failed", "cancelled", "unknown"].includes(status ?? "")) return
  if (status === "completed" && !media?.id) return
  return {
    id: JSON.stringify([event.thread_id, event.turn_id, event.tool_call_id]),
    status: status as ImageGenerationStatus,
    seq: event.seq,
    code: event.err,
    media: status === "completed" ? media : undefined,
  }
}

export function imageGenerationIsNewer(next: ImageGeneration, current?: ImageGeneration): boolean {
  if (!current) return true
  if (current.status !== "running" && next.status === "running") return false
  return next.seq > current.seq
}
