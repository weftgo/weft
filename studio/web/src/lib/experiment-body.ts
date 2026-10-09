// The playground's command (WEFT-PLAYGROUND §5.1, §10.4): who it goes
// to (pickTarget), the body one experiment becomes (buildRunBody —
// only what changed), and the kept prefix's transcript edits
// (editFieldsOf, wireEdits). Shared by the playground page and the run
// page's replay drawer (plan F1): one form, one body, one wire.
import type { AgentDefaults, AgentView, Message, PlaygroundRunBody, RuntimeView, ToolChoiceWire } from "@/lib/api"
import { placeBatches } from "@/lib/events"
import { endsInAnsweredCalls, maxFromStep, transcriptStepCount } from "@/lib/replay"
import type { TranscriptBatch } from "@/lib/events"
import { kindOf, wireEdits } from "@/lib/edits"
import type { ReplayEdit } from "@/lib/edits"

export type Engine = "live" | "scripted"
export type SideEffects = "substitute" | "park" | "allow"
export type ThreadMode = "ephemeral" | "fork"

/** One experiment's knobs (a playground variant, the replay drawer's
 * form): the overrides, the input and the modes. */
export interface VariantFields {
  instructions: string
  toolsOff: Set<string>
  model: string
  thinking: string
  input: string
  engine: Engine
  sideEffects: SideEffects
  /** §5.4's thread mode (review fix 4a): ephemeral — an experiment,
   * never a turn — or fork, a new session with lineage whose next
   * turn is the input. */
  thread: ThreadMode
  /** The option lab's knobs (plan F3); absent is every knob at the
   * agent's default. */
  lab?: LabFields
}

/** toolsOffFor turns the hand-off's tools= (the names left ON) into
 * the agent's turned-off set. Names the agent does not have are
 * ignored; no tools= means everything stays on. */
export function toolsOffFor(tools: string | undefined, agent: AgentView): Set<string> {
  if (!tools) return new Set()
  const on = new Set(tools.split(",").map((t) => t.trim()).filter(Boolean))
  return new Set(agent.tools.map((t) => t.name).filter((n) => !on.has(n)))
}

/**
 * pickTarget names the runtime and agent a command goes to. The agent
 * is the one asked for — the hand-off's agent=, else the source run's
 * own — on a runtime that registers it; a runtime named explicitly is
 * honoured when it exists. Only when nothing was asked for (or nobody
 * registers it) does the first registered agent stand in, and
 * `mismatch` then names the agent that could not be found, so the page
 * says so instead of silently experimenting on a different agent.
 */
export function pickTarget(
  runtimes: RuntimeView[],
  want: { runtime?: string; agent?: string }
): { runtime?: RuntimeView; agent?: AgentView; mismatch?: string } {
  const named = want.runtime
    ? runtimes.find((r) => r.id === want.runtime)
    : undefined
  const has = (r: RuntimeView) => r.agents.some((a) => a.name === want.agent)
  const runtime =
    named && (!want.agent || has(named))
      ? named
      : ((want.agent ? runtimes.find(has) : undefined) ??
        named ??
        runtimes.find((r) => r.agents.length > 0))
  if (!runtime) return {}
  const agent = runtime.agents.find((a) => a.name === want.agent)
  if (agent) return { runtime, agent }
  return {
    runtime,
    agent: runtime.agents.at(0),
    mismatch: want.agent || undefined,
  }
}

/** The overrides a variant applies to the registered agent — only
 * what changed (§10.1): an unchanged prompt is not an override, and
 * no tool turned off sends no tools_enabled. */
export function overridesOf(
  variant: Pick<VariantFields, "instructions" | "toolsOff" | "model" | "thinking" | "lab">,
  agent: AgentView
): NonNullable<PlaygroundRunBody["overrides"]> {
  const overrides: NonNullable<PlaygroundRunBody["overrides"]> = {}
  if (variant.instructions && variant.instructions !== (agent.instructions ?? ""))
    overrides.instructions = variant.instructions
  const names = agent.tools.map((t) => t.name)
  const enabled = names.filter((n) => !variant.toolsOff.has(n))
  if (enabled.length < names.length) {
    // The wire cannot say "no tools": an empty tools_enabled reads as
    // "not overridden" on the other side and the run would get every
    // tool back. Refuse here rather than run the opposite experiment.
    if (enabled.length === 0)
      throw new Error(
        "at least one tool must stay on — the command cannot express an empty tool set (it would run with every tool)"
      )
    overrides.tools_enabled = enabled
  }
  if (variant.model) overrides.model = variant.model
  if (variant.thinking) overrides.thinking = variant.thinking
  const lab = labOverrides(variant.lab, agent, overrides.tools_enabled ?? [])
  if (lab.problems.length) throw new Error(lab.problems[0].message)
  return { ...overrides, ...lab.overrides }
}

/**
 * buildRunBody is §5.1's command for one variant. Throws with the
 * reason when the command would be refused or would run something
 * other than what the form shows (no input and no source; fork without
 * its input; every tool turned off).
 */
export function buildRunBody(opts: {
  runtime: string
  agent: AgentView
  variant: VariantFields
  sourceRunID: string
  fromStep: number
  /** The input for this command (the variant's, or a matrix row's). */
  input: string
  edits?: EditDraft[]
  /** The source run's public id: the experiment carries it, so the
   * page that owns the conversation sees it too (S4.6). */
  publicID?: string
  experimentID?: string
}): PlaygroundRunBody {
  const { variant, sourceRunID, fromStep } = opts
  const input = fromStep === 0 ? opts.input : ""
  if (!sourceRunID && !input)
    throw new Error("a run needs an input, or a source run to take the turn from")
  if (variant.thread === "fork" && !(sourceRunID && input && fromStep === 0))
    throw new Error(
      "fork continues the conversation in a new session: it needs a source run, an input, and step 0"
    )
  const body: PlaygroundRunBody = {
    runtime: opts.runtime,
    agent: opts.agent.name,
    source: sourceRunID ? { run_id: sourceRunID, from_step: fromStep } : null,
    overrides: overridesOf(variant, opts.agent),
    engine: variant.engine,
    side_effects: variant.sideEffects,
    thread: variant.thread,
  }
  if (input) body.input = input
  if (fromStep > 0 && opts.edits?.length) body.transcript_edits = wireEdits(opts.edits)
  if (opts.experimentID) body.experiment_id = opts.experimentID
  if (opts.publicID) body.public_id = opts.publicID
  return body
}

/** One transcript edit draft (§5.1's wire shape — review fix 4b; plan
 * F2's five kinds, lib/edits.ts): a patched tool result, a rewritten
 * call-free reply, a user message, a call's arguments, an insert. */
export type EditDraft = ReplayEdit

/** One editable field of a kept step, derived from the source
 * transcript. */
export interface EditField {
  step: number
  callID?: string
  /** The tool's name, or "reply". */
  name: string
  placeholder: string
}

/** ownMessages is the run's own messages (the input record left out),
 * each tagged with the step it counts toward: the batch's stored step
 * when every batch carries one and the input flag, else numbered by
 * order — each assistant message opens the next step, what precedes
 * the first is step 0's (editFieldsOf's rule, below). */
export function ownMessages(batches: TranscriptBatch[]): { step: number; m: Message }[] {
  const placed = placeBatches(batches)
  const list = Array.isArray(batches) ? batches : []
  const stored =
    list.length > 0 &&
    list.every(
      (b) =>
        typeof b.step === "number" && b.step >= 0 && typeof b.input === "boolean"
    )
  const tagged: { step: number; m: Message }[] = []
  for (const [i, b] of placed.entries()) {
    const flag = list[i]?.input
    const input = typeof flag === "boolean" ? flag : b.input
    if (input) continue
    for (const m of b.messages)
      tagged.push({ step: stored ? (list[i].step as number) : -1, m })
  }
  if (!stored) {
    let at = -1
    for (const t of tagged) {
      if (t.m.role === "assistant") at++
      t.step = Math.max(at, 0)
    }
  }
  return tagged
}

/** replayBounds is what the server will accept as from_step for this
 * source transcript (studio/edits.go): its step count, whether the kept
 * prefix ends in answered calls, and the highest from_step. */
export function replayBounds(batches: TranscriptBatch[]): {
  stepCount: number
  answeredCalls: boolean
  /** The last assistant message made at least one tool call (with
   * answeredCalls false: some are unanswered; false: a call-free
   * reply, or no step at all). */
  lastCalls: boolean
  max: number
} {
  const own = ownMessages(batches)
  const assistants = own.filter((t) => t.m.role === "assistant")
  const stepCount = transcriptStepCount(assistants.map((t) => t.step))
  const answeredCalls = endsInAnsweredCalls(own.map((t) => t.m))
  const lastCalls = assistants.at(-1)?.m.content.some((p) => p.type === "tool_call") ?? false
  return { stepCount, answeredCalls, lastCalls, max: maxFromStep(stepCount, answeredCalls) }
}

/** One step of a source run as the step picker lists it. */
export interface SourceStep {
  /** The step's place in the list (display only: from_step is the
   * ordinal). */
  position: number
  /** The step ordinal the transcript stamps (weft.step.index; by
   * order only for records that carry none) — source.from_step's
   * number. */
  ordinal: number
  /** The tools the step's reply called, in order. */
  tools: string[]
  /** A tool result of the step was an error. */
  toolError: boolean
  /** A run-scope compaction view sits at this step (the model saw a
   * compacted prefix there). */
  compacted: boolean
}

/** sourceSteps lists a source run's own steps in order — the
 * playground's step picker and the replay drawer read it. compactedAt
 * holds the ordinals that carry a run-scope compaction view (the run
 * document's compactions). */
export function sourceSteps(
  batches: TranscriptBatch[],
  compactedAt: ReadonlySet<number> = new Set()
): SourceStep[] {
  const out: SourceStep[] = []
  const byOrdinal = new Map<number, SourceStep>()
  for (const { step, m } of ownMessages(batches)) {
    if (step < 0) continue
    let st = byOrdinal.get(step)
    if (!st) {
      if (m.role !== "assistant") continue
      st = { position: out.length, ordinal: step, tools: [], toolError: false, compacted: compactedAt.has(step) }
      byOrdinal.set(step, st)
      out.push(st)
    }
    for (const p of m.content) {
      if (m.role === "assistant" && p.type === "tool_call") st.tools.push(p.name)
      if (m.role === "tool" && p.type === "tool_result" && p.is_error) st.toolError = true
    }
  }
  return out
}

/** editFieldsOf derives the kept steps' editable fields from the
 * source transcript (steps 0..fromStep−1): every tool result the
 * prefix holds, and each step's reply when that step carried no tool
 * calls (a reply rewrite may not drop a step's calls — D2/D3). The
 * panel's per-step fields are the shape (element.ts's drawer).
 *
 * Each message counts toward the step its batch joined — the stored
 * step the API carries (ADR 0028 §8) — and the input record (the
 * conversation the run was fed) is not the run's own steps. The split
 * is the row's input flag; only when a row carries no stored step (-1,
 * or an older Studio without the flag) are the steps numbered by order:
 * each assistant message opens the next, what precedes the first is
 * step 0's. Studio's runSteps and the runtime's orderSteps apply the
 * same rule, so a field offered here is one both accept. */
export function editFieldsOf(
  batches: TranscriptBatch[],
  fromStep: number
): EditField[] {
  const tagged = ownMessages(batches)
  const fields: EditField[] = []
  for (const { step, m } of tagged) {
    if (step < 0 || step >= fromStep) continue
    if (m.role === "assistant") {
      let text = ""
      let hadCalls = false
      for (const p of m.content) {
        if (p.type === "tool_call") hadCalls = true
        if (p.type === "text" && p.text) text += p.text
      }
      if (text && !hadCalls) fields.push({ step, name: "reply", placeholder: text })
    } else if (m.role === "tool") {
      for (const p of m.content) {
        if (p.type === "tool_result")
          fields.push({
            step,
            callID: p.call_id,
            name: p.name || p.call_id,
            placeholder: typeof p.content === "string" ? p.content : "",
          })
      }
    }
  }
  return fields
}

/** unmatchedDrafts is the drafts no editable field of the kept prefix
 * (steps < fromStep) matches — sent, the server would refuse them
 * (400). fields null (not read, or unreadable) leaves every draft
 * unchecked: all of them are returned, so the caller holds Run rather
 * than send what it cannot check. None at fromStep 0 (no edit is sent). */
export function unmatchedDrafts(
  drafts: EditDraft[],
  fields: EditField[] | null,
  fromStep: number
): EditDraft[] {
  if (fromStep <= 0) return []
  if (fields === null) return drafts
  const keys = new Set(fields.filter((f) => f.step < fromStep).map((f) => `${f.step}\u0000${f.callID ?? ""}`))
  // The Story's editor (plan F2) names user messages, arguments and
  // boundaries it read from the transcript itself: those are checked
  // against the kept range only (the server checks the rest).
  return drafts.filter((d) => {
    const k = kindOf(d)
    if (k === "insert") return d.step > fromStep
    if (k === "user" || k === "tool_args") return d.step >= fromStep
    return !keys.has(`${d.step}\u0000${d.callID ?? ""}`)
  })
}

/** wireEdits maps the drafts to §5.1's wire shape, kind always sent —
 * lib/edits.ts's, the mapping the panel's buildRunBody makes too. */
export { wireEdits }

// ── The option lab (plan F3) ─────────────────────────────────────
// Every run option the core exposes as narrowing or neutral, as the
// form holds it: a knob left empty (or equal to the agent's default)
// is no override — the wire's "absent keeps the agent's value". The
// rules mirror the server's by name (studio/playground.go's
// checkRegistered, weft/runtime's validate); the server stays the
// authority and its 400 is shown verbatim. One function for both
// surfaces: the panel's drawer builds its overrides here too, so the
// two post byte-identical JSON for the same choices.

/** The option lab's draft: every knob as text ("" = no override), the
 * tool sets as names ([] = no override). */
export interface LabFields {
  max_steps: string
  parallelism: string
  temperature: string
  top_p: string
  max_tokens: string
  seed: string
  /** Stop sequences, one per line. */
  stop: string
  /** "" keeps the agent's tool_choice. */
  tool_choice: "" | ToolChoiceWire["mode"]
  tool_choice_name: string
  park_on: string[]
  only_tools: string[]
}

export const emptyLab = (): LabFields => ({
  max_steps: "",
  parallelism: "",
  temperature: "",
  top_p: "",
  max_tokens: "",
  seed: "",
  stop: "",
  tool_choice: "",
  tool_choice_name: "",
  park_on: [],
  only_tools: [],
})

/** Each knob's name on both surfaces' forms. */
export const LAB_LABELS: Record<keyof LabFields, string> = {
  max_steps: "max steps",
  parallelism: "parallelism",
  temperature: "temperature",
  top_p: "top_p",
  max_tokens: "max tokens",
  seed: "seed",
  stop: "stop (one per line)",
  tool_choice: "tool choice",
  tool_choice_name: "tool",
  park_on: "park on",
  only_tools: "only tools",
}

/** The numeric knobs, in wire order: options first, then params. */
export const LAB_NUMBERS = ["max_steps", "parallelism", "temperature", "top_p", "max_tokens", "seed"] as const
export type LabNumber = (typeof LAB_NUMBERS)[number]

/** The knobs the server refuses on a registration that predates the
 * option lab, and its sentence. */
export const LAB_NEW: (keyof LabFields)[] = ["top_p", "max_tokens", "seed", "stop", "tool_choice", "park_on", "only_tools"]
export const LAB_PREDATES =
  "runtime predates the option lab: upgrade weft/runtime to use params, tool_choice, park_on, only_tools"

/** One refusal the form found, on the knob it names. */
export interface LabProblem {
  field: keyof LabFields | "lab"
  message: string
}

/** labLacksDefaults: the registration carries no option-lab defaults
 * — the server's own test (tool_choice mode unset). /api/runtimes
 * omits them only when Studio itself predates the field; a current
 * Studio fills an old runtime's in (its caps, tool_choice auto), and
 * then the server's 400 is the one that says so, shown verbatim. */
export function labLacksDefaults(agent: Pick<AgentView, "defaults">): boolean {
  return !agent.defaults?.tool_choice.mode
}

/** labDefault is the agent's default for a knob as the form shows it,
 * greyed: "" when the registration reports none. */
export function labDefault(key: keyof LabFields, d: AgentDefaults | undefined): string {
  if (!d) return ""
  switch (key) {
    case "stop":
      return (d.stop ?? []).join("\n")
    case "tool_choice":
      return d.tool_choice.mode
    case "tool_choice_name":
      return d.tool_choice.name ?? ""
    case "park_on":
    case "only_tools":
      return ""
  }
  const v = (d as unknown as Record<string, unknown>)[key]
  // A 0 cap is no bound (the server refuses a raise only over a cap > 0).
  if ((key === "max_steps" || key === "parallelism") && !(typeof v === "number" && v > 0)) return ""
  return typeof v === "number" ? String(v) : ""
}

/** stopsOf splits the stop field: one sequence per line, empty lines
 * dropped. */
const stopsOf = (s: string) => s.split("\n").filter((x) => x !== "")

/** inOrder keeps the agent's tool order (both surfaces send the same
 * array whatever the clicks' order). */
const inOrder = (names: string[], tools: string[]) => tools.filter((t) => names.includes(t))

/**
 * labOverrides turns the lab into the command's typed fields and the
 * problems that would make the server refuse it. enabled is the
 * tools_enabled the command sends ([] = none). A knob equal to the
 * agent's default is no override (§10.1: only what changed).
 */
export function labOverrides(
  lab: LabFields | undefined,
  agent: Pick<AgentView, "name" | "defaults"> & { tools: { name: string }[] },
  enabled: string[]
): { overrides: Partial<NonNullable<PlaygroundRunBody["overrides"]>>; problems: LabProblem[] } {
  const out: Partial<NonNullable<PlaygroundRunBody["overrides"]>> = {}
  const problems: LabProblem[] = []
  const d = agent.defaults
  const tools = agent.tools.map((t) => t.name)
  const no = (field: keyof LabFields, message: string) => problems.push({ field, message })
  if (!lab) lab = emptyLab()
  const options: Record<string, number> = {}
  const params: NonNullable<NonNullable<PlaygroundRunBody["overrides"]>["params"]> = {}
  for (const key of LAB_NUMBERS) {
    const s = lab[key].trim()
    if (!s) continue
    const v = Number(s)
    const def = labDefault(key, d)
    if (!Number.isFinite(v)) {
      no(key, `${key} must be a number`)
      continue
    }
    if (def !== "" && Number(def) === v) continue
    const whole = Number.isSafeInteger(v)
    if (key === "max_steps" || key === "parallelism") {
      // checkNeutral's range: a whole number from 1, at most 1e6.
      if (!whole || v < 1 || v > 1e6) no(key, `${key} must be a whole number from 1`)
      else if (def !== "" && v > Number(def)) no(key, `${key} may only lower the agent's cap`)
      options[key] = v
    } else if (key === "temperature") {
      if (v < 0 || v > 2) no(key, "temperature must be between 0 and 2")
      options[key] = v
    } else if (key === "top_p") {
      if (v < 0 || v > 1) no(key, "top_p must be between 0 and 1")
      params.top_p = v
    } else if (key === "max_tokens") {
      if (!whole) no(key, "max_tokens must be a whole number")
      else if (v <= 0) no(key, "max_tokens must be positive")
      params.max_tokens = v
    } else {
      if (!whole) no(key, "seed must be a whole number")
      params.seed = v
    }
  }
  const stop = stopsOf(lab.stop)
  if (stop.length && stop.join("\n") !== labDefault("stop", d)) {
    if (stop.length > 4) no("stop", `stop takes at most 4 sequences, got ${stop.length}`)
    params.stop = stop
  }
  // params in the wire's order, whatever order the knobs were read.
  const p: typeof params = {}
  if (params.top_p !== undefined) p.top_p = params.top_p
  if (params.max_tokens !== undefined) p.max_tokens = params.max_tokens
  if (params.stop) p.stop = params.stop
  if (params.seed !== undefined) p.seed = params.seed
  // studio/playground.go toolOverrides' words, once per list.
  for (const k of ["only_tools", "park_on"] as const)
    for (const name of new Set(lab[k])) if (!tools.includes(name)) no(k, `tool ${name} in ${k} is not in agent ${agent.name}'s manifest`)
  let only = inOrder(lab.only_tools, tools)
  if (only.length === tools.length) only = []
  for (const name of only)
    if (enabled.length && !enabled.includes(name))
      no("only_tools", `only_tools may only narrow tools_enabled: tool ${name} is not enabled`)
  const park = inOrder(lab.park_on, tools)
  const leavesOn = (name: string) => (only.length ? only.includes(name) : enabled.length ? enabled.includes(name) : true)
  let tc: ToolChoiceWire | undefined
  if (lab.tool_choice) {
    tc = lab.tool_choice === "named" ? { mode: "named", name: lab.tool_choice_name } : { mode: lab.tool_choice }
    if (d && tc.mode === d.tool_choice.mode && (tc.mode !== "named" || tc.name === d.tool_choice.name)) tc = undefined
  }
  if (tc?.mode === "named") {
    const n = tc.name ?? ""
    if (!n) no("tool_choice", "tool_choice named needs a tool name")
    else if (!tools.includes(n)) no("tool_choice", `tool ${n} in tool_choice is not in agent ${agent.name}'s manifest`)
    else if (!leavesOn(n)) no("tool_choice", `tool_choice names ${n}, which this command turns off`)
    else if (park.includes(n)) no("tool_choice", `tool_choice names ${n}, which park_on parks: every forced call would park`)
  } else if (!tc && d?.tool_choice.mode === "named" && d.tool_choice.name && !leavesOn(d.tool_choice.name))
    no("tool_choice", `the agent's default tool_choice names ${d.tool_choice.name}, which this command turns off; send tool_choice`)
  if (Object.keys(options).length) out.options = options
  if (Object.keys(p).length) out.params = p
  if (tc) out.tool_choice = tc
  if (park.length) out.park_on = park
  if (only.length) out.only_tools = only
  if (labLacksDefaults(agent) && (out.params || out.tool_choice || out.park_on || out.only_tools))
    problems.unshift({ field: "lab", message: LAB_PREDATES })
  return { overrides: out, problems }
}

/** labProblems is a variant's option-lab refusals against its agent
 * (the tools it leaves on as the tools_enabled it sends): what holds
 * Run on Studio's surfaces, as the panel's labOf does in the drawer. */
export function labProblems(variant: Pick<VariantFields, "lab" | "toolsOff">, agent: AgentView): LabProblem[] {
  const names = agent.tools.map((t) => t.name)
  const enabled = names.filter((n) => !variant.toolsOff.has(n))
  return labOverrides(variant.lab, agent, enabled.length < names.length ? enabled : []).problems
}

/** labOn: the knob is an override (it differs from the agent's
 * default and would be sent) — the form draws it in colour. */
export function labOn(lab: LabFields | undefined, key: keyof LabFields, d: AgentDefaults | undefined): boolean {
  if (!lab) return false
  const v = lab[key]
  if (Array.isArray(v)) return v.length > 0
  if (key === "stop") return stopsOf(v).length > 0 && stopsOf(v).join("\n") !== labDefault(key, d)
  if (key === "tool_choice")
    return v !== "" && !(v === labDefault(key, d) && (v !== "named" || lab.tool_choice_name === labDefault("tool_choice_name", d)))
  if (key === "tool_choice_name") return false
  const def = labDefault(key, d)
  return v.trim() !== "" && !(def !== "" && Number(def) === Number(v))
}
