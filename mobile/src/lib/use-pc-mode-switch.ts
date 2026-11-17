import { useEffect } from "react"
import type { RemoteLink } from "./link"
import type { PhoneMode } from "./phone-mode"
import { OpSetProductMode } from "./rpc"

export type BindPcModeSwitch = (switcher: (mode: PhoneMode) => Promise<void>) => () => void

export class PcModeSwitchError extends Error {
  constructor(readonly reason: "unsupported" | "busy" | "failed" | "changed") { super(reason) }
}

/** Register only the live, selected link. A debug gesture owns one request;
 * reconnecting or selecting another PC never replays that mutation. */
export function usePcModeSwitch(link: RemoteLink | null, bind?: BindPcModeSwitch) {
  useEffect(() => {
    if (!link?.alive() || !bind) return
    let current = true
    const unbind = bind(async mode => {
      if (!current || !link.alive()) throw new PcModeSwitchError("changed")
      const productMode = mode === "local" ? "local" : "fofa"
      const response = await link.rpc({ op: OpSetProductMode, product_mode: productMode }, {
        dropOnTimeout: false, timeoutMs: 15_000,
      }).catch(() => { throw new PcModeSwitchError("failed") })
      if (!current || !link.alive()) throw new PcModeSwitchError("changed")
      if (!response.ok) throw new PcModeSwitchError(response.code === "unknown_op" ? "unsupported"
        : ["backup_busy", "busy", "restoring"].includes(response.code ?? "") ? "busy" : "failed")
      if (response.product_mode !== productMode) throw new PcModeSwitchError("failed")
    })
    return () => { current = false; unbind() }
  }, [link, bind])
}
