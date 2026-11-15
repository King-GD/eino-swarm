import { useCallback, useEffect, useRef, useState, type RefObject } from "react"
import { useApp } from "@/store/app"
import { toastError } from "@/store/toasts"
import { useT } from "./use-t"
import { speechRecordingSupported, startSpeechRecording, type SpeechRecording } from "./speech-media"
import { insertSpeechText } from "./speech-text"
import { transcribeSpeech } from "./speech-api"

export type VoiceInputState = "idle" | "opening" | "recording" | "transcribing" | "failed"

/** Bind every asynchronous microphone/model result to its original draft. */
export function useComposerSpeech(input: {
  scope?: string; text: string; onText: (text: string) => void; textarea: RefObject<HTMLTextAreaElement | null>; disabled: boolean
}) {
  const t = useT()
  const snapshot = useApp((store) => store.meta?.speech)
  const ready = Boolean(snapshot?.input.enabled && snapshot.input.ready && speechRecordingSupported())
  const signature = JSON.stringify(snapshot)
  const [state, setState] = useState<VoiceInputState>("idle")
  const [seconds, setSeconds] = useState(0)
  const [level, setLevel] = useState(0)
  const latest = useRef(input)
  latest.current = input
  const recording = useRef<SpeechRecording | undefined>(undefined)
  const controller = useRef<AbortController | undefined>(undefined)
  const captured = useRef({ original: "", caret: 0 })
  const retryBlob = useRef<Blob | undefined>(undefined)
  const cancel = useCallback(() => {
    controller.current?.abort()
    controller.current = undefined
    recording.current?.cancel()
    recording.current = undefined
    retryBlob.current = undefined
    setState("idle")
    setLevel(0)
  }, [])

  useEffect(() => { cancel(); return cancel }, [input.scope, signature, ready, cancel])
  useEffect(() => {
    if (state === "idle") return
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); cancel() } }
    const hidden = () => { if (document.hidden) cancel() }
    window.addEventListener("keydown", key, true)
    document.addEventListener("visibilitychange", hidden)
    return () => { window.removeEventListener("keydown", key, true); document.removeEventListener("visibilitychange", hidden) }
  }, [state, cancel])

  const recognize = async (blob: Blob, owner: AbortController) => {
    if (owner.signal.aborted || controller.current !== owner) return
    retryBlob.current = blob
    setState("transcribing")
    try {
      const spoken = await transcribeSpeech(blob, owner.signal)
      if (owner.signal.aborted || controller.current !== owner) return
      const next = insertSpeechText(latest.current.text, captured.current.original, captured.current.caret, spoken)
      latest.current.onText(next.text)
      requestAnimationFrame(() => {
        if (owner.signal.aborted || controller.current !== owner) return
        latest.current.textarea.current?.focus()
        latest.current.textarea.current?.setSelectionRange(next.caret, next.caret)
      })
      retryBlob.current = undefined
      setState("idle")
    } catch (error) {
      if (owner.signal.aborted || controller.current !== owner) return
      setState("failed")
      toastError(error instanceof Error ? error.message : t("speech.failed"), { title: t("speech.input") })
    }
  }
  const finish = async () => {
    const owner = controller.current
    const capture = recording.current
    if (!owner || !capture) return
    recording.current = undefined
    setState("transcribing")
    try { await recognize(await capture.stop(), owner) }
    catch (error) {
      if (owner.signal.aborted || controller.current !== owner) return
      setState("idle")
      toastError(error instanceof Error ? error.message : t("speech.failed"), { title: t("speech.input") })
    }
  }
  const toggle = async () => {
    if (state === "recording") { await finish(); return }
    if (state === "failed" && retryBlob.current && controller.current) { await recognize(retryBlob.current, controller.current); return }
    if (!ready || input.disabled || !snapshot || state !== "idle") return
    const owner = new AbortController()
    controller.current = owner
    captured.current = { original: latest.current.text, caret: latest.current.textarea.current?.selectionStart ?? latest.current.text.length }
    setState("opening"); setSeconds(0); setLevel(0)
    try {
      const capture = await startSpeechRecording({
        signal: owner.signal, maxDurationSeconds: snapshot.input.max_duration_seconds, maxBytes: snapshot.limits.input_bytes,
        format: snapshot.input.recording_format, onDuration: setSeconds, onLevel: setLevel,
        onCancel: () => { if (controller.current === owner) { owner.abort(); controller.current = undefined; recording.current = undefined; setState("idle"); setLevel(0) } },
        onLimit: () => { void finish() }, onError: (error) => {
          if (owner.signal.aborted || controller.current !== owner) return
          cancel(); toastError(error instanceof Error ? error.message : t("speech.failed"))
        },
      })
      if (owner.signal.aborted || controller.current !== owner) { capture.cancel(); return }
      recording.current = capture
      setState("recording")
    } catch (error) {
      if (owner.signal.aborted || controller.current !== owner) return
      cancel()
      const key = error instanceof DOMException && error.name === "NotAllowedError" ? "speech.permissionDenied" : "speech.failed"
      toastError(t(key), { title: t("speech.input") })
    }
  }
  return { ready, state, seconds, level, busy: state !== "idle", disabled: input.disabled, toggle, cancel }
}
