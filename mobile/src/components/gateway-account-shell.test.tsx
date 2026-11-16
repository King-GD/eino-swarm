import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "@/app"
import { GatewayAccountShell, usePhoneAccount } from "./gateway-account-shell"
import type { useGatewayAccount } from "@/lib/use-gateway-account"
import { saveThreads, newThread } from "@/lib/direct-threads"
import { openSaved } from "@/lib/client"
import { saveLink } from "@/lib/store"
import { saveProviders } from "@/lib/direct-provider"
import { GatewayError, saveGatewaySession } from "@/lib/gateway-account"
import { setLocale, t } from "@/lib/i18n"
import { MockHost, MockLink } from "@/lib/mock-link"

const mocks = vi.hoisted(() => ({ account: {} as ReturnType<typeof useGatewayAccount>, discover: vi.fn() }))
vi.mock("@/lib/client", async (original) => ({ ...await original<typeof import("@/lib/client")>(), openSaved: vi.fn() }))
vi.mock("@/lib/use-gateway-account", () => ({ useGatewayAccount: () => mocks.account }))
vi.mock("@/lib/openai-client", async (original) => ({ ...await original<typeof import("@/lib/openai-client")>(), discoverModels: mocks.discover }))

const session = { origin: "https://gateway.invalid", token: "agm_" + "a".repeat(64), user: { id: "owner", username: "User", approval_status: "approved" }, device: { id: "phone", expires_at: "2099-01-01T00:00:00Z" } }
const bootstrap = { user: session.user, device: session.device, models: { source_id: "gateway:owner", source_kind: "gateway" as const, label: "Team", execution: "phone" as const, api_base_url: "https://gateway.invalid/v1", catalog_url: "https://gateway.invalid/v1/models", api_access_enabled: true }, pairlink: { enabled: true, hub_url: session.origin, redeem_url: session.origin + "/pairlink/v1/pairings/redeem", websocket_url: "wss://gateway.invalid/pairlink/v1/ws", info_url: session.origin + "/pairlink/v1/info" }, heartbeat_interval: 30, presence_ttl: 90 }

beforeEach(() => {
  vi.mocked(openSaved).mockReset()
  setLocale("en")
  mocks.account = { status: "signed_out", session: null, bootstrap: undefined, hosts: [], origin: session.origin, error: undefined, busy: false, freshLogin: false, consumeFreshLogin: vi.fn(), setOrigin: vi.fn(), login: vi.fn(), refresh: vi.fn(async () => undefined), logout: vi.fn(async () => undefined), connectHost: vi.fn() }
  mocks.discover.mockReset().mockResolvedValue(["shared-model"])
})

describe("gateway account shell", () => {

  it.each([{}, { gatewayOrigin: session.origin, gatewayOwnerID: "other", gatewayMobileID: session.device.id }, { gatewayOrigin: session.origin, gatewayOwnerID: session.user.id, gatewayMobileID: "other" }])("never reconnects a saved PC outside the current account/device (%j)", async (metadata) => {
    saveLink({ hubURL: "https://relay.invalid", ticket: "ticket", hostPub: "pub", sessionID: "session", fingerprint: "local-pc", ...metadata })
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap }
    render(<App />)
    await screen.findByTestId("pc-wait")
    expect(openSaved).not.toHaveBeenCalled()
    expect(screen.queryByRole("tab", { name: "local-pc" })).not.toBeInTheDocument()
    expect(localStorage.getItem("zwai.remote.links")).toContain("ticket")
  })

  it("still dials a saved PC owned by the authorized account and this phone", async () => {
    const saved = { hubURL: session.origin, ticket: "ticket", hostPub: "pub", sessionID: "session", fingerprint: "account-pc", gatewayOrigin: session.origin, gatewayOwnerID: session.user.id, gatewayMobileID: session.device.id, gatewayHostID: "host" }
    saveLink(saved)
    saveGatewaySession(session)
    vi.mocked(openSaved).mockRejectedValue(new Error("fixture stops after observing dial"))
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap }
    render(<App />)
    await waitFor(() => expect(openSaved).toHaveBeenCalledOnce())
    expect(openSaved).toHaveBeenCalledWith(saved)
  })

  it("preserves the account conversation draft across a disconnected transport and its replacement", async () => {
    const host = new MockHost(0), first = new MockLink(host)
    let reconnect!: () => void
    saveLink({ hubURL: session.origin, ticket: "ticket", hostPub: "pub", sessionID: "session", fingerprint: "account-pc", gatewayOrigin: session.origin, gatewayOwnerID: session.user.id, gatewayMobileID: session.device.id, gatewayHostID: "host" })
    saveGatewaySession(session)
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap }
    vi.mocked(openSaved).mockResolvedValueOnce(first as never).mockImplementationOnce(() => new Promise(resolve => { reconnect = () => resolve(new MockLink(host) as never) }))
    const page = render(<App />)
    try {
      const input = await screen.findByRole("textbox", { name: /^Message$/ })
      fireEvent.change(input, { target: { value: "Continue after the computer reconnects" } })
      const history = screen.getByTestId("transcript").textContent
      act(() => { first.close(); first.onDisconnect?.(new Error("offline")) })
      expect(await screen.findByTestId("pc-wait")).toBeVisible()
      expect(screen.queryByRole("textbox", { name: /^Message$/ })).not.toBeInTheDocument()
      expect(input).not.toBeVisible()
      await act(async () => reconnect())
      await waitFor(() => expect(input).toBeVisible())
      expect(screen.getByRole("textbox", { name: /^Message$/ })).toBe(input)
      expect(input).toHaveValue("Continue after the computer reconnects")
      expect(screen.getByTestId("transcript").textContent).toBe(history)
      expect(mocks.discover).not.toHaveBeenCalled()
    } finally { page.unmount(); host.stop() }
  })

  it("revokes the main UI immediately on logout and retains the default gate after remount", async () => {
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap }
    const view = render(<App />)
    await screen.findByTestId("pc-wait")
    mocks.account = { ...mocks.account, status: "signed_out", session: null, bootstrap: undefined }
    view.rerender(<App />)
    expect(screen.queryByRole("tab", { name: "Chat" })).not.toBeInTheDocument()
    expect(screen.getByTestId("gateway-account")).toBeVisible()
    view.unmount()
    render(<App />)
    expect(screen.queryByLabelText("Message")).not.toBeInTheDocument()
  })

  it.each([0, 1])("counts touch releases once when the browser synthesizes clicks with detail=%i", (detail) => {
    render(<App />)
    const portrait = screen.getByTestId("account-portrait")
    const tap = () => {
      const event = new Event("pointerup", { bubbles: true })
      Object.defineProperties(event, { pointerType: { value: "touch" }, isPrimary: { value: true } })
      fireEvent(portrait, event)
      fireEvent.click(portrait, { detail })
    }
    tap(); tap()
    expect(localStorage.getItem("zwai.phone.mode")).toBeNull()
    tap()
    expect(localStorage.getItem("zwai.phone.mode")).toBe("local")
  })

  it("closes User center after the debug mode changes and keeps the account signed in", async () => {
    localStorage.setItem("zwai.phone.mode", "local")
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap }
    function Menu() {
      const { openAccount, mode } = usePhoneAccount()
      return <button data-mode={mode} onClick={() => openAccount?.()}>Open user center</button>
    }
    render(<GatewayAccountShell><Menu /></GatewayAccountShell>)
    fireEvent.click(screen.getByRole("button", { name: "Open user center" }))
    const dialog = screen.getByRole("dialog", { name: "User center" })
    const portrait = within(dialog).getByTestId("account-portrait")
    fireEvent.click(portrait); fireEvent.click(portrait); fireEvent.click(portrait)
    expect(localStorage.getItem("zwai.phone.mode")).toBe("account")
    expect(screen.queryByRole("dialog", { name: "User center" })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Open user center" })).toHaveAttribute("data-mode", "account")
    expect(mocks.account.logout).not.toHaveBeenCalled()
    expect(mocks.discover).toHaveBeenCalledOnce()
  })

  it("expires an incomplete avatar gesture and permits an explicit Local choice from the login screen", () => {
    vi.useFakeTimers()
    try {
      render(<App />)
      const portrait = screen.getByTestId("account-portrait")
      fireEvent.click(portrait); fireEvent.click(portrait)
      act(() => { vi.advanceTimersByTime(1201) })
      fireEvent.click(portrait)
      expect(localStorage.getItem("zwai.phone.mode")).toBeNull()
      fireEvent.click(portrait); fireEvent.click(portrait)
      expect(localStorage.getItem("zwai.phone.mode")).toBe("local")
      expect(screen.queryByTestId("gateway-account")).not.toBeInTheDocument()
    } finally { vi.useRealTimers() }
  })
  it.each(["signed_out", "expired", "error", "access_denied"] as const)("%s blocks saved personal models by default", (status) => {
    saveProviders([{ id: "personal", label: "Personal", baseURL: "https://private.invalid/v1", apiKey: "secret", api: "chat", model: "private", catalog: [], timeoutSeconds: 300 }])
    mocks.account = { ...mocks.account, status }
    render(<App />)
    expect(screen.getByTestId("gateway-account")).toBeVisible()
    expect(screen.queryByLabelText("Message")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Menu" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument()
  })

  it("hides local menus and switches mode after three portrait clicks", async () => {
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap }
    render(<App />)
    await screen.findByTestId("pc-wait")
    expect(screen.queryByRole("button", { name: "Model" })).not.toBeInTheDocument()
    for (const name of ["Models", "Add a PC", "Unlink"]) expect(screen.queryByRole("menuitem", { name })).not.toBeInTheDocument()
    const portrait = screen.getByTestId("account-portrait")
    fireEvent.click(portrait); fireEvent.click(portrait)
    expect(localStorage.getItem("zwai.phone.mode")).toBeNull()
    fireEvent.click(portrait)
    expect(localStorage.getItem("zwai.phone.mode")).toBe("local")
    expect(screen.getByRole("button",{name:"Other ways"})).toBeVisible()
    fireEvent.click(screen.getByTestId("account-portrait"));fireEvent.click(screen.getByTestId("account-portrait"));fireEvent.click(screen.getByTestId("account-portrait"))
    expect(localStorage.getItem("zwai.phone.mode")).toBe("account")
  })

  it.each([
    ["en", "browser_unavailable", "Could not open the sign-in browser. Try again."],
    ["en", "gateway_timeout", "The account operation timed out. Try again."],
    ["zh", "browser_unavailable", "无法打开登录浏览器，请重试。"],
    ["zh", "gateway_timeout", "账号操作超时，请重试。"],
  ] as const)("describes %s %s as an account retry instead of a server rejection", (locale, error, message) => {
    setLocale(locale)
    mocks.account = { ...mocks.account, status: "error", error }
    render(<App />)
    fireEvent.click(screen.getByRole("button", { name: t("account.login") }))
    expect(screen.getByRole("alert")).toHaveTextContent(message)
    expect(screen.queryByText(t("chat.failed"))).not.toBeInTheDocument()
  })

  it.each(["account_pending", "account_rejected", "access_denied"] as const)("%s exposes only account review, even with private models stored", async (status) => {
    saveProviders([{ id: "p-private", label: "Personal", baseURL: "https://private.invalid/v1", apiKey: "secret", api: "chat", model: "private-model", catalog: [], timeoutSeconds: 300 }])
    mocks.account = { ...mocks.account, status, session }
    render(<App />)
    expect(screen.getByRole("heading", { name: "User center" })).toBeVisible()
    expect(screen.queryByRole("button", { name: "Menu" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Scan QR" })).not.toBeInTheDocument()
    expect(screen.queryByLabelText("Message")).not.toBeInTheDocument()
    expect(screen.queryByRole("heading", { name: "Your computers" })).not.toBeInTheDocument()
    expect(screen.queryByLabelText("Phone chat Web tools")).not.toBeInTheDocument()
    expect(screen.queryByLabelText("PC conversation Web tools")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument()
    expect(mocks.discover).not.toHaveBeenCalled()
    expect(localStorage.getItem("zwai.phone.providers")).toContain("private-model")
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }))
    expect(mocks.account.logout).toHaveBeenCalledOnce()
  })

  it("keeps device management accessible when API access is disabled and no PCs are connected", async () => {
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap: { ...bootstrap, models: { ...bootstrap.models, api_access_enabled: false } } }
    render(<App />)
    expect(screen.getByRole("heading", { name: "User center" })).toBeVisible()
    expect(screen.getByRole("heading", { name: "Your computers" })).toBeVisible()
    expect(screen.getByTestId("pc-wait")).toBeVisible()
    expect(screen.queryByRole("button", { name: "Scan QR" })).not.toBeInTheDocument()
    expect(mocks.discover).not.toHaveBeenCalled()
  })

  it("uses verified account and PC names without exposing legacy identifiers", () => {
    const username = "nosec_Old_Name_0123456789abcdef"
    const fingerprint = "8cb10e221045cd61"
    const alias = "7db10e221045cd62"
    mocks.account = {
      ...mocks.account,
      status: "authorized",
      session: { ...session, user: { ...session.user, username, display_name: "  林小明  " } },
      bootstrap: { ...bootstrap, models: { ...bootstrap.models, api_access_enabled: false } },
      hosts: [
        { id: "pc-online", name: "open-abcdef0123456789", hostname: "desk-host", display_name: "  Office Mac  ", version: "1", online: true, enabled: true },
        { id: "pc-legacy", name: "open-abcdef0123456788", hostname: "  Home Mac  ", version: "1", online: false, enabled: true },
        { id: "32c7e49348b6077123aa38a120177553", name: "open-abcdef0123456787", hostname: "", version: "1", online: false, enabled: true },
        { id: "pc-fingerprint", name: fingerprint, hostname: "", host_fp: fingerprint, version: "1", online: false, enabled: true },
        { id: "pc-alias", name: alias, hostname: "", host_fp: "9cb10e221045cd63", version: "1", online: false, enabled: true },
      ],
    }
    render(<App />)
    expect(screen.getByText("林小明", { exact: true })).toBeVisible()
    expect(document.querySelector("img")).toBeNull()
    expect(screen.getByText("林", { exact: true })).toBeVisible()
    expect(screen.getByRole("button", { name: "Connect Office Mac" })).toBeEnabled()
    expect(screen.getByRole("button", { name: "Connect Home Mac" })).toBeDisabled()
    expect(screen.getAllByRole("button", { name: "Connect Unnamed PC" })).toHaveLength(2)
    expect(screen.getByRole("button", { name: "Connect " + alias })).toBeDisabled()
    expect(document.body.textContent).not.toContain(username)
    expect(document.body.textContent).not.toContain("open-abcdef012345678")
    expect(document.body.textContent).not.toContain("32c7e49348b6077123aa38a120177553")
    expect(document.body.textContent).not.toContain(fingerprint)
  })

  it("shows a public avatar without sending a referrer and falls back to the initial", () => {
    const picture = "https://images.fofa.info/a.png?v=1"
    mocks.account = {
      ...mocks.account, status: "authorized",
      session: { ...session, user: { ...session.user, display_name: "林小明", avatar_url: picture } },
      bootstrap: { ...bootstrap, models: { ...bootstrap.models, api_access_enabled: false } },
    }
    const view = render(<App />)
    const image = document.querySelector("img")
    expect(image).toHaveAttribute("src", picture)
    expect(image).toHaveAttribute("referrerpolicy", "no-referrer")
    fireEvent.error(image!)
    expect(document.querySelector("img")).toBeNull()
    expect(screen.getByText("林", { exact: true })).toBeVisible()
    expect(screen.getByText("林小明", { exact: true })).toBeVisible()
    view.unmount()
    mocks.account = {
      ...mocks.account,
      session: { ...session, user: { ...session.user, display_name: "林小明", avatar_url: "javascript:alert(1)" } },
    }
    render(<App />)
    expect(document.querySelector("img")).toBeNull()
    expect(screen.getByText("林", { exact: true })).toBeVisible()
  })

  it.each([
    ["nosec_旧用户名_0123456789abcdef", "旧用户名"],
    ["nosec___0123456789abcdef", "Account"],
    ["nosec_user_123", "nosec_user_123"],
  ])("formats only known synthetic legacy usernames (%s)", (username, expected) => {
    mocks.account = { ...mocks.account, status: "account_pending", session: { ...session, user: { ...session.user, username } } }
    render(<App />)
    expect(screen.getByText(expected, { exact: true })).toBeVisible()
    if (username !== expected) expect(screen.queryByText(username, { exact: true })).not.toBeInTheDocument()
  })

  it("retains direct history without exposing or executing it in gateway mode", async () => {
    const own = newThread({providerId:"gateway:owner",model:"shared-model",reasoning:""}); own.title="Own history"
    saveThreads([own])
    mocks.account={...mocks.account,status:"authorized",session,bootstrap}
    render(<App />)
    expect(await screen.findByTestId("pc-wait")).toBeVisible()
    expect(screen.queryByRole("button",{name:"Chat history"})).not.toBeInTheDocument()
    expect(screen.queryByLabelText("Message")).not.toBeInTheDocument()
    expect(localStorage.getItem("zwai.phone.directThreads")).toContain("Own history")
    expect(mocks.discover).not.toHaveBeenCalled()
  })

  it("requires a PC despite a usable gateway catalog and ignores direct execution events", async () => {
    mocks.account={...mocks.account,status:"authorized",session,bootstrap}
    render(<App />)
    expect(await screen.findByTestId("pc-wait")).toBeVisible()
    for(const name of ["chat","models","scan"]) await act(async()=>{window.dispatchEvent(new Event("zwai.gateway."+name))})
    expect(screen.queryByLabelText("Message")).not.toBeInTheDocument()
    expect(screen.queryByRole("dialog",{name:"Models"})).not.toBeInTheDocument()
    expect(mocks.discover).not.toHaveBeenCalled()
    expect(localStorage.getItem("zwai.phone.providers")).toBeNull()
    expect(document.body.textContent).not.toContain(session.token)
  })

  it("shows the signed-in Gateway models beside personal models in LOCAL", async () => {
    localStorage.setItem("zwai.phone.mode", "local")
    saveGatewaySession(session)
    saveProviders([{ id: "private", label: "Personal", baseURL: "https://private.invalid/v1", apiKey: "private-secret", api: "chat", model: "shared-model", catalog: ["shared-model"], timeoutSeconds: 300 }])
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap }
    render(<App />)
    fireEvent.click(await screen.findByRole("button", { name: /^Model$/ }))
    expect(await screen.findByRole("heading", { name: "Gateway · Team" })).toBeVisible()
    expect(screen.getByRole("heading", { name: "Personal" })).toBeVisible()
    expect(screen.getAllByRole("radio", { name: "shared-model" })).toHaveLength(2)
    expect(localStorage.getItem("zwai.phone.providers")).not.toContain(session.token)
  })

  it("groups a retained third-party profile in LOCAL without importing its credential", async () => {
    localStorage.setItem("zwai.phone.mode","local")
    saveGatewaySession(session)
    mocks.account={...mocks.account,status:"authorized",session,bootstrap:{...bootstrap,models:{...bootstrap.models,profile:{label:"Partner model",tools:["read"]}}}}
    render(<App />)
    fireEvent.click(await screen.findByRole("button",{name:/^Model$/}))
    expect(await screen.findByRole("heading",{name:"Third-party · Partner model"})).toBeVisible()
    expect(screen.getByRole("radio",{name:"Partner model"})).toBeVisible()
    expect(mocks.discover).not.toHaveBeenCalled()
    expect(localStorage.getItem("zwai.phone.providers")).toBeNull()
    expect(screen.queryByRole("radio",{name:"All models"})).not.toBeInTheDocument()
  })

  it("opens default-server sign-in without asking for server configuration and Android Back closes its overlay", async () => {
    localStorage.setItem("zwai.phone.mode", "local")
    render(<App />)
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }))
    expect(screen.getByTestId("gateway-account")).toBeVisible()
    expect(screen.queryByLabelText("Server address")).not.toBeInTheDocument()
    expect(screen.getByLabelText("Phone chat Web tools")).toHaveValue("auto")
    expect(screen.getByLabelText("PC conversation Web tools")).toHaveValue("inherit")
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }))
    expect(mocks.account.login).toHaveBeenCalledOnce()
    await act(async () => { expect(window.__zwaiAndroidBack?.()).toBe(true) })
    expect(screen.queryByTestId("gateway-account")).not.toBeInTheDocument()
    expect(window.__zwaiAndroidBack?.()).toBe(false)
  })

  it("keeps an account PC going offline during selection on the account refresh path", async () => {
    mocks.account = { ...mocks.account, status: "authorized", session,
      bootstrap: { ...bootstrap, models: { ...bootstrap.models, api_access_enabled: false } },
      hosts: [{ id: "pc-online", name: "Work PC", hostname: "work", version: "1", online: true, enabled: true }],
      connectHost: vi.fn(async () => { throw new GatewayError(409, "host_offline") }),
    }
    render(<App />)
    fireEvent.click(screen.getByRole("button", { name: "Connect Work PC" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("This PC is offline. Wait for the desktop to reconnect, then refresh and try again.")
    expect(screen.queryByText(/Show a new QR/)).not.toBeInTheDocument()
  })

  it("lists same-account PCs, disables offline PCs and connects only the selected online PC without scanning", async () => {
    mocks.account = { ...mocks.account, status: "authorized", session, bootstrap, hosts: [
      { id: "pc-online", name: "Work PC", hostname: "work", version: "1", online: true, enabled: true },
      { id: "pc-offline", name: "Home PC", hostname: "home", version: "1", online: false, enabled: true },
    ], connectHost: vi.fn(async () => { throw new Error("fixture keeps the account page visible") }) }
    render(<App />)
    await screen.findByTestId("pc-wait")
    await screen.findByTestId("pc-wait")
    expect(screen.getByRole("button", { name: "Connect Home PC" })).toBeDisabled()
    fireEvent.click(screen.getByRole("button", { name: "Connect Work PC" }))
    await waitFor(() => expect(mocks.account.connectHost).toHaveBeenCalledWith("pc-online"))
    expect(mocks.account.connectHost).toHaveBeenCalledOnce()
    expect(screen.queryByRole("button", { name: "Scan QR" })).not.toBeInTheDocument()
  })
})
