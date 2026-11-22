import { beforeEach } from "vitest"
import { savePhoneMode } from "@/lib/phone-mode"
beforeEach(() => savePhoneMode("local"))
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { openSaved, type DeviceLink } from "@/lib/client"
import { t } from "@/lib/i18n"
import {
  OpClientRead,
  OpClients,
  OpHello,
  OpList,
  OpLog,
  OpMore,
  OpOpen,
  OpDeliver,
  OpMeta,
  OpRefreshCapabilities,
  OpSend,
  OpStart,
  OpUnwatch,
  OpWatch,
  type RemoteRequest,
  type RemoteResponse,
} from "@/lib/rpc"
import { saveProviders } from "@/lib/direct-provider"
import { saveActiveFingerprint, saveLastThreadId, saveLink } from "@/lib/store"

vi.mock("@/lib/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/client")>()
  return { ...actual, openSaved: vi.fn() }
})

import { App } from "./app"
import { PhoneApp } from "./components/phone-app"
import { saveGatewaySession } from "./lib/gateway-account"
import { upgradeAnonymousGatewayLinks } from "./lib/gateway-link-upgrade"

function seedLink() {
  saveLink({
    hubURL: "http://127.0.0.1:9",
    ticket: "tick",
    hostPub: "pub",
    sessionID: "sid",
    fingerprint: "fp",
  })
}

describe("App boot chrome", () => {
  afterEach(() => {
    cleanup()
    vi.mocked(openSaved).mockReset()
  })

  it("does not offer an app update in the browser", () => {
    render(<App />)
    expect(screen.queryByRole("button", { name: t("update.upgrade") })).not.toBeInTheDocument()
  })

  it("shows a chat tab instead of the scan form once a model is saved", async () => {
    saveProviders([
      {
        id: "p-1",
        label: "Desk",
        baseURL: "https://endpoint.invalid/v1",
        apiKey: "",
        api: "chat",
        model: "one",
        catalog: ["one"],
        timeoutSeconds: 300,
      },
    ])
    render(<App />)
    expect(screen.getByRole("tab", { name: t("chat.tab") })).toHaveAttribute("aria-selected", "true")
    expect(screen.queryByRole("button", { name: t("scan.camera") })).not.toBeInTheDocument()
    fireEvent.focus(screen.getByLabelText(t("thread.message")))
    expect(screen.getByRole("button", { name: t("composer.model") })).toHaveAccessibleDescription("one")
    expect(screen.getByLabelText(t("composer.thinking"))).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: t("composer.add") }))
    expect(screen.getByRole("menuitem", { name: t("composer.attach") })).toBeInTheDocument()
    expect(screen.queryByTestId("composer-command-goal")).not.toBeInTheDocument()
  })

  it("opens on sign-in when this phone has never bound, with QR pairing one tap away", () => {
    render(<App />)
    expect(screen.getByRole("heading", { name: t("welcome.title") })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: t("account.login") })).toBeInTheDocument()
    // Pairing is not removed, only folded: it must not compete with the account.
    expect(screen.queryByRole("button", { name: t("scan.camera") })).not.toBeInTheDocument()
    expect(screen.queryByLabelText(t("scan.uri"))).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: t("welcome.other") }))
    expect(screen.getByRole("button", { name: t("scan.camera") })).toBeInTheDocument()
    expect(screen.getByLabelText(t("scan.uri"))).toBeInTheDocument()
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument()
    expect(systemBack()).toBe(false)
  })

  it("paints host tabs and a connecting inbox when a ticket is saved", () => {
    seedLink()
    vi.mocked(openSaved).mockReturnValue(new Promise(() => undefined))
    render(<App />)
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
    expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true")
    expect(screen.getByText(t("scan.connecting"))).toBeInTheDocument()
    expect(screen.queryByRole("heading", { name: t("scan.title") })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: t("scan.camera") })).not.toBeInTheDocument()
  })

  it("stays off the scan form after a failed restore so a bound phone is not mistaken for unbound", async () => {
    seedLink()
    vi.mocked(openSaved).mockRejectedValue(new Error("offline"))
    render(<App />)
    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toBe(
        t("err.net.closed", { reason: "offline" }),
      )
    })
    expect(screen.getByRole("tablist")).toBeInTheDocument()
    expect(screen.queryByRole("heading", { name: t("scan.title") })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: t("scan.retry") }))
    await waitFor(() => {
      expect(vi.mocked(openSaved).mock.calls.length).toBeGreaterThanOrEqual(2)
    })
  })

  it("returns to scan after unlink from a failed restore", async () => {
    seedLink()
    vi.mocked(openSaved).mockRejectedValue(new Error("offline"))
    render(<App />)
    await waitFor(() => {
      expect(screen.getByRole("button", { name: t("home.menu") })).toBeEnabled()
    })
    fireEvent.click(screen.getByRole("button", { name: t("home.menu") }))
    fireEvent.click(screen.getByRole("menuitem", { name: t("home.unlink") }))
    expect(screen.getByRole("heading", { name: t("welcome.title") })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: t("welcome.other") })).toHaveAttribute("aria-expanded", "false")
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument()
  })

  it("opens add as a side sheet over the inbox", async () => {
    seedLink()
    vi.mocked(openSaved).mockReturnValue(new Promise(() => undefined))
    render(<App />)
    openAddHost()
    expect(screen.getByRole("dialog", { name: t("home.addHost") })).toBeInTheDocument()
    expect(screen.getByRole("tablist")).toBeInTheDocument()
    expect(screen.getByText(t("scan.connecting"))).toBeInTheDocument()
    expect(screen.getByRole("button", { name: t("scan.camera") })).toBeEnabled()
  })

  it("system back closes Add a PC and stays on the inbox", async () => {
    seedLink()
    vi.mocked(openSaved).mockReturnValue(new Promise(() => undefined))
    render(<App />)
    openAddHost()
    expect(screen.getByRole("dialog", { name: t("home.addHost") })).toBeInTheDocument()
    let stayed = false
    await act(async () => {
      stayed = systemBack()
    })
    expect(stayed).toBe(true)
    expect(screen.queryByRole("dialog", { name: t("home.addHost") })).not.toBeInTheDocument()
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
    expect(systemBack()).toBe(false)
  })

  it("keeps a restore error on the inbox when Add a PC is open", async () => {
    seedLink()
    vi.mocked(openSaved).mockRejectedValue(new Error("offline"))
    render(<App />)
    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toBe(
        t("err.net.closed", { reason: "offline" }),
      )
    })
    openAddHost()
    const dialog = screen.getByRole("dialog", { name: t("home.addHost") })
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: t("scan.camera") })).toBeEnabled()
    expect(screen.getByRole("alert").textContent).toBe(
      t("err.net.closed", { reason: "offline" }),
    )
  })

  it("says the PC may be off when the socket never opens", async () => {
    seedLink()
    vi.mocked(openSaved).mockRejectedValue(new Error("ws error"))
    render(<App />)
    const down = t("err.net.down", { reason: "ws error" })
    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toBe(down)
    })
    expect(down).not.toBe(t("err.remote", { detail: "ws error" }))
    expect(screen.getByLabelText("path=offline").className).toContain("bg-muted-foreground")
    expect(screen.getByLabelText("path=offline").className).not.toContain("--online")
  })

  it("labels a server refusal instead of a dropped socket", async () => {
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        list: async () => ({ v: 1, id: "l", ok: false, error: "quota" }),
      }),
    )
    render(<App />)
    const remote = t("err.remote", { detail: "quota" })
    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toBe(remote)
    })
    expect(remote).not.toBe(t("err.net.down", { reason: "quota" }))
    expect(screen.getByLabelText("path=relay").className).toContain("bg-[hsl(var(--online))]")
  })
})

describe("selecting a PC returns to its inbox", () => {
  afterEach(() => {
    cleanup()
    vi.mocked(openSaved).mockReset()
  })

  it("replaces same-PC anonymous authority by closing the old connection before opening the account ticket", async () => {
    seedLink()
    const order: string[] = []
    const first = hostLink({ threads: [] })
    first.close = () => { order.push("old-close") }
    vi.mocked(openSaved).mockImplementation(async (saved) => {
      order.push("open-" + saved.ticket)
      return saved.ticket === "tick" ? first : hostLink({ threads: [] })
    })
    render(<PhoneApp />)
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(1))
    await act(async () => {
      saveGatewaySession({ origin: "http://127.0.0.1:9", token: "agm_" + "b".repeat(64), user: { id: "u1", username: "user", approval_status: "approved" }, device: { id: "m1", expires_at: "2099-01-01T00:00:00Z" } })
      saveLink({ hubURL: "http://127.0.0.1:9", ticket: "managed", hostPub: "pub", sessionID: "managed-session", fingerprint: "fp", gatewayOrigin: "http://127.0.0.1:9", gatewayOwnerID: "u1", gatewayHostID: "pc1", gatewayMobileID: "m1" })
      window.dispatchEvent(new Event("zwai.gateway.account"))
    })
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(2))
    expect(order).toEqual(["open-tick", "old-close", "open-managed"])
  })

  it("closes a candidate which finishes opening after PhoneApp unmount", async () => {
    seedLink()
    let finish!: (link: DeviceLink) => void
    vi.mocked(openSaved).mockReturnValue(new Promise((resolve) => { finish = resolve }))
    const { unmount } = render(<PhoneApp />)
    await waitFor(() => expect(openSaved).toHaveBeenCalledOnce())
    const late = hostLink({ threads: [] })
    late.close = vi.fn()
    unmount()
    await act(async () => { finish(late) })
    expect(late.close).toHaveBeenCalledTimes(1)
  })

  it("keeps one opening candidate through same-authority account announcements", async () => {
    seedLink()
    let finish!: (link: DeviceLink) => void
    vi.mocked(openSaved).mockReturnValue(new Promise((resolve) => { finish = resolve }))
    render(<PhoneApp />)
    await act(async () => {
      for (let n = 0; n < 3; n++) window.dispatchEvent(new Event("zwai.gateway.account"))
      window.dispatchEvent(new CustomEvent("zwai.gateway.connect", { detail: "fp" }))
    })
    expect(openSaved).toHaveBeenCalledTimes(1)
    const candidate = hostLink({ threads: [] })
    candidate.close = vi.fn()
    await act(async () => { finish(candidate) })
    expect(candidate.close).not.toHaveBeenCalled()
  })

  it("cannot let an old recovery finish clear the replacement generation's opening operation", async () => {
    seedLink()
    const first = hostLink({ threads: [] })
    let alive = true
    first.alive = () => alive
    let oldFinish!: (link: DeviceLink) => void
    let newFinish!: (link: DeviceLink) => void
    vi.mocked(openSaved).mockResolvedValueOnce(first)
      .mockImplementationOnce(() => new Promise((resolve) => { oldFinish = resolve }))
      .mockImplementationOnce(() => new Promise((resolve) => { newFinish = resolve }))
    render(<PhoneApp />)
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(1))
    await act(async () => { alive = false; first.onDisconnect?.(new Error("offline")) })
    expect(openSaved).toHaveBeenCalledTimes(2)
    await act(async () => {
      saveLink({ hubURL: "http://127.0.0.1:9", ticket: "new", hostPub: "pub", sessionID: "new", fingerprint: "fp" })
      window.dispatchEvent(new Event("zwai.gateway.account"))
    })
    expect(openSaved).toHaveBeenCalledTimes(3)
    const late = hostLink({ threads: [] })
    late.close = vi.fn()
    await act(async () => { oldFinish(late); window.dispatchEvent(new Event("zwai.gateway.account")) })
    expect(late.close).toHaveBeenCalledTimes(1)
    expect(openSaved).toHaveBeenCalledTimes(3)
    const replacement = hostLink({ threads: [] })
    replacement.close = vi.fn()
    await act(async () => { newFinish(replacement) })
    expect(replacement.close).not.toHaveBeenCalled()
    expect(openSaved).toHaveBeenCalledTimes(3)
  })

  it("restores the current thread after account takeover without sending the user's turn twice", async () => {
    seedLink()
    const send = vi.fn(async () => ({ v: 1, id: "s", ok: true }))
    const reopen = vi.fn(async () => ({ v: 1, id: "o", ok: true, detail: { id: "th-1", title: "th-1" } }))
    const first = hostLink({ send })
    const next = hostLink({ send, open: reopen })
    vi.mocked(openSaved).mockResolvedValueOnce(first).mockResolvedValueOnce(next)
    render(<PhoneApp />)
    fireEvent.click(await screen.findByRole("button", { name: t("home.open", { title: "th-1" }) }))
    fireEvent.change(await screen.findByLabelText(t("thread.message")), { target: { value: "Continue my work" } })
    fireEvent.click(screen.getByRole("button", { name: t("thread.send") }))
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1))
    await act(async () => {
      saveGatewaySession({ origin: "http://127.0.0.1:9", token: "agm_" + "b".repeat(64), user: { id: "u1", username: "user", approval_status: "approved" }, device: { id: "m1", expires_at: "2099-01-01T00:00:00Z" } })
      saveLink({ hubURL: "http://127.0.0.1:9", ticket: "managed", hostPub: "pub", sessionID: "new", fingerprint: "fp", gatewayOrigin: "http://127.0.0.1:9", gatewayOwnerID: "u1", gatewayHostID: "pc1", gatewayMobileID: "m1" })
      window.dispatchEvent(new Event("zwai.gateway.account"))
    })
    await waitFor(() => expect(reopen).toHaveBeenCalledTimes(1))
    expect(screen.getByRole("heading", { name: "th-1" })).toBeInTheDocument()
    expect(send).toHaveBeenCalledTimes(1)
    expect(send.mock.calls[0]).toEqual([expect.objectContaining({ op: OpDeliver, mode: "queue", thread_id: "th-1", text: "Continue my work" })])
  })

  it("retries when the installed link drops during its first inbox request", async () => {
    seedLink()
    let rejectList!: (error: Error) => void
    const first = hostLink({ list: () => new Promise((_resolve, reject) => { rejectList = reject }) })
    let alive = true
    first.alive = () => alive
    vi.mocked(openSaved).mockResolvedValueOnce(first).mockResolvedValueOnce(hostLink({ threads: [] }))
    render(<PhoneApp />)
    await waitFor(() => expect(rejectList).toBeTypeOf("function"))
    await act(async () => {
      alive = false
      first.onDisconnect?.(new Error("offline"))
      rejectList(new Error("offline"))
    })
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(2), { timeout: 2200 })
  })

  it("retries when a replacement link drops while restoring the existing thread watch", async () => {
    seedLink()
    let rejectWatch!: (error: Error) => void
    let alive = true
    const replacement = hostLink({ watch: () => new Promise((_resolve, reject) => { rejectWatch = reject }) })
    replacement.alive = () => alive
    vi.mocked(openSaved).mockResolvedValueOnce(hostLink()).mockResolvedValueOnce(replacement).mockResolvedValueOnce(hostLink())
    render(<PhoneApp />)
    fireEvent.click(await screen.findByRole("button", { name: t("home.open", { title: "th-1" }) }))
    await screen.findByLabelText(t("thread.message"))
    await act(async () => {
      saveLink({ hubURL: "http://127.0.0.1:9", ticket: "new", hostPub: "pub", sessionID: "new", fingerprint: "fp" })
      window.dispatchEvent(new Event("zwai.gateway.account"))
    })
    await waitFor(() => expect(rejectWatch).toBeTypeOf("function"))
    await act(async () => {
      alive = false
      replacement.onDisconnect?.(new Error("offline"))
      rejectWatch(new Error("offline"))
    })
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(3), { timeout: 2200 })
  })

  it("pauses the retired anonymous socket after a local save failure and reconnects only after saving the new ticket", async () => {
    const origin = "http://127.0.0.1:9"
    const hostPub = "D".repeat(42) + "A"
    const session = { origin, token: "agm_" + "b".repeat(64), user: { id: "u1", username: "user", approval_status: "approved" }, device: { id: "m1", expires_at: "2099-01-01T00:00:00Z" } }
    const host = { id: "pc1", name: "PC", hostname: "pc", version: "1", enabled: true, online: true, owner_user_id: "u1", host_pub: hostPub }
    saveLink({ hubURL: origin, hostPub, ticket: "tick", sessionID: "old", fingerprint: "fp" })
    const first = hostLink({ threads: [] })
    first.close = vi.fn()
    vi.mocked(openSaved).mockResolvedValueOnce(first).mockResolvedValueOnce(hostLink({ threads: [] }))
    render(<PhoneApp />)
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(1))
    await act(async () => { saveGatewaySession(session) })
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ data: { binding_id: "b1", host_pub: hostPub, ticket: "c".repeat(64), session_id: "d".repeat(32) } })))
    const write = localStorage.setItem.bind(localStorage)
    const storage = vi.spyOn(localStorage, "setItem").mockImplementation((key, value) => { if (key === "zwai.remote.links") throw new Error("quota"); write(key, value) })
    try {
      await act(async () => { await expect(upgradeAnonymousGatewayLinks(session, [host], () => true)).rejects.toThrow("quota") })
      expect(first.close).toHaveBeenCalledTimes(1)
      expect(openSaved).toHaveBeenCalledTimes(1)
      storage.mockRestore()
      await act(async () => { await upgradeAnonymousGatewayLinks(session, [host], () => true) })
      await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(2))
      expect(vi.mocked(openSaved).mock.calls[1][0].ticket).toBe("c".repeat(64))
      expect(fetcher).toHaveBeenCalledTimes(1)
    } finally { storage.mockRestore(); fetcher.mockRestore() }
  })

  it("returns to the startup PC after selecting another PC and reconnecting through the account page", async () => {
    for (const [fingerprint, label] of [["pc-a", "PC A"], ["pc-b", "PC B"]]) saveLink({ hubURL: "http://127.0.0.1:9", ticket: "tick", hostPub: "pub", sessionID: "sid", fingerprint, label })
    saveActiveFingerprint("pc-a")
    vi.mocked(openSaved).mockImplementation(async () => hostLink({ threads: [] }))
    render(<App />)
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(2))
    fireEvent.click(await screen.findByRole("tab", { name: "PC B" }))
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(2))
    await act(async () => {
      saveActiveFingerprint("pc-a")
      window.dispatchEvent(new CustomEvent("zwai.gateway.connect", { detail: "pc-a" }))
    })
    await waitFor(() => expect(openSaved).toHaveBeenCalledTimes(2))
    expect(screen.getByRole("tab", { name: "PC A" })).toHaveAttribute("aria-selected", "true")
    expect(vi.mocked(openSaved).mock.calls.map(([pc]) => pc.fingerprint).sort()).toEqual(["pc-a", "pc-b"])
  })

  it("does not auto-open the selected PC's live or last conversation", async () => {
    const ticket = (fingerprint: string, label: string) => ({
      hubURL: "http://127.0.0.1:9", ticket: "tick", hostPub: "pub", sessionID: "sid", fingerprint, label,
    })
    saveLink(ticket("pc-a", "PC A"))
    saveLink(ticket("pc-b", "PC B"))
    saveLastThreadId("th-1")
    saveActiveFingerprint("pc-a")
    const openB = vi.fn(async () => ({ v: 1, id: "o", ok: true, detail: { id: "th-1", title: "live" } }))
    const first = hostLink({ threads: [] })
    const second = hostLink({ running: [{ thread_id: "th-1", title: "live" }], open: openB })
    const reconnected = hostLink({ running: [{ thread_id: "th-1", title: "live" }], open: openB })
    let secondAlive = true
    second.alive = () => secondAlive
    let bConnects = 0
    vi.mocked(openSaved).mockImplementation(async (saved) => {
      if (saved.fingerprint === "pc-a") return first
      return bConnects++ === 0 ? second : reconnected
    })
    render(<App />)
    fireEvent.click(await screen.findByRole("tab", { name: "PC B" }))
    await waitFor(() => expect(screen.getByRole("tab", { name: "PC B" })).toHaveAttribute("aria-selected", "true"))
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: t("thread.back") })).not.toBeInTheDocument()
    expect(openB).not.toHaveBeenCalled()
    secondAlive = false
    await act(async () => second.onDisconnect?.(new Error("lost")))
    await waitFor(() => expect(bConnects).toBe(2))
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
    expect(openB).not.toHaveBeenCalled()
  })
})

describe("system back leaves a conversation for the inbox", () => {
  afterEach(() => {
    cleanup()
    vi.mocked(openSaved).mockReset()
  })

  it("ignores an older PC's local client catalog without polling for it", async () => {
    seedLink()
    const link = hostLink({
      list: async () => ({
        v: 1, id: "l", ok: true, running: [],
        threads: [{ id: "zwai-1", title: "Zwai conversation", running: false, last_active_at: "2026-09-25T00:00:00Z" }],
        clients: { enabled: true, tools: [{
          id: "codex", more: false,
          tasks: [{ id: "client-1", title: "Private local task", status: "done", updated_at: "2026-09-25T00:00:00Z" }],
        }] },
      }),
    })
    const rpc = vi.spyOn(link, "rpc")
    vi.mocked(openSaved).mockResolvedValue(link)

    render(<App />)
    expect(await screen.findByRole("button", { name: /Zwai conversation/ })).toBeInTheDocument()
    expect(screen.queryByText(t("home.clients"))).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Private local task" })).not.toBeInTheDocument()
    expect(rpc.mock.calls.filter(([req]) => req.op === OpClients || req.op === OpClientRead)).toHaveLength(0)
  })

  it("pops an open conversation and finishes only from the inbox", async () => {
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(hostLink())
    render(<App />)
    const open = t("home.open", { title: "th-1" })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("button", { name: open }))
    await waitFor(() => {
      expect(screen.getByRole("button", { name: t("thread.back") })).toBeInTheDocument()
    })
    let stayed = false
    await act(async () => {
      stayed = systemBack()
    })
    expect(stayed).toBe(true)
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    expect(screen.queryByRole("button", { name: t("thread.back") })).not.toBeInTheDocument()
    expect(systemBack()).toBe(false)
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
  })

  it("header Back returns to the inbox", async () => {
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(hostLink())
    render(<App />)
    const open = t("home.open", { title: "th-1" })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("button", { name: open }))
    await waitFor(() => {
      expect(screen.getByRole("button", { name: t("thread.back") })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("button", { name: t("thread.back") }))
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    expect(screen.queryByRole("heading", { name: "th-1" })).not.toBeInTheDocument()
  })

  it("returns from a resumed live conversation instead of treating it as the root", async () => {
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        running: [{ thread_id: "th-1", title: "th-1" }],
        threads: [],
      }),
    )
    render(<App />)
    await waitFor(() => {
      expect(screen.getByRole("button", { name: t("thread.back") })).toBeInTheDocument()
    })
    let stayed = false
    await act(async () => {
      stayed = systemBack()
    })
    expect(stayed).toBe(true)
    await waitFor(() => {
      expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
    })
    expect(screen.queryByRole("button", { name: t("thread.back") })).not.toBeInTheDocument()
    expect(systemBack()).toBe(false)
  })

  it("does not paint the conversation back when open replies after Back", async () => {
    let releaseOpen: (resp: RemoteResponse) => void = () => undefined
    const openGate = new Promise<RemoteResponse>((resolve) => {
      releaseOpen = resolve
    })
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        open: () => openGate,
      }),
    )
    render(<App />)
    const open = t("home.open", { title: "th-1" })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("button", { name: open }))
    await waitFor(() => {
      expect(screen.getByRole("button", { name: t("thread.back") })).toBeInTheDocument()
    })
    await act(async () => {
      expect(systemBack()).toBe(true)
    })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    await act(async () => {
      releaseOpen({
        v: 1,
        id: "late",
        ok: true,
        detail: { id: "th-1", title: "th-1" },
      })
    })
    expect(screen.queryByRole("button", { name: t("thread.back") })).not.toBeInTheDocument()
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
  })

  it("does not surface a late watch failure after Back", async () => {
    let releaseWatch: (resp: RemoteResponse) => void = () => undefined
    let watchStarted = false
    const watchGate = new Promise<RemoteResponse>((resolve) => {
      releaseWatch = resolve
    })
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        watch: () => {
          watchStarted = true
          return watchGate
        },
      }),
    )
    render(<App />)
    const open = t("home.open", { title: "th-1" })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("button", { name: open }))
    await waitFor(() => {
      expect(watchStarted).toBe(true)
    })
    await act(async () => {
      expect(systemBack()).toBe(true)
    })
    await act(async () => {
      releaseWatch({ v: 1, id: "late-watch", ok: false, error: "watch failed" })
    })
    expect(screen.queryByText("watch failed")).not.toBeInTheDocument()
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
  })
})

describe("starting a conversation is its own screen", () => {
  afterEach(() => {
    cleanup()
    vi.mocked(openSaved).mockReset()
  })

  it("picks the PC and the project there, then opens what it started", async () => {
    const asked: Partial<RemoteRequest>[] = []
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        projects: [{ id: "p2", name: "notes" }],
        start: async (req) => {
          asked.push(req)
          return {
            v: 1,
            id: "s",
            ok: true,
            threads: [
              { id: "th-2", title: "th-2", running: true, last_active_at: "2026-01-01T00:00:00Z" },
            ],
          }
        },
      }),
    )
    render(<App />)
    await waitFor(() => {
      expect(screen.getByTestId("new-chat")).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId("new-chat"))
    await waitFor(() => {
      expect(screen.getByRole("radio", { name: "notes" })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("radio", { name: "notes" }))
    fireEvent.change(screen.getByLabelText(t("home.newMessage")), { target: { value: "go" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: t("home.start") }))
    })
    expect(asked).toEqual([
      expect.objectContaining({ op: OpStart, text: "go", project_id: "p2" }),
    ])
    await waitFor(() => {
      expect(screen.getByRole("button", { name: t("thread.back") })).toBeInTheDocument()
    })
    expect(screen.queryByRole("radio", { name: "notes" })).not.toBeInTheDocument()
  })

  it("a project row opens new chat already on that project", async () => {
    const asked: Partial<RemoteRequest>[] = []
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        projects: [{ id: "p2", name: "notes" }],
        start: async (req) => {
          asked.push(req)
          return {
            v: 1,
            id: "s",
            ok: true,
            threads: [
              { id: "th-2", title: "th-2", running: true, last_active_at: "2026-01-01T00:00:00Z" },
            ],
          }
        },
      }),
    )
    render(<App />)
    const startHere = t("home.newChatIn", { name: "notes" })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: startHere })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("button", { name: startHere }))
    expect(screen.getByRole("radio", { name: "notes" })).toBeChecked()
    fireEvent.change(screen.getByLabelText(t("home.newMessage")), { target: { value: "go" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: t("home.start") }))
    })
    expect(asked).toEqual([
      expect.objectContaining({ op: OpStart, text: "go", project_id: "p2" }),
    ])
  })

  it("system back leaves the new-conversation screen on the inbox", async () => {
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(hostLink())
    render(<App />)
    await waitFor(() => {
      expect(screen.getByTestId("new-chat")).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId("new-chat"))
    expect(screen.getByLabelText(t("home.newMessage"))).toBeInTheDocument()
    let stayed = false
    await act(async () => {
      stayed = systemBack()
    })
    expect(stayed).toBe(true)
    expect(screen.queryByLabelText(t("home.newMessage"))).not.toBeInTheDocument()
    expect(screen.getByRole("tablist", { name: t("home.hosts") })).toBeInTheDocument()
    expect(systemBack()).toBe(false)
  })

  // The typed message only exists in that box, so a refusal must not take
  // the screen away with it.
  it("keeps the screen and says so when the PC refuses the start", async () => {
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({ start: async () => ({ v: 1, id: "s", ok: false, error: "quota" }) }),
    )
    render(<App />)
    await waitFor(() => {
      expect(screen.getByTestId("new-chat")).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId("new-chat"))
    fireEvent.change(screen.getByLabelText(t("home.newMessage")), { target: { value: "go" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: t("home.start") }))
    })
    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toBe(t("err.remote", { detail: "quota" }))
    })
    expect(screen.getByLabelText(t("home.newMessage"))).toBeInTheDocument()
  })
})

describe("inbox window and thread loading", () => {
  afterEach(() => {
    cleanup()
    vi.useRealTimers()
    vi.mocked(openSaved).mockReset()
  })

  it("shows and submits each PC Local conversation's gateway model when only a PC default is listed", async () => {
    seedLink()
    const conversations = [
      { id: "original", provider_id: "gateway:original", model: "original-model" },
      { id: "partner", provider_id: "gateway:partner", model: "shared-model" },
    ].map(row => ({ ...row, title: row.id }))
    const threads = conversations.map(({ id, title }) => ({ id, title, running: false, last_active_at: "2026-01-01T00:00:00Z" }))
    const send = vi.fn(async () => ({ v: 1, id: "s", ok: true }))
    const link = hostLink({ threads, send })
    const rpc = link.rpc.bind(link)
    vi.spyOn(link, "rpc").mockImplementation(async (req, options): Promise<RemoteResponse> => {
      const reply = { v: 1, id: "fixture", ok: true }
      const model_policy = { mode: "local" as const, scope: "local", ready: true, configurable: true }
      if (req.op === OpMeta) return { ...reply, startup: "ready", product_mode: "local", model_policy }
      if (req.op === OpRefreshCapabilities) return { ...reply, model_policy, models: [
        { provider_id: "personal", model: "shared-model", label: "PC default", default: true, ready: true },
      ] }
      const detail = conversations.find(row => row.id === req.thread_id)
      if (req.op === OpWatch && detail) return { ...reply, op: "ready", thread_id: detail.id, detail, events: [] }
      return rpc(req, options)
    })
    vi.mocked(openSaved).mockResolvedValue(link)
    render(<App />)
    for (const thread of conversations) {
      fireEvent.click(await screen.findByRole("button", { name: t("home.open", { title: thread.title }) }))
      fireEvent.focus(await screen.findByRole("textbox", { name: t("thread.message") }))
      const model = await screen.findByRole("button", { name: t("composer.model") })
      await waitFor(() => expect(model.textContent).toBe(thread.model))
      fireEvent.change(screen.getByLabelText(t("thread.message")), { target: { value: "Continue" } })
      fireEvent.click(screen.getByRole("button", { name: t("thread.send") }))
      await waitFor(() => expect(send).toHaveBeenLastCalledWith(expect.objectContaining({
        op: OpDeliver, thread_id: thread.id, provider_id: thread.provider_id, model: thread.model,
      })))
      fireEvent.click(screen.getByRole("button", { name: t("thread.back") }))
    }
  })

  it("keeps a row loaded with more after the inbox polls", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] })
    seedLink()
    const row = (id: string, title: string) => ({
      id,
      title,
      running: false,
      last_active_at: "2026-01-01T00:00:00Z",
    })
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        list: async () => ({
          v: 1,
          id: "l",
          ok: true,
          threads: [row("a", "alpha"), row("b", "beta")],
          running: [],
          more: true,
          next: "older",
        }),
        more: async () => ({
          v: 1,
          id: "m",
          ok: true,
          threads: [row("c", "paged")],
          running: [],
          more: false,
          next: "",
        }),
      }),
    )
    render(<App />)
    await waitFor(() => {
      expect(screen.getByRole("button", { name: t("home.more") })).toBeInTheDocument()
    })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: t("home.more") }))
    })
    expect(screen.getByRole("button", { name: t("home.open", { title: "paged" }) })).toBeInTheDocument()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000)
    })
    expect(screen.getByRole("button", { name: t("home.open", { title: "paged" }) })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: t("home.more") })).not.toBeInTheDocument()
  })

  it("keeps Earlier usable when a manual page arrives after automatic opening history advanced", async () => {
    seedLink()
    let finishAutomatic: (value: RemoteResponse) => void = () => undefined
    let finishManual: (value: RemoteResponse) => void = () => undefined
    const automatic = new Promise<RemoteResponse>((resolve) => { finishAutomatic = resolve })
    const manual = new Promise<RemoteResponse>((resolve) => { finishManual = resolve })
    const log = vi.fn((_req: Partial<RemoteRequest>) => {
      if (log.mock.calls.length === 1) return automatic
      if (log.mock.calls.length === 2) return manual
      return Promise.resolve({ v: 1, id: "oldest", ok: true, thread_id: "th-1", seq: 1, more: false,
        events: [{ thread_id: "th-1", seq: 1, kind: "user_message", text: "Original request", created_at: "2026-01-01T00:00:00Z" }] })
    })
    vi.mocked(openSaved).mockResolvedValue(hostLink({
      watch: async () => ({ v: 1, id: "w", ok: true, op: "ready", thread_id: "th-1", seq: 30, more: true,
        detail: { id: "th-1", title: "th-1" },
        events: [
          { thread_id: "th-1", seq: 28, kind: "agent_message", agent_id: "worker", text: "Worker-only progress", created_at: "2026-01-01T00:00:00Z" },
          { thread_id: "th-1", seq: 30, kind: "tool_result", tool_call_id: "spawn", text: "Started worker", created_at: "2026-01-01T00:00:00Z" },
        ] }),
      log,
    }))
    render(<App />)
    fireEvent.click(await screen.findByRole("button", { name: t("home.open", { title: "th-1" }) }))
    await waitFor(() => expect(log).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole("button", { name: t("thread.earlier") }))
    expect(log.mock.calls.map(([req]) => req.before)).toEqual([28, 28])
    await act(async () => finishAutomatic({ v: 1, id: "automatic", ok: true, thread_id: "th-1", seq: 18, more: true,
      events: [{ thread_id: "th-1", seq: 18, kind: "agent_message", text: "Earlier manager answer", created_at: "2026-01-01T00:00:00Z" }] }))
    expect(screen.getByText("Earlier manager answer")).toBeInTheDocument()
    await act(async () => finishManual({ v: 1, id: "manual", ok: true, thread_id: "th-1", seq: 20, more: true, events: [] }))
    const earlier = screen.queryByRole("button", { name: t("thread.earlier") })
    expect(earlier).not.toBeNull()
    expect(earlier).toBeEnabled()
    await act(async () => fireEvent.click(earlier!))
    expect(log.mock.calls.map(([req]) => req.before)).toEqual([28, 28, 18])
    expect(screen.getByText("Original request")).toBeInTheDocument()
    expect(screen.getByText("Earlier manager answer")).toBeInTheDocument()
  })

  it("shows loading outside the transcript while the opening snapshot has not answered", async () => {
    seedLink()
    vi.mocked(openSaved).mockResolvedValue(
      hostLink({
        watch: () => new Promise(() => undefined),
      }),
    )
    render(<App />)
    const open = t("home.open", { title: "th-1" })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: open })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole("button", { name: open }))
    const status = await screen.findByRole("status")
    expect(status).toHaveTextContent(t("thread.loading"))
    expect(status.closest("[data-testid='transcript']")).toBeNull()
    expect(screen.queryByTestId("transcript")).not.toBeInTheDocument()
  })
})

/** Add a PC is a menu row: the corner it used to share is New chat now. */
function openAddHost() {
  fireEvent.click(screen.getByRole("button", { name: t("home.menu") }))
  fireEvent.click(screen.getByRole("menuitem", { name: t("home.addHost") }))
}

function systemBack(): boolean {
  const fn = window.__zwaiAndroidBack
  if (!fn) throw new Error("android back hook missing")
  return fn()
}

function hostLink(opts?: {
  threads?: { id: string; title: string; running: boolean; last_active_at: string }[]
  running?: { thread_id: string; title: string }[]
  projects?: { id: string; name: string }[]
  list?: () => Promise<RemoteResponse>
  open?: () => Promise<RemoteResponse>
  watch?: () => Promise<RemoteResponse>
  log?: (req: Partial<RemoteRequest>) => Promise<RemoteResponse>
  start?: (req: Partial<RemoteRequest>) => Promise<RemoteResponse>
  send?: (req: Partial<RemoteRequest>) => Promise<RemoteResponse>
  more?: (req: Partial<RemoteRequest>) => Promise<RemoteResponse>
}): DeviceLink {
  const threads = opts?.threads ?? [
    { id: "th-1", title: "th-1", running: false, last_active_at: "2026-01-01T00:00:00Z" },
  ]
  const running = opts?.running ?? []
  const link = {
    path: "relay",
    alive: () => true,
    close: () => undefined,
    announceDevice: async () => undefined,
    rpc: async (req: Partial<RemoteRequest>): Promise<RemoteResponse> => {
      if (req.op === OpHello) return { v: 1, id: "h", ok: true }
      if (req.op === OpStart && opts?.start) return opts.start(req)
      if ((req.op === OpSend || req.op === OpDeliver) && opts?.send) return opts.send(req)
      if (req.op === OpList) {
        if (opts?.list) return opts.list()
        return { v: 1, id: "l", ok: true, threads, running, projects: opts?.projects ?? [] }
      }
      if (req.op === OpMore && opts?.more) return opts.more(req)
      if (req.op === OpLog && opts?.log) return opts.log(req)
      if (req.op === OpOpen) {
        if (opts?.open) return opts.open()
        return {
          v: 1,
          id: "o",
          ok: true,
          detail: { id: req.thread_id ?? "th-1", title: "th-1" },
        }
      }
      if (req.op === OpWatch) {
        if (opts?.watch) return opts.watch()
        return { v: 1, id: "w", ok: true, op: "ready", events: [] }
      }
      if (req.op === OpUnwatch) return { v: 1, id: "u", ok: true }
      return { v: 1, id: "x", ok: true }
    },
  }
  return link as unknown as DeviceLink
}
