import { AccountPortrait } from "@/components/account-portrait"
import { usePhoneAccount } from "@/components/gateway-account-shell"
import { Button } from "@/components/ui/button"
import { t } from "@/lib/i18n"

export function AccountButton({ onClick }: { onClick: () => void }) {
  const { user } = usePhoneAccount()
  return (
    <Button type="button" variant="ghost" className="size-9 shrink-0 px-0" aria-label={t("account.title")} onClick={() => onClick()}>
      <AccountPortrait name={user?.name ?? ""} url={user?.avatar} />
    </Button>
  )
}
