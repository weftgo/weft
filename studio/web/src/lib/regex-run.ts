// The pass predicate's regexes, run with a time bound (U24.2-fix 1;
// the review's ReDoS: `answer ~ /(a+)+$/` took 1.3 s per cell and froze
// the experiment page). Three guards, in order:
//   1. the text tested is the answer's first REGEX_TEXT_CAP characters
//      (a miss past the cap is "not judged", never a fail it did not earn);
//   2. a pattern with a quantified group that itself holds a quantifier
//      ((a+)+, (a*)*, (\w+\s?)*) or a backreference is refused at once,
//      with its reason — the classic catastrophic shapes;
//   3. every other pattern runs in one module worker (lib/regex-worker.ts),
//      one job at a time, each bounded by REGEX_TIMEOUT_MS from the moment
//      it is handed to a worker that said it is loaded (its ready
//      message: the module's load is never charged to a pattern, the
//      first job's nor the one after a respawn's) — a load is bounded on
//      its own (REGEX_LOAD_MS). A job past its bound terminates the worker
//      (a fresh one takes the next job) and reads "timeout". Where no Worker exists (a test's jsdom), the screened
//      pattern runs inline on the capped text.
// Outcomes are memoised by the caller per (run id, predicate text).

/** The most characters of an answer a pattern is tested against. */
export const REGEX_TEXT_CAP = 8 * 1024

/** The most one pattern may run on one answer. */
export const REGEX_TIMEOUT_MS = 250

/** The most a worker may take to load (its ready message). */
export const REGEX_LOAD_MS = 10_000

/** One test's outcome: matched or not, or why it was not run. */
export type RegexOutcome = { hit: boolean; capped: boolean } | { why: string }

export const REGEX_WORDS = {
  timeout: `the pattern ran past ${REGEX_TIMEOUT_MS} ms on this answer (catastrophic backtracking?): not judged`,
  risky:
    "the pattern nests quantifiers or uses a backreference, which can freeze the page: not run — rewrite it without a quantified group of quantifiers",
  capped: `no match in the answer's first ${REGEX_TEXT_CAP / 1024} KiB (the rest is not searched): not judged`,
} as const

/** quantAt says whether source[i] opens an unbounded quantifier
 * (+, *, {n,}) and how many characters it takes; a bounded {m,n} and ?
 * are not unbounded. */
function quantAt(
  source: string,
  i: number
): { unbounded: boolean; len: number } {
  const c = source[i]
  if (c === "+" || c === "*") return { unbounded: true, len: 1 }
  if (c === "?") return { unbounded: false, len: 1 }
  if (c === "{") {
    const m = /^\{\d+(,\d*)?\}/.exec(source.slice(i))
    // {n,} is unbounded; {n} and {m,n} are not.
    if (m) return { unbounded: m[0].endsWith(",}"), len: m[0].length }
  }
  return { unbounded: false, len: 0 }
}

/** riskyPattern says why a pattern is refused, "" when it may run: a
 * group holding an unbounded quantifier (at any depth) that is itself
 * quantified unboundedly — (a+)+, ((a+))+, ((a+)b)*, (\w+\s?)* — or a
 * backreference (\1, \k<name>). The scan skips [classes] and escapes
 * (\\ is a backslash, never a backref; a + inside [+-] is a
 * character); a bounded {m,n} inside, or on the group, lets it through:
 * (\d{1,3}\.)+\d+ runs. A screen, not the bound: the worker's timeout
 * is (pump). */
export function riskyPattern(source: string): string {
  const stack: boolean[] = [] // per open group: it holds an unbounded quantifier
  let i = 0
  while (i < source.length) {
    const c = source[i]
    if (c === "\\") {
      const n = source[i + 1] ?? ""
      if (/[1-9]/.test(n) || n === "k") return REGEX_WORDS.risky
      i += 2
      continue
    }
    if (c === "[") {
      // A class: to its closing ], escapes inside skipped; a leading ]
      // (or ^]) is a character.
      let k = i + 1
      if (source[k] === "^") k++
      if (source[k] === "]") k++
      while (k < source.length && source[k] !== "]")
        k += source[k] === "\\" ? 2 : 1
      i = k + 1
      continue
    }
    if (c === "(") {
      stack.push(false)
      i++
      // (?:, (?=, (?<name> … : the group's own syntax, no quantifier.
      if (source[i] === "?") {
        i++
        if (
          source[i] === "<" &&
          source[i + 1] !== "=" &&
          source[i + 1] !== "!"
        ) {
          const end = source.indexOf(">", i)
          i = end < 0 ? source.length : end + 1
        } else i++
      }
      continue
    }
    if (c === ")") {
      const inner = stack.pop() ?? false
      const q = quantAt(source, i + 1)
      if (inner && q.unbounded) return REGEX_WORDS.risky
      // The group, quantified or holding a quantifier, makes its parent
      // hold one.
      if (stack.length && (inner || q.unbounded)) stack[stack.length - 1] = true
      i += 1 + q.len
      continue
    }
    const q = quantAt(source, i)
    if (q.len) {
      if (q.unbounded && stack.length) stack[stack.length - 1] = true
      i += q.len
      continue
    }
    i++
  }
  return ""
}

interface Pending {
  id: number
  source: string
  flags: string
  text: string
  signal?: AbortSignal
  resolve: (o: RegexOutcome) => void
}

let worker: Worker | null = null
/** The worker posted its ready message (it loaded). */
let ready = false
/** Why the worker could not start (a CSP refusal, a 404, a proxy): once
 * set, no worker is spawned again and every job reads it. */
let broken = ""
let nextID = 1
const queue: Pending[] = []
let busy = false

function spawn(): Worker {
  ready = false
  return new Worker(new URL("./regex-worker.ts", import.meta.url), {
    type: "module",
  })
}

/** The words of a job the worker could not take. */
const startFailed = (msg: string) =>
  `the regex worker could not start: ${msg || "it failed to load"}`

/** stopAll resolves every queued job (and the running one) with why:
 * the worker failed to load, so none of them will run. */
function stopAll(why: string, running?: Pending) {
  for (const j of [...(running ? [running] : []), ...queue.splice(0)])
    j.resolve({ why })
}

function pump() {
  if (busy) return
  // A job whose page let it go (its query aborted: a newer keystroke, an
  // unmounted grid) is dropped, never run.
  let job = queue.shift()
  while (job?.signal?.aborted) {
    job.resolve({ why: "cancelled" })
    job = queue.shift()
  }
  if (!job) return
  if (broken) {
    job.resolve({ why: broken })
    stopAll(broken)
    return
  }
  busy = true
  worker ??= spawn()
  const w = worker
  const current = job
  const capped = current.text.length > REGEX_TEXT_CAP
  let over = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const done = (o: RegexOutcome) => {
    if (over) return
    over = true
    clearTimeout(timer)
    w.onmessage = null
    w.onerror = null
    w.onmessageerror = null
    busy = false
    current.resolve(o)
    pump()
  }
  const failed = (msg: string) => {
    // The worker did not load: say so for every job, spawn no other.
    broken = startFailed(msg)
    w.terminate()
    if (worker === w) worker = null
    if (over) return
    over = true
    clearTimeout(timer)
    busy = false
    stopAll(broken, current)
  }
  // The job's bound starts when the loaded worker takes it.
  const start = () => {
    timer = setTimeout(() => {
      // The pattern is still running: the worker cannot be interrupted,
      // only replaced.
      w.terminate()
      if (worker === w) worker = null
      done({ why: REGEX_WORDS.timeout })
    }, REGEX_TIMEOUT_MS)
    w.postMessage({
      id: current.id,
      source: current.source,
      flags: current.flags,
      text: current.text.slice(0, REGEX_TEXT_CAP),
    })
  }
  w.onerror = (e: ErrorEvent) => {
    e.preventDefault()
    failed(e.message)
  }
  w.onmessageerror = () => failed("a message could not be read")
  w.onmessage = (
    e: MessageEvent<{
      id?: number
      hit?: boolean
      error?: string
      ready?: boolean
    }>
  ) => {
    if (e.data.ready) {
      ready = true
      clearTimeout(timer)
      start()
      return
    }
    if (e.data.id !== current.id) return
    done(
      e.data.error !== undefined
        ? { why: e.data.error }
        : { hit: !!e.data.hit, capped }
    )
  }
  if (ready) start()
  else
    timer = setTimeout(
      () => failed(`it did not load in ${REGEX_LOAD_MS / 1000} s`),
      REGEX_LOAD_MS
    )
}

/** resetRegexRunner forgets a failed worker (tests). */
export function resetRegexRunner() {
  broken = ""
  worker = null
  ready = false
  busy = false
  queue.splice(0)
}

/** testRegex tests one pattern on one answer under the three guards. */
export function testRegex(
  source: string,
  flags: string,
  text: string,
  /** The caller's query signal: an aborted job is skipped, never run. */
  signal?: AbortSignal
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
    queue.push({ id: nextID++, source, flags, text, signal, resolve })
    pump()
  })
}
