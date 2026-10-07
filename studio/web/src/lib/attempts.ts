// The step's attempts and timing (plan A4, ADR 0016's A4 note, ADR
// 0028 §7): "attempt 4 of 4 · fallback to glm-b" and "1.2 s · first
// token 180 ms", read from the facts both clients already hold — the
// step route's attempts[] and model when it is loaded, else the
// request record's rows (attempt numbers, each attempt's model) and
// the folded step_finish's latency_ms/ttft_ms. Plain TypeScript, no
// React: the run page and the devtools panel read this one module, so
// both say the same words for the same facts (parity).

/** What the attempt line says: the answering attempt's number, how
 * many attempts the step made, the model asked for and the one that
 * answered. */
export interface AttemptFacts {
  n: number
  total: number
  requested?: string
  answered?: string
}

/** The request-record row's fields the line reads (RequestRow's). */
interface RowLike {
  attempt: number
  body: { model: { name?: string } }
}

/** factsFromRows reads a step's request rows (the requests route,
 * already loaded for the Request section): the last attempt answered —
 * the loop stops calling once one does — and only a finished step has
 * an answering attempt. Null when there is nothing to say. */
export function factsFromRows(
  rows: RowLike[] | undefined,
  finished: boolean
): AttemptFacts | null {
  if (!finished || !rows?.length) return null
  let last = rows[0]
  for (const r of rows) if (r.attempt > last.attempt) last = r
  const first = rows.find((r) => r.attempt === 1) ?? rows[0]
  return {
    n: last.attempt,
    total: rows.length,
    requested: first.body.model.name || undefined,
    answered: last.body.model.name || undefined,
  }
}

/** The step route's fields the line reads (StepDoc's). */
interface StepLike {
  model?: { requested?: string; answered?: string }
  attempts?: { attempt: number; model: string; outcome?: string }[]
}

/** factsFromStep reads the step route: the answering attempt is the
 * last one whose outcome is ok, the answering model the chat span's
 * (model.answered), else that attempt's own. Null when no attempt
 * answered (a failed or running step) or none is listed. */
export function factsFromStep(doc: StepLike): AttemptFacts | null {
  // A partial document (a cache seeded with children only) says
  // nothing.
  const attempts = Array.isArray(doc.attempts) ? doc.attempts : []
  const ok = attempts.filter((a) => a.outcome === "ok").at(-1)
  if (!ok) return null
  return {
    n: ok.attempt,
    total: attempts.length,
    requested: doc.model?.requested || undefined,
    answered: doc.model?.answered || ok.model || undefined,
  }
}

/** attemptLine is the header's words: "attempt 4 of 4 · fallback to
 * glm-b" when the answering attempt is not the first — "· retry" when
 * the same model answered, "· fallback to X" when another did. Null
 * when the first attempt answered (nothing to say). */
export function attemptLine(f: AttemptFacts | null | undefined): string | null {
  if (!f || f.n <= 1) return null
  let s = `attempt ${f.n} of ${Math.max(f.total, f.n)}`
  if (f.requested && f.answered)
    s += f.answered === f.requested ? " · retry" : ` · fallback to ${f.answered}`
  return s
}

/** msText is a measured interval as the timing line says it: "180 ms",
 * "1.2 s", "1m03s". */
export function msText(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 59_950) return `${(ms / 1000).toFixed(1)} s`
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s`
}

/** timingLine is the step's latency and TTFT: "1.2 s · first token
 * 180 ms" (the panel words the second "ttft"). An absent value is not
 * measured and is left out, never written 0; null when neither is. */
export function timingLine(
  latencyMs: number | undefined,
  ttftMs: number | undefined,
  ttftWord = "first token"
): string | null {
  const parts: string[] = []
  if (latencyMs && latencyMs > 0) parts.push(msText(latencyMs))
  if (ttftMs && ttftMs > 0) parts.push(`${ttftWord} ${msText(ttftMs)}`)
  return parts.length ? parts.join(" · ") : null
}

/** attemptsHole is a finished step's not_recorded hole when it was
 * written by a weft before attempt reporting, told the way the server
 * tells a run without spans: no step_finish latency (every A4
 * step_finish carries one, rounded up, never 0) and no attempt rows.
 * Null otherwise. */
export function attemptsHole(
  finish: { latencyMs?: number } | undefined,
  rows: number
): { hole: "not_recorded"; reason: string } | null {
  if (finish === undefined || finish.latencyMs || rows !== 0) return null
  // steps.go's words for a run with no spans.
  return {
    hole: "not_recorded",
    reason:
      "the run has no spans: it was recorded without a tracer, or by a weft without attempt reporting (A4), so no attempt's outcome or timing exists",
  }
}

/** A step with no attempt row yet while the run is still running. */
export const ATTEMPTS_NOT_STORED = "attempts not stored yet"

/** relMs is an attempt's start or end as milliseconds from the step's
 * start — "+12 ms" — when both times parse; null otherwise. */
export function relMs(
  at: string | undefined,
  from: string | undefined
): string | null {
  if (!at || !from) return null
  const a = Date.parse(at)
  const b = Date.parse(from)
  if (!Number.isFinite(a) || !Number.isFinite(b)) return null
  return `+${msText(Math.max(0, a - b))}`
}
