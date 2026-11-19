import { describe, expect, it } from "vitest"

import { applySlashPick, phoneAddChoices, phoneSlashCatalog, phoneSlashChoices } from "./composer-commands"

const commands = phoneSlashCatalog.map((command) => ({ ...command, description: command.id }))

describe("phone slash commands", () => {
  it("offers goal and plan, which the engine accepts from a phone send", () => {
    expect(phoneSlashCatalog.map((command) => command.id)).toEqual(["goal", "plan"])
  })

  it("filters a typed name and hides plan once other text is present", () => {
    expect(phoneSlashChoices(commands, "", false).map((command) => command.id)).toEqual(["goal", "plan"])
    expect(phoneSlashChoices(commands, "pl", false).map((command) => command.id)).toEqual(["plan"])
    expect(phoneSlashChoices(commands, "nope", false)).toEqual([])
    expect(phoneAddChoices(commands, "already written").map((command) => command.id)).toEqual(["goal"])
    expect(phoneAddChoices(commands, "  ").map((command) => command.id)).toEqual(["goal", "plan"])
  })

  it("completes a token or inserts goal where the engine recognizes it", () => {
    expect(applySlashPick("/pl", "plan")).toEqual({ text: "/plan ", caret: 6 })
    expect(applySlashPick("keep going", "goal")).toEqual({ text: "keep going /goal ", caret: 17 })
    expect(applySlashPick("", "plan")).toEqual({ text: "/plan ", caret: 6 })
    expect(applySlashPick("abcdef", "goal")).toEqual({ text: "abcdef /goal ", caret: 13 })
    expect(applySlashPick("ab ", "goal")).toEqual({ text: "ab /goal ", caret: 9 })
  })
})
