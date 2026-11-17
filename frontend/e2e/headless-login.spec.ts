import { createServer } from "node:http"
import { expect, test } from "@playwright/test"

test("a headless host is authorized through the user's browser and keeps private proof off the page", async ({ page, request }) => {
  let origin = ""
  let approved = false
  let polls = 0
  const gateway = createServer(async (incoming, response) => {
    // Consume the request without retaining candidate tokens or proof in evidence.
    for await (const _chunk of incoming) { /* drain */ }
    if (incoming.url?.endsWith("/start")) {
      response.writeHead(201, { "Content-Type": "application/json" })
      response.end(JSON.stringify({ data: { device_code: "d".repeat(64), user_code: "PUBLIC",
        verification_uri: `${origin}/login?desktop_code=PUBLIC`, interval: 5,
        expires_at: new Date(Date.now() + 60_000).toISOString() } }))
    } else if (incoming.url?.endsWith("/poll")) {
      polls++
      response.setHeader("Content-Type", "application/json")
      response.end(JSON.stringify({ data: approved
        ? { status: "authorized", host_id: "a".repeat(32), user: { id: "b".repeat(32), username: "approved-user" } }
        : { status: "account_pending" } }))
    } else if (incoming.url === "/login?desktop_code=PUBLIC") {
      response.setHeader("Content-Type", "text/html")
      response.end('<p>Confirmation code: PUBLIC</p><form action="/confirm" method="post"><button>Authorize this device</button></form>')
    } else if (incoming.url === "/confirm" && incoming.method === "POST") {
      approved = true
      response.end("Device authorized")
    } else {
      response.writeHead(404)
      response.end()
    }
  })
  await new Promise<void>((resolve) => gateway.listen(0, "127.0.0.1", resolve))
  const address = gateway.address()
  if (!address || typeof address === "string") throw new Error("missing private Gateway listener")
  origin = `http://127.0.0.1:${address.port}`
  const { settings } = await (await request.get("/api/settings")).json()
  try {
    expect((await request.put("/api/settings", { data: { remote: { ...settings.remote,
      enabled: false, keep_awake: false, auth_mode: "independent", hub_url: origin } } })).ok()).toBeTruthy()
    await page.goto("/")
    await page.getByRole("button", { name: "Settings", exact: true }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByRole("tab", { name: "Phone", exact: true }).click()
    await dialog.getByRole("button", { name: "Sign in", exact: true }).click()
    const link = dialog.getByRole("link", { name: "Open sign-in page" })
    await expect(link).toHaveAttribute("href", `${origin}/login?desktop_code=PUBLIC`)
    await expect(dialog.getByText("PUBLIC", { exact: true })).toBeVisible()
    await expect(dialog.getByText("Waiting for administrator approval. No device is connected yet.")).toBeVisible()
    expect(approved).toBe(false)
    expect(polls).toBeGreaterThan(0)
    const pending = await (await request.get("/api/remote/status")).json()
    expect(pending.has_token).toBe(false)
    for (const key of ["device_code", "candidate_token", "code_verifier"]) {
      expect(pending.login).not.toHaveProperty(key)
      await expect(dialog.getByText(key, { exact: true })).toHaveCount(0)
    }
    const popupPromise = page.waitForEvent("popup")
    await link.click()
    const popup = await popupPromise
    await expect(popup.getByText("Confirmation code: PUBLIC")).toBeVisible()
    await popup.getByRole("button", { name: "Authorize this device" }).click()
    await expect(popup.getByText("Device authorized", { exact: true })).toBeVisible()
    await expect(dialog.getByText("Account: approved-user", { exact: true })).toBeVisible()
    await expect(link).toHaveCount(0)
    const complete = await (await request.get("/api/remote/status")).json()
    expect(complete.login.state).toBe("authorized")
    expect(complete.has_token).toBe(true)
    expect(complete.login).not.toHaveProperty("verification_uri")
    await popup.close()
  } finally {
    await request.put("/api/settings", { data: { remote: settings.remote } })
    gateway.closeAllConnections()
    await new Promise<void>((resolve, reject) => gateway.close((error) => error ? reject(error) : resolve()))
  }
})
