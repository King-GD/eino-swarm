import { createServer, type ServerResponse } from "node:http"

type Message = { role: string; content?: string; tool_call_id?: string }
type Input = {
  model?: string
  messages: Message[]
  tools?: { function: { name: string } }[]
  stream?: boolean
}
type NodeReceipt = { ok: boolean; node: { id: string; version: number } }

/** Real model HTTP → Registry tool → durable project nodes → snapshot/SSE.
 * This is protocol evidence, not a measurement of autonomous model reasoning. */
export async function startNuwaFixture(mode?: "discussion" | "published" | "ordinary" | "legacy") {
  const errors: string[] = []
  const workerModels: string[] = []
  let taskNode: { id: string; version: number } | undefined
  let workerFact: { id: string; version: number } | undefined
  let threadID = "",
    sourceTurn = ""
  let releaseTask!: () => void
  const taskGate = new Promise<void>((resolve) => {
    releaseTask = resolve
  })
  let revision: { id: string; version: number; status?: string } | undefined
  const reply = (response: ServerResponse, input: Input, delta: object, finish = "stop") => {
    response.writeHead(200, {
      "Content-Type": input.stream ? "text/event-stream" : "application/json",
    })
    response.end(
      input.stream
        ? `data: ${JSON.stringify({ id: "nuwa-fixture", choices: [{ index: 0, delta, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ id: "nuwa-fixture", choices: [{ index: 0, delta: {}, finish_reason: finish }] })}\n\ndata: [DONE]\n\n`
        : JSON.stringify({ choices: [{ index: 0, message: delta, finish_reason: finish }] })
    )
  }
  const call = (
    response: ServerResponse,
    input: Input,
    id: string,
    args: object,
    name = "record_work"
  ) =>
    reply(
      response,
      input,
      {
        role: "assistant",
        tool_calls: [
          {
            index: 0,
            id,
            type: "function",
            function: {
              name,
              arguments: JSON.stringify(name === "record_work" && mode !== "ordinary" && !("id" in args) && !("scope" in args)
                ? { ...args, scope: "project" }
                : args),
            },
          },
        ],
      },
      "tool_calls"
    )
  const endpoint = createServer((request, response) => {
    if (request.method === "GET") {
      response.writeHead(200, { "Content-Type": "application/json" })
      response.end(JSON.stringify({ data: [{ id: "nuwa-fixture" }, { id: "nuwa-worker" }] }))
      return
    }
    let raw = ""
    request.setEncoding("utf8")
    request.on("data", (part: string) => {
      raw += part
    })
    request.on("end", async () => {
      try {
        if (request.headers.authorization) throw new Error("Nuwa fixture must remain keyless")
        const input = JSON.parse(raw) as Input
        if (input.messages[0]?.content?.startsWith("## Schedule answer claims")) {
          reply(response, input, { role: "assistant", content: JSON.stringify({ claims: [] }) })
          return
        }
        if (mode === "legacy") {
          if (input.tools?.some((tool) => tool.function.name === "record_work"))
            throw new Error("Work tool appeared while creating legacy history")
          const context = input.messages.map((message) => message.content ?? "").join("\n")
          if (input.messages[0]?.content?.startsWith("Extract durable work records")) {
            if (!context.includes("Historical inspection") || input.tools?.length)
              throw new Error("Wrong legacy extraction input")
            const source = context.match(/\[seq=(\d+) kind=user/)
            if (!source) throw new Error("Historical source was not offered")
            reply(response, input, { role: "assistant", content: JSON.stringify({ records: [{
              key: "inspection", kind: "fact", title: "Distilled inspection", status: "reported",
              summary: "The saved inspection identified a durable finding.", evidence: [Number(source[1])],
            }] }) })
          } else {
            reply(response, input, { role: "assistant", content: "Historical inspection found a durable observation." })
          }
          return
        }
        if (!input.tools?.some((tool) => tool.function.name === "record_work"))
          throw new Error("record_work absent at real model boundary")
        if (input.model === "nuwa-fixture" && mode !== "ordinary") {
          const executionTools = new Set([
            "read", "write", "edit", "ls", "tree", "glob", "grep", "exec",
            "web_search", "web_fetch", "python_runner", "screenshot",
          ])
          for (const tool of input.tools) {
            if (executionTools.has(tool.function.name) || tool.function.name.startsWith("mcp_"))
              throw new Error(`Nuwa manager gained an execution tool: ${tool.function.name}`)
          }
        } else if (input.model !== "nuwa-fixture" && input.model !== "nuwa-worker") {
          throw new Error(`Unexpected Nuwa fixture model: ${input.model}`)
        }
        if (mode === "discussion") {
          if (input.model !== "nuwa-fixture") throw new Error("Discussion reached a worker model")
          await taskGate
          reply(response, input, { role: "assistant", content: "The discussion is complete." })
          return
        }
        const result = (id: string) => {
          const message = input.messages.findLast(
            (message) => message.role === "tool" && message.tool_call_id === id
          )
          if (!message) return undefined
          const receipt = JSON.parse(message.content ?? "{}") as NodeReceipt
          if (!receipt.ok || !receipt.node?.id)
            throw new Error(`Node write refused: ${message.content}`)
          return receipt.node
        }
        if (mode === "ordinary") {
          if (!input.tools.some((tool) => tool.function.name === "read_work") ||
              !input.tools.some((tool) => tool.function.name === "read") ||
              input.tools.some((tool) => tool.function.name === "list_models")) {
            throw new Error("An ordinary manager did not receive the ordinary execution and work surfaces")
          }
          const callID = `ordinary-fact-${revision?.version ?? 0}`
          if (!result(callID)) {
            call(response, input, callID, revision?.status ? {
              id: revision.id, version: revision.version, status: revision.status,
              summary: revision.status === "retracted" ? "The new evidence refutes it." : "The follow-up supports it.",
            } : {
              kind: "fact", title: "Bounded observation", summary: "An evidence-based observation", status: "reported",
            })
            return
          }
          reply(response, input, { role: "assistant", content: "The work record is saved." })
          return
        }
        if (input.model === "nuwa-worker") {
          workerModels.push(input.model)
          if (!input.tools.some((tool) => tool.function.name === "write"))
            throw new Error("Worker lost its write tool at the real model boundary")
          if (!taskNode)
            throw new Error("Worker started before its shared task")
          const reviewWritten = input.messages.findLast(
            (message) => message.role === "tool" && message.tool_call_id === "nuwa-worker-review-write"
          )
          if (!reviewWritten) {
            call(response, input, "nuwa-worker-review-write", {
              file_path: "review-notes.md",
              content: "# Review notes\n\nThe conclusion is limited to the measured input.\n",
            }, "write")
            return
          }
          if (!reviewWritten.content?.startsWith("Updated file "))
            throw new Error(`Worker review notes write failed: ${reviewWritten.content}`)
          if (mode === "published") {
            const written = input.messages.findLast(
              (message) => message.role === "tool" && message.tool_call_id === "nuwa-worker-write"
            )
            if (!written) {
              call(response, input, "nuwa-worker-write", {
                file_path: "field-observations.md",
                content: "# Field observations\n\nObserved facts were recorded.\n",
              }, "write")
              return
            }
            if (!written.content?.startsWith("Updated file "))
              throw new Error(`Worker artifact write failed: ${written.content}`)
            const fact = result("nuwa-published-fact")
            if (!fact) {
              call(response, input, "nuwa-published-fact", {
                kind: "fact", title: "The review files were written", depends_on: [taskNode.id],
                own_tool_call_ids: ["nuwa-worker-review-write", "nuwa-worker-write"],
              })
              return
            }
            workerFact = fact
            if (!result("nuwa-worker-artifact")) {
              call(response, input, "nuwa-worker-artifact", {
                kind: "artifact",
                scope: "thread",
                title: "Field observations",
                summary: "Independent observations ready for review",
                status: "reported",
                depends_on: [fact.id],
                artifact_path: "field-observations.md",
              })
              return
            }
            reply(response, input, { role: "assistant", content: "Field observations published." })
            return
          }
          const fact = result("nuwa-fact")
          if (!fact) {
            call(response, input, "nuwa-fact", {
              kind: "fact",
              title: "Measured finding",
              summary: "The input supports a bounded conclusion",
              detail: "Detailed observation with its original source.",
              status: "reported",
              depends_on: [taskNode.id],
              evidence: [
                { thread_id: threadID, turn_id: sourceTurn, seq: 1, label: "Original task input" },
              ],
            })
          } else {
            workerFact = fact
            reply(response, input, {
              role: "assistant",
              content: "The independent observation is recorded with source evidence.",
            })
          }
          return
        }
        if (revision) {
          if (!result("nuwa-revise"))
            call(response, input, "nuwa-revise", {
              ...revision,
              kind: "fact",
              title: "Measured finding",
              summary: "A later check superseded the initial result",
              status: "superseded",
              detail: "The earlier result remains in history.",
            })
          else
            reply(response, input, {
              role: "assistant",
              content: "The shared result has been revised.",
            })
          return
        }
        const task = result("nuwa-task")
        if (!task) {
          call(response, input, "nuwa-task", {
            kind: "task",
            title: "Compare the evidence",
            summary: "Inspect and compare the supplied material",
            status: "running",
          })
          return
        }
        taskNode = task
        await taskGate
        const catalogMessage = input.messages.findLast(
          (message) => message.role === "tool" && message.tool_call_id === "nuwa-models"
        )
        if (!catalogMessage) {
          call(response, input, "nuwa-models", {}, "list_models")
          return
        }
        const catalog = JSON.parse(catalogMessage.content ?? "{}") as {
          models?: { ref: string; provider_id: string; model: string }[]
        }
        const chosen = catalog.models?.find(
          (model) => model.provider_id === "nuwa-worker-endpoint" && model.model === "nuwa-worker"
        )
        if (!chosen) throw new Error("The configured independent model did not reach list_models")
        const spawned = input.messages.findLast(
          (message) => message.role === "tool" && message.tool_call_id === "nuwa-spawn"
        )
        if (!spawned) {
          call(
            response,
            input,
            "nuwa-spawn",
            {
              role: "independent-review",
              task: `Review the supplied evidence for task ${task.id}; write review-notes.md, record the bounded observation and cite its source.`,
            },
            "spawn_agent"
          )
          return
        }
        const started = JSON.parse(spawned.content ?? "{}") as { agent_id?: string; error?: string }
        if (!started.agent_id || started.error)
          throw new Error(`Worker spawn refused: ${spawned.content}`)
        const waited = input.messages.findLast(
          (message) => message.role === "tool" && message.tool_call_id === "nuwa-wait"
        )
        if (!waited) {
          call(
            response,
            input,
            "nuwa-wait",
            { agent_ids: [started.agent_id], timeout_s: 30 },
            "wait_agents"
          )
          return
        }
        const settled = JSON.parse(waited.content ?? "{}") as {
          agents?: { agent_id: string; status: string }[]
        }
        if (!settled.agents?.some(
          (agent) => agent.agent_id === started.agent_id && agent.status === "done"
        ) || !workerFact)
          throw new Error(`Actual worker result was not collected: ${waited.content}`)
        const fact = workerFact
        if (!result("nuwa-artifact")) {
          call(response, input, "nuwa-artifact", {
            kind: "artifact",
            title: "Review notes",
            summary: "A result ready for independent review",
            status: "reported",
            depends_on: [fact.id],
            artifact_path: "review-notes.md",
            detail: "# Review notes\n\nThe conclusion is limited to the measured input.",
          })
          return
        }
        if (task && !result("nuwa-task-done")) {
          call(response, input, "nuwa-task-done", {
            id: task.id,
            version: task.version,
            kind: "task",
            title: "Compare the evidence",
            summary: "The evidence was compared and review notes produced",
            status: "done",
          })
          return
        }
        reply(response, input, {
          role: "assistant",
          content: "Shared work is recorded with its evidence.",
        })
      } catch (error) {
        errors.push(String(error))
        response.writeHead(400, { "Content-Type": "application/json" })
        response.end(
          JSON.stringify({ error: { message: String(error), type: "invalid_request_error" } })
        )
      }
    })
  })
  await new Promise<void>((resolve, reject) => {
    endpoint.once("error", reject)
    endpoint.listen(0, "127.0.0.1", resolve)
  })
  const address = endpoint.address()
  if (!address || typeof address === "string") throw new Error("Nuwa fixture did not listen")
  return {
    url: `http://127.0.0.1:${address.port}`,
    errors,
    workerModels,
    source: (thread: string, turn: string) => {
      threadID = thread
      sourceTurn = turn
    },
    releaseTask,
    revise: (node: { id: string; version: number; status?: string }) => {
      revision = node
    },
    close: () =>
      new Promise<void>((resolve, reject) => {
        releaseTask()
        endpoint.close((error) => (error ? reject(error) : resolve()))
        endpoint.closeAllConnections()
      }),
  }
}
