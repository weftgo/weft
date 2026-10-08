// Every test starts with the panel's remembered layout forgotten
// (plan D1: localStorage["weft.devtools"] persists per origin, and a
// jsdom file is one origin for all its tests).
import { beforeEach } from "vitest"

beforeEach(() => {
  try {
    localStorage.removeItem("weft.devtools")
  } catch {
    // no storage: nothing remembered
  }
})
