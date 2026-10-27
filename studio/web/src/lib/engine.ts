// The engine a replay runs on, chosen per command (U17; UX-C-01,
// UX-D-04, UX-C-05, UX-C-08): `scripted` when it can answer — a source
// run, nothing that changes the replay key (an edit, a prompt or model
// override, a new input, a fork, tools / thinking / tool choice), a
// from_step the source answered, and the preview's key check (when the
// server sends one) not a miss — else `live`. The scripted option is
// never taken away silently: when it cannot answer it is disabled with
// its reason, and a user who picked it is held with that reason inline.
// Live carries what it will call and an estimate: the source's
// recorded usage from from_step to its last step at its model's price
// (each step route's cost block, studio/pricing.go — the one price
// table), or "cost unknown" with the reason, never $0 for an unknown.
// The facts come from the command itself (factsOf): what the body
// sends is what changes the key, so an override the body leaves out —
// a prompt, model or tool choice equal to the agent's own — is no
// change. Plain TypeScript: Studio's replay drawer, its playground and
// the panel's drawer read the same choice and the same words
// (parity.test.ts).
import type {
  AgentDefaults,
  CostFields,
  ModelInfo,
  PlaygroundRunBody,
  RunRow,
  Usage,
} from "./api"
import { costView, formatUSD, isScripted } from "./cost"
import { tokens } from "./format"
import type { PreviewDoc } from "./preview"

export type Engine = "live" | "scripted"

/** What the rule reads. Every field is the drawer's own state, the
 * source's record or the preview's answer. */
export interface EngineFacts {
  /** A source run to take the turn from. */
  source: boolean
  /** The transcript edits that change something (realEdits). */
  edits: number
  /** The system prompt differs from the registered one. */
  prompt: boolean
  /** The model override ("" none). */
  model: string
  /** A new input at step 0 (one equal to the source's is none). */
  input: boolean
  fork: boolean
  /** The other knobs the replay key holds, set: "tools", "thinking",
   * "tool choice" (runtime/scripted.go's key: messages, tool names,
   * thinking, tool choice). */
  keyed: string[]
  fromStep: number
  /** The source's recorded step count; undefined while unknown. */
  steps?: number
  /** The preview's scripted key check (U18.1), when the server sends
   * one: absent is unknown and disables nothing. */
  keyMatch?: {
    ok: boolean
    because?: string[]
    step?: number | null
    /** The server's one sentence for the first miss. */
    message?: string
  } | null
  /** The agent's own model as the source recorded it
   * ("provider/name"). */
  ownModel?: string
  /** The source's step-route docs from from_step to its last step
   * (index 0 is from_step; null: not read), or null when none is. */
  costs?: (StepCost | null)[] | null
  /** Why there is no cost doc (not read yet, no step route, …). */
  costWhy?: string
}

/** One step's cost block and usage (the step route's). */
export type StepCost = CostFields & { usage?: Usage }

export interface EngineChoice {
  /** The engine the command runs on unless the user picks the other. */
  default: Engine
  scripted: { enabled: boolean; reason: string }
  live: {
    model: string
    /** "~1.2k tok ≈ $0.0004 for steps 1–2", or "cost unknown: <why>". */
    estimate: string
    /** Run's figure: "≈ $0.0004", "≥ $0.0004" when later steps are not
     * read, else "". */
    usd: string
  }
}

/** engineChoice is the rule (see the header). */
export function engineChoice(f: EngineFacts): EngineChoice {
  const reason = scriptedBlock(f)
  return {
    default: reason ? "live" : "scripted",
    scripted: { enabled: !reason, reason },
    live: {
      model: f.model || f.ownModel || ENGINE_WORDS.ownModel,
      ...liveEstimate(f),
    },
  }
}

/** The engine words, both surfaces. */
export const ENGINE_WORDS = {
  scripted: "scripted · 0 tokens · replays the recorded answers",
  scriptedFor: "re-checks the same input, free",
  live: (model: string, estimate: string) =>
    `live · calls ${model} · ${estimate}`,
  ownModel: "the agent's model",
  unknown: "cost unknown",
  refused: (why: string) => `scripted is refused: ${why} — pick live`,
  /** What a change that misses the recording adds to its name. */
  changed: ": the recording answered the original",
} as const

/** A recorded run's engine in one cell (the experiment grid, the
 * replays list): what it ran on. Apart from ENGINE_WORDS, which the
 * panel bundles whole: Studio's alone. */
export const RAN_WORDS = {
  scripted: "scripted",
  live: (model: string) => `live · ${model}`,
} as const

/** runEngine is the engine a recorded run ran on: scripted when it
 * answered as the scripted engine's model (lib/cost.ts's isScripted),
 * else live. */
export const runEngine = (r: Pick<RunRow, "model">): Engine =>
  isScripted(r) ? "scripted" : "live"

/** runEngineWords is runEngine in a cell: "scripted", or "live ·
 * provider/name" (the model it answered as; "the agent's model" when
 * the row names none). */
export function runEngineWords(r: Pick<RunRow, "model">): string {
  if (runEngine(r) === "scripted") return RAN_WORDS.scripted
  const m = [r.model.provider, r.model.name].filter(Boolean).join("/")
  return RAN_WORDS.live(m || ENGINE_WORDS.ownModel)
}

/** liveEstimate is the live engine's cost line: the source's usage
 * from from_step on at its model's price — never $0 for an unknown. */
function liveEstimate(f: EngineFacts): { estimate: string; usd: string } {
  const n = f.fromStep
  const docs = f.costs ?? []
  let usd = 0
  let tok = 0
  let k = 0
  let why = f.model
    ? `the steps were priced on ${f.ownModel || ENGINE_WORDS.ownModel}`
    : !docs.length && f.costs
      ? `the source never answered step ${n}`
      : docs[0]
        ? ""
        : f.costWhy || "not read"
  for (const d of why ? [] : docs) {
    if (!d) break
    const v = costView(d)
    const t = d.usage ? d.usage.input_tokens + d.usage.output_tokens : 0
    why = !t ? `step ${n + k} recorded no usage` : v.known ? "" : v.title
    if (why) break
    usd += v.usd ?? 0
    tok += t
    k++
  }
  if (why) return { estimate: `${ENGINE_WORDS.unknown}: ${why}`, usd: "" }
  const all = k === docs.length
  const text = formatUSD(usd)
  return {
    estimate: `~${tokens(tok)} tokens ≈ ${text} for ${k > 1 ? `steps ${n}–${n + k - 1}` : `step ${n}`}${all ? "" : " · later steps also run live"}`,
    usd: `${all ? "≈" : "≥"} ${text}`,
  }
}

/** scriptedBlock is why the scripted engine cannot answer, "" when it
 * can: the server's refusals first (studio/playground.go), then what
 * would miss the recorded key. */
function scriptedBlock(f: EngineFacts): string {
  const n = f.fromStep
  if (!f.source) return "no source run to replay"
  if (f.fromStep > 0 && f.steps !== undefined && n >= f.steps)
    return `the source never answered step ${n}`
  const what = f.edits
    ? `your edit changes step ${n}'s request`
    : f.prompt
      ? "a changed prompt"
      : f.model
        ? "a model override"
        : f.fork
          ? "a fork's new message"
          : f.input
            ? "a new input"
            : f.keyed.length
              ? `${f.keyed.join(", ")} changed`
              : ""
  if (what) return what + ENGINE_WORDS.changed
  // The preview's key check (U18.1): the server's own sentence.
  const k = f.keyMatch
  return k?.ok === false
    ? k.message || `no recorded answer for step ${k.step ?? n}`
    : ""
}

/** engineOf is the engine the command sends: the user's pick, else the
 * default. */
export const engineOf = (c: EngineChoice, picked?: Engine): Engine =>
  picked ?? c.default

/** engineHeld is the inline refusal while the engine sent is scripted
 * and scripted cannot answer ("" when not held). */
export const engineHeld = (c: EngineChoice, picked?: Engine): string =>
  engineOf(c, picked) === "scripted" && !c.scripted.enabled
    ? ENGINE_WORDS.refused(c.scripted.reason)
    : ""

/** factsOf is the one reading of a command both drawers make: the
 * overrides the body sends (overridesOf's and labOverrides' equality —
 * a knob equal to the agent's default is not sent, so it is no
 * change), the real edits, the input against the source's, and the
 * preview's key check. */
export function factsOf(p: {
  /** A source run (default true: the drawers always have one). */
  source?: boolean
  overrides: Overrides
  defaults?: AgentDefaults
  edits: number
  /** The input the body sends (step 0); the source's own. */
  input: string
  sourceInput?: string
  fork: boolean
  fromStep: number
  steps?: number
  own?: ModelInfo
  costs?: (StepCost | null)[] | null
  costWhy?: string
  preview?: PreviewDoc
}): EngineFacts {
  const o = p.overrides
  const keyed: string[] = []
  if (o.tools_enabled?.length || o.only_tools?.length) keyed.push("tools")
  if (o.thinking && o.thinking !== p.defaults?.thinking) keyed.push("thinking")
  if (o.tool_choice) keyed.push("tool choice")
  // Parallelism 1 is SequentialTools, which the key holds.
  if (o.options?.parallelism === 1)
    keyed.push("parallelism 1 (sequential tools)")
  return {
    source: p.source ?? true,
    edits: p.fork ? 0 : p.edits,
    prompt: !!o.instructions,
    model: o.model ?? "",
    input:
      p.fromStep === 0 && !!p.input.trim() && !noEdit(p.input, p.sourceInput),
    fork: p.fork,
    keyed,
    fromStep: p.fromStep,
    steps: p.fork ? undefined : p.steps,
    keyMatch: p.preview?.scripted_key_match,
    ownModel: p.own
      ? [p.own.provider, p.own.name].filter(Boolean).join("/")
      : undefined,
    costs: p.costs,
    costWhy: p.costWhy,
  }
}

/** noEdit says a field's value is no edit: empty, or the recording
 * itself (an edit equal to the recording is not an edit). */
export const noEdit = (value: string, recorded: string | undefined): boolean =>
  value === "" || value === recorded

/** The most steps the estimate reads (each its own step-route read). */
export const COST_STEPS = 20

/** runLabel is Run's label: it names the engine (and, on the
 * playground, the variant: "Run A on scripted · 0 tokens"). */
export function runLabel(c: EngineChoice, picked?: Engine, name = ""): string {
  const run = name ? `Run ${name}` : "Run"
  if (engineOf(c, picked) === "scripted") return `${run} on scripted · 0 tokens`
  return `${run} live on ${c.live.model} · ${c.live.usd || ENGINE_WORDS.unknown}`
}

type Overrides = NonNullable<PlaygroundRunBody["overrides"]>

/** sameModel says a model override names the agent's own model as the
 * source recorded it (its name, or provider/name): no override — the
 * body leaves it out (lib/experiment-body.ts's overridesOf, the
 * panel's buildRunBody). */
export const sameModel = (m: string, own?: ModelInfo): boolean =>
  !!own && (m === own.name || m === `${own.provider}/${own.name}`)

/** modelLabel is a model choice's label (UX-D-04): a model the source
 * never ran on is a live call, said beside it. */
export const modelLabel = (m: string, own?: ModelInfo): string =>
  sameModel(m, own) ? m : `${m} · live call`

/** stepsOf is the steps the estimate reads, from from_step on: up to
 * the source's step count (unknown: from_step alone), at most
 * COST_STEPS — a -1 last marks the steps left unread (the estimate then
 * says "later steps also run live"). */
export function stepsOf(from: number, steps?: number): number[] {
  const n = steps === undefined ? 1 : Math.max(0, steps - from)
  const at = Array.from({ length: Math.min(n, COST_STEPS) }, (_, i) => from + i)
  return steps === undefined || n > COST_STEPS ? [...at, -1] : at
}
