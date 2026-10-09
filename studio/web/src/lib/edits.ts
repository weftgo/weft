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

/** schemaAt is the input schema the step's catalog recorded for tool
 * (its answering attempt's row), undefined where no catalog says. */
export function schemaAt(
  steps: Map<number, { rows: { tools?: unknown }[] }> | undefined,
  step: number,
  tool: string
): unknown {
  const tools = (steps?.get(step)?.rows.at(-1)?.tools as { tools?: { name: string; schema?: unknown }[] } | undefined)?.tools
  return tools?.find((t) => t.name === tool)?.schema
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
const fits = (t: string, v: unknown): boolean =>
  t === "integer"
    ? v instanceof Number && /^-?\d+$/.test((v as unknown as Num).lit ?? String(+v))
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

/** checkArgs reads an args edit: the object to send, or the refusal in
 * the loop's words. A tool with no recorded schema needs an object. */
export function checkArgs(tool: string, schema: unknown, text: string): { args?: Record<string, unknown>; error?: string } {
  const fail = (m: string) => ({ error: `INVALID_INPUT: tool "${tool}": ${m}` })
  let v: unknown
  try {
    // Each number as a Number carrying its literal: integer means an
    // integral literal (3.0 is refused, as the loop's decode does).
    v = JSON.parse(text, (_k, x: unknown, ctx?: { source?: string }) => {
      if (typeof x !== "number") return x
      const n = new Number(x) as Num
      n.lit = ctx?.source
      return n
    })
  } catch (e) {
    return fail(`invalid JSON: ${e instanceof Error ? e.message : String(e)}`)
  }
  if (kind(v) !== "object") return fail(`expected object at the top level, got ${kind(v)}`)
  const m = schema && typeof schema === "object" ? walk("", schema as Schema, v) : ""
  return m ? fail(m) : { args: JSON.parse(text) as Record<string, unknown> }
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
    const [s, a, b] = tok.split(":")
    const step = Number(s)
    if (!Number.isInteger(step)) continue
    if (b === "args" || b === "result") marks.push({ step, what: b, callID: a })
    else if (a === "reply" || a === "insert") marks.push({ step, what: a })
    else if (a === "user") marks.push({ step, what: "user", index: b ? Number(b) : 0 })
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

/** markedPart is what the replay's input holds for a call token (its
 * kept prefix is the source's, positional: the call id joins). */
export function markedPart(batches: TranscriptBatch[] | undefined, m: EditMark): string {
  if (!m.callID || !batches) return ""
  for (const b of placeBatches(batches)) {
    if (!b.input) continue
    for (const msg of b.messages)
      for (const p of msg.content as (Part | null)[]) {
        if (m.what === "args" && p?.type === "tool_call" && p.id === m.callID) return `${p.name}(${JSON.stringify(p.args)})`
        if (m.what === "result" && p?.type === "tool_result" && p.call_id === m.callID) return String(p.content)
      }
  }
  return ""
}
