// The vocabulary audit (U21.3; UX-D-22, UX-F-13, UX-F-15): no retired
// word — a glossary alias, lib/glossary.ts TERMS[*].aliases — reaches the
// screen from either renderer. It runs lib/vocab-scan.ts's scanner over
// the four trees whose strings a user reads (Studio's components/ and
// routes/, the shared lib/, the panel's panel/) and fails on any hit the
// one reasoned allowlist (scripts/vocab-allow.json) does not name, and
// on an allowlist entry that no longer matches a hit (a stale reason
// hides the next real one). `make studio-check` runs it:
//
//   bun run scripts/vocab-audit.ts
//
// src/lib/vocab.studio.test.ts runs audit() on the tree as it is and
// with an alias injected into each of the four.

import { readFileSync, readdirSync, statSync } from "node:fs"
import { basename, dirname, join, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"

import { TERMS } from "../src/lib/glossary"
import { aliasesOf, scanVocab } from "../src/lib/vocab-scan"
import type { VocabHit } from "../src/lib/vocab-scan"

/** The trees (and files) the audit reads, relative to src/: every
 * source whose strings reach a screen — Studio's components, routes,
 * hooks (their toasts) and router (its error and not-found pages), the
 * shared lib/ and the panel. */
export const AUDIT_DIRS = [
  "components",
  "routes",
  "hooks",
  "router.tsx",
  "lib",
  "panel",
] as const

/** One file the audit reads: its path relative to src/ and its text. */
export interface Source {
  path: string
  text: string
}

/** The allowlist: files that are not screen text at all, and hits that
 * may stay, each with its reason. */
export interface Allowlist {
  skip: { file: string; why: string }[]
  allow: { file: string; alias: string; text: string; why: string }[]
}

const ALIASES = aliasesOf(TERMS)
const here = dirname(fileURLToPath(import.meta.url))

/** sources reads every renderer file of src/'s AUDIT_DIRS: .ts and
 * .tsx, tests and the test kit left out (they say the retired words to
 * prove they are gone). */
export function sources(src: string): Source[] {
  const walk = (p: string): string[] => {
    if (statSync(p).isDirectory())
      return readdirSync(p).flatMap((f) => walk(join(p, f)))
    const f = basename(p)
    if (!/\.tsx?$/.test(f) || /\.test\.tsx?$/.test(f) || f === "testkit.ts")
      return []
    return [p]
  }
  return AUDIT_DIRS.flatMap((d) =>
    walk(join(src, d)).map((p) => ({
      path: relative(src, p),
      text: readFileSync(p, "utf8"),
    }))
  )
}

/** The audit's verdict: the hits no entry allows, and the entries no
 * hit matches. Clean when both are empty. */
export interface AuditResult {
  hits: VocabHit[]
  stale: Allowlist["allow"]
}

const matches = (
  a: Allowlist["allow"][number],
  h: { path: string; alias: string; text: string }
) => h.path === a.file && h.alias === a.alias && h.text === a.text

/** audit scans the files against the allowlist. */
export function audit(files: readonly Source[], list: Allowlist): AuditResult {
  const skip = new Set(list.skip.map((s) => s.file))
  const all = files
    .filter((f) => !skip.has(f.path))
    .flatMap((f) => scanVocab(f.path, f.text, ALIASES))
  return {
    hits: all.filter((h) => !list.allow.some((a) => matches(a, h))),
    stale: list.allow.filter((a) => !all.some((h) => matches(a, h))),
  }
}

/** loadAllowlist reads scripts/vocab-allow.json. */
export function loadAllowlist(
  path = resolve(here, "vocab-allow.json")
): Allowlist {
  return JSON.parse(readFileSync(path, "utf8")) as Allowlist
}

/** report is the audit's lines: one per hit, one per stale entry. */
export function report(r: AuditResult): string[] {
  return [
    ...r.hits.map(
      (h) =>
        `src/${h.path}:${h.line}: retired word "${h.alias}" (glossary ${h.term}) in "${h.text.slice(0, 120)}"`
    ),
    ...r.stale.map(
      (a) =>
        `scripts/vocab-allow.json: "${a.alias}" in src/${a.file} ("${a.text}") matches no hit — drop the entry`
    ),
  ]
}

// bun sets import.meta.main on the entry script; vitest never does.
if ((import.meta as { main?: boolean }).main) {
  const src = resolve(here, "../src")
  const files = sources(src)
  const lines = report(audit(files, loadAllowlist()))
  if (lines.length) {
    console.error(lines.join("\n"))
    console.error(
      `vocab audit: ${lines.length} problem(s) — say the glossary's word (lib/glossary.ts), or add a reasoned entry to scripts/vocab-allow.json`
    )
    process.exit(1)
  }
  console.log(
    `vocab audit: ${files.length} files over ${AUDIT_DIRS.join(", ")} — no retired word`
  )
}
