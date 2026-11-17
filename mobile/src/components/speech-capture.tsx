import { GATEWAY_CAPABILITY_REFRESH_EVENT } from "@/lib/gateway-capabilities"
import { useEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { Check, Loader2, Mic, RotateCcw, X } from "lucide-react"
import { speechRecordingSupported, startSpeechRecording, type SpeechRecording } from "../../../frontend/src/lib/speech-media"
import { installAndroidBack } from "@/lib/android-back"
import { t } from "@/lib/i18n"
import { cancelPhoneSpeech, transcribePhoneSpeech, usePhoneSpeech } from "@/lib/phone-speech"
import { SpeechCallError } from "@/lib/speech-transport"
import { Button } from "./ui/button"

export function speechFailureMessage(error: unknown): string {
  const name = error && typeof error === "object" && "name" in error ? String(error.name) : ""
  if (name === "NotAllowedError" || name === "SecurityError") return t("speech.permission")
  if (error instanceof SpeechCallError) {
    if (error.code === "timeout") return t("speech.timeout")
    if (error.code === "empty") return t("speech.empty")
    if (error.code === "too_large") return t("speech.tooLarge")
  }
  return t("speech.failed")
}

type Phase = "idle" | "preparing" | "recording" | "transcribing" | "error"

/** The phone keeps recording controls in a touch-sized panel. The result is
 * always a draft edit, never a send or a turn-start operation. */
export function SpeechCapture({ disabled, onStart, onText, onBusyChange }: {
  disabled?: boolean
  onStart: () => void
  onText: (text: string) => void
  onBusyChange: (busy: boolean) => void
}) {
  const speech = usePhoneSpeech()
  const [phase, setPhase] = useState<Phase>("idle")
  const [seconds, setSeconds] = useState(0)
  const [level, setLevel] = useState(0)
  const [error, setError] = useState("")
  const controller = useRef<AbortController | undefined>(undefined)
  const recorder = useRef<SpeechRecording | undefined>(undefined)
  const recorded = useRef<Blob | undefined>(undefined)
  const phaseRef = useRef<Phase>("idle")
  const panel = useRef<HTMLDivElement>(null)
  const handlers = useRef({ onStart, onText, onBusyChange })
  handlers.current = { onStart, onText, onBusyChange }
  const changePhase = (value: Phase) => {
    phaseRef.current = value
    setPhase(value)
    handlers.current.onBusyChange(value === "preparing" || value === "recording" || value === "transcribing")
  }
  const cancel = () => {
    controller.current?.abort(); recorder.current?.cancel()
    controller.current = undefined; recorder.current = undefined; recorded.current = undefined
    setError(""); setLevel(0); changePhase("idle")
  }
  const cancelRef = useRef(cancel)
  cancelRef.current = cancel
  useEffect(() => {
    const cancelled = () => cancelRef.current()
    window.addEventListener("zwai.phone.speech.cancel", cancelled)
    return () => { window.removeEventListener("zwai.phone.speech.cancel", cancelled); cancelRef.current() }
  }, [])
  useEffect(() => { cancelRef.current() }, [speech.epoch])
  useEffect(() => {
    if (phase === "idle") return
    panel.current?.focus()
    return installAndroidBack(() => { cancelRef.current(); return true })
  }, [phase])

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
    setError(""); setSeconds(0); setLevel(0); changePhase("preparing")
    try {
      const active = await startSpeechRecording({ signal: owner.signal, format: input.recording_format,
        maxDurationSeconds: input.max_duration_seconds, maxBytes: speech.snapshot!.limits.input_bytes,
        onDuration: setSeconds, onLevel: setLevel, onLimit: () => { void finishRef.current() },
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
  const open = phase !== "idle"
  return <>
    {speech.inputReady && speechRecordingSupported() ? <button type="button" aria-label={t("speech.start")} title={t("speech.start")}
      disabled={disabled || open} className="flex size-9 shrink-0 items-center justify-center rounded-full text-muted-foreground active:bg-accent disabled:opacity-40"
      onClick={() => { void start() }}><Mic className="size-5" aria-hidden /></button> : null}
    {phase === "transcribing" ? createPortal(<div className="fixed inset-x-3 top-[max(3.5rem,env(safe-area-inset-top))] z-50 mx-auto flex max-w-lg items-center gap-3 rounded-xl border border-border bg-background p-3 shadow-lg">
      <Loader2 aria-hidden className="size-5 animate-spin motion-reduce:animate-none" />
      <p role="status" className="flex-1 text-sm">{t("speech.transcribing")}</p>
      <Button type="button" variant="ghost" aria-label={t("speech.cancel")} onClick={cancel}><X aria-hidden className="size-5" /></Button>
    </div>, document.body) : null}
    {open && phase !== "transcribing" ? createPortal(<div className="fixed inset-0 z-50 flex items-end justify-center bg-background/95 p-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
      <div ref={panel} role="dialog" aria-modal="true" aria-label={t("speech.panel")} tabIndex={-1}
        className="flex w-full max-w-lg flex-col gap-5 rounded-3xl border border-border bg-background p-6 shadow-lg focus:outline-hidden"
        onKeyDown={(event) => {
          if (event.key === "Escape") { event.preventDefault(); cancel(); return }
          if (event.key === "Tab") {
            const buttons = Array.from(panel.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? [])
            const first = buttons[0], last = buttons.at(-1)
            if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.current)) { event.preventDefault(); last?.focus() }
            else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
          }
        }}>
        <h2 className="text-center text-lg font-semibold">{t("speech.panel")}</h2>
        <p role="status" className="text-center text-base tabular-nums">{phase === "preparing" ? t("speech.preparing") : phase === "recording" ? t("speech.recording", { seconds: Math.floor(seconds) }) : t("speech.failed")}</p>
        {phase === "recording" ? <meter aria-label={t("speech.level")} className="h-6 w-full" min={0} max={1} value={level} /> : null}
        {phase === "preparing" ? <Loader2 aria-hidden className="mx-auto size-8 animate-spin motion-reduce:animate-none" /> : null}
        {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        <div className="flex gap-3">
          <Button type="button" variant="outline" className="h-14 flex-1 gap-2" onClick={cancel}><X aria-hidden className="size-5" />{t("speech.cancel")}</Button>
          {phase === "error" ? <Button type="button" className="h-14 flex-1 gap-2" onClick={retry}><RotateCcw aria-hidden className="size-5" />{t("speech.retry")}</Button>
            : <Button type="button" className="h-14 flex-1 gap-2" disabled={phase !== "recording"} onClick={() => { void finish() }}><Check aria-hidden className="size-5" />{t("speech.finish")}</Button>}
        </div>
      </div>
    </div>, document.body) : null}
  </>
}
