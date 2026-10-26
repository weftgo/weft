// The glossary's shape (U21.1): every term a tooltip of at most 90
// characters that starts with its word, a 2–4 line glossary entry, a
// docs anchor that exists, and the status words' tooltips lib/status.ts's.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import { CORE_TERMS, GROUPS, TERMS } from "./glossary"
import { STATUS } from "./status"

/** GitHub's heading anchor: lower case, punctuation dropped, spaces to -. */
const slug = (h: string) =>
  h
    .toLowerCase()
    .replace(/[^\w\- ]/g, "")
    .replace(/ /g, "-")

const repo = resolve(process.cwd(), "../..")

describe("the glossary's TERMS (U21.1)", () => {
  const entries = Object.entries(TERMS)

  it("holds the audit's §2.1 concepts", () => {
    expect(Object.keys(TERMS)).toEqual(
      expect.arrayContaining([
        "run",
        "step",
        "attempt",
        "turn",
        "session",
        "publicId",
        "scope",
        "flow",
        "subagent",
        "childRun",
        "own",
        "delegated",
        "playback",
        "replay",
        "experiment",
        "fork",
        "scripted",
        "live",
        "running",
        "succeeded",
        "failed",
        "interrupted",
        "parked",
        "approve",
        "gap",
        "derived",
        "compacted",
        "runtime",
        "appOwned",
        "waterfall",
        "story",
        "request",
        "raw",
        "event",
        "manifest",
        "trace",
        "span",
      ])
    )
    for (const k of CORE_TERMS) expect(TERMS[k], k).toBeDefined()
  })

  for (const [k, t] of entries)
    it(`${k}: short, long, docs and group`, () => {
      expect(t.short.length, t.short).toBeLessThanOrEqual(90)
      expect(t.short.startsWith(`${t.term} `), t.short).toBe(true)
      expect(t.short).toContain(" — ")
      const lines = t.long.split("\n")
      expect(lines.length).toBeGreaterThanOrEqual(2)
      expect(lines.length).toBeLessThanOrEqual(4)
      for (const l of lines) expect(l.trim()).not.toBe("")
      expect(GROUPS[t.group]).toBeDefined()
      // The anchor is a heading of the file it names.
      const [file, anchor] = t.docs.split("#")
      const md = readFileSync(resolve(repo, file), "utf8")
      const anchors = Array.from(md.matchAll(/^#+ (.+)$/gm), (m) => slug(m[1]))
      expect(anchors, t.docs).toContain(anchor)
    })

  it("a retired alias is no term's word", () => {
    const words = new Set(entries.map(([, t]) => t.term))
    for (const [k, t] of entries)
      for (const a of t.aliases) expect(words.has(a), `${k}: ${a}`).toBe(false)
  })

  it("the audit's retired words are aliases; the parked call's own line is not", () => {
    expect(TERMS.approve.aliases).toEqual(["continue", "skip"])
    expect(TERMS.experiment.aliases).toEqual([
      "✎ Experiment",
      "Run experiment ▶",
    ])
    expect(TERMS.fork.aliases.length).toBeGreaterThan(0)
    // "awaiting a decision" stays the parked call's line (lib/status.ts).
    expect(TERMS.parked.aliases).toEqual(["pending approval"])
  })

  it("the status words' tooltips are lib/status.ts's hints", () => {
    for (const k of [
      "running",
      "succeeded",
      "failed",
      "interrupted",
      "parked",
    ] as const)
      expect(TERMS[k].short).toBe(`${STATUS[k].word} — ${STATUS[k].hint}`)
    // The audit's §2.1 tooltips, word for word.
    expect(TERMS.running.short).toBe("running — the run is reporting now")
    expect(TERMS.parked.short).toBe(
      "parked — waiting for a decision on a tool call (approve / deny)"
    )
  })
})
