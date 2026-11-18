import { render, screen } from "@testing-library/react"
import { expect, it } from "vitest"
import { PhoneAccountContext } from "./gateway-account-shell"
import { PcConnectionGate } from "./pc-connection-gate"
import { setLocale } from "@/lib/i18n"

it("shows the selected PC throughout connection and readiness without flashing User center", () => {
  setLocale("en")
  const host = { fingerprint: "selected", label: "Selected PC", hubURL: "https://hub.invalid", ticket: "ticket", hostPub: "pub", sessionID: "session" }
  const page = (connecting: boolean, connected: boolean, ready: boolean) => (
    <PhoneAccountContext.Provider value={{ providers: [], mode: "account", empty: <p>User center page</p> }}>
      <PcConnectionGate hosts={[host]} activeFingerprint={host.fingerprint} connecting={connecting} connected={connected}
        ready={ready} onRetry={() => undefined} onSelectHost={() => undefined} onOpenChat={() => undefined}><p>PC inbox</p></PcConnectionGate>
    </PhoneAccountContext.Provider>
  )
  const view = render(page(true, false, false))
  expect(screen.getByRole("heading", { name: "Selected PC" })).toBeVisible()
  expect(screen.getByRole("tab", { name: "Chat" })).toBeVisible()
  expect(screen.queryByText("User center page")).not.toBeInTheDocument()
  view.rerender(page(false, true, false))
  expect(screen.getByTestId("pc-connection")).toBeVisible()
  expect(screen.queryByText("User center page")).not.toBeInTheDocument()
  view.rerender(page(false, true, true))
  expect(screen.getByText("PC inbox")).toBeVisible()
  expect(screen.queryByTestId("pc-connection")).not.toBeInTheDocument()
  view.rerender(page(false, false, false))
  expect(screen.getByText("User center page")).toBeVisible()
  expect(screen.getByRole("tab", { name: "Chat" })).toBeVisible()
})
