// Every test starts with the panel's remembered layout forgotten
// (plan D1: localStorage["weft.devtools"] persists per origin, and a
// jsdom file is one origin for all its tests).
import { beforeEach } from "vitest"

// jsdom has no ResizeObserver; every supported browser has one. The
// resizable splits (react-resizable-panels) and cmdk need it to mount:
// a stub that never fires (jsdom lays nothing out). A test that wants
// the no-ResizeObserver fallback stubs it away (vi.stubGlobal).
if (typeof globalThis.ResizeObserver !== "function") {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

beforeEach(() => {
  try {
    localStorage.removeItem("weft.devtools")
  } catch {
    // no storage: nothing remembered
  }
})
