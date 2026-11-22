import type { Step } from "./mock-turn"

export function longAgentRosterHistory(): Step[] {
  return [
    { kind: "spawned", agentID: "running-1", role: "reader" },
    { kind: "spawned", agentID: "running-2", role: "writer" },
    ...Array.from({ length: 40 }, (_, index) => {
      const agentID = `completed-${index + 1}`
      return [
        { kind: "spawned", agentID, role: "reviewer" },
        { kind: "agent_message", agentID, text: "Review result" },
        { kind: "finished", agentID, text: "Review complete" },
      ]
    }).flat(),
  ]
}
