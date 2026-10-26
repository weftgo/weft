// The pass predicate (UX plan D8): the language parsed, a cell judged —
// an unknown fact is "unknown", never a pass — and the grid's summary.
import { afterEach, describe, expect, it, vi } from "vitest"

import {
  DEFAULT_PREDICATE,
  NOT_JUDGED,
  costWords,
  judge,
  judgeWith,
  parsePredicate,
  passRate,
  summarize,
} from "./predicate"
import type { ColumnTally, Predicate } from "./predicate"
import {
  REGEX_TEXT_CAP,
  REGEX_TIMEOUT_MS,
  REGEX_WORDS,
  resetRegexRunner,
  riskyPattern,
  testRegex,
} from "./regex-run"

const p = (s: string): Predicate => {
  const r = parsePredicate(s)
  if (!r.ok) throw new Error(r.error)
  return r.pred
}

describe("parsePredicate", () => {
  it("reads the audit's predicate and the default", () => {
    const pred = p(String.raw`status = succeeded AND answer ~ /\d+ units/`)
    expect(pred.clauses).toHaveLength(2)
    expect(pred.needsAnswer).toBe(true)
    expect(p("").text).toBe(DEFAULT_PREDICATE)
    expect(p("  ").needsAnswer).toBe(false)
  })
  it("keeps AND inside a regex or a quoted text", () => {
    expect(p("answer ~ /cats and dogs/i").clauses).toHaveLength(1)
    expect(p('answer = "a and b" and steps <= 3').clauses).toHaveLength(2)
  })
  it("says what is wrong", () => {
    for (const [src, want] of [
      ["status = done", "status is one of"],
      ["answer ~ units", "answer ~ takes /regex/flags"],
      ["answer ~ /(/", "answer ~ /(/"],
      ["cost ~ 3", "cost takes"],
      ["steps < many", "steps takes"],
      ["latency < 3", "is not a clause"],
    ]) {
      const r = parsePredicate(src)
      expect(r.ok).toBe(false)
      if (!r.ok) expect(r.error).toContain(want)
    }
  })
})

describe("judge", () => {
  const pred = p(String.raw`status = succeeded AND answer ~ /\d+ units/`)
  it("passes, fails, or does not judge", () => {
    expect(
      judge(pred, { status: "succeeded", answer: "We have 42 units." })
    ).toBe("pass")
    expect(judge(pred, { status: "succeeded", answer: "none left" })).toBe(
      "fail"
    )
    // A known fail wins over an unknown answer.
    expect(judge(pred, { status: "failed", answer: null })).toBe("fail")
    // The answer not read: not judged, never a pass.
    expect(judge(pred, { status: "succeeded", answer: null })).toBe("unknown")
    expect(judge(pred, { status: "running", answer: "42 units" })).toBe(
      "unknown"
    )
  })
  it("reads numbers, and an unpriced cost is unknown", () => {
    const q = p("status = succeeded AND cost < 0.01 AND tokens <= 2000")
    expect(judge(q, { status: "succeeded", cost: 0.002, tokens: 1500 })).toBe(
      "pass"
    )
    expect(judge(q, { status: "succeeded", cost: 0.02, tokens: 1500 })).toBe(
      "fail"
    )
    expect(judge(q, { status: "succeeded", cost: null, tokens: 1500 })).toBe(
      "unknown"
    )
    expect(judge(p("status != failed"), { status: "succeeded" })).toBe("pass")
  })
})

describe("summarize", () => {
  const col = (o: Partial<ColumnTally> & { key: string }): ColumnTally => ({
    label: o.key,
    pass: 0,
    fail: 0,
    unknown: 0,
    missing: 0,
    usd: 0,
    ...o,
  })
  it("names the cheapest column passing every row and the cheapest overall", () => {
    const glm = col({ key: "glm", pass: 5, fail: 2, usd: 0.0021 })
    const kimi = col({ key: "kimi", pass: 7, usd: 0.0108 })
    const opus = col({ key: "opus", pass: 7, usd: 0.4 })
    const s = summarize([glm, kimi, opus])
    expect(s.cheapestPassing?.key).toBe("kimi")
    expect(s.cheapestOverall?.key).toBe("glm")
    expect(passRate(glm)).toBe("5/7")
  })
  it("never counts an unpriced column as $0", () => {
    const free = col({ key: "free", pass: 7, usd: null })
    const paid = col({ key: "paid", pass: 6, fail: 1, usd: 0.001 })
    const s = summarize([free, paid])
    expect(s.cheapestPassing).toBeNull()
    expect(s.why).toContain("not known")
    expect(s.cheapestOverall?.key).toBe("paid")
  })
  // U24.2-fix2 1, the backstop: one cell not judged — a cap, a timeout,
  // an unreadable answer — and no cheapest is claimed, the reason said;
  // pass rates count the judged rows.
  it("claims no cheapest while any cell is not judged, and says why", () => {
    const cheap = col({
      key: "cheap",
      pass: 6,
      unknown: 1,
      usd: 0.001,
      why: "timed out",
    })
    const dear = col({ key: "dear", pass: 7, usd: 0.4 })
    const s = summarize([cheap, dear])
    expect(s.cheapestPassing).toBeNull()
    expect(s.cheapestOverall).toBeNull()
    expect(s.why).toBe("1 cell not judged: no cheapest yet (timed out)")
    expect(passRate(cheap)).toBe("6/6 judged")
    expect(passRate(dear)).toBe("7/7")
  })
})

describe("the ReDoS guards (U24.2-fix 1)", () => {
  it("refuses a nested-quantifier pattern when parsed, fast", () => {
    const t0 = performance.now()
    const r = parsePredicate("answer ~ /(a+)+$/")
    expect(r.ok).toBe(false)
    if (!r.ok) expect(r.error).toContain("nests quantifiers")
    expect(parsePredicate(String.raw`answer ~ /(\w+\s?)*$/`).ok).toBe(false)
    expect(parsePredicate(String.raw`answer ~ /(.)\1/`).ok).toBe(false)
    expect(performance.now() - t0).toBeLessThan(50)
  })
  // U24.2-fix2 3: the screen skips classes and escapes, lets a bounded
  // inner quantifier through, and sees nested groups.
  it.each([
    [String.raw`(\d{1,3}\.)+\d+`, ""],
    [String.raw`([+-]\d)+`, ""],
    [String.raw`C:\\1st`, ""],
    [String.raw`(ab)+c*`, ""],
    [String.raw`(a+){2,5}`, ""],
    [String.raw`((a+))+`, REGEX_WORDS.risky],
    [String.raw`((a+)b)+`, REGEX_WORDS.risky],
    [String.raw`(?:a*b?)*`, REGEX_WORDS.risky],
    [String.raw`(a{2,})+`, REGEX_WORDS.risky],
    [String.raw`(?<n>a)\k<n>`, REGEX_WORDS.risky],
  ])("riskyPattern(%s)", (src, want) => {
    expect(riskyPattern(src)).toBe(want)
  })
  it("tests a capped answer: a miss past the cap is not judged", () => {
    const pred = p("answer ~ /needle/")
    const long = "x".repeat(REGEX_TEXT_CAP) + " needle"
    expect(judgeWith(pred, { status: "succeeded", answer: long })).toEqual({
      verdict: "unknown",
      why: REGEX_WORDS.capped,
    })
    expect(judge(pred, { status: "succeeded", answer: "needle" + long })).toBe(
      "pass"
    )
  })
  it("a timed-out or still-running pattern reads not judged with its reason", () => {
    const pred = p("answer ~ /x/")
    expect(
      judgeWith(pred, {
        status: "succeeded",
        answer: "x",
        matches: [{ why: REGEX_WORDS.timeout }],
      })
    ).toEqual({ verdict: "unknown", why: REGEX_WORDS.timeout })
    expect(
      judgeWith(pred, { status: "succeeded", answer: "x", matches: [] }).why
    ).toBe(NOT_JUDGED.evaluating)
  })
})

describe("testRegex (lib/regex-run.ts)", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    resetRegexRunner()
  })
  // U24.2-fix2 2: a worker that cannot load (CSP, 404, proxy) is said
  // for every queued job — never read as a timeout — and not respawned.
  it("says the worker could not start, for every job, spawning it once", async () => {
    let made = 0
    class Broken {
      onerror: ((e: ErrorEvent) => void) | null = null
      onmessage: unknown = null
      onmessageerror: unknown = null
      constructor() {
        made++
      }
      postMessage() {
        setTimeout(
          () =>
            this.onerror?.({
              message: "refused by CSP",
              preventDefault() {},
            } as ErrorEvent),
          1
        )
      }
      terminate() {}
    }
    vi.stubGlobal("Worker", Broken)
    const outs = await Promise.all([
      testRegex("a", "", "a"),
      testRegex("b", "", "b"),
      testRegex("c", "", "c"),
    ])
    for (const o of outs)
      expect(o).toEqual({
        why: "the regex worker could not start: refused by CSP",
      })
    expect(await testRegex("d", "", "d")).toEqual({
      why: "the regex worker could not start: refused by CSP",
    })
    expect(made).toBe(1)
  })
  // U24.2-fix2 4: a job whose query let it go is skipped, never run.
  it("skips a job whose signal aborted before it ran", async () => {
    const ran: string[] = []
    class Echo {
      onmessage: ((e: MessageEvent) => void) | null = null
      onerror: unknown = null
      onmessageerror: unknown = null
      postMessage(job: { id: number; source: string }) {
        ran.push(job.source)
        setTimeout(
          () =>
            this.onmessage?.({
              data: { id: job.id, hit: true },
            } as MessageEvent),
          1
        )
      }
      terminate() {}
    }
    vi.stubGlobal("Worker", Echo)
    const gone = new AbortController()
    const first = testRegex("first", "", "x")
    const stale = testRegex("stale", "", "x", gone.signal)
    gone.abort()
    const last = testRegex("last", "", "x")
    await Promise.all([first, stale, last])
    expect(await stale).toEqual({ why: "cancelled" })
    expect(ran).toEqual(["first", "last"])
  })
  it("refuses a risky shape without running it", async () => {
    expect(await testRegex("(a+)+$", "", "a".repeat(28) + "!")).toEqual({
      why: REGEX_WORDS.risky,
    })
  })
  it("terminates a worker past the timeout and answers the next job on a fresh one", async () => {
    const made: FakeWorker[] = []
    class FakeWorker {
      onmessage: ((e: MessageEvent) => void) | null = null
      terminated = false
      constructor() {
        made.push(this)
      }
      postMessage(job: { id: number; text: string }) {
        // The first worker hangs (a catastrophic pattern); the next answers.
        if (made.length === 1) return
        setTimeout(
          () =>
            this.onmessage?.({
              data: { id: job.id, hit: job.text === "y" },
            } as MessageEvent),
          1
        )
      }
      terminate() {
        this.terminated = true
      }
    }
    vi.stubGlobal("Worker", FakeWorker)
    const t0 = performance.now()
    const [slow, next] = await Promise.all([
      testRegex("x", "", "x"),
      testRegex("y", "", "y"),
    ])
    expect(slow).toEqual({ why: REGEX_WORDS.timeout })
    expect(next).toEqual({ hit: true, capped: false })
    expect(made[0].terminated).toBe(true)
    expect(performance.now() - t0).toBeLessThan(REGEX_TIMEOUT_MS + 1_000)
  })
})

describe("costWords (U24.2-fix 8)", () => {
  it("says unpriced only for a model with no price", () => {
    expect(costWords({ usd: 0.0021 })).toBe("$0.0021")
    expect(costWords({ usd: null, costWord: "unpriced" })).toBe("unpriced")
    expect(costWords({ usd: null })).toBe("cost unknown")
  })
})
