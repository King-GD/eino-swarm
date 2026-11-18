import { useEffect } from "react"
import { AudioParameterControls } from "../../../frontend/src/components/shared/audio-parameters"
import { currentGatewayAudioView, GATEWAY_CAPABILITY_REFRESH_EVENT } from "@/lib/gateway-capabilities"
import { saveGatewayAudioPreference } from "@/lib/gateway-audio-parameters"
import { loadGatewaySession } from "@/lib/gateway-account"
import { t } from "@/lib/i18n"
import { usePhoneSpeech } from "@/lib/phone-speech"
import { speechRoute } from "../../../frontend/src/lib/speech-types"

/** Gateway defaults are independent; explicit personal settings remain local. */
export function SpeechStatus() {
  const state = usePhoneSpeech()
  const speech = state.snapshot
  const identity = JSON.stringify([speech?.input.source_id,speech?.input.model,speech?.output.source_id,speech?.output.model])
  useEffect(() => { window.dispatchEvent(new Event(GATEWAY_CAPABILITY_REFRESH_EVENT)) },[identity])
  const session = loadGatewaySession()
  const inputRoute = speech && speechRoute(speech, "input")
  const outputRoute = speech && speechRoute(speech, "output")
  const routeLabel = (route: typeof inputRoute) => route?.execution === "gateway" ? t("speech.gateway") : state.standalone ? t("speech.phone") : route ? t("speech.local") : undefined
  return <section aria-label={t("speech.title")} className="flex flex-col gap-2 rounded-xl border border-border p-3" data-testid="speech-status">
    <h2 className="text-sm font-semibold">{t("speech.title")}{state.host ? ` · ${state.host}` : ""}</h2>
    <p className="text-xs text-muted-foreground">{t("speech.settingsHint")}</p>
    {speech ? <>
      <p className="text-xs">{t("speech.input")}: {t(state.inputReady ? "speech.ready" : speech.input.enabled && speech.input.ready ? "speech.unavailable" : "speech.off")}{speech.input.label ? ` · ${speech.input.label}` : ""}{routeLabel(inputRoute) ? ` · ${routeLabel(inputRoute)}` : ""}</p>
      <p className="text-xs">{t("speech.output")}: {t(state.outputReady ? "speech.ready" : speech.output.enabled && speech.output.ready ? "speech.unavailable" : "speech.off")}{speech.output.label ? ` · ${speech.output.label}` : ""}{routeLabel(outputRoute) ? ` · ${routeLabel(outputRoute)}` : ""}</p>
      {!state.connected && !state.standalone && (inputRoute?.execution === "pc_local" && speech.input.enabled || outputRoute?.execution === "pc_local" && speech.output.enabled) ? <p role="status" className="text-xs text-muted-foreground">{t("speech.pcOffline")}</p> : null}
      {(["input","output"] as const).map(kind => {
        const route = speechRoute(speech,kind)
        if (route?.execution !== "gateway" || !session) return null
        const view = currentGatewayAudioView(session,speech,kind)
        return <details key={kind}><summary className="text-xs">{t(kind === "input" ? "speech.input" : "speech.output")}</summary>
          <AudioParameterControls view={view} role={kind === "input" ? "transcription" : "speech"} value={view?.overrides ?? {}} onChange={value => { if (view) saveGatewayAudioPreference(view,value) }} labels={{voice:t("speech.voice"),response_format:t("speech.outputFormat"),stream_format:t("speech.streamFormat"),language:t("speech.language"),speed:t("speech.speed"),instructions:t("speech.instructions"),prompt:t("speech.prompt"),follow:t("speech.audioFollow"),invalid:t("speech.audioInvalid"),unavailable:t("speech.audioUnavailable"),max_input_characters:t("speech.textLimit"),max_file_bytes:t("speech.fileLimit")}} />
        </details>
      })}
      {state.syncedAt ? <p className="text-xs text-muted-foreground">{t("speech.synced", { time: new Date(state.syncedAt).toLocaleString() })}</p> : null}
    </> : <p className="text-xs text-muted-foreground">{t("speech.noConfig")}</p>}
  </section>
}
