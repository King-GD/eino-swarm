// @vitest-environment jsdom

import { act, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { api } from "@/lib/api"
import type { KnowledgeStatus } from "@/lib/knowledge"
import type { Project } from "@/lib/types"
import { KnowledgePanel } from "./knowledge-panel"

const status = (enabled: boolean): KnowledgeStatus => ({ enabled, page_count: 0, pending_count: 0 })
const project = (id: string): Project => ({
  id, name: id, system_prompt: "", workdir: "", resolved_workdir: "", memory_enabled: false,
  memory_dir: "", created_at: "2026-11-23T00:00:00Z", updated_at: "2026-11-23T00:00:00Z",
})

afterEach(() => vi.restoreAllMocks())

describe("Knowledge panel project ownership", () => {
  it("cancels the old project request and discards its late enabled result", async () => {
    let finishFirst: (value: KnowledgeStatus) => void = () => {}
    const first = new Promise<KnowledgeStatus>((resolve) => { finishFirst = resolve })
    const read = vi.spyOn(api, "knowledgeStatus").mockImplementation((id) =>
      id === "first" ? first : Promise.resolve(status(false)),
    )
    const readTree = vi.spyOn(api, "knowledgeTree")
    const actions = { onDistill: vi.fn(), onTrace: vi.fn(), onOpenThread: vi.fn() }
    const { rerender } = render(<KnowledgePanel project={project("first")} {...actions} />)
    const oldSignal = read.mock.calls[0]?.[1]
    rerender(<KnowledgePanel project={project("second")} {...actions} />)
    expect(await screen.findByRole("button", { name: "Enable knowledge base" })).toBeTruthy()
    expect(oldSignal?.aborted).toBe(true)

    await act(async () => finishFirst(status(true)))
    expect(screen.getByRole("button", { name: "Enable knowledge base" })).toBeTruthy()
    expect(readTree).not.toHaveBeenCalled()
  })
})
