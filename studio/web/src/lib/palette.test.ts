// The status roles (U1, UX-F-07): each role names one Studio variable
// and one panel token, and they are the same colour in each theme — a
// status reads alike on both surfaces. panel/theme.test.ts holds the
// shared tokens to styles.css; this holds the roles to both.
import { readdirSync, readFileSync, statSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import { INK, PALETTE, ROLE, STUDIO_NAMES, TONE, TONE_STUDIO } from "./palette"
import type { Role } from "./palette"
import { STATUS } from "./status"
import { ROLE_CLASS, ROLE_SVG, studioVar } from "./status-class"

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
    // The SVG paints (the call graph's nodes): the same variables.
    for (const [v, c] of Object.entries(ROLE_CLASS))
      expect(ROLE_SVG[v], v).toEqual({
        fill: c.text.replace(/^text-/, "fill-"),
        stroke: c.dot.replace(/^bg-/, "stroke-"),
      })
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

describe("diff roles (UX-F-20)", () => {
  // One pair on both surfaces: added green, removed red, a changed
  // cell mustard — the panel's rules in DIFF's token, Studio's
  // DiffBlock and step diff in DIFF_STUDIO's variable, one hex.
  it("the panel's diff rules draw DIFF's tokens", async () => {
    const { DIFF } = await import("./palette")
    const { PANEL_CSS } = await import("../panel/styles")
    expect(PANEL_CSS).toContain(
      `.weft-diff-add, .weft-diff-b { color: var(--weft-${DIFF.add}); }`
    )
    expect(PANEL_CSS).toContain(
      `.weft-diff-del { color: var(--weft-${DIFF.del}); text-decoration: line-through; }`
    )
    expect(PANEL_CSS).toContain(
      `.weft-diff-a { color: var(--weft-${DIFF.del}); }`
    )
    expect(PANEL_CSS).toContain(
      `td.weft-diff-changed { color: var(--weft-${DIFF.changed}); }`
    )
  })
  it("Studio's DiffBlock and step diff draw DIFF_STUDIO's variables", async () => {
    const { DIFF_STUDIO } = await import("./palette")
    const req = readFileSync(
      resolve(process.cwd(), "src/components/studio/step-request.tsx"),
      "utf8"
    )
    expect(req).toContain(`text-${DIFF_STUDIO.add}`)
    expect(req).toContain(`text-${DIFF_STUDIO.del} line-through`)
    const sd = readFileSync(
      resolve(process.cwd(), "src/components/studio/step-diff.tsx"),
      "utf8"
    )
    expect(sd).toContain(`changed: "bg-${DIFF_STUDIO.changed}/10`)
  })
  for (const [theme, sel] of [
    ["light", ":root"],
    ["dark", ".dark"],
  ] as const)
    it(`${theme}: each diff role is the same hex on both surfaces`, async () => {
      const { DIFF, DIFF_STUDIO } = await import("./palette")
      const studio = studioTokens(sel)
      for (const [k, t] of Object.entries(DIFF)) {
        const v = DIFF_STUDIO[k as keyof typeof DIFF]
        expect(studio[v], `${k}: --${v}`).toMatch(/^#[0-9a-f]{6}$/)
        expect(PALETTE[theme][t], `${k}: --weft-${t} vs --${v}`).toBe(studio[v])
      }
    })
})

// WCAG 2.x contrast (no library), as panel/theme.test.ts measures it.
function contrast(a: string, b: string): number {
  const lum = (hex: string) => {
    const n = parseInt(hex.slice(1), 16)
    const [r, g, bl] = [n >> 16, (n >> 8) & 255, n & 255].map((v) => {
      const c = v / 255
      return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
    })
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl
  }
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p)
  return (x + 0.05) / (y + 0.05)
}
/** fg drawn at opacity a over bg: what a bg-x/40 tint is. */
function over(fg: string, bg: string, a: number): string {
  const ch = (h: string) =>
    [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16))
  const [f, b] = [ch(fg), ch(bg)]
  return (
    "#" +
    f
      .map((v, i) =>
        Math.round(v * a + b[i] * (1 - a))
          .toString(16)
          .padStart(2, "0")
      )
      .join("")
  )
}

describe("Studio's text tokens (U34-fix3): --faint is text", () => {
  // --faint is the .eyebrow utility's colour and 300-odd text-faint
  // labels (the run header, step labels, the request's field names, the
  // sidebar's version lines): text, so 4.5:1 on every surface it sits on
  // — the page, a card, the quiet panels and sidebar, and the output
  // card / subagent block's bg-secondary/40 tint. (A hovered row's wash,
  // sidebar-accent, carries foreground: light's is the line colour.)
  for (const [theme, sel] of [
    ["light", ":root"],
    ["dark", ".dark"],
  ] as const)
    it(`${theme}: faint and muted-foreground clear 4.5:1 on every surface`, () => {
      const s = studioTokens(sel)
      const surfaces: Record<string, string> = {
        background: s.background,
        card: s.card,
        secondary: s.secondary,
        sidebar: s.sidebar,
        "secondary/40 on card": over(s.secondary, s.card, 0.4),
        "secondary/40 on background": over(s.secondary, s.background, 0.4),
      }
      for (const t of ["faint", "muted-foreground"])
        for (const [name, bg] of Object.entries(surfaces))
          expect(
            contrast(s[t], bg),
            `${theme} --${t} on ${name}`
          ).toBeGreaterThanOrEqual(4.5)
    })
})

describe("Studio's tinted surfaces and code window (U34-fix4)", () => {
  const src = (p: string) => readFileSync(resolve(process.cwd(), p), "utf8")
  for (const [theme, sel] of [
    ["light", ":root"],
    ["dark", ".dark"],
  ] as const) {
    it(`${theme}: faint and muted-foreground clear 4.5:1 on the status washes`, () => {
      const s = studioTokens(sel)
      // The failure callout and flow strip (bg-status-bad/5) and the
      // thread's quiet blocks (bg-thread/5) hold faint labels.
      for (const t of ["faint", "muted-foreground"])
        for (const base of ["background", "card"])
          for (const tint of ["status-bad", "thread"])
            expect(
              contrast(s[t], over(s[tint], s[base], 0.05)),
              `${theme} --${t} on ${tint}/5 over ${base}`
            ).toBeGreaterThanOrEqual(4.5)
    })

    it(`${theme}: a tinted row's ink clears 4.5:1 on its status tint`, () => {
      const s = studioTokens(sel)
      // [ink, tint, alpha], each as its source draws it.
      const pairs: [string, string, number][] = [
        // DiffBlock's rows (step-request.tsx): /8 — status-ok reads
        // 4.47:1 on its /10 in light.
        ["status-ok", "status-ok", 0.08],
        ["status-bad", "status-bad", 0.08],
        // A changed step-diff cell (step-diff.tsx).
        ["foreground", "ev-error", 0.1],
        // The destructive badge and button: /10 in light, /20 in dark.
        ["destructive", "destructive", theme === "light" ? 0.1 : 0.2],
      ]
      for (const [ink, tint, a] of pairs)
        for (const base of ["background", "card"])
          expect(
            contrast(s[ink], over(s[tint], s[base], a)),
            `${theme} --${ink} on ${tint}/${Math.round(a * 100)} over ${base}`
          ).toBeGreaterThanOrEqual(4.5)
      const req = src("src/components/studio/step-request.tsx")
      expect(req).toContain('"bg-status-ok/8 text-status-ok"')
      expect(req).toContain('"bg-status-bad/8 text-status-bad line-through"')
    })

    it(`${theme}: on the sidebar's hover wash (sidebar-accent) text is the item's ink`, () => {
      const s = studioTokens(sel)
      expect(
        contrast(s["sidebar-accent-foreground"], s["sidebar-accent"]),
        `${theme} sidebar-accent-foreground on sidebar-accent`
      ).toBeGreaterThanOrEqual(4.5)
      // faint does not clear the wash in light (4.18:1): the locked
      // item's "needs a token" note takes the item's ink under it.
      if (contrast(s.faint, s["sidebar-accent"]) < 4.5)
        expect(src("src/components/studio/app-shell.tsx")).toContain(
          "text-faint group-hover/menu-button:text-sidebar-accent-foreground group-active/menu-button:text-sidebar-accent-foreground"
        )
    })

    it(`${theme}: every code-* token and the literal clear 4.5:1 on --code-bg`, () => {
      const s = studioTokens(sel)
      const code = Object.keys(s).filter(
        (k) => k.startsWith("code-") && k !== "code-bg"
      )
      expect(code).toEqual(
        expect.arrayContaining([
          "code-ink",
          "code-mut",
          "code-str",
          "code-num",
          "code-lit",
        ])
      )
      for (const k of code)
        expect(
          contrast(s[k], s["code-bg"]),
          `${theme} --${k} on --code-bg`
        ).toBeGreaterThanOrEqual(4.5)
      // The folded window's "show all" foot: code-mut on the bar.
      expect(
        contrast(s["code-mut"], s["win-bar"]),
        `${theme} --code-mut on --win-bar`
      ).toBeGreaterThanOrEqual(4.5)
    })

    it(`${theme}: faint is a step under muted-foreground, faint-deco decoration`, () => {
      const s = studioTokens(sel)
      // Dark: a visible step (≥ 1.2:1 between the two). Light: 4.5:1 on
      // the quiet panels leaves faint 4.74 against muted-foreground's
      // 4.96 there — a shade lighter, never darker, no more is possible
      // with muted-foreground the panel's dim.
      const step = contrast(s.faint, s["muted-foreground"])
      expect(step).toBeGreaterThan(1)
      if (theme === "dark") expect(step).toBeGreaterThanOrEqual(1.2)
      expect(contrast(s.faint, s.background)).toBeLessThan(
        contrast(s["muted-foreground"], s.background)
      )
      expect(s["faint-deco"]).toMatch(/^#[0-9a-f]{6}$/)
    })
  }

  it("the code surfaces draw --code-lit and --code-num, never an --ev- token", () => {
    const tree = src("src/components/studio/json-tree.tsx")
    const win = src("src/components/studio/codewin.tsx")
    expect(tree).toContain('<span className="text-code-lit">')
    expect(tree).toContain('<span className="text-code-num">')
    expect(tree).not.toMatch(/text-ev-/)
    expect(win).toContain('literal: "text-code-lit",')
    expect(win).not.toContain("text-ev-error")
  })

  it("no text is drawn in faint-deco; the delta's marks are", () => {
    const walk = (d: string): string[] =>
      readdirSync(d).flatMap((f) => {
        const p = resolve(d, f)
        return statSync(p).isDirectory() ? walk(p) : [p]
      })
    for (const f of walk(resolve(process.cwd(), "src")).filter(
      (p) => /\.tsx?$/.test(p) && !/\.test\.tsx?$/.test(p)
    ))
      expect(readFileSync(f, "utf8"), f).not.toMatch(
        /(text|fill|stroke)-faint-deco/
      )
    const sum = src("src/lib/summarize.ts")
    expect(sum).toContain('delta: "bg-faint-deco",')
    expect(sum).toContain('delta: "border-faint-deco/40",')
    expect(sum).toContain('delta: "text-faint",')
  })
})
