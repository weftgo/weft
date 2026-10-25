// The engine a replay runs on, chosen per command (U17; UX-C-01,
// UX-D-04, UX-C-05, UX-C-08): `scripted` when it can answer — a source
// run, nothing that changes the replay key (an edit, a prompt or model
// override, a new input, a fork, tools / thinking / tool choice), a
// from_step the source answered, and the preview's key check (when the
// server sends one) not a miss — else `live`. The scripted option is
// never taken away silently: when it cannot answer it is disabled with
// its reason, and a user who picked it is held with that reason inline.
// Live carries what it will call and an estimate: the source step's
// recorded usage at its model's price (the step route's cost block,
// studio/pricing.go — the one price table), or "cost unknown" with the
// reason, never $0 for an unknown. Plain TypeScript: Studio's replay
// drawer and the panel's drawer read the same choice and the same
// words (parity.test.ts).
import type { CostFields, Usage } from "./api"
import { costView } from "./cost"
import { tokens } from "./format"

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
  keyMatch?: { ok: boolean; because?: string[] } | null
  /** The preview's per-step scripted hit, when sent. */
  hit?: boolean | null
  /** The agent's own model as the source recorded it
   * ("provider/name"). */
  ownModel?: string
  /** The source step's step-route doc (its cost block and usage), or
   * null when it is not there. */
  cost?: (CostFields & { usage?: Usage }) | null
  /** Why there is no cost doc (not read yet, no step route, …). */
  costWhy?: string
}

export interface EngineChoice {
  /** The engine the command runs on unless the user picks the other. */
  default: Engine
  scripted: { enabled: boolean; reason: string }
  live: {
    model: string
    /** "~1.2k tok ≈ $0.0004", or "cost unknown: <why>". */
    estimate: string
    /** The figure ("$0.0004") when known, else "". */
    usd: string
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
              : f.keyMatch?.ok === false
                ? `the key misses${f.keyMatch.because?.length ? ` (${f.keyMatch.because.join("; ")})` : ""}`
                : ""
  if (what) return what + ENGINE_WORDS.changed
  return f.hit === false ? "no recorded answer for this request" : ""
}

/** liveEstimate is the live engine's cost line: the source step's
 * recorded usage at its model's price — never $0 for an unknown. */
function liveEstimate(f: EngineFacts): { estimate: string; usd: string } {
  const u = f.cost?.usage
  const n = u ? u.input_tokens + u.output_tokens : 0
  const v = f.cost && costView(f.cost)
  const why = f.model
    ? `the step was priced on ${f.ownModel || ENGINE_WORDS.ownModel}`
    : !v
      ? f.costWhy || "the step is not read"
      : !n
        ? "the step recorded no usage"
        : v.known
          ? ""
          : v.title
  return why
    ? { estimate: `${ENGINE_WORDS.unknown}: ${why}`, usd: "" }
    : { estimate: `~${tokens(n)} tok ≈ ${v!.text}`, usd: v!.text }
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

/** runLabel is Run's label: it names the engine. */
export function runLabel(c: EngineChoice, picked?: Engine): string {
  if (engineOf(c, picked) === "scripted") return "Run on scripted · 0 tokens"
  return `Run live on ${c.live.model} · ${c.live.usd ? `≈ ${c.live.usd}` : ENGINE_WORDS.unknown}`
}

/** noEdit says a field's value is no edit: empty, or the recording
 * itself (an edit equal to the recording is not an edit). */
export const noEdit = (value: string, recorded: string | undefined): boolean =>
  value === "" || value === recorded

/** keyedKnobs names the knobs set that the replay key holds. */
export function keyedKnobs(k: {
  tools?: boolean
  thinking?: string
  toolChoice?: string
}): string[] {
  const out: string[] = []
  if (k.tools) out.push("tools")
  if (k.thinking) out.push("thinking")
  if (k.toolChoice) out.push("tool choice")
  return out
}
