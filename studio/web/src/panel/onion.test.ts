// U23.2's Done line in the panel's Story: the onion's effects as chips
// on the tool row, in lib/onion.ts's words — the words the run page
// draws (routes/run-onion.test.tsx) — over the seeded shapes recorded
// through the real pipeline (studio/cards_test.go): mw.allow's denied
// call reads "denied by policy" beside its "✋ allow" chip,
// mw.repairjson's call "✎ repaired", rw.mcp-mesh's calls their server.
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import { deniedWord, effectChips } from "../lib/onion"
import { panelRoutes } from "../test/cards-fixture"
import type { WeftDevtools } from "./element"
import { $, fakeStudio, META, mount, settle, setup, teardown } from "./testkit"

beforeEach(setup)
afterEach(teardown)

const CAPS = {
  ...META,
  capabilities: [...META.capabilities, "requests", "steps"],
}

async function story(name: string): Promise<WeftDevtools> {
  fakeStudio(panelRoutes(name), CAPS)
  const el = await mount()
  await settle()
  return el
}
const call = (el: WeftDevtools, id: string) =>
  $(el, `.weft-call[data-key="${id}"]`) as HTMLElement | null
const badges = (c: HTMLElement | null) =>
  Array.from(c?.querySelectorAll(".weft-badge") ?? [], (b) => b.textContent)

describe("the panel's effect chips (U23.2)", () => {
  it("mw.allow: the denied call reads 'denied by policy' beside its ✋ allow chip; the permitted sibling none", async () => {
    const el = await story("mw.allow")
    const denied = badges(call(el, "a1"))
    expect(denied).toContain(deniedWord("allow"))
    expect(denied).toContain("denied by policy")
    expect(denied).toContain(effectChips({ deniedBy: "allow" })[0])
    expect(denied).toContain("✋ allow")
    expect(badges(call(el, "a2")).some((b) => b.startsWith("✋"))).toBe(false)
  })

  it("mw.repairjson: the repaired call carries ✎ repaired", async () => {
    const el = await story("mw.repairjson")
    expect(badges(call(el, "f1"))).toContain("✎ repaired")
  })

  it("rw.mcp-mesh: each imported call names its server; the local call none", async () => {
    const el = await story("rw.mcp-mesh")
    for (const id of ["m2", "m3", "m4"])
      expect(badges(call(el, id)), id).toContain("mcp:billing")
    expect(badges(call(el, "m1"))).not.toContain("mcp:billing")
  })
})
