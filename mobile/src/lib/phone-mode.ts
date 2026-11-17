/** Phone preference; an explicit avatar gesture also switches the live selected PC. */
export type PhoneMode = "account" | "local"
const KEY = "zwai.phone.mode"

export function loadPhoneMode(): PhoneMode {
  try { return localStorage.getItem(KEY) === "local" ? "local" : "account" }
  catch { return "account" }
}

export function savePhoneMode(mode: PhoneMode) {
  localStorage.setItem(KEY, mode)
}
