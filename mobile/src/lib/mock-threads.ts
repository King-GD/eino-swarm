import type { MockGoalFields } from "./mock-goal"
import type { Step } from "./mock-turn"
import type { FollowupView, RemoteEvent, ThreadView } from "./rpc"

export type MockThread = MockGoalFields & {
  id: string
  title: string
  projectID: string
  scheduleSource?: ThreadView["schedule_source"]
  summary: string
  running: boolean
  waiting: boolean
  action: string
  askUser: boolean
  minutesAgo: number
  providerID?: string
  model?: string
  reasoning?: string
  events: RemoteEvent[]
  completedSeq?: number
  readSeq?: number
  followups?: FollowupView[]
  pendingInputs?: RemoteEvent[]
  script?: number
  finishingSeed?: boolean
}

export function mockTurnID(thread: MockThread): string {
  for (let i = thread.events.length - 1; i >= 0; i--) {
    const id = thread.events[i].turn_id
    if (id) return id
  }
  return thread.id + "-live"
}

export function threadSeed(): MockThread[] {
  return [
    {
      id: "t-live",
      title: "Trim the layout pass",
      projectID: "p-platform",
      summary: "",
      running: true,
      waiting: false,
      action: "Reading src/lib/layout.ts",
      askUser: false,
      minutesAgo: 0,
      events: [],
    },
    {
      id: "t-wait",
      title: "Watch the nightly export",
      projectID: "p-platform",
      summary: "Checking again after the next run",
      running: false,
      waiting: true,
      action: "",
      askUser: false,
      minutesAgo: 6,
      goal: "Keep the export green and report anything that breaks",
      events: [],
    },
    {
      id: "t-recent",
      title: "Sweep the unused exports",
      projectID: "p-platform",
      summary: "Removed four dead helpers and kept the tests green",
      running: false,
      waiting: false,
      action: "",
      askUser: false,
      minutesAgo: 52,
      events: [],
    },
    {
      id: "t-notes",
      title: "Summarise this week",
      projectID: "p-notes",
      summary: "Wrote the digest into the notes folder",
      running: false,
      waiting: false,
      action: "",
      askUser: false,
      minutesAgo: 20 * 60,
      events: [],
    },
    {
      id: "t-loose",
      title: "Name the release",
      projectID: "",
      summary: "Three candidates, one of them survives a trademark check",
      running: false,
      waiting: false,
      action: "",
      askUser: false,
      minutesAgo: 3 * 24 * 60,
      events: [],
    },
  ]
}

export function olderThreadSeed(): MockThread {
  return {
    id: "t-older",
    title: "Page past the first",
    projectID: "",
    summary: "",
    running: false,
    waiting: false,
    action: "",
    askUser: false,
    minutesAgo: 90,
    events: [],
  }
}

export function transcriptFileLinksSeed(): Step[] {
  return [{ kind: "agent_message", agentID: "manager", text: [
    "Read [the task result](results/report.txt), or open `draft.txt`.",
    "Pick a copy of report.txt. Missing file: `missing.txt`.",
    "[Public reference](https://example.invalid/report.txt#zwai-file:draft.txt) · [Outside task](../secret.txt)",
  ].join("\n\n") }]
}
