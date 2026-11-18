import { createServer, type ServerResponse } from "node:http"
import type { EasmResult } from "../src/lib/easm/types"

export const easmPrompt = "帮我梳理清华大学的暴露面数据"
export const easmCompletion = "清华大学暴露面模拟测试已完成，三批结果已更新到 EASM。"

const root = {
  id: "root|sample", kind: "root" as const, label: "清华大学", ip_count: 12,
  detail: { website: "https://example.test", description: "本地模拟数据", region: "北京" },
}
const company = { id: "company|sample", kind: "company" as const, label: "示例科技" }
const domain = { id: "domain|example.test", kind: "domain" as const, label: "example.test", detail: { reason: "示例域名归属依据" } }
const certificate = {
  id: "feature|certificate", kind: "feature" as const, featureKind: "cert" as const, label: "*.example.test",
  detail: { serial: "sample-certificate", issuer: "示例 CA", not_before: "2026-01-01", not_after: "2027-01-01" },
}

/** Three complete accumulated results, never a request to collect real assets. */
export const easmSnapshots: EasmResult[] = [
  {
    schema_version: 1, title: "EASM", nodes: [root], edges: [],
    summary: { clues: 1, fofa_assets: 12, fofa_ips: 3 },
    heat: [{ date: "2026-11-10", value: 3 }], components: [],
  },
  {
    schema_version: 1, title: "EASM", nodes: [root, company, domain],
    edges: [
      { source: root.id, target: company.id, confirmed: true },
      { source: company.id, target: domain.id, confirmed: true },
    ],
    summary: { clues: 3, fofa_assets: 24, fofa_ips: 6 },
    heat: [{ date: "2026-11-10", value: 3 }, { date: "2026-11-17", value: 5 }],
    components: [{ name: "nginx", display_name: "NGINX", count: 12 }],
  },
  {
    schema_version: 1, title: "EASM",
    nodes: [{ ...root, ip_count: 120 }, company, { ...domain, ip_count: 42, detail: { reason: "第三批已复核的域名归属依据" } }, certificate],
    edges: [
      { source: root.id, target: company.id, confirmed: true },
      { source: company.id, target: domain.id, confirmed: true },
      { source: domain.id, target: certificate.id },
    ],
    summary: { clues: 4, fofa_assets: 120, fofa_ips: 30 },
    heat: [{ date: "2026-11-10", value: 3 }, { date: "2026-11-17", value: 5 }, { date: "2026-11-18", value: 8 }],
    components: [
      { name: "nginx", display_name: "NGINX", count: 42, hot_days: 6, tags: [{ type: "hot" }, { type: "kev", summary: "已列入 KEV" }] },
      { name: "openssl", count: 7 },
    ],
  },
]

type ModelInput = {
  stream?: boolean
  messages: { role: string; content?: string; tool_call_id?: string }[]
  tools?: { function: { name: string } }[]
}
type Receipt = { id?: string; panel_id?: string; revision: number }

function reply(response: ServerResponse, input: ModelInput, message: object, finish = "stop") {
  if (!input.stream) {
    response.writeHead(200, { "Content-Type": "application/json" })
    response.end(JSON.stringify({ choices: [{ index: 0, message, finish_reason: finish }] }))
    return
  }
  if (!response.headersSent) response.writeHead(200, { "Content-Type": "text/event-stream" })
  const chunk = (delta: object, reason: string | null) => ({ choices: [{ index: 0, delta, finish_reason: reason }] })
  response.end(`data: ${JSON.stringify(chunk(message, null))}\n\ndata: ${JSON.stringify(chunk({}, finish))}\n\ndata: [DONE]\n\n`)
}

/** Drives the real model/tool boundary, with each next batch released by its UI observation. */
export async function startEasmFixture() {
  const errors: string[] = []
  const receipts = new Map<string, Receipt>()
  const releases: (() => void)[] = []
  const gates = easmSnapshots.map(() => new Promise<void>((resolve) => releases.push(resolve)))
  let finish!: () => void
  const finalReply = new Promise<void>((resolve) => { finish = resolve })
  let requests = 0
  const endpoint = createServer(async (incoming, response) => {
    try {
      const path = new URL(incoming.url ?? "/", "http://127.0.0.1").pathname
      if (incoming.method === "GET") {
        response.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify({ data: [{ id: "easm-fixture" }] }))
        return
      }
      if (path !== "/chat/completions" || incoming.headers.authorization) throw new Error("Expected a keyless loopback model request")
      let raw = ""
      incoming.setEncoding("utf8")
      for await (const part of incoming) raw += part
      const input = JSON.parse(raw) as ModelInput
      if (input.messages[0]?.content?.startsWith("## Schedule answer claims")) {
        reply(response, input, { role: "assistant", content: '{"claims":[]}' })
        return
      }
      requests++
      for (const message of input.messages) {
        if (message.role === "tool" && message.tool_call_id?.startsWith("easm-")) {
          receipts.set(message.tool_call_id, JSON.parse(message.content ?? "{}") as Receipt)
        }
      }
      const call = (id: string, name: string, args: object) => {
        if (!input.tools?.some((entry) => entry.function.name === name)) throw new Error(`Missing real tool ${name}`)
        reply(response, input, { role: "assistant", tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }] }, "tool_calls")
      }
      if (!receipts.has("easm-open")) {
        if (!input.messages.some((message) => message.role === "user" && message.content?.includes(easmPrompt))) {
          throw new Error("The model did not receive the exposure inventory request")
        }
        call("easm-open", "open_task_panel", { plugin_id: "easm-asset-portrait" })
        return
      }
      const opened = receipts.get("easm-open")!
      const panelId = opened.panel_id ?? opened.id
      if (!panelId) throw new Error("Real open receipt omitted panel identity")
      if (!receipts.has("easm-read")) {
        call("easm-read", "read_panel_state", { panel_id: panelId })
        return
      }
      for (let index = 0; index < easmSnapshots.length; index++) {
        const id = `easm-batch-${index + 1}`
        if (receipts.has(id)) continue
        const previous = receipts.get(index === 0 ? "easm-read" : `easm-batch-${index}`)!
        if (!Number.isInteger(previous.revision)) throw new Error("Real panel receipt omitted its revision")
        if (input.stream) {
          response.writeHead(200, { "Content-Type": "text/event-stream" })
          response.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: "assistant", reasoning_content: `Preparing portrait batch ${index + 1}.` }, finish_reason: null }] })}\n\n`)
        }
        await gates[index]
        if (response.destroyed) return
        call(id, "publish_panel_update", {
          protocol_version: 1, panel_id: panelId, operation_id: id,
          base_revision: previous.revision, kind: "snapshot", state: easmSnapshots[index],
        })
        return
      }
      await finalReply
      if (!response.destroyed) reply(response, input, { role: "assistant", content: easmCompletion })
    } catch (error) {
      errors.push(String(error))
      if (!response.headersSent) response.writeHead(400, { "Content-Type": "application/json" })
      response.end(JSON.stringify({ error: { message: String(error), type: "invalid_request_error" } }))
    }
  })
  await new Promise<void>((resolve, reject) => { endpoint.once("error", reject); endpoint.listen(0, "127.0.0.1", resolve) })
  const address = endpoint.address()
  if (!address || typeof address === "string") throw new Error("EASM fixture did not listen")
  return {
    url: `http://127.0.0.1:${address.port}`,
    errors,
    releaseBatch: (batch: number) => releases[batch - 1](),
    finish,
    state: () => ({ requests, receipts: Object.fromEntries(receipts) }),
    close: () => new Promise<void>((resolve, reject) => {
      for (const release of releases) release()
      finish()
      endpoint.close((error) => error ? reject(error) : resolve())
      endpoint.closeAllConnections()
    }),
  }
}
