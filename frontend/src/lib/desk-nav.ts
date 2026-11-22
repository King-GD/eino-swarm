import { useSyncExternalStore } from "react"

/** Which list the leftmost rail is showing. The task column is never one
 *  of these lists. Projects is the list people live in; conversations
 *  with no project and waits each have their own. */
export type DeskDest = "projects" | "chats" | "scheduled"

const DEST_KEY = "zwai.desk.dest"

function readDest(): DeskDest {
  try {
    const raw = localStorage.getItem(DEST_KEY)
    if (raw === "projects" || raw === "chats" || raw === "scheduled") {
      return raw
    }
  } catch {
    // A preference is not worth failing to start over.
  }
  return "projects"
}

let dest: DeskDest = readDest()
/** The last project or conversation list that Escape from Scheduled restores. */
let back: DeskDest = dest === "chats" ? "chats" : "projects"
const listeners = new Set<() => void>()

function emit() {
  for (const listener of listeners) listener()
}

export function deskDest(): DeskDest {
  return dest
}

export function setDeskDest(next: DeskDest) {
  if (next === "projects" || next === "chats") back = next
  if (dest === next) return
  dest = next
  try {
    localStorage.setItem(DEST_KEY, next)
  } catch {
    // Same as a missing preference: the in-memory choice still applies.
  }
  emit()
}

/** A project topic stays in the project list. Anything else is the
 *  conversation list, including a thread we have not loaded yet. */
export function listDestForThread(projectId?: string): DeskDest {
  return projectId ? "projects" : "chats"
}

export function scheduledReturnDest(): DeskDest {
  return back
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useDeskDest(): DeskDest {
  return useSyncExternalStore(subscribe, deskDest, () => "projects")
}

/** Inbox-open wins so a wait page cannot keep painting another list.
 *  A stale "scheduled" choice with the inbox closed falls back to
 *  projects: the rail and the column have to agree after Escape. */
export function visibleDest(inboxOpen: boolean, dest: DeskDest): DeskDest {
  if (inboxOpen) return "scheduled"
  if (dest === "chats") return dest
  return "projects"
}
