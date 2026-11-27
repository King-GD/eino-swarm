import { HomeEarth } from "./home-earth"
import { FileText, Layers, Search } from "lucide-react"

import { chromeTypeClass } from "@/lib/chrome-type"
import { cn } from "@/lib/utils"
import { useDesktopAccount } from "@/lib/use-remote-status"
import { remoteAccountName } from "@/lib/remote-account-name"
import { useT } from "@/lib/use-t"

/** Shown before the first message. Its job is to answer "what do I type?",
 *  so the examples describe shapes of work rather than a canned prompt. */
export function EmptyState({ onPick }: { onPick: (text: string) => void }) {
  const t = useT()
  const { status, unavailable } = useDesktopAccount()
  const login = status?.auth_mode === "account" ? status.login : undefined
  const name = !unavailable && login?.state === "authorized" ? remoteAccountName(login) : ""
  const ideas = [
    {
      icon: Search,
      title: t("empty.research.title"),
      hint: t("empty.research.hint"),
      text: t("empty.research.text"),
    },
    {
      icon: FileText,
      title: t("empty.files.title"),
      hint: t("empty.files.hint"),
      text: t("empty.files.text"),
    },
    {
      icon: Layers,
      title: t("empty.split.title"),
      hint: t("empty.split.hint"),
      text: t("empty.split.text"),
    },
  ]

  return (
    <section className="wb-welcome" aria-labelledby="welcome-title">
      <div className="wb-intro wb-home-column">
        <div className="wb-heading">
          <h1 id="welcome-title"><span className="home-title-gradient">{name ? t("empty.greeting", { name }) : t("empty.title")}</span></h1>
        </div>
      </div>
      <div className="wb-ideas wb-home-column">
        {ideas.map(({ icon: Icon, title, hint, text }) => (
          <button key={title} type="button" onClick={() => onPick(text)}
            title={hint} className={cn(chromeTypeClass, "wb-idea")}>
            <span className="wb-idea-icon"><Icon aria-hidden="true" size={18} /></span>
            <span>{title}</span>
          </button>
        ))}
      </div>
      <HomeEarth />
    </section>
  )
}
