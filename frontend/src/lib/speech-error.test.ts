import { describe, expect, it } from "vitest"
import { ApiError } from "./api"
import { t } from "./i18n"
import { speechFailureMessage } from "./speech-error"

describe("speech failure text", () => {
  it("uses the interface language instead of the generic English voice sentence", () => {
    const missing = new ApiError("Voice request could not be completed.", 400, "speech_not_configured")
    const rejected = new ApiError("Voice request could not be completed.", 502, "speech_upstream_error")
    expect(speechFailureMessage(missing, (key) => t("zh", key))).toBe("未配置或暂不可用")
    expect(speechFailureMessage(rejected, (key) => t("zh", key))).toBe("语音服务未能完成这次请求。请检查所选语音模型后重试。")
    expect(speechFailureMessage(new DOMException("Speech cancelled", "AbortError"), (key) => t("en", key))).toBeUndefined()
    expect(speechFailureMessage(new Error("The speech service returned invalid audio."), (key) => t("en", key))).toBe("The speech service returned invalid audio.")
  })
})
