// The status roles (U1, UX-F-07): each role names one Studio variable
// and one panel token, and they are the same colour in each theme — a
// status reads alike on both surfaces. panel/theme.test.ts holds the
// shared tokens to styles.css; this holds the roles to both.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import { INK, PALETTE, ROLE, STUDIO_NAMES, TONE, TONE_STUDIO } from "./palette"
import type { Role } from "./palette"
import { STATUS } from "./status"
import { ROLE_CLASS, studioVar } from "./status-class"

function studioTokens(selector: string): Record<string, string> {
  const css = readFileSync(resolve(process.cwd(), "src/styles.css"), "utf8")
  const at = css.search(new RegExp(`^${selector.replace(".", "\\.")} \\{`, "m"))
  expect(at, selector).toBeGreaterThanOrEqual(0)
  const body = css.slice(at, css.indexOf("}", at))
  return Object.fromEntries(
    Array.from(body.matchAll(/--([a-z0-9-]+):\s*([^;]+);/g), (m) => [
      m[1],
      m[2].trim(),
    ])
  )
}

describe("status roles (U1)", () => {
  for (const [theme, sel] of [
    ["light", ":root"],
    ["dark", ".dark"],
  ] as const)
    it(`${theme}: each role is the same hex on both surfaces`, () => {
      const studio = studioTokens(sel)
      // A role's word ink (U1-fix) is held like its mark.
      for (const [role, t] of [
        ...Object.entries(ROLE),
        ...Object.entries(ROLE).flatMap(([r, m]) =>
          INK[m] ? [[`${r} word`, INK[m]] as const] : []
        ),
      ]) {
        // The same variable in both themes: Studio's class is one.
        const v = STUDIO_NAMES[theme][t]
        if (!role.endsWith(" word"))
          expect(v, role).toBe(studioVar(role as Role))
        expect(studio[v], `${role}: --${v}`).toMatch(/^#[0-9a-f]{6}$/)
        expect(PALETTE[theme][t], `${role}: --weft-${t} vs --${v}`).toBe(
          studio[v]
        )
      }
    })

  it("every status has a role, every role's variable a Studio class", () => {
    for (const w of Object.values(STATUS)) expect(ROLE[w.role]).toBeDefined()
    for (const role of Object.keys(ROLE) as Role[]) {
      const studio = studioVar(role)
      expect(ROLE_CLASS[studio], studio).toEqual({
        dot: `bg-${studio}`,
        text: `text-${STUDIO_NAMES.light[INK[ROLE[role]] ?? ROLE[role]]}`,
      })
    }
    // The live dot is the succeeded colour (UX-F-07).
    expect(ROLE.live).toEqual(ROLE["succeeded"])
  })
})

describe("run tree tones (U8.3)", () => {
  // The panel's Timeline draws a row's tone in TONE's token; Studio's
  // Waterfall in TONE_STUDIO's variable (its toneText class): one hex.
  const src = readFileSync(
    resolve(process.cwd(), "src/components/studio/waterfall.tsx"),
    "utf8"
  )
  it("Studio's Waterfall labels each tone in TONE_STUDIO's variable", () => {
    for (const [tone, v] of Object.entries(TONE_STUDIO))
      expect(src).toContain(`  ${tone}: "text-${v}",`)
  })
  for (const [theme, sel] of [
    ["light", ":root"],
    ["dark", ".dark"],
  ] as const)
    it(`${theme}: each tone is the same hex on both surfaces`, () => {
      const studio = studioTokens(sel)
      for (const [tone, t] of Object.entries(TONE)) {
        const v = TONE_STUDIO[tone as keyof typeof TONE]
        expect(studio[v], `${tone}: --${v}`).toMatch(/^#[0-9a-f]{6}$/)
        expect(PALETTE[theme][t], `${tone}: --weft-${t} vs --${v}`).toBe(
          studio[v]
        )
      }
    })
})
