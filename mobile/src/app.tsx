import { Capacitor } from "@capacitor/core"
import { useEffect } from "react"
import { GatewayAccountShell } from "@/components/gateway-account-shell"
import { PhoneApp } from "@/components/phone-app"
import { installIOSContentBack } from "@/lib/ios-content-back"

export function App() {
  useEffect(() => {
    if (Capacitor.getPlatform() === "ios") return installIOSContentBack()
  }, [])
  return <GatewayAccountShell><PhoneApp /></GatewayAccountShell>
}
