// The retired words (U21.2): no glossary alias reaches the screen from
// Studio's components or routes, outside the reasoned allowlist below.
// lib/vocab-scan.ts is the scanner (U21.3 lifts it into
// scripts/vocab-audit.mjs over both renderers).
import { readFileSync, readdirSync, statSync } from "node:fs"
import { join, relative, resolve } from "node:path"
import { describe, expect, it } from "vitest"

import { TERMS } from "./glossary"
import { aliasesOf, scanVocab } from "./vocab-scan"

const src = resolve(process.cwd(), "src")
const ALIASES = aliasesOf(TERMS)

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f)
    if (statSync(p).isDirectory()) return files(p)
    if (!/\.tsx?$/.test(f) || /\.test\.tsx?$/.test(f)) return []
    return [p]
  })
}

/** Where a retired word may stay, and why: file (relative to src/),
 * alias, a substring of the text, the reason. */
const SESSION_TIMELINE =
  "the session page's timeline view (its turns and marks in lanes), not the run's waterfall the alias retires"
const ALLOW: { file: string; alias: string; text: string; why: string }[] = [
  {
    file: "components/studio/session-approvals.tsx",
    alias: "Timeline",
    text: "open the mark on the timeline",
    why: SESSION_TIMELINE,
  },
  {
    file: "components/studio/session-tree.tsx",
    alias: "Timeline",
    text: "open the mark on the timeline",
    why: SESSION_TIMELINE,
  },
]

const allowed = (h: { path: string; alias: string; text: string }) =>
  ALLOW.some(
    (a) => h.path === a.file && h.alias === a.alias && h.text.includes(a.text)
  )

function scanTree() {
  return ["components", "routes"].flatMap((d) =>
    files(join(src, d)).flatMap((p) =>
      scanVocab(relative(src, p), readFileSync(p, "utf8"), ALIASES)
    )
  )
}

describe("retired words (U21.2)", () => {
  it("no alias reaches the screen from components/ or routes/", () => {
    const hits = scanTree().filter((h) => !allowed(h))
    expect(
      hits.map(
        (h) => `${h.path}:${h.line} "${h.alias}" (${h.term}) in "${h.text}"`
      )
    ).toEqual([])
  })

  it("every allowlist entry still matches a hit", () => {
    const hits = scanTree()
    for (const a of ALLOW)
      expect(
        hits.some(
          (h) =>
            h.path === a.file && h.alias === a.alias && h.text.includes(a.text)
        ),
        `${a.file} ${a.alias}: ${a.why}`
      ).toBe(true)
  })

  it("fails on an injected alias", () => {
    const code = [
      'import { thread } from "./threads"',
      'const a = <p title="the thread of this run">3 threads</p>',
      'if (kind === "thread") go()',
      "const b = <Button>Continue</Button>",
      "const c = <p>continue here with a new message</p>",
      'const d = <span className="thread" data-kind="hole">ok</span>',
      "const e = `${n} holes left`",
      'const f = { thread: "/api/threads/x" }',
      "const g = <p>kept by weft/thread, read as thread.PublicID</p>",
      "const h = <p>one thread.</p>",
      "const i = <p>threads:</p>",
      "const j = <p>",
      "  a delegated",
      "  session of the pool",
      "</p>",
    ].join("\n")
    const hits = scanVocab("x.tsx", code, ALIASES)
    expect(hits.map((h) => [h.line, h.alias])).toEqual([
      [2, "thread"],
      [2, "threads"],
      [4, "continue"],
      [7, "holes"],
      [10, "thread"],
      [11, "threads"],
      [13, "delegated session"],
    ])
  })
})
