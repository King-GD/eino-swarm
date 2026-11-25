import { expect, test } from "vitest"

import { startMemoryReviewFixture } from "../../e2e/memory-review-fixture"

test("memory review fixture matches the requested completion framing", async () => {
  const fixture = await startMemoryReviewFixture()
  try {
    const send = (stream?: boolean) => fetch(`${fixture.url}/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ messages: [{ role: "user", content: "Check this result." }], stream }),
    })

    // The real composer prediction omits stream. A JSON decoder must not
    // receive event-stream data and mark the shared endpoint unhealthy.
    const completion = await send()
    expect(completion.ok).toBe(true)
    expect(completion.headers.get("content-type")).toContain("application/json")
    expect(await completion.json()).toMatchObject({ choices: [{
      message: { role: "assistant", content: expect.any(String) }, finish_reason: "stop",
    }] })

    const stream = await send(true)
    expect(stream.ok).toBe(true)
    expect(stream.headers.get("content-type")).toContain("text/event-stream")
    expect(await stream.text()).toContain("data: [DONE]\n\n")
  } finally {
    await fixture.close()
  }
})
