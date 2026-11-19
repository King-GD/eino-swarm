import type { ComposerExtra } from "@/components/composer"
import type { InsertMode } from "../../../frontend/src/lib/delivery-mode"

import { LinkFault, linkError, remoteError } from "./client"
import type { RemoteLink } from "./link"
import { OpAnswer, OpContinue, OpDeliver, OpSend, type RemoteRequest, type RemoteResponse } from "./rpc"
import { t } from "./i18n"
import type { PhoneView } from "./session"
import { markRunning } from "./session"
import { sendComposed } from "./turn-send"
import { pcWebSelection } from "./pc-web-execution"
import { gatewayImageCode } from "./paired-image-generation"
import { imageGenerationLabel } from "./image-generation-labels"

type TurnDeps = {
  link: () => RemoteLink | null
  view: () => PhoneView
  commit: (view: PhoneView) => void
  fail: (err: unknown) => void
  setError: (message: string) => void
  setPending: (pending: boolean) => void
}

export async function continuePhoneTurn(link: RemoteLink | null, threadId: string, proceed: boolean): Promise<void> {
  if (!link?.alive()) throw new Error(linkError(new LinkFault("offline", "network")))
  let response
  try { response = await link.rpc({ op: OpContinue, thread_id: threadId, continue: proceed }) }
  catch (err) { throw new Error(linkError(err)) }
  if (!response.ok) throw new Error(remoteError(response.error || response.code || ""))
}

export async function answerPhoneQuestion(
  deps: Pick<TurnDeps, "link" | "setError" | "setPending">,
  threadId: string,
  answer: Pick<RemoteRequest, "text" | "call_id" | "answers">,
) {
  const target = deps.link()
  if (!target?.alive()) {
    const message = linkError(new LinkFault("offline", "network"))
    if (answer.answers === undefined) deps.setError(message)
    throw new Error(message)
  }
  deps.setPending(true)
  let response
  try {
    response = await target.rpc({ op: OpAnswer, thread_id: threadId, ...answer })
  } catch (error) {
    const message = linkError(error)
    if (answer.answers === undefined) deps.setError(message)
    throw new Error(message)
  } finally {
    deps.setPending(false)
  }
  if (!response.ok) {
    const message = remoteError(response.error || response.code || "")
    if (answer.answers === undefined) deps.setError(message)
    throw new Error(message)
  }
}

export async function sendPhoneQueue(
  deps: TurnDeps,
  op: string,
  threadId: string,
  followupId?: string,
) {
  const target = deps.link()
  if (!target?.alive()) return
  try {
    const r = await target.rpc({ op, thread_id: threadId, followup_id: followupId })
    if (deps.link() !== target || deps.view().threadId !== threadId) return
    if (!r.ok) {
      deps.setError(remoteError(r.error || r.code || ""))
      return
    }
    if (r.followups) deps.commit({ ...deps.view(), followups: r.followups })
  } catch (e) {
    deps.fail(e)
  }
}

export async function deliverPhoneFollowup(deps: TurnDeps, threadId: string, followupId: string, mode: InsertMode, turnId: string) {
  const target = deps.link()
  if (!target?.alive()) {
    deps.setError(linkError(new LinkFault("offline", "network")))
    return
  }
  try {
    const response = await target.rpc({
      op: OpDeliver, thread_id: threadId, followup_id: followupId, mode, expected_turn_id: turnId,
    })
    if (deps.link() !== target || deps.view().threadId !== threadId) return
    if (!response.ok) {
      deps.setError(deliveryError(response))
      return
    }
    if (response.followups) deps.commit({ ...deps.view(), followups: response.followups })
  } catch (e) {
    deps.fail(e)
  }
}

export async function sendPhoneTurn(
  deps: TurnDeps,
  op: string,
  threadId: string,
  text: string,
  extra?: ComposerExtra,
  insertion?: { mode: InsertMode; turnId: string },
) {
  const target = deps.link()
  if (!target?.alive()) {
    deps.setError(linkError(new LinkFault("offline", "network")))
    throw new Error("offline")
  }
  deps.setPending(true)
  try {
    const baseline = deps.view().lastSeq
    const request = { op, thread_id: threadId, text, ...await pcWebSelection(target), ...(op === OpDeliver ? {
      mode: insertion?.mode ?? ("queue" as const), expected_turn_id: insertion?.turnId,
    } : {}) }
    const rpc = (req: Parameters<typeof target.rpc>[0]) => target.rpc(req)
    let r = await sendComposed(rpc, request, extra)
    // An older PC can still send ordinary text using its legacy default.
    // Its attachment send would steer, so it needs an explicit upgrade.
    if (op === OpDeliver && r.code === "unknown_op" && !insertion && !extra?.files.length && !extra?.images.length) {
      r = await sendComposed(rpc, { ...request, op: OpSend }, extra)
    }
    if (!r.ok) {
      const imageCode = gatewayImageCode(r.code)
      deps.setError(imageCode ? imageGenerationLabel(imageCode) : deliveryError(r))
      throw new Error("refused")
    }
    const view = deps.view()
    if (deps.link() !== target || view.threadId !== threadId) return
    const next = (op === OpSend || r.delivery?.turn_id && !r.delivery.steer_seq) && view.lastSeq === baseline ? markRunning(view) : view
    deps.commit(r.followups ? { ...next, followups: r.followups } : next)
  } catch (e) {
    if (e instanceof Error && (e.message === "refused" || e.message === "offline")) throw e
    deps.fail(e)
    throw e
  } finally {
    deps.setPending(false)
  }
}

function deliveryError(response: RemoteResponse) {
  if (response.code === "delivery_turn_changed") return t("delivery.turnChanged")
  if (response.code === "unknown_op") return t("delivery.updatePC")
  return remoteError(response.error || response.code || "")
}
