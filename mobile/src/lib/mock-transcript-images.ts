import { mockFileChunk, mockOutputFiles } from "./mock-files"
import type { Step } from "./mock-turn"

const landscape = "img_018a"
const tall = "img_028b"

export function mockTranscriptImageHistory(): Step[] {
  return [
    { kind: "user_message", text: "Screenshots from the PC", imageIDs: [landscape, tall] },
    { kind: "steer", imageIDs: [landscape] },
    { kind: "agent_message", text: "I received both screenshots." },
    { kind: "done" },
  ]
}

export async function mockInputImageChunk(id: string | undefined, before: number) {
  if (!id || new URLSearchParams(location.search).get("history") !== "input-images") return
  const path = id === landscape ? "outputs/landscape.png" : id === tall ? "outputs/tall.png" : undefined
  if (!path) return
  const chunk = await mockFileChunk(mockOutputFiles("?file-preview=tall-image"), path, before)
  if (chunk) return { ...chunk, path: id }
}
