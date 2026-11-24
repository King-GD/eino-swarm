import { create } from "zustand"
import { ApiError } from "@/lib/api"
import { nuwaAPI, subscribeNuwa } from "@/lib/nuwa-api"
import { subscribeWorkEvents } from "@/lib/nuwa-events"
import { workAtLeast, type ThreadWorkSnapshot, type WorkVersion } from "@/lib/nuwa"

export interface WorkOwner {
  threadId?: string
  projectId?: string
  generation: number
}

interface NuwaState {
  owner?: WorkOwner
  snapshot?: ThreadWorkSnapshot
  tag?: string
  loading: boolean
  connected: boolean
  error?: string
  promoting: boolean
  promoteError?: { id: string; message: string; conflict: boolean }
  connect: (owner: WorkOwner) => () => void
  refresh: () => Promise<void>
  promote: (id: string) => Promise<void>
}

let visit = 0
let closeStream: (() => void) | undefined
let reading: Promise<void> | undefined
let wanted: WorkVersion | undefined

function projectVersion(owner: WorkOwner, revision: number): WorkVersion {
  return {
    thread_id: "",
    project_id: owner.projectId ?? "",
    thread_revision: 0,
    project_revision: revision,
    data_generation: owner.generation,
  }
}

/** One inspector visit owns its reads, mutations and invalidations. */
export const useNuwa = create<NuwaState>((set, get) => {
  function invalidate(version: WorkVersion) {
    const current = get().snapshot
    if ((current && workAtLeast(current, version)) || (wanted && workAtLeast(wanted, version))) return
    wanted = version
    if (current && (version.project_id !== current.project_id || version.data_generation !== current.data_generation)) {
      set({ snapshot: undefined, tag: undefined, loading: true })
    }
    void get().refresh()
  }

  return {
    loading: false,
    connected: false,
    promoting: false,
    connect: (owner) => {
      const token = ++visit
      closeStream?.()
      reading = undefined
      wanted = undefined
      set({ owner, snapshot: undefined, tag: undefined, loading: true, connected: false, error: undefined, promoting: false, promoteError: undefined })
      const connection = (connected: boolean) => {
        if (token !== visit) return
        set({ connected })
        if (connected) void get().refresh()
      }
      closeStream = owner.threadId
        ? subscribeWorkEvents(owner.threadId, (event) => {
          if (token !== visit) return
          if (event.kind === "connection") connection(event.connected)
          else if (event.version.data_generation >= owner.generation) invalidate(event.version)
        })
        : subscribeNuwa(owner.projectId!, {
          onRevision: (revision) => {
            if (token === visit) invalidate(projectVersion(owner, revision))
          },
          onOpen: () => connection(true),
          onError: () => connection(false),
        })
      void get().refresh()
      return () => {
        if (token !== visit) return
        ++visit
        reading = undefined
        closeStream?.()
        closeStream = undefined
        set({ owner: undefined, snapshot: undefined, tag: undefined, connected: false })
      }
    },
    refresh: async () => {
      const owner = get().owner
      if (!owner) return
      if (reading) return reading
      const token = visit
      const requested = wanted
      set({ loading: !get().snapshot, error: undefined })
      reading = (async () => {
        try {
          const result = owner.threadId
            ? await nuwaAPI.threadSnapshot(owner.threadId)
            : await nuwaAPI.snapshot(owner.projectId!).then((snapshot) => ({
              view: { ...projectVersion(owner, snapshot.revision), project_id: snapshot.project_id, nodes: snapshot.nodes, workers: snapshot.workers },
              tag: undefined,
            }))
          if (token !== visit) return
          const { view, tag } = result
          if (view.thread_id !== (owner.threadId ?? "") ||
              (!owner.threadId && view.project_id !== owner.projectId) ||
              view.data_generation < owner.generation) return
          const current = get().snapshot
          if ((current && !workAtLeast(view, current)) || (wanted && !workAtLeast(view, wanted))) return
          set({ snapshot: view, tag, error: undefined })
        } catch (error) {
          if (token === visit) set({ error: error instanceof Error ? error.message : String(error) })
        } finally {
          if (token === visit) {
            reading = undefined
            set({ loading: false })
            const current = get().snapshot
            if (wanted && wanted !== requested && (!current || !workAtLeast(current, wanted))) {
              await get().refresh()
            }
          }
        }
      })()
      return reading
    },
    promote: async (id) => {
      const { owner, snapshot, tag, promoting } = get()
      if (!owner?.threadId || !snapshot?.project_id || !tag || promoting ||
          !snapshot.nodes.some((node) => node.id === id && node.scope === "thread")) return
      const token = visit
      set({ promoting: true, promoteError: undefined })
      try {
        const revision = await nuwaAPI.promote(owner.threadId, [id], tag)
        if (token === visit) {
          invalidate(revision)
          await get().refresh()
        }
      } catch (error) {
        if (token === visit) {
          const conflict = error instanceof ApiError && error.code === "work_conflict"
          set({ promoteError: { id, conflict, message: error instanceof Error ? error.message : String(error) } })
          if (conflict) await get().refresh()
        }
      } finally {
        if (token === visit) set({ promoting: false })
      }
    },
  }
})
