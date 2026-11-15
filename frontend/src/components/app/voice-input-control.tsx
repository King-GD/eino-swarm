import { Loader2, Mic, RotateCcw, Square, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useT } from "@/lib/use-t"
import type { useComposerSpeech } from "@/lib/use-composer-speech"

export function VoiceInputControl({ control }: { control: ReturnType<typeof useComposerSpeech> }) {
  const t = useT()
  if (!control.ready) return null
  const { state } = control
  const label = state === "recording" ? t("speech.finishRecording") : state === "failed" ? t("speech.retry") : t("speech.startRecording")
  return <div className="flex items-center gap-1" data-testid="voice-input-control">
    {control.busy ? <>
      <span role="status" className="text-xs tabular-nums text-muted-foreground">
        {state === "transcribing" ? t("speech.transcribing") : state === "opening" ? t("speech.opening") : `${Math.floor(control.seconds / 60)}:${Math.floor(control.seconds % 60).toString().padStart(2, "0")}`}
      </span>
      <Button type="button" variant="ghost" size="icon-xs" onClick={control.cancel} aria-label={t("speech.cancel")} title={t("speech.cancel")}><X /></Button>
    </> : null}
    <Button type="button" size="icon" variant={state === "recording" ? "destructive" : "secondary"} className="size-8 rounded-full"
      disabled={control.disabled || state === "opening" || state === "transcribing"} onClick={() => void control.toggle()} aria-label={label} title={label}>
      {state === "opening" || state === "transcribing" ? <Loader2 className="animate-spin" /> : state === "recording" ? <Square className="fill-current" style={{ opacity: 0.4 + control.level * 0.6 }} /> : state === "failed" ? <RotateCcw /> : <Mic />}
    </Button>
  </div>
}
