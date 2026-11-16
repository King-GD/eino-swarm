const otherWriting = /\p{Script=Han}|\p{Script=Hiragana}|\p{Script=Katakana}|\p{Script=Hangul}|\p{Script=Cyrillic}|\p{Script=Arabic}|\p{Script=Hebrew}|\p{Script=Greek}/u

/** Whether a Chinese interface should offer translation for this selection.
 *  One Latin letter is not a passage, and any other writing system means the
 *  selection is not English. */
export function englishSelection(text: string): boolean {
  const value = text.normalize("NFKC").trim()
  if (!value || otherWriting.test(value)) return false
  return (value.match(/[A-Za-z]/g)?.length ?? 0) >= 2
}
