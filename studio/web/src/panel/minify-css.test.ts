// The panel build's stylesheet squeeze (P8-fix-a; P8-fix-b #10):
// vite.panel.config.ts's minifyCss over PANEL_CSS keeps every rule —
// the same count as the browser's CSS parser reads them, the same
// selectors, the same declarations — so a whitespace rule that ate a
// descendant space or a quoted value would fail here, not in a page.
import { createRequire } from "node:module"

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

// P8-pre-web #1: a real CSS parser over PANEL_CSS, raw and squeezed —
// the one jsdom carries (css-tree), so a rule the page's adopted sheet
// would drop ("Could not parse CSS stylesheet": a selector list that
// swallowed an @keyframes) fails here, not in make devtools-vite-check.
// Every animation name used is a declared @keyframes.
type CssTree = {
  parse: (css: string, opts: Record<string, unknown>) => unknown
  walk: (
    ast: unknown,
    fn: (node: {
      type: string
      name?: string
      prelude?: unknown
      property?: string
      value?: unknown
    }) => void
  ) => void
  generate: (node: unknown) => string
}
const csstree = createRequire(import.meta.url)("css-tree") as CssTree

function cssErrors(css: string): string[] {
  const errs: string[] = []
  csstree.parse(css, {
    context: "stylesheet",
    parseCustomProperty: false,
    onParseError: (e: { message: string; offset: number }) =>
      errs.push(
        `${e.message} @ ${JSON.stringify(css.slice(Math.max(0, e.offset - 60), e.offset + 30))}`
      ),
  })
  return errs
}

function animationNames(css: string): {
  used: Set<string>
  declared: Set<string>
} {
  const used = new Set<string>()
  const declared = new Set<string>()
  const ast = csstree.parse(css, { context: "stylesheet" })
  csstree.walk(ast, (node) => {
    if (node.type === "Atrule" && node.name === "keyframes" && node.prelude)
      declared.add(csstree.generate(node.prelude).trim())
    if (
      node.type === "Declaration" &&
      (node.property === "animation" || node.property === "animation-name")
    ) {
      for (const part of csstree.generate(node.value).split(",")) {
        const name = part
          .trim()
          .split(/\s+/)
          .find(
            (w) =>
              /^[a-z-][\w-]*$/i.test(w) &&
              !/^(none|infinite|linear|ease(-in|-out|-in-out)?|alternate|forwards|backwards|both|normal|reverse|running|paused)$/.test(
                w
              )
          )
        if (name) used.add(name)
      }
    }
  })
  return { used, declared }
}

describe("PANEL_CSS through a real CSS parser", () => {
  for (const [label, css] of [
    ["raw", PANEL_CSS],
    ["minified", minifyCss(PANEL_CSS)],
  ] as const) {
    it(`${label}: parses without an error`, () => {
      expect(cssErrors(css)).toEqual([])
    })
    it(`${label}: every animation name has its @keyframes`, () => {
      const { used, declared } = animationNames(css)
      expect(used.size).toBeGreaterThan(0)
      for (const n of used) expect(declared, n).toContain(n)
    })
  }

  it("the running dot and the pill's pulse both animate", () => {
    expect(animationNames(PANEL_CSS).used).toContain("weft-pulse")
    const ast = csstree.parse(PANEL_CSS, { context: "stylesheet" })
    const animated: string[] = []
    csstree.walk(ast, (node) => {
      if (node.type !== "Rule" || !node.prelude) return
      const block = csstree.generate(node)
      if (/animation:weft-pulse/.test(block))
        animated.push(csstree.generate(node.prelude))
    })
    expect(animated.join(",")).toContain(".weft-dot.weft-run")
    expect(animated.join(",")).toContain(".weft-fab-pulse")
  })

  it("the reviewer's broken rule is caught", () => {
    expect(
      cssErrors(
        ".a{color:red}\n.b, @keyframes p { 50% { opacity: .35; } }\n.c{animation:p 1s}"
      )
    ).not.toEqual([])
  })
})
