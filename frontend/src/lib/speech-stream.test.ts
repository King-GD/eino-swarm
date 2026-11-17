// @vitest-environment node
import { describe, expect, it, vi } from "vitest"
import { createServer } from "node:http"
import { gzipSync } from "node:zlib"
import { speechAudioResponse, type SpeechAudioStream } from "./speech-stream"

function fixture(type: string) {
  let push!: ReadableStreamDefaultController<Uint8Array>
  const cancel = vi.fn()
  const response = new Response(new ReadableStream<Uint8Array>({ start(controller) { push = controller }, cancel }), { headers: { "Content-Type": type } })
  return { response, cancel, push, write: (text: string) => push.enqueue(new TextEncoder().encode(text)) }
}
const collect = async (audio: SpeechAudioStream) => { const result: number[] = []; for await (const part of audio.chunks) result.push(...part); return result }

describe("incremental speech responses", () => {
  it("delivers PCM before the response finishes and cancels an unconsumed response", async () => {
    const live = fixture("audio/pcm")
    const audio = await speechAudioResponse(live.response, new AbortController().signal, 100) as SpeechAudioStream
    const iterator = audio.chunks[Symbol.asyncIterator]()
    live.push.enqueue(new Uint8Array([1, 2, 3]))
    expect(await iterator.next()).toEqual({ value: new Uint8Array([1, 2, 3]), done: false })
    audio.cancel()
    await expect(iterator.next()).rejects.toMatchObject({ name: "AbortError" })
    expect(live.cancel).toHaveBeenCalledOnce()
    const unused = fixture("audio/pcm")
    const abort = new AbortController()
    await speechAudioResponse(unused.response, abort.signal, 100)
    abort.abort()
    await vi.waitFor(() => expect(unused.cancel).toHaveBeenCalledOnce())
  })

  it("parses split SSE lines and audio data before done", async () => {
    const live = fixture("text/event-stream")
    const audio = await speechAudioResponse(live.response, new AbortController().signal, 10) as SpeechAudioStream
    const iterator = audio.chunks[Symbol.asyncIterator]()
    live.write('event: speech.audio.delta\r\ndata: {"type":"speech.audio.delta","audio":"AQID"}\r')
    live.write('\n\r\n')
    expect(await iterator.next()).toEqual({ done: false, value: new Uint8Array([1, 2, 3]) })
    live.write('data: {"type":"speech.audio.done"}\n\n')
    expect(await iterator.next()).toEqual({ done: true, value: undefined })
    expect(live.cancel).toHaveBeenCalledOnce()
  })

  it.each([
    ['data: {"type":"speech.audio.delta","audio":"AQI="}\n\n', /ended|complete/i],
    ['data: {"type":"error","error":{"message":"private upstream"}}\n\n', /failed/i],
    ['event: speech.audio.delta\ndata: {"type":"speech.audio.done"}\n\n', /invalid/i],
    ['data: {"type":"speech.audio.delta","audio":"@@@="}\n\n', /invalid/i],
    ['data: {"type":"speech.audio.delta","delta":"AQI="}\n\n', /invalid/i],
  ])("rejects incomplete and invalid SSE without exposing payloads", async (frame, reason) => {
    const live = fixture("text/event-stream")
    const audio = await speechAudioResponse(live.response, new AbortController().signal, 100) as SpeechAudioStream
    live.write(frame); live.push.close()
    await expect(collect(audio)).rejects.toThrow(reason)
  })

  it("bounds total audio and unfinished SSE frames", async () => {
    const pcm = fixture("audio/pcm")
    const audio = await speechAudioResponse(pcm.response, new AbortController().signal, 2) as SpeechAudioStream
    pcm.push.enqueue(new Uint8Array([1, 2, 3]))
    await expect(collect(audio)).rejects.toThrow(/limit/i)
    expect(pcm.cancel).toHaveBeenCalledOnce()
    const sse = fixture("text/event-stream")
    const stream = await speechAudioResponse(sse.response, new AbortController().signal, 2 << 20) as SpeechAudioStream
    sse.write("data: " + "x".repeat(1 << 20))
    await expect(collect(stream)).rejects.toThrow(/limit/i)
    expect(sse.cancel).toHaveBeenCalledOnce()
    const decoded = fixture("text/event-stream")
    const over = await speechAudioResponse(decoded.response, new AbortController().signal, 1) as SpeechAudioStream
    decoded.write('data: {"type":"speech.audio.delta","audio":"AQI="}\n\n')
    await expect(collect(over)).rejects.toMatchObject({ code: "too_large" })
    expect(decoded.cancel).toHaveBeenCalledOnce()
  })

  it("cancels a paused body on caller abort and on an early consumer exit", async () => {
    const live = fixture("audio/pcm")
    const abort = new AbortController()
    const audio = await speechAudioResponse(live.response, abort.signal, 10) as SpeechAudioStream
    const iteration = collect(audio)
    abort.abort()
    await expect(iteration).rejects.toMatchObject({ name: "AbortError" })
    expect(live.cancel).toHaveBeenCalledOnce()
    const early = fixture("audio/pcm")
    const stream = await speechAudioResponse(early.response, new AbortController().signal, 10) as SpeechAudioStream
    early.push.enqueue(new Uint8Array([1, 2]))
    for await (const bytes of stream.chunks) { expect(bytes.length).toBe(2); break }
    expect(early.cancel).toHaveBeenCalledOnce()
  })

  it("refuses empty and truncated responses and declared over-limit bodies", async () => {
    const signal = new AbortController().signal
    const empty = await speechAudioResponse(new Response(new Uint8Array(), { headers: { "Content-Type": "audio/pcm" } }), signal, 10) as SpeechAudioStream
    await expect(collect(empty)).rejects.toMatchObject({ code: "empty" })
    const partial = await speechAudioResponse(new Response(new Uint8Array([1, 2]), { headers: { "Content-Type": "audio/pcm", "Content-Length": "4" } }), signal, 10) as SpeechAudioStream
    await expect(collect(partial)).rejects.toMatchObject({ code: "invalid_audio" })
    const large = fixture("audio/wav")
    large.response.headers.set("Content-Length", "100")
    await expect(speechAudioResponse(large.response, signal, 10)).rejects.toMatchObject({ code: "too_large" })
    expect(large.cancel).toHaveBeenCalledOnce()
  })

  it("measures decoded PCM after real fetch decompression rather than compressed Content-Length", async () => {
    const server = createServer((request, response) => {
      const pcm = new Uint8Array(request.url === "/short" ? 2 : 2400)
      const compressed = gzipSync(pcm)
      response.writeHead(200, { "Content-Type": "audio/pcm", "Content-Encoding": "gzip", "Content-Length": compressed.length })
      response.end(compressed)
    })
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve))
    const address = server.address()
    if (!address || typeof address === "string") throw new Error("Fixture did not listen")
    const endpoint = `http://127.0.0.1:${address.port}`
    try {
      for (const [path, length] of [["/long", 2400], ["/short", 2]] as const) {
        const response = await fetch(endpoint + path)
        // Both compressed-smaller and compressed-larger cases use actual HTTP.
        expect(Number(response.headers.get("Content-Length"))).not.toBe(length)
        const audio = await speechAudioResponse(response, new AbortController().signal, length) as SpeechAudioStream
        expect(await collect(audio)).toHaveLength(length)
      }
      const over = await speechAudioResponse(await fetch(endpoint + "/long"), new AbortController().signal, 2399) as SpeechAudioStream
      await expect(collect(over)).rejects.toMatchObject({ code: "too_large" })
    } finally {
      server.closeAllConnections()
      await new Promise<void>((resolve) => server.close(() => resolve()))
    }
  })

  it("accepts completed audio and explicit octet PCM, and rejects ambiguous endian formats", async () => {
    const blob = await speechAudioResponse(new Response(new Uint8Array([1, 2]), { headers: { "Content-Type": "audio/wav" } }), new AbortController().signal, 10)
    expect(blob).toBeInstanceOf(Blob)
    expect(new Uint8Array(await (blob as Blob).arrayBuffer())).toEqual(new Uint8Array([1, 2]))
    for (const type of ["audio/L16", "application/octet-stream", "text/html", "audio/pcm;rate=16000"]) {
      await expect(speechAudioResponse(new Response("bytes", { headers: { "Content-Type": type } }), new AbortController().signal, 10)).rejects.toThrow(/audio|format/i)
    }
    const pcm = await speechAudioResponse(new Response(new Uint8Array([0, 1]), { headers: { "Content-Type": "application/octet-stream" } }), new AbortController().signal, 10, true) as SpeechAudioStream
    expect(await collect(pcm)).toEqual([0, 1])
  })

  it.each(["audio/pcm;endianness=big", "audio/pcm;encoding=mp3", "audio/pcm;rate=24000;channels=2", "application/octet-stream;endianness=big"])("refuses contradictory or unknown raw PCM parameters: %s", async (type) => {
    const live = fixture(type)
    await expect(speechAudioResponse(live.response, new AbortController().signal, 10, true)).rejects.toMatchObject({ code: "invalid_audio" })
    expect(live.cancel).toHaveBeenCalledOnce()
  })

  it.each([
    { sample_rate: 48000 }, { sample_rate: "24000" }, { channels: 2 },
    { response_format: "mp3" }, { mime_type: "audio/mpeg" }, { mime_type: "audio/pcm;endianness=big" },
  ])("refuses explicitly incompatible SSE PCM metadata before yielding audio: %j", async (metadata) => {
    const live = fixture("text/event-stream")
    const audio = await speechAudioResponse(live.response, new AbortController().signal, 10, true) as SpeechAudioStream
    live.write(`data: ${JSON.stringify({ type: "speech.audio.delta", audio: "AQI=", ...metadata })}\n\ndata: {"type":"speech.audio.done"}\n\n`)
    const iterator = audio.chunks[Symbol.asyncIterator]()
    await expect(iterator.next()).rejects.toMatchObject({ code: "invalid_audio" })
    expect(live.cancel).toHaveBeenCalledOnce()
  })

  it("accepts matching PCM metadata and supported numeric MIME parameters", async () => {
    const pcm = await speechAudioResponse(new Response(new Uint8Array([1, 2]), { headers: { "Content-Type": 'audio/pcm; rate="24000"; channels=1' } }), new AbortController().signal, 10) as SpeechAudioStream
    expect(await collect(pcm)).toEqual([1, 2])
    const live = fixture("text/event-stream")
    const audio = await speechAudioResponse(live.response, new AbortController().signal, 10, true) as SpeechAudioStream
    live.write('data: {"type":"speech.audio.delta","audio":"AQI=","sample_rate":24000,"channels":1,"response_format":"pcm","mime_type":"audio/pcm;rate=24000;channels=1"}\n\ndata: {"type":"speech.audio.done"}\n\n')
    expect(await collect(audio)).toEqual([1, 2])
  })
})
