import { useState } from "react"
import { UserRound } from "lucide-react"
import { acceptAvatarURL } from "@/lib/avatar-url"

export function AccountPortrait({ name, url }: { name: string; url?: string }) {
  const picture = acceptAvatarURL(url)
  const [failedPicture, setFailedPicture] = useState<string>()
  return <span aria-hidden="true" className="flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-full bg-muted text-sm font-medium">
    {picture && failedPicture !== picture ? <img src={picture} alt="" referrerPolicy="no-referrer" className="size-full object-cover" onError={() => setFailedPicture(picture)} /> : Array.from(name)[0] || <UserRound className="size-5" />}
  </span>
}
