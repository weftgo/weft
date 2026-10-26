// The experiment's pass predicate (UX plan D8; UX-C-38, UX-C-16): the
// language the playground's matrix form writes, the server stores
// verbatim (obsdb's pass_predicate, at most 4096 bytes) and the
// experiment grid evaluates — here, the one place it is read.
//
// The language, small on purpose:
//
//	predicate := clause { AND clause }          (AND in any case)
//	clause    := status ( = | != ) <status word>
//	           | answer ( ~ | !~ ) /regex/flags   (JavaScript RegExp; flags i m s u)
//	           | answer ( = | != ) "text"         (the whole answer, exact)
//	           | ( steps | tokens | cost ) ( < | <= | = | >= | > ) <number>
//
// status is the run's derived status (succeeded, failed, interrupted,
// running); answer the run's last own assistant text (the cell's answer
// line's source); steps its step count; tokens its input + output
// tokens; cost its whole bill in USD (own + delegated). An empty
// predicate is the default, `status = succeeded`. A clause that reads a
// fact not known (an answer not read yet, an unpriced cost) makes the
// cell's verdict unknown — never a pass or a fail it did not earn; a
// known fail wins over an unknown (AND). An answer ~ pattern is a
// user's regex: parsePredicate refuses the catastrophic shapes and the
// grid tests the rest off the page's thread under a time bound
// (lib/regex-run.ts, U24.2-fix 1). Studio's alone: the server stores
// the text verbatim and validates nothing in it.

import { REGEX_TEXT_CAP, REGEX_WORDS, riskyPattern } from "./regex-run"
import type { RegexOutcome } from "./regex-run"

import { formatUSD } from "./cost"

/** The predicate a matrix starts with, and an empty one means. */
export const DEFAULT_PREDICATE = "status = succeeded"

type Num = "steps" | "tokens" | "cost"
type Clause =
  | { field: "status"; neg: boolean; word: string }
  | { field: "answer"; neg: boolean; re: RegExp; source: string; flags: string }
  | { field: "answer"; neg: boolean; text: string }
  | { field: Num; op: "<" | "<=" | "=" | ">=" | ">"; n: number }

export interface Predicate {
  /** The text as read (trimmed; the default when empty). */
  text: string
  clauses: Clause[]
  /** A clause reads the answer: the grid then reads each cell's. */
  needsAnswer: boolean
}

/** The facts one cell is judged on (null: not known). */
export interface CellFacts {
  status: string
  /** The run's whole last answer; null when not known (not read yet,
   * content off, stripped, unreadable) — never "" for "not known". */
  answer?: string | null
  /** The answer ~ clauses' outcomes, in clause order (regexClauses),
   * run off the page's thread by lib/regex-run.ts; an entry not there
   * yet is "evaluating". Absent: the clause is tested inline on the
   * capped answer (tests; a pattern parsePredicate let through). */
  matches?: (RegexOutcome | undefined)[]
  steps?: number | null
  tokens?: number | null
  cost?: number | null
}

/** A cell's verdict: pass, fail, or unknown (a fact the predicate
 * reads is not known yet — or at all). */
export type Verdict = "pass" | "fail" | "unknown"

/** The words a verdict is drawn with. */
export const VERDICT_WORDS: Record<Verdict, string> = {
  pass: "✓ pass",
  fail: "✗ fail",
  unknown: "? not judged",
}

const STATUS_WORDS = ["succeeded", "failed", "interrupted", "running"]
const CLAUSE =
  /^(status|answer|steps|tokens|cost)\s*(!~|~|!=|<=|>=|=|<|>)\s*([\s\S]+)$/

/** parsePredicate reads a predicate: the parsed clauses, or the first
 * clause's error in words. */
export function parsePredicate(
  src: string | undefined
): { ok: true; pred: Predicate } | { ok: false; error: string } {
  const text = (src ?? "").trim() || DEFAULT_PREDICATE
  const clauses: Clause[] = []
  // AND splits clauses outside a /regex/ or a "text".
  for (const part of splitAnd(text)) {
    const m = CLAUSE.exec(part.trim())
    if (!m)
      return {
        ok: false,
        error: `“${part.trim()}” is not a clause: status = <word>, answer ~ /re/, answer = "text", or steps / tokens / cost <op> <number>`,
      }
    const [, field, op, raw] = m
    const v = raw.trim()
    if (field === "status") {
      if (op !== "=" && op !== "!=")
        return { ok: false, error: "status takes = or !=" }
      if (!STATUS_WORDS.includes(v))
        return {
          ok: false,
          error: `status is one of ${STATUS_WORDS.join(", ")} (not “${v}”)`,
        }
      clauses.push({ field, neg: op === "!=", word: v })
    } else if (field === "answer") {
      if (op === "~" || op === "!~") {
        const rm = /^\/([\s\S]*)\/([imsu]*)$/.exec(v)
        if (!rm) return { ok: false, error: "answer ~ takes /regex/flags" }
        const risky = riskyPattern(rm[1])
        if (risky) return { ok: false, error: `answer ~ ${v}: ${risky}` }
        try {
          clauses.push({
            field,
            neg: op === "!~",
            re: new RegExp(rm[1], rm[2]),
            source: rm[1],
            flags: rm[2],
          })
        } catch (e) {
          return {
            ok: false,
            error: `answer ~ ${v}: ${e instanceof Error ? e.message : String(e)}`,
          }
        }
      } else if (op === "=" || op === "!=") {
        const sm = /^"((?:[^"\\]|\\.)*)"$/.exec(v)
        if (!sm) return { ok: false, error: 'answer = takes a "quoted text"' }
        clauses.push({
          field,
          neg: op === "!=",
          text: sm[1].replace(/\\(.)/g, "$1"),
        })
      } else return { ok: false, error: "answer takes ~, !~, = or !=" }
    } else {
      const n = Number(v)
      if (!v || !Number.isFinite(n) || op === "~" || op === "!~" || op === "!=")
        return {
          ok: false,
          error: `${field} takes <, <=, =, >= or > and a number`,
        }
      clauses.push({
        field: field as Num,
        op: op as "<" | "<=" | "=" | ">=" | ">",
        n,
      })
    }
  }
  return {
    ok: true,
    pred: {
      text,
      clauses,
      needsAnswer: clauses.some((c) => c.field === "answer"),
    },
  }
}

/** splitAnd cuts at the word AND outside /…/ and "…". */
function splitAnd(s: string): string[] {
  const out: string[] = []
  let cur = ""
  let quote = ""
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (quote) {
      cur += c
      if (c === "\\" && i + 1 < s.length) cur += s[++i]
      else if (c === quote) quote = ""
      continue
    }
    if ((c === "/" || c === '"') && /[~=]\s*$/.test(cur)) {
      quote = c
      cur += c
      continue
    }
    const and = /^\s+and\s+/i.exec(s.slice(i))
    if (and && cur.trim()) {
      out.push(cur)
      cur = ""
      i += and[0].length - 1
      continue
    }
    cur += c
  }
  out.push(cur)
  return out
}

/** A verdict and, when it is not a pass, why. */
export interface Judged {
  verdict: Verdict
  why?: string
}

/** regexClauses is p's answer ~ clauses in order: the jobs a grid runs
 * through lib/regex-run.ts per cell. */
export function regexClauses(
  p: Predicate
): { source: string; flags: string }[] {
  return p.clauses.flatMap((c) =>
    c.field === "answer" && "re" in c
      ? [{ source: c.source, flags: c.flags }]
      : []
  )
}

/** The words of a clause not judged. */
export const NOT_JUDGED = {
  answer:
    "the answer is not known (not read yet, content off, stripped or unreadable)",
  evaluating: "evaluating the pattern…",
  fact: (f: string) => `the run's ${f} is not known`,
  running: "the run is still running",
} as const

function one(c: Clause, f: CellFacts, k: number): Judged {
  const v = (b: boolean): Judged => ({ verdict: b ? "pass" : "fail" })
  switch (c.field) {
    case "status":
      return v((f.status === c.word) !== c.neg)
    case "answer": {
      if (f.answer === undefined || f.answer === null)
        return { verdict: "unknown", why: NOT_JUDGED.answer }
      if (!("re" in c)) return v((f.answer === c.text) !== c.neg)
      let o: RegexOutcome | undefined
      if (f.matches) o = f.matches[k]
      else
        o = {
          hit: c.re.test(f.answer.slice(0, REGEX_TEXT_CAP)),
          capped: f.answer.length > REGEX_TEXT_CAP,
        }
      if (!o) return { verdict: "unknown", why: NOT_JUDGED.evaluating }
      if ("why" in o) return { verdict: "unknown", why: o.why }
      // A miss past the cap is not a known miss.
      if (!o.hit && o.capped)
        return { verdict: "unknown", why: REGEX_WORDS.capped }
      return v(o.hit !== c.neg)
    }
    default: {
      const x = f[c.field]
      if (x === undefined || x === null)
        return { verdict: "unknown", why: NOT_JUDGED.fact(c.field) }
      switch (c.op) {
        case "<":
          return v(x < c.n)
        case "<=":
          return v(x <= c.n)
        case "=":
          return v(x === c.n)
        case ">=":
          return v(x >= c.n)
        case ">":
          return v(x > c.n)
      }
    }
  }
}

/** judgeWith is a cell's verdict under p and why: every clause must
 * pass; a known fail wins over an unknown. A run still running is not
 * judged. */
export function judgeWith(p: Predicate, f: CellFacts): Judged {
  if (f.status === "running")
    return { verdict: "unknown", why: NOT_JUDGED.running }
  let out: Judged = { verdict: "pass" }
  let k = 0
  for (const c of p.clauses) {
    const r = one(c, f, k)
    if (c.field === "answer" && "re" in c) k++
    if (r.verdict === "fail") return r
    if (r.verdict === "unknown" && out.verdict === "pass") out = r
  }
  return out
}

/** judge is judgeWith's verdict alone. */
export const judge = (p: Predicate, f: CellFacts): Verdict =>
  judgeWith(p, f).verdict

/** One column's tally under the predicate. */
export interface ColumnTally {
  key: string
  /** The column's label (its model). */
  label: string
  pass: number
  fail: number
  unknown: number
  /** Rows with no run yet. */
  missing: number
  /** The column's bill (USD), null when any run's is not known. */
  usd: number | null
  /** The bill's words when usd is null: "unpriced" only when a model
   * has no price, else "cost unknown" (lib/cost.ts's sumCost badge);
   * its title says why. */
  costWord?: string
  costTitle?: string
  /** The first unjudged cell's reason (a cap, a timeout, an unreadable
   * answer), when one is. */
  why?: string
}

/** A column's bill when it is not known: "unpriced" only when a model
 * has no price, else "cost unknown" (ENGINE_WORDS.unknown's word). */
export const COST_WORDS = {
  unpriced: "unpriced",
  unknown: "cost unknown",
} as const

/** costWords is a column's bill in words: $x, or why not. */
export const costWords = (t: Pick<ColumnTally, "usd" | "costWord">): string =>
  t.usd === null ? (t.costWord ?? COST_WORDS.unknown) : formatUSD(t.usd)

/** The grid's summary line (the audit's "cheapest model passing every
 * row: kimi ($0.0108) · cheapest overall: glm (5/7)"). */
export interface MatrixSummary {
  /** The cheapest column whose every row passed (null: none did, or a
   * passing column's bill is not known — said in why). */
  cheapestPassing: ColumnTally | null
  /** The cheapest column with a known bill, with its pass rate. */
  cheapestOverall: ColumnTally | null
  /** Why a part is missing ("" when both are there). */
  why: string
}

/** passRate is "5/7" — the rows judged pass of the rows. */
/** passRate is "5/7" with every row judged; with some not judged or
 * missing, "3/4 judged" — the judged rows only, said. */
export const passRate = (t: ColumnTally): string => {
  const judged = t.pass + t.fail
  return t.unknown + t.missing
    ? `${t.pass}/${judged} judged`
    : `${t.pass}/${judged}`
}

/** The summary's words when a cell is not judged. */
export const UNJUDGED_WORDS = (k: number, why?: string) =>
  `${k} ${k === 1 ? "cell" : "cells"} not judged: no cheapest yet${why ? ` (${why})` : ""}`

/** summarize picks the cheapest passing column and the cheapest
 * overall. A column with an unknown verdict or a missing row cannot
 * be "passing every row" yet; a bill not known never counts as $0. */
export function summarize(cols: ColumnTally[]): MatrixSummary {
  // The backstop: a cell not judged (a cap, a timeout, an unreadable
  // answer) could be the one that decides — no cheapest is claimed.
  const unjudged = cols.reduce((n, c) => n + c.unknown, 0)
  if (unjudged)
    return {
      cheapestPassing: null,
      cheapestOverall: null,
      why: UNJUDGED_WORDS(unjudged, cols.find((c) => c.why)?.why),
    }
  // A column missing rows billed fewer of them: its smaller bill is no
  // "cheapest overall" (P10a-pre-web-fix).
  const priced = cols.filter((c) => c.usd !== null && c.missing === 0)
  const byCost = (a: ColumnTally, b: ColumnTally) => a.usd! - b.usd!
  const allPass = cols.filter(
    (c) => c.fail === 0 && c.unknown === 0 && c.missing === 0 && c.pass > 0
  )
  const passingPriced = allPass.filter((c) => c.usd !== null).sort(byCost)
  const cheapestPassing = passingPriced.at(0) ?? null
  const cheapestOverall = [...priced].sort(byCost).at(0) ?? null
  const why = !allPass.length
    ? "no column passed every row"
    : !cheapestPassing
      ? "the passing columns' cost is not known"
      : !cheapestOverall
        ? "no column's cost is known"
        : ""
  return { cheapestPassing, cheapestOverall, why }
}
