import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "@/app"
import type { useGatewayAccount } from "@/lib/use-gateway-account"
import { saveLink } from "@/lib/store"
import { setLocale, t } from "@/lib/i18n"

const mocks = vi.hoisted(() => ({ account: {} as ReturnType<typeof useGatewayAccount>, discover: vi.fn() }))
vi.mock("@/lib/use-gateway-account", () => ({ useGatewayAccount: () => mocks.account }))
vi.mock("@/lib/openai-client", async (original) => ({ ...await original<typeof import("@/lib/openai-client")>(), discoverModels: mocks.discover }))

const session = { origin: "https://gateway.invalid", token: "agm_" + "a".repeat(64), user: { id: "owner", username: "User", approval_status: "approved" }, device: { id: "phone", expires_at: "2099-01-01T00:00:00Z" } }
const bootstrap = { user: session.user, device: session.device, models: { source_id: "gateway:owner", source_kind: "gateway" as const, label: "Team", execution: "phone" as const, api_base_url: "https://gateway.invalid/v1", catalog_url: "https://gateway.invalid/v1/models", api_access_enabled: true }, pairlink: { enabled: true, hub_url: session.origin, redeem_url: session.origin + "/pairlink/v1/pairings/redeem", websocket_url: "wss://gateway.invalid/pairlink/v1/ws", info_url: session.origin + "/pairlink/v1/info" }, heartbeat_interval: 30, presence_ttl: 90 }
const noModels = { ...bootstrap, models: { ...bootstrap.models, api_access_enabled: false } }
const pc = (id: string, name: string, online = true) => ({ id, name, hostname: name, version: "1", online, enabled: true })

// The page's own listener is registered on mount, after this one, so stopping
// propagation keeps the fixture from booting a real connection to a fake PC.
const opened: string[] = []
const record = (event: Event) => { opened.push((event as CustomEvent<string>).detail); event.stopImmediatePropagation() }

beforeEach(() => {
  setLocale("en")
  opened.length = 0
  window.addEventListener("zwai.gateway.connect", record)
  mocks.account = { status: "signed_out", session: null, bootstrap: undefined, hosts: [], origin: session.origin, error: undefined, busy: false, freshLogin: false, consumeFreshLogin: vi.fn(), setOrigin: vi.fn(), login: vi.fn(), refresh: vi.fn(async () => undefined), logout: vi.fn(async () => undefined), connectHost: vi.fn() }
  mocks.discover.mockReset().mockResolvedValue(["shared-model"])
})
afterEach(() => window.removeEventListener("zwai.gateway.connect", record))

const signedIn = (patch: Partial<ReturnType<typeof useGatewayAccount>>) => {
  mocks.account = { ...mocks.account, status: "authorized", session, bootstrap, ...patch }
}

describe("automatically connecting account PCs", () => {
  const prepared = (id: string, options?: { select?: boolean }) => {
    const saved = { fingerprint: "fp-" + id, gatewayHostID: id, gatewayOrigin: session.origin,
      gatewayOwnerID: session.user.id, gatewayMobileID: session.device.id, hubURL: session.origin,
      ticket: id, hostPub: id, sessionID: id }
    saveLink(saved, options?.select !== false)
    return saved
  }
  it.each([true, false])("automatically prepares all online PCs for freshLogin=%s", async freshLogin => {
    signedIn({ hosts: [pc("first", "Work PC"), pc("second", "Lab PC"), pc("offline", "Home PC", false)],
      freshLogin, connectHost: vi.fn(async (id: string, options?: { select?: boolean }) => prepared(id, options)) })
    const { rerender } = render(<App />)
    await waitFor(() => expect(mocks.account.connectHost).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(opened).toHaveLength(1))
    expect(mocks.account.connectHost).toHaveBeenCalledWith("first", { select: false })
    expect(mocks.account.connectHost).toHaveBeenCalledWith("second", { select: false })
    rerender(<App />)
    expect(mocks.account.connectHost).toHaveBeenCalledTimes(2)
  })
  it.each([{ hosts: [] }, { hosts: [pc("offline", "Home PC", false)] }])("does not dial unavailable PCs: %j", async ({ hosts }) => {
    signedIn({ hosts })
    render(<App />)
    await screen.findByTestId("pc-wait")
    expect(mocks.account.connectHost).not.toHaveBeenCalled()
    expect(opened).toEqual([])
  })
  it("waits for the authorized bootstrap before preparing credentials", () => {
    signedIn({ hosts: [pc("first", "Work PC")], bootstrap: undefined })
    render(<App />)
    expect(mocks.account.connectHost).not.toHaveBeenCalled()
  })
  it("keeps the PC list available when automatic preparation fails", async () => {
    signedIn({ bootstrap: noModels, hosts: [pc("first", "Work PC")], connectHost: vi.fn(async () => { throw new Error("offline") }) })
    render(<App />)
    await waitFor(() => expect(mocks.account.connectHost).toHaveBeenCalledOnce())
    expect(opened).toEqual([])
    expect(screen.getByRole("button", { name: "Connect Work PC" })).toBeEnabled()
  })
})

describe("the signed-in start page", () => {
  it("keeps pairing by QR and a model service of one's own under Other ways in Local mode", async () => {
    localStorage.setItem("zwai.phone.mode", "local")
    signedIn({ bootstrap: noModels, hosts: [] })
    render(<App />)
    expect(screen.getByRole("heading", { name: t("account.pcs") })).toBeVisible()
    expect(screen.queryByRole("button", { name: t("home.addHost") })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: t("welcome.other") }))
    fireEvent.click(screen.getByRole("button", { name: t("chat.connect") }))
    expect(await screen.findByRole("dialog", { name: t("chat.add") })).toBeVisible()
    fireEvent.click(within(screen.getByRole("dialog", { name: t("chat.add") })).getAllByRole("button", { name: t("home.close") })[0])
    fireEvent.click(screen.getByRole("button", { name: t("home.addHost") }))
    expect(await screen.findByRole("dialog", { name: t("home.addHost") })).toBeVisible()
    expect(screen.getByLabelText(t("scan.uri"))).toBeVisible()
  })

  it("keeps alternate configuration out of the gateway waiting page", async () => {
    signedIn({ hosts: [pc("pc-online", "Work PC")], freshLogin: false })
    render(<App />)
    await screen.findByTestId("pc-wait")
    expect(screen.queryByRole("tab", {name:"Chat"})).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Connect Work PC" })).toBeVisible()
    expect(screen.queryByRole("button", { name: t("welcome.other") })).not.toBeInTheDocument()
  })
})
