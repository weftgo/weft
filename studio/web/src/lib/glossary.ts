// The glossary (U21.1; UX-A-20, UX-D-22, UX-F-01, the audit's §2.1):
// one entry per concept a user meets on either surface — the word, its
// tooltip (short, ≤ 90 characters, "word — what it is"), its glossary
// text (long, 2–4 lines), the docs section that defines it and the
// words it retires (aliases: what a renderer must stop saying).
//
// Studio's <Term> tooltips and its Concepts tab (U21.2) read TERMS; the
// panel's ? overlay and scope chips (U21.3) read lib/glossary-core.ts's
// SHORT lines only — the entries below take their short from there — so
// the panel bundles no entry here (panel-budget.json).
//
// The status words' tooltips are lib/status.ts's hints: the one place
// both renderers already read them from, so the words cannot drift.
import { STATUS } from "./status"
import { SHORT } from "./glossary-core"

export { CORE_TERMS, SHORT } from "./glossary-core"

/** One concept: word, tooltip, glossary text, docs anchor, retired words. */
export interface Term {
  /** The word, as both renderers spell it. */
  term: string
  /** The tooltip: "word — what it is", at most 90 characters. */
  short: string
  /** The glossary entry: 2–4 lines, joined by "\n". */
  long: string
  /** The docs section that defines it: a repo path and its anchor. */
  docs: string
  /** Words to retire for this concept (U21.3's vocabulary audit). */
  aliases: readonly string[]
  /** The Concepts tab's group (the audit's §2.1 headings). */
  group: TermGroup
}

export type TermGroup = "work" | "delegation" | "replay" | "state" | "machinery"

/** The Concepts tab's group headings, in order. */
export const GROUPS: Record<TermGroup, string> = {
  work: "The work: run ⊃ step ⊃ attempt",
  delegation: "Delegation",
  replay: "Replay, experiment, fork",
  state: "State: status, gaps, approvals",
  machinery: "Machinery",
}

const RC = "docs/record-contract.md"
const LOOP = "docs/agent-loop-deep-dive.md"
const THREAD = "docs/thread-operations.md"
const PG = "studio/README.md#the-playground-weft-playgroundmd"
const PANEL = "studio/README.md#the-devtools-panel-weft-devtoolsmd"

/** short is a status word's tooltip: its STATUS hint. */
const said = (k: keyof typeof STATUS) => `${STATUS[k].word} — ${STATUS[k].hint}`

// ── The work hierarchy ─────────────────────────────────────────────

export const RUN: Term = {
  term: "run",
  short: SHORT.run,
  long: [
    "A run is one pass of the agent loop. It has steps; each step may call tools.",
    "A tool may be a subagent, whose run is a run of its own: a child run.",
    "A run inside a session is also called a turn of that session.",
    "Replays and experiments create new runs that point back at their source.",
  ].join("\n"),
  docs: `${RC}#invoke_agent--the-run`,
  aliases: [],
  group: "work",
}

export const STEP: Term = {
  term: "step",
  short: SHORT.step,
  long: [
    "A step is one request to the model, its answer, and the tool calls that answer asked for.",
    "A run stops when a step asks for no tool (stop), hits a limit, or fails.",
    "Step numbers start at 0 and are the same on every surface and in every link.",
  ].join("\n"),
  docs: `${RC}#chat--one-model-call-a-step`,
  aliases: [],
  group: "work",
}

export const ATTEMPT: Term = {
  term: "attempt",
  short: SHORT.attempt,
  long: [
    "When mw.Retry or mw.Fallback wraps the model, one step can call the provider several times.",
    "Each try is an attempt; the one that answered is the step's response.",
    "Attempts carry their own latency, error and (development only) wire bodies.",
  ].join("\n"),
  docs: `${RC}#attempt--one-try-inside-a-model-call`,
  aliases: [],
  group: "work",
}

export const TURN: Term = {
  term: "turn",
  short: SHORT.turn,
  long: [
    "A session is a sequence of turns. Each turn is a run that starts from the session's history.",
    "A turn parked on an approval continues as a resume turn.",
    "A run that belongs to no session is a run, never a turn.",
  ].join("\n"),
  docs: `${THREAD}#1-what-is-on-disk`,
  aliases: [],
  group: "work",
}

export const SESSION: Term = {
  term: "session",
  short: SHORT.session,
  long: [
    "A session is a conversation your app stores with weft/thread: an id s_…, its turns,",
    "its history, its compactions and branches. Studio's Sessions page lists them.",
    "weft/thread is the Go package's name; the UI says session.",
  ].join("\n"),
  docs: `${THREAD}#1-what-is-on-disk`,
  aliases: ["thread", "threads"],
  group: "work",
}

export const PUBLIC_ID: Term = {
  term: "public id",
  short:
    "public id — the conversation's browser-safe name; the panel follows it",
  long: [
    "The browser-safe handle (weft.public_id) that names a conversation to a page and the panel",
    "without exposing the session id. Harness runs carry one per scenario, with no session.",
  ].join("\n"),
  docs: `${RC}#2-the-identity-chain`,
  aliases: [],
  group: "work",
}

export const CONVERSATION: Term = {
  term: "conversation",
  short: "conversation — the runs of one public id: what the panel follows",
  long: [
    "The panel's noun: everything recorded under one public id (and session, if named).",
    "Elsewhere the word is session.",
  ].join("\n"),
  docs: PANEL,
  aliases: [],
  group: "work",
}

export const SCOPE: Term = {
  term: "scope",
  short: SHORT.scope,
  long: [
    "The panel's scope names the conversation it follows: a public id, a session, a run.",
    "A page declares it (data-scope, the Weft-Scope header, #weft_scope=) or the panel asks.",
    "What a token may read is its access (read-only token, playground token), not a scope.",
  ].join("\n"),
  docs: PANEL,
  aliases: ["token scope", "debug scope"],
  group: "work",
}

export const FLOW: Term = {
  term: "flow",
  short: SHORT.flow,
  long: [
    "A flow is run metadata (weft.flow): scope.Header stamps the scope's flow= on every run",
    "its handler starts (scope.WithFlow in code). Studio's lists and the panel filter by it.",
    "The step pills above Studio's waterfall are the step strip, not a flow.",
  ].join("\n"),
  docs: `${RC}#2-the-identity-chain`,
  aliases: ["flow strip"],
  group: "work",
}

// ── Delegation ─────────────────────────────────────────────────────

export const SUBAGENT: Term = {
  term: "subagent",
  short:
    "subagent — an agent called as a tool; its run is a child run with its own steps",
  long: [
    "A subagent is an agent exposed as a tool (weft.AgentTool).",
    "Calling it starts a child run whose parent is the calling run.",
  ].join("\n"),
  docs: `${LOOP}#11-subagents-one-agent-using-another`,
  aliases: ["subagent run", "subagent runs", "subagent children"],
  group: "delegation",
}

export const CHILD_RUN: Term = {
  term: "child run",
  short:
    "child run — the run a subagent call started; its parent is the calling run",
  long: [
    "A child run is a run of its own: steps, tokens, cost, a page.",
    "Its usage and cost roll into the parent's as delegated, shown apart from the parent's own.",
  ].join("\n"),
  docs: `${LOOP}#11-subagents-one-agent-using-another`,
  aliases: [],
  group: "delegation",
}

export const OWN: Term = {
  term: "own",
  short:
    "own — this run's model calls; delegated — its child runs', shown apart",
  long: [
    "A run's own tokens and cost are its model calls'. Its delegated ones are its child runs'.",
    'The two are two figures, never folded: "$0.0012 own · $0.0040 delegated".',
  ].join("\n"),
  docs: `${RC}#6-the-run-row-what-a-reader-derives`,
  aliases: [],
  group: "delegation",
}

export const DELEGATED: Term = {
  term: "delegated",
  short:
    "delegated — what this run's child runs spent: tokens and cost, apart from its own",
  long: [
    "Delegated is a cost and usage word only: the child runs' part of a run's bill.",
    "A session thread/pool started for a call is a pool session, not delegated.",
  ].join("\n"),
  docs: `${RC}#6-the-run-row-what-a-reader-derives`,
  aliases: ["delegated session", "delegated sessions"],
  group: "delegation",
}

// ── Replay, experiment, fork ───────────────────────────────────────

export const PLAYBACK: Term = {
  term: "playback",
  short:
    "playback — the recorded events played again in the page; nothing runs",
  long: [
    "The run page's bar plays the recorded events back (space plays or pauses).",
    "Nothing runs: it is the recording, step by step. A replay runs the agent again.",
  ].join("\n"),
  docs: `${RC}#event-and-delta`,
  aliases: ["play / pause replay", "replay speed"],
  group: "replay",
}

export const REPLAY: Term = {
  term: "replay",
  short: SHORT.replay,
  long: [
    "A replay is a new run in your app (through weft/runtime) that keeps the transcript",
    "up to a step and runs the rest again, live or scripted, with your edits.",
    'Its row says "↻ replay of t3 · step 1": the source and the step it starts from.',
  ].join("\n"),
  docs: PG,
  aliases: ["experiment of"],
  group: "replay",
}

export const EXPERIMENT: Term = {
  term: "experiment",
  short:
    "experiment — replays of a run with changed inputs, options or models, kept to compare",
  long: [
    "An experiment is a set of replays you can come back to: a playground matrix,",
    "or a single replay saved from the drawer. /experiments lists them.",
  ].join("\n"),
  docs: PG,
  aliases: ["✎ Experiment", "Run experiment ▶"],
  group: "replay",
}

export const FORK: Term = {
  term: "fork",
  short:
    "fork — a new session that continues this one from a point; the original is untouched",
  long: [
    "A fork (thread.Fork) is a new session that continues an existing one from an entry",
    '(weft.session.forked_from = <session>#<entry>). Its rows say "fork of s_…#12".',
  ].join("\n"),
  docs: `${RC}#thread--the-sessions-state-log`,
  aliases: ["experiments forked from", "· fork"],
  group: "replay",
}

export const SCRIPTED: Term = {
  term: "scripted",
  short:
    "scripted — answers from the recording; free, fails if the request changed",
  long: [
    "The scripted engine answers each model call from the recorded turns: zero tokens.",
    "It fails when the request differs from the recording (model script exhausted).",
  ].join("\n"),
  docs: PG,
  aliases: ["scripted (0 tokens)", "scripted (zero tokens)"],
  group: "replay",
}

export const LIVE: Term = {
  term: "live",
  short: "live — calls the model for real; costs tokens",
  long: [
    "The live engine sends each model call to the provider, as the app does.",
    "It costs tokens and may answer differently from the recording.",
  ].join("\n"),
  docs: PG,
  aliases: [],
  group: "replay",
}

// ── State words ────────────────────────────────────────────────────

export const RUNNING: Term = {
  term: "running",
  short: said("running"),
  long: [
    "The run's recorder is still reporting: its heartbeat is fresh.",
    "A run that stops reporting reads interrupted after interrupted_after (30 s by default).",
  ].join("\n"),
  docs: `${RC}#heartbeat`,
  aliases: [],
  group: "state",
}

export const SUCCEEDED: Term = {
  term: "succeeded",
  short: said("succeeded"),
  long: [
    "The loop returned a result. A run that finished with calls awaiting a decision",
    "reads parked instead, until a turn resumes it.",
  ].join("\n"),
  docs: `${RC}#6-the-run-row-what-a-reader-derives`,
  aliases: [],
  group: "state",
}

export const FAILED: Term = {
  term: "failed",
  short: said("failed"),
  long: [
    "The loop returned an error. The row names its kind (provider, tool, limit, cancelled)",
    "and the run page says why, with the step it happened at.",
  ].join("\n"),
  docs: `${RC}#6-the-run-row-what-a-reader-derives`,
  aliases: [],
  group: "state",
}

export const INTERRUPTED: Term = {
  term: "interrupted",
  short: said("interrupted"),
  long: [
    "Stored as running, but silent for longer than interrupted_after (30 s by default):",
    "the process crashed or was killed before the run could finish.",
  ].join("\n"),
  docs: `${RC}#heartbeat`,
  aliases: [],
  group: "state",
}

export const PARKED: Term = {
  term: "parked",
  short: SHORT.parked,
  long: [
    "The run finished its step, but tool calls await a decision: an approval or a breakpoint.",
    'Approve or deny on its page; a resume turn continues it ("parked → t4").',
  ].join("\n"),
  docs: `${THREAD}#11-approvals-operations`,
  aliases: ["pending approval"],
  group: "state",
}

export const APPROVE: Term = {
  term: "approve",
  short:
    "approve — let a parked tool call run; deny refuses it; resolve… answers it yourself",
  long: [
    "A parked call waits for a decision: approve runs it, deny refuses it with a reason,",
    'resolve… supplies its result. The outcome reads "approved by <who> · resumed as t2"',
    'or "denied: <reason>"; weft.Approve and weft.Deny are the API\'s words.',
  ].join("\n"),
  docs: `${THREAD}#11-approvals-operations`,
  aliases: ["continue", "skip"],
  group: "state",
}

export const GAP: Term = {
  term: "gap",
  short:
    "gap — something this record cannot show, and why; the fix is in the badge",
  long: [
    "A gap badge marks what the record cannot show: truncated, not captured, redacted,",
    "interrupted, compacted, hidden by your token, derived. Each says why and how to fix it.",
  ].join("\n"),
  docs: `${RC}#7-badges`,
  aliases: ["hole", "holes"],
  group: "state",
}

export const DERIVED: Term = {
  term: "derived",
  short:
    "derived — rebuilt from standard OTel spans; no transcript, events or replay",
  long: [
    "Studio built this run from stock OpenTelemetry GenAI spans, with no weft records:",
    "it has a waterfall and usage, no transcript, no events and no replay.",
  ].join("\n"),
  docs: `${RC}#9-deriving-a-run-from-stock-spans`,
  aliases: [],
  group: "state",
}

export const COMPACTED: Term = {
  term: "compacted",
  short:
    "compacted — older messages were replaced by a summary before this request",
  long: [
    "The session (or the run) replaced older messages with a summary to fit the context.",
    "The model saw the compacted view; the originals stay in the session's log.",
  ].join("\n"),
  docs: `${RC}#compaction--the-session-marker`,
  aliases: [],
  group: "state",
}

// ── Machinery ──────────────────────────────────────────────────────

export const RUNTIME: Term = {
  term: "runtime",
  short:
    "runtime — your app's link to Studio: it runs replays and lets Studio act on turns",
  long: [
    "runtime.Install in your app opens the link: it registers agents, runs replays,",
    "honours breakpoints and acts on the app's own turns when the app opts in.",
  ].join("\n"),
  docs: PG,
  aliases: [],
  group: "machinery",
}

export const APP_OWNED: Term = {
  term: "app-owned",
  short:
    "app-owned — your app started this turn; Studio acts on it only via the runtime",
  long: [
    "A turn your app started, not a replay. Studio reads it; it approves, denies or",
    "cancels it only through the runtime, when the app opted in (runtime.Sessions).",
  ].join("\n"),
  docs: PG,
  aliases: [],
  group: "machinery",
}

export const WATERFALL: Term = {
  term: "waterfall",
  short: "waterfall — the run's model and tool calls on a time axis",
  long: [
    "The run's tree of model calls, tool calls and child runs, each a bar on a time axis.",
    "Both surfaces call it the waterfall; trace names the OpenTelemetry trace page only.",
  ].join("\n"),
  docs: `${RC}#3-spans`,
  aliases: ["Timeline", "trace view"],
  group: "machinery",
}

export const STORY: Term = {
  term: "story",
  short:
    "story — the run as a conversation: each step's messages, tool calls and results",
  long: [
    "The run's transcript, step by step: what the model was sent, what it answered,",
    "the tools it called and what they returned. Both surfaces call it the story.",
  ].join("\n"),
  docs: `${RC}#messages--the-transcript-and-the-compaction-view`,
  aliases: [],
  group: "machinery",
}

export const REQUEST: Term = {
  term: "request",
  short:
    "request — what one model call was given: its messages, prompt and tools",
  long: [
    "A step's request is exactly what the model was sent: the messages after compaction,",
    "the system prompt and the tool catalog. Studio shows it as a step's pane.",
  ].join("\n"),
  docs: `${RC}#request-prompt-tools--what-each-model-call-was-given`,
  aliases: [],
  group: "machinery",
}

export const RAW: Term = {
  term: "raw",
  short: "raw — the run's recorded events and its JSON document, as stored",
  long: [
    "The events the run recorded, in order, and the run's JSON as the API serves it:",
    "what every other view is drawn from. The playback bar plays these events.",
  ].join("\n"),
  docs: `${RC}#event-and-delta`,
  aliases: [],
  group: "machinery",
}

export const EVENT: Term = {
  term: "event",
  short:
    "event — one record of what the run did, in order: a step, a tool call, a result",
  long: [
    "Every run records its loop as events (step started, tool call, result, step finished),",
    "each with its position. Raw lists them; the playback plays them back in order.",
  ].join("\n"),
  docs: `${RC}#event-and-delta`,
  aliases: [],
  group: "machinery",
}

export const MANIFEST: Term = {
  term: "manifest",
  short:
    "manifest — the agent as it was built: model, instructions, tools; its hash names it",
  long: [
    "An agent's manifest is its definition as data (weft.json, or what a runtime registers):",
    "model, instructions, tools and policy. A run records its hash, so a change shows.",
  ].join("\n"),
  docs: `${LOOP}#the-manifest-the-agent-as-a-diffable-artifact`,
  aliases: [],
  group: "machinery",
}

export const TRACE: Term = {
  term: "trace",
  short: "trace — OpenTelemetry's spans of one request, across services",
  long: [
    "A trace is every span of one request, across services; a span is one timed operation",
    "(invoke_agent, chat, execute_tool). Studio's Traces page lists them.",
  ].join("\n"),
  docs: `${RC}#8-the-genai-semantic-conventions`,
  aliases: [],
  group: "machinery",
}

export const SPAN: Term = {
  term: "span",
  short:
    "span — one timed operation of a trace: invoke_agent, chat or execute_tool",
  long: [
    "A span is one timed operation OpenTelemetry records: the run (invoke_agent),",
    "a model call (chat), a tool call (execute_tool). The waterfall draws them as bars.",
  ].join("\n"),
  docs: `${RC}#3-spans`,
  aliases: [],
  group: "machinery",
}

export const TOKENS: Term = {
  term: "tokens",
  short: 'tokens — what the model read and wrote: "60 in · 30 out"',
  long: [
    "A step's or a run's input and output tokens, as the provider reported them.",
    'Both surfaces write them "60 in · 30 out"; delegated tokens are shown apart.',
  ].join("\n"),
  docs: `${RC}#6-the-run-row-what-a-reader-derives`,
  aliases: ["tok"],
  group: "machinery",
}

export const FIRST_TOKEN: Term = {
  term: "first token",
  short: "first token — how long the model took to send its first token (TTFT)",
  long: [
    "Time to first token: from the request to the first streamed token of the answer.",
    'Both surfaces say "first token 180 ms" beside the step\'s latency.',
  ].join("\n"),
  docs: `${RC}#chat--one-model-call-a-step`,
  aliases: ["ttft"],
  group: "machinery",
}

export const MIDDLEWARE: Term = {
  term: "middleware",
  short:
    "middleware — a layer wrapped around every model or tool call: Retry, Allow, …",
  long: [
    "WrapModel and WrapTools wrap each call in layers, outer to inner: the onion.",
    "The manifest lists the chain; each call's spans say what a layer did on it.",
    "A greyed layer did nothing on that call, or records no decision (Log, Audit).",
  ].join("\n"),
  docs: `docs/life-of-a-call.md#a-tool-call`,
  aliases: [],
  group: "machinery",
}

export const MCP_SERVER: Term = {
  term: "MCP server",
  short:
    "MCP server — where an imported tool runs; mcp.Server names it mcp:<name>",
  long: [
    "weft/mcp imports a server's tools as ordinary tools; mcp.Server(name) names it.",
    "Each call then records its origin (mcp:billing), and the call graph draws the server.",
    "MCP unreachable: the call never completed; MCP tool error: the server said isError.",
  ].join("\n"),
  docs: "docs/adr/0015-mcp-interop.md#amendment-2026-10-26--mcp-identity-in-the-record-u232-o8",
  aliases: [],
  group: "machinery",
}

/** TERMS is the whole table, by key: Studio's (tooltips, the Concepts
 * tab). The panel imports single terms, never this. */
export const TERMS = {
  run: RUN,
  step: STEP,
  attempt: ATTEMPT,
  turn: TURN,
  session: SESSION,
  publicId: PUBLIC_ID,
  conversation: CONVERSATION,
  scope: SCOPE,
  flow: FLOW,
  subagent: SUBAGENT,
  childRun: CHILD_RUN,
  own: OWN,
  delegated: DELEGATED,
  playback: PLAYBACK,
  replay: REPLAY,
  experiment: EXPERIMENT,
  fork: FORK,
  scripted: SCRIPTED,
  live: LIVE,
  running: RUNNING,
  succeeded: SUCCEEDED,
  failed: FAILED,
  interrupted: INTERRUPTED,
  parked: PARKED,
  approve: APPROVE,
  gap: GAP,
  derived: DERIVED,
  compacted: COMPACTED,
  runtime: RUNTIME,
  appOwned: APP_OWNED,
  waterfall: WATERFALL,
  story: STORY,
  request: REQUEST,
  raw: RAW,
  event: EVENT,
  manifest: MANIFEST,
  trace: TRACE,
  span: SPAN,
  tokens: TOKENS,
  firstToken: FIRST_TOKEN,
  middleware: MIDDLEWARE,
  mcpServer: MCP_SERVER,
} as const satisfies Record<string, Term>

export type TermName = keyof typeof TERMS
