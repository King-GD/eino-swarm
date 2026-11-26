import { GATEWAY_CAPABILITY_REFRESH_EVENT } from "@/lib/gateway-capabilities"
import { useEffect, useRef, useState } from "react"
import { Check, Loader2, Mic, RotateCcw, X } from "lucide-react"
import { speechRecordingSupported, startSpeechRecording, type SpeechRecording } from "../../../frontend/src/lib/speech-media"
import { installAndroidBack } from "@/lib/android-back"
import { t } from "@/lib/i18n"
import { cancelPhoneSpeech, transcribePhoneSpeech, usePhoneSpeech } from "@/lib/phone-speech"
import { SpeechCallError } from "@/lib/speech-transport"
import { cn } from "@/lib/cn"

export function speechFailureMessage(error: unknown): string {
  const name = error && typeof error === "object" && "name" in error ? String(error.name) : ""
  if (name === "NotAllowedError" || name === "SecurityError") return t("speech.permission")
  if (error instanceof SpeechCallError) {
    if (error.code === "invalid_audio_parameter") return t("speech.audioInvalid")
    if (error.code === "network") return t("speech.network")
    if (error.code === "timeout") return t("speech.timeout")
    if (error.code === "empty") return t("speech.empty")
    if (error.code === "too_large") return t("speech.tooLarge")
    if (error.httpStatus) return t("speech.serviceFailed", { status: error.httpStatus })
  }
  return t("speech.failed")
}

export type SpeechCapturePhase = "idle" | "preparing" | "recording" | "transcribing" | "error"
const emptyLevels = Array.from({ length: 20 }, () => 0)
const actionClass = "flex size-11 shrink-0 items-center justify-center rounded-full focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-40"

/** The composer hosts the recording controls. The result is
 * always a draft edit, never a send or a turn-start operation. */
export function SpeechCapture({ disabled, onStart, onText, onPhaseChange }: {
  disabled?: boolean
  onStart: () => void
  onText: (text: string) => void
  onPhaseChange: (phase: SpeechCapturePhase) => void
}) {
  const speech = usePhoneSpeech()
  const [phase, setPhase] = useState<SpeechCapturePhase>("idle")
  const [seconds, setSeconds] = useState(0)
  const [levels, setLevels] = useState(emptyLevels)
  const [error, setError] = useState("")
  const controller = useRef<AbortController | undefined>(undefined)
  const recorder = useRef<SpeechRecording | undefined>(undefined)
  const recorded = useRef<Blob | undefined>(undefined)
  const phaseRef = useRef<SpeechCapturePhase>("idle")
  const panel = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const handlers = useRef({ onStart, onText, onPhaseChange })
  handlers.current = { onStart, onText, onPhaseChange }
  const changePhase = (value: SpeechCapturePhase) => {
    phaseRef.current = value
    setPhase(value)
    handlers.current.onPhaseChange(value)
  }
  const cancel = () => {
    controller.current?.abort(); recorder.current?.cancel()
    controller.current = undefined; recorder.current = undefined; recorded.current = undefined
    setError(""); setLevels(emptyLevels); changePhase("idle")
  }
  const cancelFromControl = () => {
    cancel()
    requestAnimationFrame(() => trigger.current?.focus())
  }
  const cancelRef = useRef(cancel)
  cancelRef.current = cancel
  useEffect(() => {
    const cancelled = () => cancelRef.current()
    window.addEventListener("zwai.phone.speech.cancel", cancelled)
    return () => { window.removeEventListener("zwai.phone.speech.cancel", cancelled); cancelRef.current() }
  }, [])
  useEffect(() => { cancelRef.current() }, [speech.epoch])
  const open = phase !== "idle"
  useEffect(() => {
    if (!open) return
    panel.current?.focus()
    return installAndroidBack(() => { cancelRef.current(); return true })
  }, [open])

  const transcribe = async (blob: Blob, owner: AbortController) => {
    recorded.current = blob
    changePhase("transcribing")
    try {
      const text = await transcribePhoneSpeech(blob, owner.signal)
      if (controller.current !== owner || owner.signal.aborted) return
      handlers.current.onText(text)
      recorded.current = undefined; controller.current = undefined; changePhase("idle")
    } catch (failure) {
      if (controller.current !== owner || owner.signal.aborted) return
      setError(speechFailureMessage(failure)); changePhase("error")
    }
  }
  const finish = async () => {
    const owner = controller.current
    const active = recorder.current
    if (!owner || !active || phaseRef.current !== "recording") return
    changePhase("transcribing")
    try {
      const blob = await active.stop()
      recorder.current = undefined
      if (controller.current !== owner || owner.signal.aborted) return
      await transcribe(blob, owner)
    } catch (failure) {
      if (controller.current !== owner || owner.signal.aborted) return
      setError(speechFailureMessage(failure)); changePhase("error")
    }
  }
  const finishRef = useRef(finish)
  finishRef.current = finish
  const start = async () => {
    window.dispatchEvent(new Event(GATEWAY_CAPABILITY_REFRESH_EVENT))
    const input = speech.snapshot?.input
    if (!input || !speech.inputReady || disabled) return
    cancelPhoneSpeech()
    handlers.current.onStart()
    const owner = new AbortController()
    controller.current = owner
    recorded.current = undefined
    setError(""); setSeconds(0); setLevels(emptyLevels); changePhase("preparing")
    try {
      const active = await startSpeechRecording({ signal: owner.signal, format: input.recording_format,
        maxDurationSeconds: input.max_duration_seconds, maxBytes: speech.snapshot!.limits.input_bytes,
        onDuration: (elapsed) => setSeconds(Math.floor(elapsed)),
        onLevel: (value) => setLevels(history => [...history.slice(1), value]),
        onLimit: () => { void finishRef.current() },
        onError: (failure) => { if (!owner.signal.aborted) { setError(speechFailureMessage(failure)); changePhase("error") } },
        onCancel: () => { if (controller.current === owner) { owner.abort(); controller.current = undefined; recorder.current = undefined; changePhase("idle") } },
      })
      if (controller.current !== owner || owner.signal.aborted) { active.cancel(); return }
      recorder.current = active; changePhase("recording")
    } catch (failure) {
      if (controller.current !== owner || owner.signal.aborted) return
      setError(speechFailureMessage(failure)); changePhase("error")
    }
  }
  const retry = () => {
    if (!recorded.current) { void start(); return }
    const owner = new AbortController()
    controller.current?.abort(); controller.current = owner
    setError("")
    void transcribe(recorded.current, owner)
  }
  if (!open) return speech.inputReady && speechRecordingSupported() ? (
    // The one-line field is 36px. A 44px control aligns to the bottom and
    // leaves empty space across the top of the rounded row.
    <button ref={trigger} type="button" aria-label={t("speech.start")} title={t("speech.start")} disabled={disabled}
      className="flex size-9 shrink-0 items-center justify-center self-end rounded-full text-muted-foreground focus-visible:outline-2 focus-visible:outline-ring active:bg-accent disabled:opacity-40"
      onClick={() => { void start() }}><Mic className="size-4" aria-hidden /></button>
  ) : null

  const recording = phase === "recording"
  const preparing = phase === "preparing"
  const status = recording ? t("speech.recording", { seconds }) : preparing ? t("speech.preparing") : t("speech.transcribing")
  return (
    <div ref={panel} role="group" aria-label={t("speech.panel")} tabIndex={-1}
      className="order-first flex min-h-11 min-w-0 basis-full items-center gap-2 outline-none"
      onKeyDown={(event) => {
        if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); cancelFromControl() }
      }}>
      <button type="button" className={cn(actionClass, "text-muted-foreground active:bg-accent")}
        aria-label={t("speech.cancel")} title={t("speech.cancel")} onClick={cancelFromControl}><X className="size-5" aria-hidden /></button>
      {phase === "error" ? (
        <p role="alert" className="min-w-0 flex-1 py-1 text-sm leading-snug text-destructive">{error}</p>
      ) : recording ? (
        <div className="flex min-w-0 flex-1 items-center justify-between gap-2.5">
          <span className="sr-only">{status}</span>
          <meter className="sr-only" aria-label={t("speech.level")} min={0} max={1} value={levels.at(-1) ?? 0} />
          <span aria-hidden className="flex shrink-0 items-center gap-1.5 text-xs font-medium tabular-nums">
            <span className="size-1.5 rounded-full bg-destructive" />
            {Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}
          </span>
          <div className="flex h-7 min-w-0 flex-1 items-center justify-center gap-[3px]" aria-hidden>
            {levels.map((value, index) => (
              // scaleY keeps the meter on the compositor; animating the bars'
              // height relaid out the row on every frame of the recording.
              <span key={index} className="h-6 max-w-1 flex-1 origin-center rounded-full bg-current text-muted-foreground transition-transform duration-100 motion-reduce:transition-none"
                style={{ transform: `scaleY(${0.125 + 0.875 * value ** 0.65})` }} />
            ))}
          </div>
        </div>
      ) : (
        <div role="status" className="flex min-w-0 flex-1 items-center gap-2 py-1 text-sm text-muted-foreground">
          <Loader2 className="size-4 shrink-0 motion-safe:animate-spin" aria-hidden />
          <span>{status}</span>
        </div>
      )}
      {phase === "error" ? (
        <button type="button" disabled={disabled} className={cn(actionClass, "bg-primary text-primary-foreground active:opacity-75")}
          aria-label={t("speech.retry")} title={t("speech.retry")} onClick={retry}><RotateCcw className="size-5" aria-hidden /></button>
      ) : preparing || recording ? (
        <button type="button" disabled={!recording} className={cn(actionClass, "bg-primary text-primary-foreground active:opacity-75")}
          aria-label={t("speech.finish")} title={t("speech.finish")}
          onClick={() => { void finish() }}><Check className="size-5" aria-hidden /></button>
      ) : null}
    </div>
  )
}
