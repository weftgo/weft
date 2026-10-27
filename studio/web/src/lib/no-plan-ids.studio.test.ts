// No internal planning reference reaches the screen (P020.1): a design
// doc's name (WEFT-PROMPT.md), a plan item's id (U21, I4, P3, UX-F-28)
// lives in comments, never in a string or JSX text either renderer
// draws. The ADRs are published (docs/adr), so "ADR 0019" may stay.
import { resolve } from "node:path"
import ts from "typescript"
import { describe, expect, it } from "vitest"

import { sources } from "../../scripts/vocab-audit"

const LEAK =
  /WEFT-[A-Z-]+\.md|\b(?:[UIPOBJG]\d{1,2}(?:\.\d+)?(?:-fix\d*)?|UX-[A-Z0-9]+-\d+)\b/

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
