import { beforeEach, describe, expect, it, vi } from "vitest"
import { useNuwa } from "./nuwa"
import { nuwaAPI, subscribeNuwa } from "@/lib/nuwa-api"
import { publishWorkEvent } from "@/lib/nuwa-events"
import type { ThreadWorkSnapshot, NuwaSnapshot } from "@/lib/nuwa"

vi.mock("@/lib/nuwa-api", () => ({ nuwaAPI: { snapshot: vi.fn(), threadSnapshot: vi.fn(), promote: vi.fn() }, subscribeNuwa: vi.fn() }))

const snapshot = (project_id: string, revision: number): NuwaSnapshot => ({ project_id, revision, nodes: [], workers: [] })
let handlers: Parameters<typeof subscribeNuwa>[1]
const close = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(subscribeNuwa).mockImplementation((_id, next) => { handlers = next; return close })
  vi.mocked(nuwaAPI.snapshot).mockResolvedValue(snapshot("a", 1))
})

describe("Project collaboration snapshots", () => {
  it("rejects an older A visit after navigating A → B → A", async () => {
    let finish!: (value: NuwaSnapshot) => void
    vi.mocked(nuwaAPI.snapshot).mockReturnValueOnce(new Promise((resolve) => { finish = resolve }))
    const stopA = useNuwa.getState().connect({ projectId: "a", generation: 0 })
    stopA()
    useNuwa.getState().connect({ projectId: "b", generation: 0 })()
    useNuwa.getState().connect({ projectId: "a", generation: 0 })
    await vi.waitFor(() => expect(useNuwa.getState().snapshot?.project_revision).toBe(1))
    finish(snapshot("a", 99))
    await Promise.resolve()
    expect(useNuwa.getState().snapshot?.project_revision).toBe(1)
    expect(close).toHaveBeenCalledTimes(2)
  })

  it("coalesces duplicate revisions and refreshes on reconnect", async () => {
    const stop = useNuwa.getState().connect({ projectId: "a", generation: 0 })
    await vi.waitFor(() => expect(useNuwa.getState().snapshot?.project_revision).toBe(1))
    handlers.onRevision(1)
    expect(nuwaAPI.snapshot).toHaveBeenCalledTimes(1)
    vi.mocked(nuwaAPI.snapshot).mockResolvedValue(snapshot("a", 2))
    handlers.onRevision(2)
    await vi.waitFor(() => expect(useNuwa.getState().snapshot?.project_revision).toBe(2))
    handlers.onError()
    expect(useNuwa.getState().connected).toBe(false)
    handlers.onOpen()
    await vi.waitFor(() => expect(nuwaAPI.snapshot).toHaveBeenCalledTimes(3))
    expect(useNuwa.getState().connected).toBe(true)
    stop()
  })

  it("exposes a failed snapshot and permits a manual retry", async () => {
    vi.mocked(nuwaAPI.snapshot).mockRejectedValueOnce(new Error("read unavailable"))
    const stop = useNuwa.getState().connect({ projectId: "a", generation: 0 })
    await vi.waitFor(() => expect(useNuwa.getState().error).toBe("read unavailable"))
    await useNuwa.getState().refresh()
    expect(useNuwa.getState().error).toBeUndefined()
    expect(useNuwa.getState().snapshot?.project_revision).toBe(1)
    stop()
  })
})

const threadSnapshot = (thread_id: string, project_id: string, thread_revision: number, project_revision: number, data_generation = 0): ThreadWorkSnapshot => ({
  thread_id, project_id, thread_revision, project_revision, data_generation, nodes: [], workers: [],
})

describe("Conversation work snapshots", () => {
  it("rejects the first A response after changing conversation and returning to A", async () => {
    let finish!: (value: { view: ThreadWorkSnapshot }) => void
    vi.mocked(nuwaAPI.threadSnapshot).mockReturnValueOnce(new Promise((resolve) => { finish = resolve }))
      .mockResolvedValueOnce({ view: threadSnapshot("b", "p", 1, 1) })
      .mockResolvedValueOnce({ view: threadSnapshot("a", "p", 2, 1) })
    useNuwa.getState().connect({ threadId: "a", projectId: "p", generation: 0 })()
    useNuwa.getState().connect({ threadId: "b", projectId: "p", generation: 0 })()
    const stop = useNuwa.getState().connect({ threadId: "a", projectId: "p", generation: 0 })
    await vi.waitFor(() => expect(useNuwa.getState().snapshot?.thread_revision).toBe(2))
    finish({ view: threadSnapshot("a", "p", 99, 99) })
    await Promise.resolve()
    expect(useNuwa.getState().snapshot?.thread_revision).toBe(2)
    stop()
  })

  it("fences both revisions and accepts a project move or data restore", async () => {
    const earlier = threadSnapshot("a", "p", 3, 10)
    vi.mocked(nuwaAPI.threadSnapshot).mockResolvedValueOnce({ view: earlier })
    const stop = useNuwa.getState().connect({ threadId: "a", projectId: "p", generation: 0 })
    await vi.waitFor(() => expect(useNuwa.getState().snapshot).toEqual(earlier))
    const notify = (version: ThreadWorkSnapshot) => publishWorkEvent("a", { kind: "revision", version })
    let resolveMoved!: (value: { view: ThreadWorkSnapshot }) => void
    vi.mocked(nuwaAPI.threadSnapshot).mockReturnValueOnce(new Promise((resolve) => { resolveMoved = resolve }))
    const moved = threadSnapshot("a", "new", 4, 0)
    notify(moved)
    expect(useNuwa.getState().snapshot).toBeUndefined()
    resolveMoved({ view: moved })
    await vi.waitFor(() => expect(useNuwa.getState().snapshot).toEqual(moved))
    const restored = threadSnapshot("a", "new", 1, 0, 1)
    vi.mocked(nuwaAPI.threadSnapshot).mockResolvedValueOnce({ view: restored })
    notify(restored)
    await vi.waitFor(() => expect(useNuwa.getState().snapshot).toEqual(restored))
    notify(earlier)
    expect(useNuwa.getState().snapshot).toEqual(restored)
    expect(nuwaAPI.threadSnapshot).toHaveBeenCalledTimes(3)
    stop()
  })
})
