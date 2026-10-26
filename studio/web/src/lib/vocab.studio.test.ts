// The retired words (U21.2, U21.3): no glossary alias reaches the
// screen from either renderer — Studio's components/ and routes/, the
// shared lib/, the panel's panel/ — outside the reasoned allowlist
// (scripts/vocab-allow.json). scripts/vocab-audit.ts is the audit `make
// studio-check` runs; lib/vocab-scan.ts is its scanner, unit-tested
// below.
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import {
  AUDIT_DIRS,
  audit,
  loadAllowlist,
  report,
  sources,
} from "../../scripts/vocab-audit"
import { TERMS } from "./glossary"
import { aliasesOf, scanVocab } from "./vocab-scan"

const src = resolve(process.cwd(), "src")
const ALIASES = aliasesOf(TERMS)

describe("retired words (U21.2, U21.3)", () => {
  it("the audit passes on the tree: no alias reaches the screen, no stale allowlist entry", () => {
    const files = sources(src)
    for (const d of AUDIT_DIRS)
      expect(
        files.some((f) => f.path === d || f.path.startsWith(`${d}/`)),
        d
      ).toBe(true)
    expect(report(audit(files, loadAllowlist()))).toEqual([])
  })

  it("the audit fails on an alias injected into each tree it reads", () => {
    const files = sources(src)
    const list = loadAllowlist()
    const inject: Record<
      (typeof AUDIT_DIRS)[number],
      { path: string; text: string }
    > = {
      components: {
        path: "components/studio/x.tsx",
        text: "export const X = () => <p>3 threads</p>",
      },
      routes: {
        path: "routes/x.tsx",
        text: 'export const X = () => <button title="the trace view">go</button>',
      },
      lib: {
        path: "lib/x.ts",
        text: "export const X = (n: number) => `${n} delegated session`",
      },
      panel: {
        path: "panel/x.ts",
        text: 'el("button", "weft-btn", "✎ Experiment")',
      },
      hooks: {
        path: "hooks/x.ts",
        text: 'toast.error("the thread could not be read")',
      },
      "router.tsx": {
        path: "router.tsx",
        text: "export const X = () => <p>no such thread</p>",
      },
    }
    for (const d of AUDIT_DIRS) {
      const r = audit([...files, inject[d]], list)
      expect(
        r.hits.map((h) => h.path),
        d
      ).toEqual([inject[d].path])
      expect(r.stale, d).toEqual([])
    }
    // Seven audits of the whole tree: seconds alone, past vitest's 5 s
    // default under the full suite's parallel load.
  }, 20_000)

  it("an allowlist entry no hit matches fails the audit", () => {
    const list = loadAllowlist()
    const stale = {
      file: "panel/element.ts",
      alias: "thread",
      text: "thread: ephemeral",
      why: "gone",
    }
    const r = audit(sources(src), { ...list, allow: [...list.allow, stale] })
    expect(r.stale).toEqual([stale])
    expect(report(r)).toHaveLength(1)
  })

  it("an allowlist entry matches its literal exactly, never a sentence that contains it (U21.3-fix)", () => {
    const list = loadAllowlist()
    const inject = [
      {
        path: "lib/timeline.ts",
        text: 'export const X = "open the thread in a new tab"',
      },
      {
        path: "lib/timeline.ts",
        text: 'export const Y = "the timeline of this run"',
      },
    ]
    const r = audit([...sources(src), ...inject], list)
    expect(r.hits.map((h) => [h.alias, h.text])).toEqual([
      ["thread", "open the thread in a new tab"],
      ["Timeline", "the timeline of this run"],
    ])
  })

  it("a view / verb value is hidden only when it is an id: words under the key are screen text (U21.3-fix)", () => {
    const code = [
      'export const A = { view: "timeline", verb: "continue" }',
      'export const B = { view: "the thread compacted this run" }',
      'export const C = { verb: "skip this thread" }',
    ].join("\n")
    expect(
      scanVocab("v.ts", code, ALIASES).map((h) => [h.line, h.alias])
    ).toEqual([
      [2, "thread"],
      [3, "thread"],
    ])
  })

  it("a glossary entry's aliases list is not screen text", () => {
    const code = [
      'export const T = { term: "x", aliases: ["thread", "holes"] }',
      'export const U = { term: "x", short: "one thread" }',
    ].join("\n")
    expect(
      scanVocab("g.ts", code, ALIASES).map((h) => [h.line, h.alias])
    ).toEqual([[2, "thread"]])
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
