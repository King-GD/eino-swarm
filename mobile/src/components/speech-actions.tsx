import { useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { Check, Copy, Loader2, Square, Volume2 } from "lucide-react"
import { speechPlayback } from "../../../frontend/src/lib/speech-media"
import { speechText } from "../../../frontend/src/lib/speech-text"
import { copyText } from "@/lib/copy-text"
import { t } from "@/lib/i18n"
import { cancelPhoneSpeech, synthesizePhoneSpeech, usePhoneSpeech } from "@/lib/phone-speech"
import { speechFailureMessage } from "./speech-capture"

export function SpeechActions({ id, text }: { id: string; text: string }) {
  const speech = usePhoneSpeech()
  const playback = useSyncExternalStore(speechPlayback.subscribe, speechPlayback.getSnapshot, speechPlayback.getSnapshot)
  const active = playback.key === id && playback.state !== "idle"
  const hasProse = useMemo(() => Boolean(speechText(text)), [text])
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState("")
  useEffect(() => () => { if (speechPlayback.getSnapshot().key === id) speechPlayback.stop() }, [id])
  useEffect(() => { if (!copied) return; const timer = setTimeout(() => setCopied(false), 1600); return () => clearTimeout(timer) }, [copied])
  const read = () => {
    setError("")
    if (active) { speechPlayback.stop(); return }
    if (!speech.outputReady || !speech.snapshot || !hasProse) return
    cancelPhoneSpeech()
    void speechPlayback.play(id, text, { chunkChars: speech.snapshot.limits.chunk_chars, synthesize: synthesizePhoneSpeech }).catch((failure) => setError(speechFailureMessage(failure)))
  }
  return <div className="mt-2 flex flex-wrap items-center gap-1 text-muted-foreground" data-testid="response-actions">
    <button type="button" aria-label={t(copied ? "speech.copied" : "speech.copy")} className="flex size-9 items-center justify-center rounded-full active:bg-accent" onClick={() => { void copyText(text).then((ok) => { setCopied(ok); if (!ok) setError(t("speech.copyFailed")) }) }}>
      {copied ? <Check aria-hidden className="size-4" /> : <Copy aria-hidden className="size-4" />}
    </button>
    {speech.outputReady && hasProse ? <button type="button" aria-label={t(active ? "speech.stop" : "speech.read")} data-playback-state={active ? playback.state : "idle"} className="flex size-9 items-center justify-center rounded-full active:bg-accent" onClick={read}>
      {active && playback.state === "loading" ? <Loader2 aria-hidden className="size-4 animate-spin motion-reduce:animate-none" /> : active ? <Square aria-hidden className="size-4" /> : <Volume2 aria-hidden className="size-4" />}
    </button> : null}
    {error ? <p role="alert" className="w-full text-xs text-destructive">{error}</p> : null}
  </div>
}
