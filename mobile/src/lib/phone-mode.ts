/** Phone-only product preference; it never changes the connected PC's mode. */
export type PhoneMode = "account" | "local"
const KEY = "zwai.phone.mode"

export function loadPhoneMode(): PhoneMode {
  try { return localStorage.getItem(KEY) === "local" ? "local" : "account" }
  catch { return "account" }
}

export function savePhoneMode(mode: PhoneMode) {
  localStorage.setItem(KEY, mode)
}
