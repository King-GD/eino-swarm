import { foldPhoneItems, managerBlocks, type CompactBlock } from "./transcript"
import { dropPackedJson } from "./tool-preview"

export function hasPhoneTurnStart(blocks: CompactBlock[], turnId: string, hasMore: boolean): boolean {
  let earlierTurnEnded = false
  let foundTurn = false
  for (const block of blocks) {
    if (block.agentId || !block.turnId) continue
    if (block.turnId !== turnId) {
      if (!foundTurn && block.kind === "turn_end") earlierTurnEnded = true
      continue
    }
    foundTurn = true
    if (block.kind === "user" && (block.hasImages || dropPackedJson(block.text))) return true
  }
  // A page can start at the reply of a turn whose prompt is still unloaded.
  // A promptless turn can use its first loaded row once we reach its boundary.
  return foundTurn && (!hasMore || earlierTurnEnded)
}

/** A boundary or a filtered empty bubble cannot give a reader conversation
 * context. Opening should keep paging until content can actually be painted. */
export function hasPhoneHistoryContent(blocks: CompactBlock[]): boolean {
  return foldPhoneItems(managerBlocks(blocks)).some((item) => {
    if (item.type === "work") return true
    const block = item.block
    if (block.kind === "turn_end") return false
    if (block.kind === "user" && block.hasImages) return true
    if (["user", "steer", "answer"].includes(block.kind)) {
      return Boolean(dropPackedJson(block.text).trim())
    }
    return true
  })
}
