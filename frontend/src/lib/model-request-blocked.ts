export const MODEL_REQUEST_BLOCKED = "model_request_blocked"

const legacyBlock = "model api request failed: http 403 forbidden: request blocked by the integration security guard"

export function isModelRequestBlocked(text?: string): boolean {
  return text === MODEL_REQUEST_BLOCKED || text?.trim().toLowerCase().replace(/\.$/, "") === legacyBlock
}

type TranscriptBlock = { kind: string; turnId?: string; text: string; generation?: { reason: string } }

/** Keep generation receipts for Trace and stream reduction. Chat shows the
 * terminal notice when available even if the two events arrived out of order. */
export function visibleModelRequestNotices<T extends TranscriptBlock>(blocks: T[]): T[] {
  const blockedTurns = new Set(blocks.filter((b) => b.kind === "error" && b.turnId && isModelRequestBlocked(b.text)).map((b) => b.turnId))
  if (!blockedTurns.size) return blocks
  return blocks.filter((b) => b.generation?.reason !== MODEL_REQUEST_BLOCKED || !blockedTurns.has(b.turnId))
}
