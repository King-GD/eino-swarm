import type { Step } from "./mock-turn"

/** Retained history describes an old interrupted output and a distinct live
 *  generation owned by the same worker. No finished receipt is fabricated. */
export function generationHistory(): Step[] {
  const first = "generation-before-restart"
  const cause = { reason: "engine_restart", detail: "", recovery: "pending" }
  return [
    { kind: "user_message", text: "Continue the assigned work." },
    { kind: "spawned", agentID: "worker-1", role: "worker" },
    { kind: "reasoning", agentID: "worker-1", messageID: first, text: "Interrupted thought before restart." },
    { kind: "generation_interrupted", agentID: "worker-1", messageID: first, text: JSON.stringify(cause) },
    { kind: "generation_recovered", agentID: "worker-1", messageID: first, text: JSON.stringify({ ...cause, recovery: "recovered" }) },
    { kind: "reasoning", agentID: "worker-1", messageID: "generation-after-restart", text: "New thought after recovery." },
  ]
}

export function modelFailureHistory(readTimeout: boolean): Step[] {
  const reason = readTimeout ? "model_read_timeout" : "model_response_invalid"
  const error = readTimeout ? "model response read timed out" : "the model service returned a response that could not be decoded"
  const interrupted: Step[] = readTimeout ? [
    { kind: "reasoning", agentID: "manager", messageID: "partial", text: "Reviewing the available findings." },
    { kind: "generation_interrupted", agentID: "manager", messageID: "partial", text: JSON.stringify({ reason: "model_error", recovery: "none", detail: error }) },
  ] : []
  return [
    ...interrupted,
    { kind: "model_retry", agentID: "manager", text: JSON.stringify({ attempt: 1, cap: 2, reason }) },
    { kind: "model_retry", agentID: "manager", text: JSON.stringify({ attempt: 2, cap: 2, reason }) },
    { kind: "error", agentID: "manager", err: error },
    { kind: "done" },
  ]
}
