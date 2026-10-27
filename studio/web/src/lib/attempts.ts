// The step's attempts and timing (plan A4, ADR 0016's A4 note, ADR
// 0028 §7): "attempt 4 of 4 · fallback to glm-b" and "1.2 s · first
// token 180 ms", read from the facts both clients already hold — the
// step route's attempts[] and model when it is loaded, else the
// request record's rows (attempt numbers, each attempt's model) and
// the folded step_finish's latency_ms/ttft_ms. Plain TypeScript, no
// React: the run page and the devtools panel read this one module, so
// both say the same words for the same facts (parity).

/** What the attempt line says: the answering attempt's number (0 when
 * the step ended with none answering), how many attempts the step
 * made, the model asked for and the one that answered. */
import { FIRST_TOKEN, plural, spanMs } from "./format"

export interface AttemptFacts {
  n: number
  total: number
  requested?: string
  answered?: string
}

/** attemptsHole is a finished step's not_recorded hole when it was
 * written by a weft before attempt reporting: no step_finish latency
 * (every A4 step_finish carries one, rounded up, never 0) and no
 * attempt rows. The reason says what the client checked; the fix is
 * the shared table's. Null otherwise. */
export function attemptsHole(
  finish: { latencyMs?: number } | undefined,
  rows: number
): { hole: "not_recorded"; reason: string } | null {
  if (finish === undefined || finish.latencyMs || rows !== 0) return null
  return {
    hole: "not_recorded",
    reason:
      "this step's step_finish has no timing and its request record no attempt rows: it was recorded by a weft before attempt reporting (A4)",
  }
}

/** attemptLayer is an attempt span's weft.attempt.layer — the run's
 * span for that step and attempt — what the attempts lists label
 * "fallback" when the step route (StepAttempt.layer) is not what they
 * draw from. */
export function attemptLayer(
  spans: { name: string; attrs: Record<string, unknown> }[] | null | undefined,
  runId: string,
  step: number,
  n: number
): string | undefined {
  return spans?.find(
    ({ name, attrs: a }) =>
      name === "attempt" &&
      a["weft.run.id"] === runId &&
      a["weft.step.index"] === step &&
      a["weft.attempt.index"] === n
  )?.attrs["weft.attempt.layer"] as string | undefined
}

/** The request-record row's fields the line reads (RequestRow's). */
interface RowLike {
  attempt: number
  body: { model: { name?: string } }
}

/** attemptLine is the header's words: "attempt 4 of 4 · fallback to
 * glm-b" when the answering attempt is not the first — "· retry" when
 * the same model answered, "· fallback to X" when another did — and
 * "3 attempts · none answered" when the step ended without an answer.
 * Null when the first attempt answered (nothing to say). */
export function attemptLine(f: AttemptFacts | null | undefined): string | null {
  if (!f) return null
  if (f.n === 0)
    return f.total > 0 ? `${plural(f.total, "attempt")} · none answered` : null
  if (f.n <= 1) return null
  let s = `attempt ${f.n} of ${Math.max(f.total, f.n)}`
  if (f.requested && f.answered)
    s +=
      f.answered === f.requested ? " · retry" : ` · fallback to ${f.answered}`
  return s
}

/** The step route's fields the line reads (StepDoc's). */
interface StepLike {
  status?: string
  model?: { requested?: string; answered?: string }
  attempts?: { attempt: number; model: string; outcome?: string }[]
}

/** factsFromStep reads the step route: the answering attempt is the
 * last one whose outcome is ok, the answering model the chat span's
 * (model.answered), else that attempt's own. A step that is not
 * running with attempts and no ok one answered none (n 0). Null when
 * none is listed or the step is still running. */
export function factsFromStep(doc: StepLike): AttemptFacts | null {
  // A partial document (a cache seeded with children only) says
  // nothing.
  const attempts = Array.isArray(doc.attempts) ? doc.attempts : []
  const ok = attempts.filter((a) => a.outcome === "ok").at(-1)
  if (!ok)
    return attempts.length && doc.status !== "running"
      ? { n: 0, total: attempts.length }
      : null
  return {
    n: ok.attempt,
    total: attempts.length,
    requested: doc.model?.requested || undefined,
    answered: doc.model?.answered || ok.model || undefined,
  }
}

/** attemptLabel is an attempt's name in the attempts lists (U4-fix):
 * "attempt 2", "attempt 2 · fallback" / "attempt 2 · retry" when its
 * attempt span's weft.attempt.layer names a layer (mw.Fallback's
 * "fallback", mw.Retry's "retry", U23.1). */
export function attemptLabel(
  n: number,
  layer?: string,
  cause?: string | null
): string {
  return [`attempt ${n}`, layer, cause].filter(Boolean).join(" · ")
}

/** factsFromRows reads a step's request rows (the requests route,
 * already loaded for the Request section): the last attempt of a
 * finished step answered — the loop stops calling once one does; a
 * step that ended unfinished (the run is no longer running) made its
 * attempts and none answered. Null when there is nothing to say (no
 * rows, or the step is still running). */
export function factsFromRows(
  rows: RowLike[] | undefined,
  finished: boolean,
  running = false
): AttemptFacts | null {
  if (!rows?.length) return null
  if (!finished) return running ? null : { n: 0, total: rows.length }
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

/** msText is a measured interval as the timing line says it: "180 ms",
 * "1.2 s", "1m03s" — lib/format.ts's spanMs, the one format (UX-F-16). */
export const msText = spanMs

/** timingLine is the step's latency and TTFT: "1.2 s · first token
 * 180 ms", both surfaces (FIRST_TOKEN, U21.1). An absent value is not
 * measured and is left out, never written 0; null when neither is. */
export function timingLine(
  latencyMs: number | undefined,
  ttftMs: number | undefined
): string | null {
  const parts: string[] = []
  if (latencyMs && latencyMs > 0) parts.push(msText(latencyMs))
  if (ttftMs && ttftMs > 0) parts.push(`${FIRST_TOKEN} ${msText(ttftMs)}`)
  return parts.length ? parts.join(" · ") : null
}

/** Why the retrying layer stopped (weft.attempt.giveup_reason,
 * UX-S2-02). */
export const GIVE_UP: Record<string, string | undefined> = {
  budget: "retries spent",
  not_retryable: "not retryable",
  retry_after_too_long: "the provider asked to wait past MaxWait",
}

/** giveUpLine is the line after a step's last attempt when the step
 * got no answer (UX-S2-02): "gave up: retries spent (3 attempts)" from
 * the last attempt's giveup_reason; "gave up after 3 attempts" when
 * several failed and none says why (a record before it, a layer that
 * does not say). Null when the last attempt answered, or a single
 * failure says nothing more than its own row. */
export function giveUpLine(
  attempts: { outcome?: string; giveup_reason?: string }[] | undefined
): string | null {
  const last = attempts?.at(-1)
  if (!last || last.outcome !== "error") return null
  const n = plural(attempts!.length, "attempt")
  const why = last.giveup_reason
  if (why) return `gave up: ${GIVE_UP[why] ?? why} (${n})`
  return attempts!.length > 1 ? `gave up after ${n}` : null
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
