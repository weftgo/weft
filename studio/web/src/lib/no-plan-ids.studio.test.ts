// No internal planning reference reaches the screen (P020.1): a design
// doc's name (WEFT-PROMPT.md), a plan item's id (U21, I4, P3, UX-F-28)
// lives in comments, never in a string or JSX text either renderer
// draws. The ADRs are published (docs/adr), so "ADR 0019" may stay.
import { resolve } from "node:path"
import ts from "typescript"
import { describe, expect, it } from "vitest"

import { sources } from "../../scripts/vocab-audit"

/** A leak: a design doc's name; a UX finding (UX-F-28); a U item (U21,
 * U21.2, U21-fix); another track's item only in its plan shapes — a
 * sub-item, a fix or a pre-release round (I4-fix, P10b-pre-web-fix2,
 * O8.1) — so a percentile (P95, p99) or a key (G1, I2) is no leak. */
const LEAK =
  /WEFT-[A-Z-]+\.md|\bUX-[A-Z0-9]+-\d+\b|\bU\d{1,2}(?:\.\d+)?(?:-fix\d*)?\b|\b[IPOBJG]\d{1,2}[a-z]?(?:\.\d+|-fix\d*|-pre-[\w-]+)\b/

/** strings is every string literal, template part and JSX text of a
 * source, with its line. */
function strings(path: string, text: string): [number, string][] {
  const sf = ts.createSourceFile(
    path,
    text,
    ts.ScriptTarget.Latest,
    true,
    path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS
  )
  const out: [number, string][] = []
  const visit = (n: ts.Node) => {
    if (
      ts.isStringLiteral(n) ||
      ts.isNoSubstitutionTemplateLiteral(n) ||
      ts.isTemplateHead(n) ||
      ts.isTemplateMiddle(n) ||
      ts.isTemplateTail(n) ||
      ts.isJsxText(n)
    )
      out.push([
        sf.getLineAndCharacterOfPosition(n.getStart()).line + 1,
        n.text,
      ])
    ts.forEachChild(n, visit)
  }
  visit(sf)
  return out
}

describe("no plan ids on screen (P020.1)", () => {
  it("no string in components, routes, hooks, lib or the panel names a design doc or a plan item", () => {
    const hits = sources(resolve(process.cwd(), "src")).flatMap((f) =>
      strings(f.path, f.text)
        .filter(([, s]) => LEAK.test(s))
        .map(([l, s]) => `src/${f.path}:${l}: ${s.trim().slice(0, 120)}`)
    )
    expect(hits).toEqual([])
  })

  it("a percentile or a key is no leak", () => {
    for (const t of [
      "p95 latency",
      "P95",
      "duration P99 1.2 s",
      "G1",
      "I2",
      "press G1 then I2",
    ])
      expect(LEAK.test(t), t).toBe(false)
    for (const t of [
      "see U21",
      "U21.2",
      "UX-F-28",
      "I4-fix",
      "P10b-pre-web-fix2",
      "WEFT-PROMPT.md",
    ])
      expect(LEAK.test(t), t).toBe(true)
  })

  it("the scanner catches a leak", () => {
    expect(
      strings("x.ts", 'const a = "the durable path is WEFT-PROMPT.md P3"').some(
        ([, s]) => LEAK.test(s)
      )
    ).toBe(true)
    expect(
      strings("x.tsx", "const a = <p>see U21.2 for more</p>").some(([, s]) =>
        LEAK.test(s)
      )
    ).toBe(true)
    expect(
      strings("x.ts", "// U21.2 in a comment\nconst a = 'ADR 0019'").some(
        ([, s]) => LEAK.test(s)
      )
    ).toBe(false)
  })
})
