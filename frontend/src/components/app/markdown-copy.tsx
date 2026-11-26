import { CopyButton } from "@/components/app/copy-button"
import { useT } from "@/lib/use-t"

/** Copy the fence or formula source. Icon-only so the toolbar does not
 *  become a second caption; the accessible name says what it copies. It is
 *  the transcript's own copy control, so the copied state looks the same
 *  everywhere. */
export function MarkdownCopy({
  text,
  kind,
}: {
  text: string
  kind: "code" | "formula"
}) {
  const t = useT()
  const name = kind === "formula" ? t("markdown.copyFormula") : t("markdown.copyCode")
  return <CopyButton text={text} size="icon-xs" aria-label={name} />
}
