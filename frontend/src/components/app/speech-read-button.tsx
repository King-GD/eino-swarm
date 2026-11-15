import { Loader2, Square, Volume2 } from "lucide-react"
import { useEffect, useMemo, useSyncExternalStore } from "react"
import { Button } from "@/components/ui/button"
import { useApp } from "@/store/app"
import { toastError } from "@/store/toasts"
import { speechPlayback } from "@/lib/speech-media"
import { speechText } from "@/lib/speech-text"
import { synthesizeSpeech } from "@/lib/speech-api"
import { useT } from "@/lib/use-t"

export function SpeechReadButton({ id, text }: { id: string; text: string }) {
  const t = useT()
  const speech = useApp((store) => store.meta?.speech)
  const activeId = useApp((store) => store.activeId)
  const playback = useSyncExternalStore(speechPlayback.subscribe, speechPlayback.getSnapshot)
  const current = playback.key === id && playback.state !== "idle"
  const signature = JSON.stringify(speech)
  const hasProse = useMemo(() => Boolean(speechText(text)), [text])
  useEffect(() => () => { if (speechPlayback.getSnapshot().key === id) speechPlayback.stop() }, [id, signature, activeId])
  useEffect(() => {
    const hidden = () => { if (document.hidden && speechPlayback.getSnapshot().key === id) speechPlayback.stop() }
    document.addEventListener("visibilitychange", hidden)
    return () => document.removeEventListener("visibilitychange", hidden)
  }, [id])
  if (!speech?.output.enabled || !speech.output.ready || !hasProse) return null
  const label = current ? t("speech.stopReading") : t("speech.read")
  return <Button type="button" variant="ghost" size="icon-2xs" className="text-muted-foreground" aria-label={label} title={label}
    onClick={() => {
      if (current) { speechPlayback.stop(); return }
      void speechPlayback.play(id, text, { chunkChars: speech.limits.chunk_chars,
        synthesize: (part, signal) => synthesizeSpeech(part, signal, speech.limits.output_bytes),
      }).catch((error) => toastError(error instanceof Error ? error.message : t("speech.failed"), { title: t("speech.output") }))
    }}>
    {current && playback.state === "loading" ? <Loader2 className="animate-spin" /> : current ? <Square className="fill-current" /> : <Volume2 />}
  </Button>
}
