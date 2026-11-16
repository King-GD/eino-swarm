import { t } from "@/lib/i18n"
import { usePhoneSpeech } from "@/lib/phone-speech"
import { speechRoute } from "../../../frontend/src/lib/speech-types"

/** Read-only: the PC remains the sole editor of model and service settings. */
export function SpeechStatus() {
  const state = usePhoneSpeech()
  const speech = state.snapshot
  const inputRoute = speech && speechRoute(speech, "input")
  const outputRoute = speech && speechRoute(speech, "output")
  const routeLabel = (route: typeof inputRoute) => state.standalone ? t("speech.phone") : route ? t(route.execution === "gateway" ? "speech.gateway" : "speech.local") : undefined
  return <section aria-label={t("speech.title")} className="flex flex-col gap-2 rounded-xl border border-border p-3" data-testid="speech-status">
    <h2 className="text-sm font-semibold">{t("speech.title")}{state.host ? ` · ${state.host}` : ""}</h2>
    <p className="text-xs text-muted-foreground">{t(state.standalone ? "speech.phoneSettingsHint" : "speech.settingsHint")}</p>
    {speech ? <>
      <p className="text-xs">{t("speech.input")}: {t(state.inputReady ? "speech.ready" : speech.input.enabled && speech.input.ready ? "speech.unavailable" : "speech.off")}{speech.input.label ? ` · ${speech.input.label}` : ""}{routeLabel(inputRoute) ? ` · ${routeLabel(inputRoute)}` : ""}</p>
      <p className="text-xs">{t("speech.output")}: {t(state.outputReady ? "speech.ready" : speech.output.enabled && speech.output.ready ? "speech.unavailable" : "speech.off")}{speech.output.label ? ` · ${speech.output.label}` : ""}{routeLabel(outputRoute) ? ` · ${routeLabel(outputRoute)}` : ""}</p>
      {!state.connected ? <p role="status" className="text-xs text-muted-foreground">{t(inputRoute?.execution === "gateway" || outputRoute?.execution === "gateway" ? "speech.offlineCache" : "speech.pcOffline")}</p> : null}
      {state.syncedAt ? <p className="text-xs text-muted-foreground">{t("speech.synced", { time: new Date(state.syncedAt).toLocaleString() })}</p> : null}
    </> : <p className="text-xs text-muted-foreground">{t("speech.noConfig")}</p>}
  </section>
}
