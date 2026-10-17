// The playground's form as a value (plan F5): what a draft keeps in
// this browser (?exp=<draft id>, lib/drafts.ts), what a share link
// carries (#state=<compact>), and what "clone into the form" builds
// from a saved experiment. One shape for all three, so a form saved,
// shared or cloned reopens as the same form. Plain TypeScript — no
// React, no DOM beyond the platform's CompressionStream.
//
// A saved experiment (GET /api/experiments/{id}, studio/experiments.go)
// keeps its name, agent, variants (key + the overrides object the
// command sent) and inputs (key, source run, text) and — since F5.1 —
// the command itself: engine, side_effects, thread (per variant where
// they differ), from_step, source_run_id and transcript_edits, which
// reopen it exactly. A definition saved before F5.1 has none of them:
// they are then read back from the experiment's runs:
// from_step from weft.forked_from ("<run>#<step>"), the engine from the
// run's model (weft/runtime · scripted), the thread mode from a session
// id (fork mode writes one), and the side-effect mode from the cell's
// command id (cellCommandID below), the one knob neither the definition
// nor the run row records.
import type {
  ExperimentRow,
  PlaygroundRunBody,
  RunRow,
  ToolChoiceWire,
} from "./api"
import type { ReplayEdit } from "./edits"
import { wireEdits } from "./edits"
import type {
  Engine,
  LabFields,
  SideEffects,
  ThreadMode,
  VariantFields,
} from "./experiment-body"
import { editsFromHandoff, emptyLab } from "./experiment-body"
import { forkSource, validExperimentID } from "./experiments"

/** The form's format version: another one is refused, never guessed.
 * 2 (U26; UX-C-21): one step for the page — `step`, which Run and the
 * matrix both continue from, with the page's transcript edits; v1's
 * second step (matrix.from_step) is gone, and a v1 link or draft is
 * refused rather than read with one of its two steps dropped. */
export const FORM_VERSION = 2

/** One variant as the form holds it. Absent fields are the defaults:
 * instructions "" (the agent's registered prompt), every tool on, no
 * model or thinking override, engine live, side effects substitute,
 * thread ephemeral, no option-lab knob. */
export interface FormVariant {
  key: string
  instructions?: string
  /** The tools turned off (the playground's own form). */
  tools_off?: string[]
  /** The tools left on (a saved experiment's tools_enabled): resolved
   * against the agent once it is known. */
  tools_on?: string[]
  model?: string
  thinking?: string
  input?: string
  engine?: Engine
  side_effects?: SideEffects
  thread?: ThreadMode
  /** The option lab's set knobs only. */
  lab?: Partial<LabFields>
}

/** One matrix input row. */
export interface FormInput {
  key: string
  text: string
}

/** The playground's whole form. */
export interface FormState {
  v: typeof FORM_VERSION
  agent?: string
  runtime?: string
  /** The source run and the page's one step (its ordinal): Run and
   * every matrix cell continue from it. */
  run?: string
  step?: number
  /** The page's transcript edits: Run and every matrix cell send them
   * (past step 0). */
  edits?: ReplayEdit[]
  variants: FormVariant[]
  matrix?: {
    /** The matrix's name, as typed (free text). */
    name?: string
    /** The experiment id it is saved under (lib/experiments.ts's rule;
     * one that breaks it is dropped and re-minted). */
    id?: string
    inputs: FormInput[]
  }
}

const isObj = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v)
const isStrings = (v: unknown): v is string[] =>
  Array.isArray(v) && v.every((x) => typeof x === "string")
const ordinal = (v: unknown): number | undefined =>
  typeof v === "number" && Number.isInteger(v) && v >= 0 ? v : undefined
const text = (v: unknown): string | undefined =>
  typeof v === "string" ? v : undefined
const ENGINES: Engine[] = ["live", "scripted"]
const SIDE_EFFECTS: SideEffects[] = ["substitute", "park", "allow"]
const THREADS: ThreadMode[] = ["ephemeral", "fork"]
const pick = <T extends string>(v: unknown, of: T[]): T | undefined =>
  of.find((x) => x === v)

/** labOf keeps the option lab's known knobs of the right shape. */
function labOf(v: unknown): Partial<LabFields> | undefined {
  if (!isObj(v)) return undefined
  const out: Record<string, unknown> = {}
  for (const [k, want] of Object.entries(emptyLab())) {
    const x = v[k]
    if (
      Array.isArray(want)
        ? isStrings(x) && x.length
        : typeof x === "string" && x !== ""
    )
      out[k] = x
  }
  return Object.keys(out).length ? out : undefined
}

/** The most variants and inputs a form brings: a bigger one is cut, so
 * a hand-built link cannot stall the page. */
export const FORM_MAX_ROWS = 64

/** Why a form was not read (formRefusalOf): an older format (a v1
 * link or draft, before U26's one step), a newer one, or not a form. */
export type FormRefusal = "older" | "newer" | "unreadable"

/** formRefusalOf names why validateForm refused o: its version is
 * below FORM_VERSION (older), above it (newer), or it is no form. */
export function formRefusalOf(o: unknown): FormRefusal {
  if (isObj(o) && typeof o.v === "number" && Number.isInteger(o.v)) {
    if (o.v < FORM_VERSION) return "older"
    if (o.v > FORM_VERSION) return "newer"
  }
  return "unreadable"
}

/** The playground's line for a share link it did not read, per
 * refusal: an older one names the way out (the CHANGELOG's). */
export const LINK_REFUSED: Record<FormRefusal, string> = {
  older:
    "the link's form is from an older Studio (before the playground's one step picker): re-create it from the experiment — open it and \"clone into the form\"",
  newer: "the link's form was made by a newer Studio: open it there",
  unreadable: "the link's form could not be read: it was cut or damaged",
}

/** The line for a draft (?exp=) the page did not reopen. */
export function draftRefusedWords(
  id: string,
  why: "older" | "missing"
): string {
  return why === "older"
    ? `draft ${id} is from an older Studio (before the playground's one step picker): re-create it from the experiment — open it and "clone into the form"`
    : `draft ${id} is not in this browser — a draft stays in the browser that made it; a share link travels`
}

/** validateForm reads a form back: unknown keys dropped, wrong shapes
 * dropped, a newer version or no variant refused (null). */
export function validateForm(o: unknown): FormState | null {
  if (!isObj(o) || o.v !== FORM_VERSION || !Array.isArray(o.variants))
    return null
  const seen = new Set<string>()
  const variants: FormVariant[] = []
  for (const raw of o.variants.slice(0, FORM_MAX_ROWS)) {
    if (
      !isObj(raw) ||
      typeof raw.key !== "string" ||
      !raw.key ||
      seen.has(raw.key)
    )
      continue
    seen.add(raw.key)
    const v: FormVariant = { key: raw.key }
    for (const k of ["instructions", "model", "thinking", "input"] as const) {
      const s = text(raw[k])
      if (s) v[k] = s
    }
    if (isStrings(raw.tools_off) && raw.tools_off.length)
      v.tools_off = raw.tools_off
    if (isStrings(raw.tools_on) && raw.tools_on.length)
      v.tools_on = raw.tools_on
    const engine = pick(raw.engine, ENGINES)
    if (engine) v.engine = engine
    const se = pick(raw.side_effects, SIDE_EFFECTS)
    if (se) v.side_effects = se
    const th = pick(raw.thread, THREADS)
    if (th) v.thread = th
    const lab = labOf(raw.lab)
    if (lab) v.lab = lab
    variants.push(v)
  }
  if (!variants.length) return null
  const out: FormState = { v: FORM_VERSION, variants }
  for (const k of ["agent", "runtime", "run"] as const) {
    const s = text(o[k])
    if (s) out[k] = s
  }
  const step = ordinal(o.step)
  if (step) out.step = step
  if (Array.isArray(o.edits)) {
    const edits = editsFromHandoff(JSON.stringify(o.edits))
    if (edits.length) out.edits = edits
  }
  if (isObj(o.matrix) && Array.isArray(o.matrix.inputs)) {
    const keys = new Set<string>()
    const inputs: FormInput[] = []
    for (const r of o.matrix.inputs.slice(0, FORM_MAX_ROWS)) {
      if (!isObj(r) || typeof r.key !== "string" || !r.key || keys.has(r.key))
        continue
      keys.add(r.key)
      inputs.push({ key: r.key, text: text(r.text) ?? "" })
    }
    if (inputs.length) {
      out.matrix = { inputs }
      const name = text(o.matrix.name)
      if (name) out.matrix.name = name
      const id = text(o.matrix.id)
      if (validExperimentID(id)) out.matrix.id = id
    }
  }
  return out
}

/**
 * formOfFields is the playground's state as a form: only what differs
 * from the defaults, and a prompt equal to the agent's registered one
 * left out (it is the default) — so an untouched form is small, and the
 * same form always serialises the same.
 */
export function formOfFields(p: {
  variants: (VariantFields & { key: string; toolsOn?: string[] })[]
  /** The agent's registered prompt ("" while unknown). */
  registered: string
  agent?: string
  runtime?: string
  run?: string
  step?: number
  edits?: ReplayEdit[]
  matrix?: {
    name?: string
    id?: string
    inputs: FormInput[]
  }
}): FormState {
  const form: FormState = {
    v: FORM_VERSION,
    variants: p.variants.map((v) => {
      const fv: FormVariant = { key: v.key }
      if (v.instructions && v.instructions !== p.registered)
        fv.instructions = v.instructions
      if (v.toolsOn?.length) fv.tools_on = v.toolsOn
      else if (v.toolsOff.size) fv.tools_off = [...v.toolsOff].sort()
      if (v.model) fv.model = v.model
      if (v.thinking) fv.thinking = v.thinking
      if (v.input) fv.input = v.input
      if (v.engine !== "live") fv.engine = v.engine
      if (v.sideEffects !== "substitute") fv.side_effects = v.sideEffects
      if (v.thread !== "ephemeral") fv.thread = v.thread
      const lab = labOf(v.lab)
      if (lab) fv.lab = lab
      return fv
    }),
  }
  if (p.agent) form.agent = p.agent
  if (p.runtime) form.runtime = p.runtime
  if (p.run) form.run = p.run
  if (p.step) form.step = p.step
  if (p.edits?.length) form.edits = p.edits
  if (p.matrix) {
    form.matrix = {
      inputs: p.matrix.inputs.map((i) => ({ key: i.key, text: i.text })),
    }
    if (p.matrix.name) form.matrix.name = p.matrix.name
    if (validExperimentID(p.matrix.id)) form.matrix.id = p.matrix.id
  }
  return form
}

// ── The share link (#state=) ───────────────────────────────────────

/** The longest #state= value a share link carries, in characters: a
 * longer form stays a draft in this browser (chat apps and some
 * browsers cut long URLs; the fragment itself never reaches a server). */
export const SHARE_CAP = 8000

/** The encoding's tag, its first character: z — deflate-raw
 * (CompressionStream), then base64url; j — the JSON itself, base64url
 * (a browser without CompressionStream). */
export type ShareCodec = "z" | "j"

function b64url(bytes: Uint8Array): string {
  let bin = ""
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
}

function unb64url(s: string): Uint8Array | null {
  if (!/^[A-Za-z0-9_-]*$/.test(s)) return null
  try {
    const bin = atob(
      s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4)
    )
    const out = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
    return out
  } catch {
    return null
  }
}

/** pipe runs bytes through a (de)compression stream; null when the
 * platform has none or the bytes are not its format. */
async function pipe(
  bytes: Uint8Array,
  stream: { readable: ReadableStream; writable: WritableStream } | null
): Promise<Uint8Array | null> {
  if (!stream) return null
  try {
    const body = new Response(bytes as BlobPart).body
    if (!body) return null
    return new Uint8Array(
      await new Response(body.pipeThrough(stream)).arrayBuffer()
    )
  } catch {
    return null
  }
}

function deflater(): CompressionStream | null {
  try {
    return typeof CompressionStream === "function"
      ? new CompressionStream("deflate-raw")
      : null
  } catch {
    return null
  }
}

function inflater(): DecompressionStream | null {
  try {
    return typeof DecompressionStream === "function"
      ? new DecompressionStream("deflate-raw")
      : null
  } catch {
    return null
  }
}

/** encodeForm is a form as a #state= value: deflated when the platform
 * can (codec "z"), else the JSON (codec "j"); tooLong when it passes
 * SHARE_CAP (the link is then not offered). */
export async function encodeForm(
  form: FormState,
  opts: { codec?: ShareCodec } = {}
): Promise<{ value: string; codec: ShareCodec; tooLong: boolean }> {
  const json = new TextEncoder().encode(JSON.stringify(form))
  const z = opts.codec === "j" ? null : await pipe(json, deflater())
  const value = z ? `z${b64url(z)}` : `j${b64url(json)}`
  return { value, codec: z ? "z" : "j", tooLong: value.length > SHARE_CAP }
}

/** The most bytes a #state= value inflates to: a form is a few KiB, so
 * a value that inflates past this (a deflate bomb in a hand-built link)
 * is refused before it is parsed. */
export const DECODE_CAP = 256 * 1024

/** inflateBounded inflates deflate-raw bytes, counting what comes out
 * and cancelling past cap: null when it passes, or the platform has no
 * DecompressionStream, or the bytes are not deflate. */
async function inflateBounded(
  bytes: Uint8Array,
  cap: number
): Promise<Uint8Array | null> {
  const ds = inflater()
  if (!ds) return null
  try {
    const body = new Response(bytes as BlobPart).body
    if (!body) return null
    const reader = (
      body.pipeThrough(ds) as ReadableStream<Uint8Array>
    ).getReader()
    const chunks: Uint8Array[] = []
    let n = 0
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      n += value.byteLength
      if (n > cap) {
        await reader.cancel().catch(() => undefined)
        return null
      }
      chunks.push(value)
    }
    const out = new Uint8Array(n)
    let at = 0
    for (const c of chunks) {
      out.set(c, at)
      at += c.byteLength
    }
    return out
  } catch {
    return null
  }
}

/** decodeForm reads a #state= value back: null when it is not one (a
 * damaged or cut link, an unknown codec, a newer version), when it is
 * longer than a share link carries (SHARE_CAP), or when it inflates
 * past DECODE_CAP. */
export async function decodeForm(value: string): Promise<FormState | null> {
  const out = await decodeFormOutcome(value)
  return "form" in out ? out.form : null
}

/** decodeFormOutcome is decodeForm saying why it read nothing: an
 * older or newer version (FormRefusal), else unreadable. */
export async function decodeFormOutcome(
  value: string
): Promise<{ form: FormState } | { refused: FormRefusal }> {
  const unreadable = { refused: "unreadable" as const }
  if (!value || value.length > SHARE_CAP) return unreadable
  const bytes = unb64url(value.slice(1))
  if (!bytes) return unreadable
  const json =
    value[0] === "z"
      ? await inflateBounded(bytes, DECODE_CAP)
      : value[0] === "j"
        ? bytes
        : null
  if (!json) return unreadable
  let o: unknown
  try {
    o = JSON.parse(new TextDecoder().decode(json))
  } catch {
    return unreadable
  }
  const form = validateForm(o)
  return form ? { form } : { refused: formRefusalOf(o) }
}

/** stateFromHash is the #state= value of a URL fragment, if any. */
export function stateFromHash(hash: string): string | undefined {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash
  if (!raw) return undefined
  return new URLSearchParams(raw).get("state") ?? undefined
}

// ── A matrix cell's command id ─────────────────────────────────────

/** The command id the matrix sends for one cell (command_id, §10.4's
 * at-most-once id, [A-Za-z0-9._:-]{1,128}): `cell.<variant>.<input>.<s>.<nonce>`,
 * the keys base64url, s the side-effect mode's letter. The runtime
 * stamps it on the run (weft.playground.command): the experiment page
 * reads the cell a run belongs to back from it. */
export function cellCommandID(
  variant: string,
  input: string,
  sideEffects: SideEffects,
  nonce = randomNonce()
): string | undefined {
  const enc = (s: string) => b64url(new TextEncoder().encode(s))
  const id = `cell.${enc(variant)}.${enc(input)}.${sideEffects[0]}.${nonce}`
  // A key too long for the id: the server mints one, and the run is
  // placed by order (placeRuns) instead.
  return id.length <= 128 ? id : undefined
}

function randomNonce(): string {
  const b = new Uint8Array(6)
  crypto.getRandomValues(b)
  return b64url(b)
}

/** parseCellCommandID reads a cell's command id back (null: not one). */
export function parseCellCommandID(
  id: string | undefined
): { variant: string; input: string; sideEffects: SideEffects } | null {
  if (!id) return null
  const parts = id.split(".")
  if (parts.length !== 5 || parts[0] !== "cell") return null
  const dec = (s: string) => {
    const b = unb64url(s)
    return b ? new TextDecoder().decode(b) : ""
  }
  const variant = dec(parts[1])
  const input = dec(parts[2])
  const sideEffects = SIDE_EFFECTS.find((s) => s[0] === parts[3])
  if (!variant || !input || !sideEffects) return null
  return { variant, input, sideEffects }
}

/** The run row's command id (weft.playground.command, in meta). */
export const commandOf = (r: Pick<RunRow, "meta">): string | undefined =>
  r.meta["weft.playground.command"]

/** placeRuns maps an experiment's runs to its cells (variant × input),
 * oldest first: by the command id each carries, else — when no run of
 * it carries one and there are exactly variants × inputs runs (a
 * pre-F5 matrix) — by the matrix's issue order, variant-major. byOrder
 * says the second rule placed them; loose is every run left over. */
export function placeRuns(
  exp: Pick<ExperimentRow, "variants" | "inputs" | "runs">
): {
  cells: Map<string, RunRow[]>
  loose: RunRow[]
  byOrder: boolean
} {
  const runs = exp.runs ?? []
  const vs = exp.variants.map((v) => v.key)
  const is = exp.inputs.map((i) => i.key)
  const cells = new Map<string, RunRow[]>()
  const add = (k: string, r: RunRow) => {
    const l = cells.get(k)
    if (l) l.push(r)
    else cells.set(k, [r])
  }
  const loose: RunRow[] = []
  let addressed = 0
  for (const r of runs) {
    const c = parseCellCommandID(commandOf(r))
    if (c && vs.includes(c.variant) && is.includes(c.input)) {
      add(cellKey(c.variant, c.input), r)
      addressed++
    } else loose.push(r)
  }
  if (
    addressed === 0 &&
    runs.length > 0 &&
    runs.length === vs.length * is.length
  ) {
    let n = 0
    for (const v of vs) for (const i of is) add(cellKey(v, i), runs[n++])
    return { cells, loose: [], byOrder: true }
  }
  return { cells, loose, byOrder: false }
}

/** A cell's key: variant × input (the playground's own). */
export const cellKey = (variant: string, input: string): string =>
  `${variant}\u0000${input}`

// ── A saved experiment as the form ─────────────────────────────────

/** variantFromOverrides is the form a variant's overrides object reads
 * back as (overridesOf's inverse, lib/experiment-body.ts): the prompt,
 * the tools left on, model, thinking and the option lab's knobs. */
export function variantFromOverrides(key: string, o: unknown): FormVariant {
  const v: FormVariant = { key }
  if (!isObj(o)) return v
  if (typeof o.instructions === "string" && o.instructions)
    v.instructions = o.instructions
  if (isStrings(o.tools_enabled) && o.tools_enabled.length)
    v.tools_on = o.tools_enabled
  if (typeof o.model === "string" && o.model) v.model = o.model
  if (typeof o.thinking === "string" && o.thinking) v.thinking = o.thinking
  const lab: Partial<LabFields> = {}
  const num = (x: unknown) =>
    typeof x === "number" && Number.isFinite(x) ? String(x) : undefined
  if (isObj(o.options))
    for (const k of ["max_steps", "parallelism", "temperature"] as const) {
      const s = num(o.options[k])
      if (s !== undefined) lab[k] = s
    }
  if (isObj(o.params)) {
    for (const k of ["top_p", "max_tokens", "seed"] as const) {
      const s = num(o.params[k])
      if (s !== undefined) lab[k] = s
    }
    if (isStrings(o.params.stop) && o.params.stop.length)
      lab.stop = o.params.stop.join("\n")
  }
  if (isObj(o.tool_choice)) {
    const mode = o.tool_choice.mode as ToolChoiceWire["mode"]
    if (["auto", "any", "none", "named"].includes(mode)) {
      lab.tool_choice = mode
      if (mode === "named" && typeof o.tool_choice.name === "string")
        lab.tool_choice_name = o.tool_choice.name
    }
  }
  if (isStrings(o.park_on) && o.park_on.length) lab.park_on = o.park_on
  if (isStrings(o.only_tools) && o.only_tools.length)
    lab.only_tools = o.only_tools
  if (Object.keys(lab).length) v.lab = lab
  return v
}

/** What a variant's runs say about its modes (see the header). */
function modesOf(runs: RunRow[]): Pick<
  FormVariant,
  "engine" | "side_effects" | "thread"
> & {
  from_step?: number
} {
  const out: Pick<FormVariant, "engine" | "side_effects" | "thread"> & {
    from_step?: number
  } = {}
  const r = runs.at(-1)
  if (!r) return out
  if (r.model.provider === "weft/runtime" && r.model.name === "scripted")
    out.engine = "scripted"
  if (r.session_id) out.thread = "fork"
  const c = parseCellCommandID(commandOf(r))
  if (c && c.sideEffects !== "substitute") out.side_effects = c.sideEffects
  const src = forkSource(r)
  if (src) out.from_step = src.fromStep
  return out
}

/** editsFromWire reads §5.1's transcript_edits (lib/edits.ts's
 * wireEdits — call_id, tool_result or content, args, index) back into
 * the form's edits; editsFromHandoff then keeps the well-formed ones. */
function editsFromWire(list: unknown[]): ReplayEdit[] {
  return list.filter(isObj).map((w) => {
    const e: ReplayEdit = { step: typeof w.step === "number" ? w.step : -1 }
    if (typeof w.kind === "string") e.kind = w.kind as ReplayEdit["kind"]
    if (typeof w.call_id === "string") e.callID = w.call_id
    if (w.kind === "tool_result") {
      const r = text(w.tool_result) ?? text(w.content)
      if (r !== undefined) e.toolResult = r
    } else if (typeof w.content === "string") e.content = w.content
    if (isObj(w.args)) e.args = w.args
    if (typeof w.index === "number") e.index = w.index
    return e
  })
}

/**
 * formFromExperiment is a saved experiment as the playground's form:
 * its variants (overrides read back), its inputs as the matrix's rows,
 * and the command it saved (F5.1: engine, side_effects, thread — the
 * variant's own over the experiment's — from_step, source_run_id,
 * transcript_edits), each field falling back, when the definition
 * predates it, to what the runs say (modesOf, newest run); its name as
 * the matrix's and its id kept — so Run matrix re-runs it under the
 * same id.
 */
export function formFromExperiment(exp: ExperimentRow): FormState {
  const { cells } = placeRuns(exp)
  let fromStep: number | undefined
  const variants = exp.variants.map((ev) => {
    const runs = exp.inputs.flatMap(
      (i) => cells.get(cellKey(ev.key, i.key)) ?? []
    )
    runs.sort((a, b) => Date.parse(a.started) - Date.parse(b.started))
    const { from_step, ...guessed } = modesOf(runs)
    if (from_step !== undefined && fromStep === undefined) fromStep = from_step
    const v: FormVariant = { ...variantFromOverrides(ev.key, ev.overrides) }
    const saved = {
      engine: pick(ev.engine ?? exp.engine, ENGINES),
      side_effects: pick(ev.side_effects ?? exp.side_effects, SIDE_EFFECTS),
      thread: pick(ev.thread ?? exp.thread, THREADS),
    }
    // A saved default is left out of the form (absent is the default).
    const engine = saved.engine ?? guessed.engine
    if (engine && engine !== "live") v.engine = engine
    const se = saved.side_effects ?? guessed.side_effects
    if (se && se !== "substitute") v.side_effects = se
    const th = saved.thread ?? guessed.thread
    if (th && th !== "ephemeral") v.thread = th
    return v
  })
  const savedStep = ordinal(exp.from_step)
  if (savedStep !== undefined) fromStep = savedStep
  const source =
    exp.source_run_id || exp.inputs.find((i) => i.source_run_id)?.source_run_id
  const form: FormState = {
    v: FORM_VERSION,
    agent: exp.agent || undefined,
    variants: variants.length ? variants : [{ key: "A" }],
    matrix: {
      name: exp.name || exp.id,
      inputs: exp.inputs.map((i) => ({ key: i.key, text: i.text ?? "" })),
    },
  }
  if (validExperimentID(exp.id)) form.matrix!.id = exp.id
  if (source) form.run = source
  if (Array.isArray(exp.transcript_edits) && exp.transcript_edits.length) {
    const edits = editsFromHandoff(
      JSON.stringify(editsFromWire(exp.transcript_edits))
    )
    if (edits.length) form.edits = edits
  }
  if (fromStep) form.step = fromStep
  if (!form.matrix!.inputs.length)
    form.matrix!.inputs = [{ key: "1", text: "" }]
  return validateForm(form) ?? form
}

/** cellBody is the command that re-runs one cell of a saved experiment
 * alone: the variant's saved overrides as they were sent, the input
 * row's text and source, the modes read back (formFromExperiment), and
 * the same experiment id — POST /api/playground/runs takes any id, so
 * the run joins the experiment's history (GET /api/experiments/{id}
 * lists its runs by weft.experiment.id). */
export function cellBody(
  exp: ExperimentRow,
  form: FormState,
  variant: string,
  input: string,
  target: { runtime: string; publicID?: string }
): PlaygroundRunBody {
  const ev = exp.variants.find((v) => v.key === variant)
  const ei = exp.inputs.find((i) => i.key === input)
  if (!ev || !ei)
    throw new Error(`no cell ${variant}×${input} in experiment ${exp.id}`)
  const fv = form.variants.find((v) => v.key === variant)
  const fromStep = form.step ?? 0
  const sideEffects = fv?.side_effects ?? "substitute"
  const body: PlaygroundRunBody = {
    runtime: target.runtime,
    agent: exp.agent,
    source: ei.source_run_id
      ? { run_id: ei.source_run_id, from_step: fromStep }
      : null,
    overrides: isObj(ev.overrides) ? ev.overrides : {},
    engine: fv?.engine ?? "live",
    side_effects: sideEffects,
    thread: fv?.thread ?? "ephemeral",
    experiment_id: exp.id,
  }
  // At a later step the cell continues the kept prefix: no input (the
  // matrix's own rule, buildRunBody).
  if (ei.text && fromStep === 0) body.input = ei.text
  // The page's transcript edits travel with every cell past step 0
  // (U26: one step, one set of edits — the matrix's rule is Run's).
  if (fromStep > 0 && form.edits?.length)
    body.transcript_edits = wireEdits(form.edits)
  if (!body.source && !body.input)
    throw new Error(`cell ${variant}×${input} has no input and no source run`)
  if (target.publicID) body.public_id = target.publicID
  const id = cellCommandID(variant, input, sideEffects)
  if (id) body.command_id = id
  return body
}
