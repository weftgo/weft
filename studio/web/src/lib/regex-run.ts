// The pass predicate's regexes, run with a time bound (U24.2-fix 1;
// the review's ReDoS: `answer ~ /(a+)+$/` took 1.3 s per cell and froze
// the experiment page). Three guards, in order:
//   1. the text tested is the answer's first REGEX_TEXT_CAP characters
//      (a miss past the cap is "not judged", never a fail it did not earn);
//   2. a pattern with a quantified group that itself holds a quantifier
//      ((a+)+, (a*)*, (\w+\s?)*) or a backreference is refused at once,
//      with its reason — the classic catastrophic shapes;
//   3. every other pattern runs in one module worker (lib/regex-worker.ts),
//      one job at a time, each bounded by REGEX_TIMEOUT_MS: a job past it
//      terminates the worker (a fresh one takes the next job) and reads
//      "timeout". Where no Worker exists (a test's jsdom), the screened
//      pattern runs inline on the capped text.
// Outcomes are memoised by the caller per (run id, predicate text).

/** The most characters of an answer a pattern is tested against. */
export const REGEX_TEXT_CAP = 8 * 1024

/** The most one pattern may run on one answer. */
export const REGEX_TIMEOUT_MS = 250

/** One test's outcome: matched or not, or why it was not run. */
export type RegexOutcome = { hit: boolean; capped: boolean } | { why: string }

export const REGEX_WORDS = {
  timeout: `the pattern ran past ${REGEX_TIMEOUT_MS} ms on this answer (catastrophic backtracking?): not judged`,
  risky:
    "the pattern nests quantifiers or uses a backreference, which can freeze the page: not run — rewrite it without a quantified group of quantifiers",
  capped: `no match in the answer's first ${REGEX_TEXT_CAP / 1024} KiB (the rest is not searched): not judged`,
} as const

/** A quantified group holding a quantifier, or a backreference. */
const RISKY = [
  /\((?:[^()\\]|\\.)*(?:[+*]|\{\d+,\d*\})(?:[^()\\]|\\.)*\)(?:[+*]|\{\d+,\d*\})/,
  /\\[1-9]|\\k</,
]

/** riskyPattern says why a pattern is refused, "" when it may run. */
export function riskyPattern(source: string): string {
  return RISKY.some((r) => r.test(source)) ? REGEX_WORDS.risky : ""
}

interface Pending {
  id: number
  source: string
  flags: string
  text: string
  resolve: (o: RegexOutcome) => void
}

let worker: Worker | null = null
let nextID = 1
const queue: Pending[] = []
let busy = false

function spawn(): Worker {
  return new Worker(new URL("./regex-worker.ts", import.meta.url), {
    type: "module",
  })
}

function pump() {
  if (busy) return
  const job = queue.shift()
  if (!job) return
  busy = true
  worker ??= spawn()
  const w = worker
  const capped = job.text.length > REGEX_TEXT_CAP
  const done = (o: RegexOutcome) => {
    clearTimeout(timer)
    w.onmessage = null
    busy = false
    job.resolve(o)
    pump()
  }
  const timer = setTimeout(() => {
    // The pattern is still running: the worker cannot be interrupted,
    // only replaced.
    w.terminate()
    if (worker === w) worker = null
    done({ why: REGEX_WORDS.timeout })
  }, REGEX_TIMEOUT_MS)
  w.onmessage = (
    e: MessageEvent<{ id: number; hit?: boolean; error?: string }>
  ) => {
    if (e.data.id !== job.id) return
    done(
      e.data.error !== undefined
        ? { why: e.data.error }
        : { hit: !!e.data.hit, capped }
    )
  }
  w.postMessage({
    id: job.id,
    source: job.source,
    flags: job.flags,
    text: job.text.slice(0, REGEX_TEXT_CAP),
  })
}

/** testRegex tests one pattern on one answer under the three guards. */
export function testRegex(
  source: string,
  flags: string,
  text: string
): Promise<RegexOutcome> {
  const risky = riskyPattern(source)
  if (risky) return Promise.resolve({ why: risky })
  const capped = text.length > REGEX_TEXT_CAP
  if (typeof Worker === "undefined") {
    try {
      return Promise.resolve({
        hit: new RegExp(source, flags).test(text.slice(0, REGEX_TEXT_CAP)),
        capped,
      })
    } catch (e) {
      return Promise.resolve({
        why: e instanceof Error ? e.message : String(e),
      })
    }
  }
  return new Promise((resolve) => {
    queue.push({ id: nextID++, source, flags, text, resolve })
    pump()
  })
}
