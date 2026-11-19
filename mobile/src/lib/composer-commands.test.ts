import { describe, expect, it } from "vitest"

import { applySlashPick, directImageSlashCommands, pairedSlashCommands } from "./composer-commands"

const goal = pairedSlashCommands.find((command) => command.id === "goal")!
const plan = pairedSlashCommands.find((command) => command.id === "plan")!
const image = directImageSlashCommands[0]!

describe("phone slash commands", () => {
  it("offers the commands each phone chat can execute", () => {
    expect(pairedSlashCommands.map((command) => command.id)).toEqual(["goal", "plan", "image"])
    expect(directImageSlashCommands.map((command) => command.id)).toEqual(["image"])
  })

  it("uses the existing draft as the argument when a leading command is picked", () => {
    expect(applySlashPick("/pl", plan)).toEqual({ text: "/plan ", caret: 6 })
    expect(applySlashPick("", plan)).toEqual({ text: "/plan ", caret: 6 })
    expect(applySlashPick("keep going /pl", plan)).toEqual({ text: "/plan keep going", caret: 16 })
    expect(applySlashPick("draw it", image)).toEqual({ text: "/image draw it", caret: 14 })
    expect(applySlashPick("draw it /ima", image)).toEqual({ text: "/image draw it", caret: 14 })
  })

  it("can still append goal where the engine recognizes it", () => {
    expect(applySlashPick("keep going", goal)).toEqual({ text: "keep going /goal ", caret: 17 })
    expect(applySlashPick("abcdef", goal)).toEqual({ text: "abcdef /goal ", caret: 13 })
    expect(applySlashPick("ab ", goal)).toEqual({ text: "ab /goal ", caret: 9 })
  })
})
