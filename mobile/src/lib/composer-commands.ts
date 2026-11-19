import {
  completeSlashCommand,
  slashDraft,
  SLASH_COMMANDS,
  stripSlashToken,
  type SlashCommand,
} from "../../../frontend/src/lib/slash-core"

export type PhoneSlashCommand = SlashCommand & { id: "goal" | "plan" | "image" }

export const pairedSlashCommands = SLASH_COMMANDS.filter(
  (command): command is PhoneSlashCommand => command.id === "goal" || command.id === "plan" || command.id === "image",
)

export const directImageSlashCommands = pairedSlashCommands.filter((command) => command.id === "image")

export function applySlashPick(text: string, { name, leadingOnly }: SlashCommand): { text: string; caret: number } {
  const draft = slashDraft(text)
  if (leadingOnly && (!draft || draft.afterText)) {
    // The explicit pick puts the command in front and keeps the draft as its argument.
    const argument = draft ? stripSlashToken(text, draft).trimEnd() : text
    const next = `/${name} ${argument}`
    return { text: next, caret: next.length }
  }
  if (draft) {
    return {
      text: completeSlashCommand(text, draft, name),
      caret: draft.start + name.length + 2,
    }
  }
  const gap = text.length > 0 && !/\s$/u.test(text) ? " " : ""
  const next = `${text}${gap}/${name} `
  return { text: next, caret: next.length }
}
