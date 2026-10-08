// The Studio step card's Request pane (plan E1.1): what the request
// record's hashes say about a step's prompt — "changed by PrepareStep",
// "overridden by experiment", "catalog changed at this step" — the
// baseline its prompt is diffed against, and the messages it sent,
// resolved against the transcript the page already holds. Pure
// readings of what the routes answer; the pane (step-request.tsx)
// draws them. Kept apart from lib/requests.ts, which the panel bundles.
import type {
  Manifest,
  ManifestAgent,
  Message,
  RequestRow,
  RunCompaction,
  Span,
  Transcript,
} from "./api"
import { isHoleRef } from "./api"

/** The chips on the pane's header, in that order. */
export const CHIP_PREPARE_STEP = "changed by PrepareStep"
export const CHIP_EXPERIMENT = "overridden by experiment"
export const CHIP_CATALOG = "catalog changed at this step"
/** The neutral prompt chip: the hash moved, and the tool set did too
 * (a PrepareStep, or the tools' PromptSnippets). */
export const CHIP_PROMPT_CHANGED = "prompt changed at this step"

/** The sha256 of "": the instructions hash of a run with no
 * instructions (ADR 0028 §4). */
export const EMPTY_SHA256 =
  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

/** The run's weft.override.* fingerprint, read off its invoke_agent
 * span (ADR 0024 S1.2): present only when a RunOption changed the
 * agent's configuration for this run. */
export interface RunOverride {
  /** weft.override.hash: the experiment's fingerprint. */
  hash?: string
  /** weft.override.instructions = true: the instructions were
   * replaced (the text is the prompt record's, never the span's). */
  instructions: boolean
  /** Every overridden knob, by its attribute's last segment. */
  fields: string[]
}

const OVERRIDE = "weft.override."

/** overrideOf reads the run's own invoke_agent span (by weft.run.id; a
 * subagent child's span in the same trace is not this run's) for the
 * override fingerprint. Undefined when the run carries none. */
export function overrideOf(spans: Span[] | undefined, runId: string): RunOverride | undefined {
  const span = spans?.find(
    (s) =>
      s.attrs["gen_ai.operation.name"] === "invoke_agent" &&
      s.attrs["weft.run.id"] === runId
  )
  if (!span) return undefined
  const fields: string[] = []
  let hash: string | undefined
  for (const [k, v] of Object.entries(span.attrs)) {
    if (!k.startsWith(OVERRIDE)) continue
    const name = k.slice(OVERRIDE.length)
    if (name === "hash") hash = typeof v === "string" ? v : String(v)
    else fields.push(name)
  }
  if (!hash && fields.length === 0) return undefined
  const ins = span.attrs[`${OVERRIDE}instructions`]
  return {
    hash,
    instructions: ins === true || ins === "true",
    fields: fields.sort(),
  }
}

/** The manifest's agent for a run, and whether it is the version the
 * run ran. */
export interface Registered {
  agent: ManifestAgent
  /** The agent carries the run's own weft.manifest.hash, or the
   * manifest's file source lists it under that hash. A name-only match
   * (a weft.json that may be stale) is not verified: its snippets
   * decide nothing and its instructions are not presented as the run's. */
  verified: boolean
}

/** registeredAgent picks the manifest's agent for a run: the one with
 * the run's own manifest hash, else the one with its name (unverified
 * unless the file source lists that name under the run's hash). */
export function registeredAgent(
  manifest: Manifest | undefined,
  agent: string,
  manifestHash?: string
): Registered | undefined {
  const agents = manifest?.agents ?? []
  if (manifestHash) {
    const exact = agents.find((a) => a.manifest_hash === manifestHash)
    if (exact) return { agent: exact, verified: true }
  }
  const named = agents.find((a) => a.name === agent)
  if (!named) return undefined
  const listed =
    !!manifestHash &&
    (manifest?.sources ?? []).some(
      (src) =>
        src.source === "file" &&
        src.agents.some((a) => a.name === agent && a.manifest_hash === manifestHash)
    )
  return { agent: named, verified: listed }
}

/** sha256Hex is the lowercase hex sha256 of the UTF-8 bytes — the
 * encoding every ADR 0028 hash uses. Undefined where the page has no
 * WebCrypto (an insecure, non-loopback origin). */
export async function sha256Hex(text: string): Promise<string | undefined> {
  const subtle = (globalThis.crypto as Crypto | undefined)?.subtle
  if (!subtle) return undefined
  const sum = await subtle.digest("SHA-256", new TextEncoder().encode(text))
  return Array.from(new Uint8Array(sum), (b) => b.toString(16).padStart(2, "0")).join("")
}

/** composeSystem mirrors core/loop.go's: the instructions, then each
 * offered tool's PromptSnippet, paragraphs apart. */
export function composeSystem(instructions: string, snippets: string[]): string {
  let out = instructions
  for (const s of snippets) {
    if (!s) continue
    if (out.length > 0) out += "\n\n"
    out += s
  }
  return out
}

/** The offered tools' PromptSnippets in offer order, from the
 * registered agent; undefined when the manifest does not know the
 * agent or any offered tool (a ToolSource tool is never in it). */
export function snippetsOf(
  names: string[],
  agent: ManifestAgent | undefined
): string[] | undefined {
  if (!agent) return undefined
  const byName = new Map(agent.tools.map((t) => [t.name, t.prompt_snippet ?? ""]))
  if (names.some((n) => !byName.has(n))) return undefined
  return names.map((n) => byName.get(n) ?? "")
}

/** The prompt text a row carries, when the record is here. */
export function promptText(row: RequestRow | undefined): string | undefined {
  const p = row?.prompt
  if (!row || !p || isHoleRef(p)) return row && !row.system_hash ? "" : undefined
  return p.text
}

/**
 * composedFromInstructions says whether the first recorded step's
 * system text is the run's configured instructions (instructions_hash)
 * plus nothing but the offered tools' PromptSnippets — the loop's own
 * composition, no PrepareStep. true or false when the hashes and texts
 * decide it; undefined when they cannot: the text is not here, no
 * WebCrypto, or tools were offered and the snippets are unknown or
 * unverified (a stale weft.json, a ToolSource tool) — then only an
 * exact match decides, and a mismatch is undefined, never false.
 * `snippets` must come from a verified agent to decide a mismatch.
 */
export async function composedFromInstructions(
  row: RequestRow,
  instructionsHash: string,
  snippets: string[] | undefined
): Promise<boolean | undefined> {
  const sys = row.system_hash
  if (sys === instructionsHash) return true
  if (sys === "" && instructionsHash === EMPTY_SHA256) return true
  const text = promptText(row)
  if (text === undefined) return undefined
  if (snippets === undefined) {
    // Without the snippets only an exact match decides; a step that
    // offered no tools has no snippets to compose.
    if (row.body.tools.names.length > 0) {
      const h = await sha256Hex(text)
      return h === undefined || h !== instructionsHash ? undefined : true
    }
    snippets = []
  }
  const parts = snippets.filter(Boolean)
  // From here the snippets are the verified agent's: a mismatch is the
  // text's, not the manifest's.
  if (parts.length === 0) {
    const h = await sha256Hex(text)
    return h === undefined ? undefined : h === instructionsHash
  }
  const tail = parts.join("\n\n")
  if (instructionsHash === EMPTY_SHA256) return text === tail
  if (!text.endsWith(`\n\n${tail}`)) return false
  const h = await sha256Hex(text.slice(0, text.length - tail.length - 2))
  return h === undefined ? undefined : h === instructionsHash
}

/** What a step's prompt diff is against. */
export interface PromptBaseline {
  /** "previous": the previous recorded step's prompt; "registered":
   * the agent's registered instructions (composed with the offered
   * tools' snippets when the manifest names them). */
  kind: "previous" | "registered"
  /** The previous step's ordinal (kind "previous"). */
  step?: number
  text: string
  /** The baseline's own record was cut by the recorder. */
  truncated?: boolean
  /** kind "registered": the manifest's agent is the run's version. */
  verified?: boolean
  /** kind "registered": the run replaced its instructions. */
  overridden?: boolean
}

/** The diff's caption. */
export function baselineCaption(b: PromptBaseline): string {
  if (b.kind === "previous") return `diff vs step ${b.step}`
  if (!b.verified) return "diff vs weft.json's instructions — not verified for this run"
  return b.overridden
    ? "diff vs the registered instructions (overridden for this run)"
    : "diff vs the registered instructions"
}

/**
 * toolSetMayExplain says whether a system hash that moved between two
 * steps could be the tool set's doing rather than a PrepareStep: the
 * offered names differ and the verified agent cannot show that every
 * tool added or dropped carries no PromptSnippet (a ToolSource tool is
 * never in the manifest; an unverified manifest decides nothing).
 */
export function toolSetMayExplain(
  before: string[],
  after: string[],
  reg: Registered | undefined
): boolean {
  const a = new Set(before)
  const b = new Set(after)
  const changed = [...before.filter((n) => !b.has(n)), ...after.filter((n) => !a.has(n))]
  if (changed.length === 0) return false
  if (!reg?.verified) return true
  const snippets = snippetsOf(changed, reg.agent)
  return snippets === undefined || snippets.some(Boolean)
}

/** The previous recorded step's last row, for each step that has one. */
export function previousRows(rows: RequestRow[]): Map<number, RequestRow> {
  const sorted = [...rows].sort((a, b) => a.step - b.step || a.index - b.index)
  const out = new Map<number, RequestRow>()
  let prev: RequestRow | undefined
  for (const r of sorted) {
    if (prev && prev.step !== r.step && !out.has(r.step)) out.set(r.step, prev)
    if (!prev || prev.step !== r.step || r.index > prev.index) prev = r
  }
  return out
}

/** What one request sent as messages: the count the record keeps and,
 * when the page's transcript places them, their bytes and the last
 * few. hole says why only the count is known. */
export interface MessagesSent {
  count: number
  /** UTF-8 bytes of the messages as JSON. */
  bytes?: number
  /** The last messages sent (at most `last`), oldest first. */
  last: Message[]
  /** How many messages came before `last`. */
  earlier: number
  /** Those messages, when the transcript placed them (the raw tree). */
  before: Message[]
  /** compacted: the request saw a compaction view (the marker above);
   * gap: the transcript does not hold what the record counts; absent
   * index (capture off): only the count was kept. */
  hole?: "compacted" | "gap" | "no_index" | "no_transcript"
}

/** The last N messages a pane renders inline. */
export const MESSAGES_INLINE = 3

/**
 * messagesSent resolves a request's messages_ref against the growth
 * records the page holds: the run's view as of record `index` is the
 * growth records up to it, concatenated (ADR 0028 §8). A view record
 * (a run-scope compaction) is never in the plain transcript: such a
 * request keeps its count and says compacted. No fetch.
 */
export function messagesSent(
  row: RequestRow,
  transcript: Transcript | null | undefined,
  compactions: RunCompaction[] = [],
  last = MESSAGES_INLINE
): MessagesSent {
  const ref = row.body.messages_ref
  const count = ref.count
  const only = (hole: MessagesSent["hole"]): MessagesSent => ({
    count,
    last: [],
    earlier: count,
    before: [],
    hole,
  })
  if (ref.index === undefined) return only("no_index")
  const at = ref.index
  if (compactions.some((c) => c.scope === "run" && c.index === at)) return only("compacted")
  if (!transcript) return only("no_transcript")
  const batches = transcript.batches.filter((b) => b.index <= at)
  if (!batches.some((b) => b.index === at) || batches.some((b) => b.unreadable))
    return only("gap")
  const msgs = batches.flatMap((b) => b.messages)
  if (msgs.length !== count) return only("gap")
  const bytes = new TextEncoder().encode(JSON.stringify(msgs)).length
  const shown = msgs.slice(Math.max(0, msgs.length - last))
  const cutAt = msgs.length - shown.length
  return { count, bytes, last: shown, earlier: cutAt, before: msgs.slice(0, cutAt) }
}
