import {
  completeSlashCommand,
  filterSlashCommands,
  slashDraft,
  SLASH_COMMANDS,
  type SlashCommand,
} from "../../../frontend/src/lib/slash-core"

/** Goal and plan travel as the message text, and the engine accepts them.
 *  Compact is a PC-local action, so the phone does not offer it. */
export const phoneSlashCatalog: readonly SlashCommand[] = SLASH_COMMANDS.filter(
  (command) => command.id === "goal" || command.id === "plan",
)

export function phoneSlashChoices(
  commands: readonly SlashCommand[],
  query: string,
  afterText: boolean,
): SlashCommand[] {
  return filterSlashCommands(query, commands, afterText)
}

/** Commands the add menu can insert into the current draft. Plan only starts
 *  a message, so it stays out once other text is already there. */
export function phoneAddChoices(commands: readonly SlashCommand[], text: string): SlashCommand[] {
  return filterSlashCommands("", commands, text.trim() !== "")
}

export function applySlashPick(text: string, name: string): { text: string; caret: number } {
  const draft = slashDraft(text)
  if (draft) {
    return {
      text: completeSlashCommand(text, draft, name),
      caret: draft.start + name.length + 2,
    }
  }
  if (name === "plan") return { text: `/plan ${text}`, caret: 6 }
  const gap = text.length > 0 && !/\s$/u.test(text) ? " " : ""
  const next = `${text}${gap}/${name} `
  return { text: next, caret: next.length }
}
