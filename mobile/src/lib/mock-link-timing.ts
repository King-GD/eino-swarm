export const MOCK_FLAG = "mock"
const DEFAULT_TICK_MS = 420

export function wantsMock(search = location.search): boolean {
  return new URLSearchParams(search).get(MOCK_FLAG) === "1"
}
export function mockTickMs(search = location.search): number {
  const raw = new URLSearchParams(search).get("tick"), n = raw ? Number(raw) : NaN
  return Number.isFinite(n) && n >= 0 ? n : DEFAULT_TICK_MS
}
export function mockPauseOpenMs(search = location.search): number {
  return new URLSearchParams(search).get("pause") === "open" ? 1200 : 0
}
export function mockPauseClientsMs(search = location.search): number {
  return new URLSearchParams(search).get("pause") === "clients" ? 1200 : 0
}
