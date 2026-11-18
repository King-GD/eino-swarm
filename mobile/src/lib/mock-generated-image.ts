import { parseSlashSubmit } from "../../../frontend/src/lib/slash-core"
import { mockFileChunk, mockOutputFiles } from "./mock-files"
import type { Step } from "./mock-turn"

const name = "generated-image.png"
const id = "img_2040"
const path = "outputs/landscape.png"

function enabled() {
  return new URLSearchParams(location.search).get("images") === "1"
}

export function mockGeneratedImageTurn(text: string): Step[] | undefined {
  if (!enabled() || parseSlashSubmit(text)?.id !== "image") return
  const turnID = crypto.randomUUID()
  const callID = turnID
  return [
    { kind: "user_message", turnID, text },
    { kind: "image_generation", turnID, callID, text: "running" },
    { kind: "image_generation", turnID, callID, text: "completed", generatedImage: { id, name, mime: "image/png", width: 1200, height: 800 } },
    { kind: "done", turnID },
  ]
}

export async function mockGeneratedImageChunk(imageId: string | undefined, before: number) {
  if (!enabled() || imageId !== id) return
  const chunk = await mockFileChunk(mockOutputFiles(), path, before)
  if (chunk) return { ...chunk, path: id, name }
}
