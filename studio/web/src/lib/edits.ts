// The transcript editor (plan F2, ADR 0029 §8) as both clients hold it:
// one edit list per experiment, its wire form, the from_step the edits
// imply, the editor's own schema check (obsdb.CheckToolArgs' rules, by
// name — the runtime stays the authority and its sentence is shown
// verbatim) and the replayed run's weft.edits mark read back. Plain
// TypeScript — no React, no DOM: Studio's run page and the devtools
// panel import this one module, so the same edits post byte-identical
// transcript_edits from either surface.
import type { Message, Part } from "./api"
import { placeBatches } from "./events"
import type { TranscriptBatch } from "./events"

/** The five kinds (the wire's `kind`). */
export type EditKind = "tool_result" | "reply" | "user" | "tool_args" | "insert"

/** One edit of the kept prefix. step is the step ordinal (as from_step):
 * for insert the boundary before step `step`'s model call. kind absent
 * reads as before F2: callID → tool_result, else reply. */
export interface ReplayEdit {
  kind?: EditKind
  step: number
  callID?: string
  toolResult?: string
  content?: string
  /** tool_args: the new arguments, a JSON object. */
  args?: Record<string, unknown>
  /** user: which of the step's user messages (0: step 0's prompt). */
  index?: number
}

export const kindOf = (e: ReplayEdit): EditKind => e.kind ?? (e.callID ? "tool_result" : "reply")

/** editKey names an edit's target: one edit per target (one insert per
 * boundary in the editors). */
export const editKey = (e: ReplayEdit): string => `${kindOf(e)}:${e.step}:${e.callID ?? ""}:${e.index ?? 0}`

/** wireEdits is §5.1's transcript_edits: kind always sent, each kind
 * with the fields it takes and no other (studio/edits.go's editKindOf
 * refuses the rest), args an object. */
export function wireEdits(list: ReplayEdit[]): unknown[] {
  return list.map((e) => {
    const kind = kindOf(e)
    const w: Record<string, unknown> = { kind, step: e.step }
    if (kind === "tool_result" || kind === "tool_args") w.call_id = e.callID
    if (kind === "tool_result") w.tool_result = e.toolResult
    else if (kind === "tool_args") w.args = e.args
    else w.content = e.content
    if (kind === "user" && e.index) w.index = e.index
    return w
  })
}

/** putEdit replaces the edit on e's target, appends it, or (value
 * empty) drops it. */
export function putEdit(list: ReplayEdit[], e: ReplayEdit, empty = false): ReplayEdit[] {
  const k = editKey(e)
  const i = list.findIndex((x) => editKey(x) === k)
  if (empty) return i < 0 ? list : list.filter((_, j) => j !== i)
  return i < 0 ? [...list, e] : list.map((x, j) => (j === i ? e : x))
}

/** impliedFromStep: the prefix keeps every edited step — an edit of
 * step N needs from_step ≥ N + 1, an insert at boundary N from_step ≥ N. */
export function impliedFromStep(list: ReplayEdit[]): number {
  let n = 0
  for (const e of list) n = Math.max(n, kindOf(e) === "insert" ? e.step : e.step + 1)
  // Edits need from_step > 0 (0 keeps nothing to edit).
  return list.length ? Math.max(n, 1) : 0
}

/** editable: the target is one a replay can keep — the from_step it
 * implies (impliedFromStep) within the server's bound (max: the
 * highest from_step it accepts; null: unknown, nothing offered). */
export function editable(target: ReplayEdit, max: number | null): boolean {
  return max !== null && target.step >= 0 && impliedFromStep([target]) <= max
}

/** The line that holds Run while a fork carries edits (both surfaces). */
export const FORK_EDITS =
  "transcript edits belong to an ephemeral replay; a fork starts at step 0 — switch thread to ephemeral or drop the edits"

/** editsProblem holds Run while a draft's edits cannot travel: they go
 * only with from_step > 0, so a draft at step 0 is refused, never sent
 * without them (both surfaces). */
export function editsProblem(thread: string, step: number, edits: ReplayEdit[] | undefined): string | null {
  return edits?.length && step === 0
    ? thread === "fork" ? FORK_EDITS : "transcript edits need from_step ≥ 1 (step 0 keeps nothing to edit) — pick a later step or drop the edits"
    : null
}

/** The edit's line in the drawer's list: its kind and its place. */
export function editLine(e: ReplayEdit): string {
  const k = kindOf(e)
  const at = k === "insert" ? `before step ${e.step}` : `step ${e.step}`
  const who = e.callID ? ` · ${e.callID}` : k === "user" && e.index ? ` · #${e.index}` : ""
  const v = k === "tool_args" ? JSON.stringify(e.args) : (e.toolResult ?? e.content ?? "")
  return `${k} · ${at}${who} → ${v.length > 60 ? `${v.slice(0, 59)}…` : v}`
}

/** One user message of a step as the user edit counts them (studio/
 * edits.go's userSeqs): step 0's turn prompt (the input's last message,
 * when a user message) first, then the user messages the step's own
 * records hold (a steer, a resumed turn's prompt). */
export interface UserMessage {
  step: number
  index: number
  text: string
}

const textOf = (m: Message) =>
  (m.content as (Part | null)[])
    .filter((p) => p?.type === "text")
    .map((p) => (p as { text: string }).text)
    .join("")

export function userMessagesOf(batches: TranscriptBatch[]): UserMessage[] {
  const out: UserMessage[] = []
  const placed = placeBatches(batches)
  const input = placed.filter((b) => b.input).flatMap((b) => b.messages)
  const last = input.at(-1)
  if (last?.role === "user") out.push({ step: 0, index: 0, text: textOf(last) })
  for (const b of placed) {
    if (b.input) continue
    for (const m of b.messages)
      if (m.role === "user") out.push({ step: b.step, index: out.filter((u) => u.step === b.step).length, text: textOf(m) })
  }
  return out
}

/** schemaOf is the input schema the run's records hold for tool: the
 * LAST catalog of the run that names it (steps in order, rows in
 * order) — what Studio's server checks against (studio/playground.go's
 * recordedSchemas keys by name over every catalog, the last one
 * winning, not by the edited step's own catalog: a Go-side debt this
 * mirrors so the editor and the server never disagree). undefined
 * where no catalog says. */
export function schemaOf(steps: Map<number, { rows: { tools?: unknown }[] }> | undefined, tool: string): unknown {
  let out: unknown
  for (const n of [...(steps?.keys() ?? [])].sort((a, b) => a - b))
    for (const row of steps?.get(n)?.rows ?? []) {
      const t = (row.tools as { tools?: { name: string; schema?: unknown }[] } | undefined)?.tools?.find((x) => x.name === tool)
      if (t?.schema) out = t.schema
    }
  return out
}

// ── The editor's schema check ─────────────────────────────────────
// obsdb.CheckToolArgs, rule for rule: type (a name or a list; integer
// is an integral literal), enum, required, properties, additional-
// Properties (false refuses, a schema checks), items; a null field is
// absent (encoding/json's rule). Its sentence is the loop's
// INVALID_INPUT, field named.

type Num = number & { lit?: string }
const kind = (v: unknown): string =>
  v === null
    ? "null"
    : Array.isArray(v)
      ? "array"
      : v instanceof Number
        ? "number"
        : typeof v === "boolean"
          ? "bool"
          : typeof v === "string"
            ? "string"
            : "object"
const canon = (v: unknown): string =>
  v instanceof Number
    ? ((v as unknown as Num).lit ?? String(+v))
    : Array.isArray(v)
      ? `[${v.map(canon).join(",")}]`
      : v && typeof v === "object"
        ? `{${Object.keys(v)
            .sort()
            .map((k) => `${JSON.stringify(k)}:${canon((v as Record<string, unknown>)[k])}`)
            .join(",")}}`
        : JSON.stringify(v)
// int64's range, as the loop's decode into an int reads it.
const INT64 = BigInt("9223372036854775808")
const integral = (v: Num): boolean => {
  // No literal (an engine without JSON.parse source access): unchecked
  // here — checkArgs says so — never a pass on 3.0's behalf.
  if (v.lit === undefined) return Number.isInteger(+v)
  if (!/^-?\d+$/.test(v.lit)) return false
  const b = BigInt(v.lit)
  return b >= -INT64 && b < INT64
}
const fits = (t: string, v: unknown): boolean =>
  t === "integer"
    ? v instanceof Number && integral(v as unknown as Num)
    : !["object", "array", "string", "boolean", "null", "number"].includes(t) || kind(v) === (t === "boolean" ? "bool" : t)
type Schema = Record<string, unknown>

function walk(path: string, s: Schema, v: unknown): string {
  if (v === null && path) return ""
  const at = (want: string, got: string) =>
    path ? `field "${path}": expected ${want}, got ${got}` : `expected ${want} at the top level, got ${got}`
  const types = typeof s.type === "string" ? (s.type ? [s.type] : []) : Array.isArray(s.type) ? s.type.filter((t) => typeof t === "string") : []
  if (types.length && !types.some((t) => fits(t, v)))
    return at(types.join(" or "), v instanceof Number && types.includes("integer") ? `number ${(v as unknown as Num).lit ?? +v}` : kind(v))
  if (Array.isArray(s.enum) && s.enum.length && !s.enum.some((e) => canon(e) === canon(v)))
    return path ? `field "${path}": ${canon(v)} is not one of the schema's values` : "the arguments are not one of the schema's values"
  const join = (k: string) => (path ? `${path}.${k}` : k)
  if (kind(v) === "object") {
    const o = v as Record<string, unknown>
    const props = (s.properties ?? {}) as Record<string, unknown>
    for (const r of Array.isArray(s.required) ? s.required : []) if (typeof r === "string" && r && !(r in o)) return `missing required field "${join(r)}"`
    for (const k of Object.keys(o).sort()) {
      const ps = props[k]
      const m =
        ps && typeof ps === "object"
          ? walk(join(k), ps as Schema, o[k])
          : k in props
            ? ""
            : s.additionalProperties === false
              ? `unknown field "${join(k)}": not in the schema`
              : s.additionalProperties && typeof s.additionalProperties === "object"
                ? walk(join(k), s.additionalProperties as Schema, o[k])
                : ""
      if (m) return m
    }
  } else if (Array.isArray(v) && s.items && typeof s.items === "object" && !Array.isArray(s.items)) {
    for (let i = 0; i < v.length; i++) {
      const m = walk(join(String(i)), s.items as Schema, v[i])
      if (m) return m
    }
  }
  return ""
}

/** lossy names the first number the browser cannot carry as written:
 * past the doubles' exact integers (9007199254740993 would be sent as
 * …992) or out of range (1e400 would be sent as null). */
function lossy(path: string, v: unknown): string {
  const at = (k: string) => (path ? `${path}.${k}` : k)
  if (v instanceof Number) {
    const lit = (v as unknown as Num).lit
    const n = +v
    if (!Number.isFinite(n)) return `field "${path}": the number ${lit ?? n} is out of range here: write it as a string`
    if (lit && /^-?\d+$/.test(lit) && BigInt(lit) !== BigInt(n))
      return `field "${path}": the number ${lit} would be sent as ${BigInt(n).toString()} (past the exact integers JSON keeps here): write it as a string or a smaller number`
    return ""
  }
  if (v && typeof v === "object")
    for (const [k, x] of Object.entries(v as Record<string, unknown>)) {
      const m = lossy(at(k), x)
      if (m) return m
    }
  return ""
}

/** checkArgs reads an args edit: the object to send, or the refusal in
 * the loop's words. A tool with no recorded schema needs an object.
 * unchecked: this engine gave no number literals (JSON.parse source
 * access), so integer literals and exactness were not checked here —
 * the runtime checks them. */
export function checkArgs(tool: string, schema: unknown, text: string): { args?: Record<string, unknown>; error?: string; unchecked?: boolean } {
  const fail = (m: string) => ({ error: `INVALID_INPUT: tool "${tool}": ${m}` })
  let v: unknown
  const seen = { unchecked: false }
  try {
    // Each number as a Number carrying its literal: integer means an
    // integral literal in int64 (3.0 is refused, as the loop's decode does).
    v = JSON.parse(text, (_k, x: unknown, ctx?: { source?: string }) => {
      if (typeof x !== "number") return x
      const n = new Number(x) as Num
      n.lit = ctx?.source
      if (n.lit === undefined) seen.unchecked = true
      return n
    })
  } catch (e) {
    return fail(`invalid JSON: ${e instanceof Error ? e.message : String(e)}`)
  }
  if (kind(v) !== "object") return fail(`expected object at the top level, got ${kind(v)}`)
  const m = lossy("", v) || (schema && typeof schema === "object" ? walk("", schema as Schema, v) : "")
  if (m) return fail(m)
  return { args: JSON.parse(text) as Record<string, unknown>, ...(seen.unchecked ? { unchecked: true } : {}) }
}

// ── The mark (weft.edits) ─────────────────────────────────────────

/** One token of a replayed run's weft.edits (source coordinates). */
export interface EditMark {
  step: number
  what: "args" | "result" | "reply" | "user" | "insert"
  callID?: string
  index?: number
}

/** The chip each token draws on the replayed run's Story. */
export const MARK_CHIPS: Record<EditMark["what"], string> = {
  args: "args edited",
  result: "result edited",
  reply: "edited",
  user: "edited",
  insert: "inserted",
}

/** editMarks reads the run's weft.edits (meta): its tokens, and how many
 * the 1024-byte cap left out ("+N more"). None on a child run: it
 * inherits its parent's mark, and its own transcript was not edited. */
export function editMarks(row: { meta?: Record<string, string>; parent_run_id?: string } | null | undefined): { marks: EditMark[]; more: number } {
  const v = row?.parent_run_id ? "" : (row?.meta?.["weft.edits"] ?? "")
  const marks: EditMark[] = []
  let more = 0
  for (const tok of v ? v.split(",") : []) {
    const m = /^\+(\d+) more$/.exec(tok)
    if (m) {
      more = Number(m[1])
      continue
    }
    // A call id may hold ":" itself: the step is the first segment,
    // the kind the last, the call id what lies between.
    const parts = tok.split(":")
    const step = Number(parts[0])
    const last = parts[parts.length - 1]
    if (!parts[0] || !Number.isInteger(step) || parts.length < 2) continue
    if ((last === "args" || last === "result") && parts.length > 2) marks.push({ step, what: last, callID: parts.slice(1, -1).join(":") })
    else if (parts.length === 2 && (last === "reply" || last === "insert" || last === "user")) marks.push(last === "user" ? { step, what: "user", index: 0 } : { step, what: last })
    else if (parts.length === 3 && parts[1] === "user") marks.push({ step, what: "user", index: Number(last) })
  }
  return { marks, more }
}

/** markLine is a token's place in words. */
export function markLine(m: EditMark): string {
  return m.what === "insert"
    ? `a message inserted before step ${m.step}`
    : m.callID
      ? `step ${m.step} · call ${m.callID}`
      : m.what === "user"
        ? `step ${m.step} · user message${m.index ? ` #${m.index}` : ""}`
        : `step ${m.step} · reply`
}

/** markedPart is what the replay's input holds for a call token. The
 * kept prefix is the source's steps 0..fromStep−1, one assistant
 * message each, at the input's end: the token's step ordinal picks its
 * assistant message (the key), the call id its part there (a result
 * in the tool messages before the next). Without fromStep, or where
 * that place does not hold the id, a call id the input names once
 * stands; one it names more than once (a reused id) gives "" — the
 * chip drawn without a value rather than the wrong pair's. */
export function markedPart(batches: TranscriptBatch[] | undefined, m: EditMark, fromStep?: number): string {
  if (!m.callID || !batches) return ""
  const input = placeBatches(batches)
    .filter((b) => b.input)
    .flatMap((b) => b.messages)
  const said = (msgs: Message[]): string[] => {
    const out: string[] = []
    for (const msg of msgs)
      for (const p of msg.content as (Part | null)[]) {
        if (m.what === "args" && p?.type === "tool_call" && p.id === m.callID) out.push(`${p.name}(${JSON.stringify(p.args)})`)
        if (m.what === "result" && p?.type === "tool_result" && p.call_id === m.callID) out.push(String(p.content))
      }
    return out
  }
  const asst = input.flatMap((x, i) => (x.role === "assistant" ? [i] : []))
  const at = fromStep === undefined ? -1 : asst.length - fromStep + m.step
  if (at >= 0 && at < asst.length && m.step < (fromStep ?? 0)) {
    const own = m.what === "args" ? input.slice(asst[at], asst[at] + 1) : input.slice(asst[at] + 1, asst[at + 1] ?? input.length)
    const hit = said(own)
    if (hit.length === 1) return hit[0]
  }
  const all = said(input)
  return all.length === 1 ? all[0] : ""
}
