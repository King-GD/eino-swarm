import { render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

vi.mock("@/lib/app-update", async () => {
  const actual = await vi.importActual<typeof import("@/lib/app-update")>("@/lib/app-update")
  return { ...actual, checkForAppUpdate: vi.fn() }
})

import { checkForAppUpdate, type UpdateOffer } from "@/lib/app-update"
import { setLocale, t } from "@/lib/i18n"

import { GatewayAccountScreen } from "./gateway-account-screen"
import type { GatewayAccountController } from "./gateway-account-shell"

const offer: UpdateOffer = {
  version: "2.4.0",
  pageURL: "https://github.com/LubyRuffy/eino-swarm/releases/tag/v2.4.0",
  apkURL: "https://github.com/LubyRuffy/eino-swarm/releases/download/v2.4.0/zwai-2.4.0-android.apk",
}

const account = {
  status: "signed_out",
  session: null,
  hosts: [],
  origin: "https://gateway.invalid",
  error: undefined,
  busy: false,
  login: vi.fn(),
  logout: vi.fn(async () => undefined),
  refresh: vi.fn(async () => undefined),
} as unknown as GatewayAccountController

describe("user center update offer", () => {
  beforeEach(() => {
    setLocale("en")
    vi.mocked(checkForAppUpdate).mockReset()
  })

  it("pins an available update above the account page", async () => {
    vi.mocked(checkForAppUpdate).mockResolvedValue(offer)
    render(<GatewayAccountScreen account={account} providers={[]} modelBusy={false} modelError={false} onConnect={async () => undefined} onRefresh={async () => undefined} />)
    const banner = await screen.findByTestId("app-update")
    const page = screen.getByTestId("gateway-account")
    const heading = screen.getByRole("heading", { name: "User center" })
    expect(page.contains(banner)).toBe(false)
    expect(banner.compareDocumentPosition(heading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(banner).toHaveTextContent(t("update.available", { version: offer.version }))
    expect(screen.getByRole("button", { name: "Sign in" })).toBeVisible()
  })
})
