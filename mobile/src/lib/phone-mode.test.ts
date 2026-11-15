import { describe, it, expect, vi } from "vitest"
import { loadPhoneMode, savePhoneMode } from "./phone-mode"

describe("phone product mode", () => {
  it("requires Account on first launch and unknown saved values", () => {
    expect(loadPhoneMode()).toBe("account")
    localStorage.setItem("zwai.phone.mode", "unexpected")
    expect(loadPhoneMode()).toBe("account")
  })
  it("persists explicit Local and Account choices", () => {
    savePhoneMode("local"); expect(loadPhoneMode()).toBe("local")
    savePhoneMode("account"); expect(loadPhoneMode()).toBe("account")
  })
  it("keeps the login gate when storage cannot be read", () => {
    const read = vi.spyOn(localStorage, "getItem").mockImplementation(() => { throw new Error("storage unavailable") })
    try { expect(loadPhoneMode()).toBe("account") } finally { read.mockRestore() }
  })
})
