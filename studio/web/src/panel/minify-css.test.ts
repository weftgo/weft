// The panel build's stylesheet squeeze (P8-fix-a; P8-fix-b #10):
// vite.panel.config.ts's minifyCss over PANEL_CSS keeps every rule —
// the same count as the browser's CSS parser reads them, the same
// selectors, the same declarations — so a whitespace rule that ate a
// descendant space or a quoted value would fail here, not in a page.
import { describe, expect, it } from "vitest"

import { minifyCss } from "../../vite.panel.config"
import { PANEL_CSS } from "./styles"

/** How many rules the DOM's parser reads (groups' inner rules too),
 * and each style rule's selector with the separators' spaces dropped.
 * Its declarations are compared by rulesOf: jsdom's parser drops a
 * shorthand it cannot read (a gradient without spaces after commas),
 * which a browser reads. */
function parsed(css: string): string[] {
  const style = document.createElement("style")
  style.textContent = css
  document.head.appendChild(style)
  const out: string[] = []
  const walk = (list: CSSRuleList) => {
    for (const r of Array.from(list)) {
      const inner = (r as Partial<CSSGroupingRule>).cssRules
      if (inner?.length) {
        out.push("@group")
        walk(inner)
      } else
        out.push(
          ((r as Partial<CSSStyleRule>).selectorText ?? r.cssText).replace(
            /\s*([,>])\s*/g,
            "$1"
          )
        )
    }
  }
  walk(style.sheet!.cssRules)
  style.remove()
  return out
}

/** A plain read of the rules: each "prelude {" with the prelude's
 * spaces around , > dropped, each declaration "property:value" with
 * the value's spaces around , dropped, and each "}". */
function rulesOf(css: string): string[] {
  const out: string[] = []
  const norm = (s: string) =>
    s
      .replace(/\s+/g, " ")
      .trim()
      .replace(/\s*([,>])\s*/g, "$1")
  let depth = 0
  let buf = ""
  const decls = () => {
    for (const d of buf.split(";")) {
      const i = d.indexOf(":")
      if (i < 0) continue
      out.push(
        `${"  ".repeat(depth)}${d.slice(0, i).trim()}:${norm(d.slice(i + 1))}`
      )
    }
  }
  for (const ch of css) {
    if (ch === "{") {
      // A media feature's "name: value" is one with or without the
      // space; a selector's space before a colon is a descendant.
      const pre = norm(buf)
      const at = pre.startsWith("@") ? pre.replace(/: /g, ":") : pre
      out.push(`${"  ".repeat(depth)}${at} {`)
      buf = ""
      depth++
    } else if (ch === "}") {
      decls()
      buf = ""
      depth--
      out.push(`${"  ".repeat(depth)}}`)
    } else buf += ch
  }
  return out
}

describe("minifyCss over PANEL_CSS", () => {
  it("keeps every rule, selector and declaration", () => {
    const min = minifyCss(PANEL_CSS)
    const before = parsed(PANEL_CSS)
    expect(before.length).toBeGreaterThan(100)
    expect(parsed(min)).toEqual(before)
    expect(rulesOf(min)).toEqual(rulesOf(PANEL_CSS))
    // It did squeeze.
    expect(min.length).toBeLessThan(PANEL_CSS.length * 0.9)
  })

  it("keeps a selector's descendant space before a colon and a quoted value", () => {
    expect(minifyCss(".a :hover { color: red ; }")).toBe(".a :hover{color:red}")
    expect(minifyCss('.a::before { content: "\\25B8 "; }')).toBe(
      '.a::before{content:"\\25B8 "}'
    )
  })
})
