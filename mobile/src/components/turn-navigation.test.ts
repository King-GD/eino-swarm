import { expect, it } from "vitest"
import { pairedTurnDestinations } from "./turn-navigation"

it("keeps legacy reply previews out of the rail until the user message loads", () => {
  const turns = [
    { id: "legacy", status: "done", text: "Assistant reply" },
    { id: "new", status: "done", text: "Another reply", user_text: "Stored prompt" },
  ]
  expect(pairedTurnDestinations(turns, [])).toEqual([
    { id: "legacy", label: "" },
    { id: "new", label: "Stored prompt" },
  ])
  expect(pairedTurnDestinations(turns, [
    { id: "legacy-prompt", kind: "user", turnId: "legacy", text: "Loaded prompt" },
  ])[0]).toEqual({ id: "legacy", label: "Loaded prompt" })
})
