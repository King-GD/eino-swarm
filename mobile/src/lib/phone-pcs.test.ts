import { expect, it } from "vitest"
import { onlineFirst, orderSavedPCs, phonePCChoices } from "./phone-pcs"
import { autoConnectTarget } from "./gateway-auto-connect"
import type { GatewayHost } from "./gateway-account"
import type { SavedLink } from "./store"

const host = (id: string, online = true): GatewayHost => ({ id, name: id, hostname: id, online, enabled: true, version: "1" })
const link = (id: string): SavedLink => ({ fingerprint: id, gatewayHostID: id, label: id, hubURL: "https://hub.invalid", hostPub: id, ticket: id, sessionID: id })

it("puts online PCs first without dropping offline PCs or fabricating pairing credentials", () => {
  const hosts = [host("offline", false), host("online"), host("not-paired", false)]
  const saved = [link("offline"), link("online")]
  expect(onlineFirst(hosts).map(host => host.id)).toEqual(["online", "offline", "not-paired"])
  expect(orderSavedPCs(saved, hosts).map(pc => pc.fingerprint)).toEqual(["online", "offline"])
  const choices = phonePCChoices(saved, hosts)
  expect(choices.map(pc => pc.hostID)).toEqual(["online", "offline", "not-paired"])
  expect(choices[2]).toMatchObject({ fingerprint: "", online: false })
  expect(choices[2]).not.toHaveProperty("ticket")
  expect(autoConnectTarget(hosts, saved, "online")?.id).toBe("online")
  expect(autoConnectTarget(hosts, saved, "offline")?.id).toBe("online")
})
