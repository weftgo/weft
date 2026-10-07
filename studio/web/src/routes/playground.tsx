// The Studio playground (WEFT-PLAYGROUND §4, P1's Studio half): the
// split view — the experiment's config on the left, the runs side by
// side on the right, the diff on top. One variant for now (P3's N-way
// compare and P5's variants × inputs matrix grow this page); the
// panel hands off into it with the context carried over (run, step,
// current overrides), so nothing is retyped (§2's parity rule).
//
//   /playground?run=<id>&step=N&instructions=…&tools=a,b&model=…
//       &thinking=…&input=…&engine=live|scripted
//       &side_effects=substitute|park|allow&thread=ephemeral|fork
//       &agent=<name>&runtime=<id>
//
// The same parameters are read from the URL fragment too —
// /playground#run=…&instructions=…&input=… — which is what the panel
// sends: a prompt in the fragment never reaches a server, an access
// log or a Referer, and no query-length limit (414/431) applies. A
// fragment parameter wins over the same query parameter; the fragment
// is read once, on arrival, and stripped from the address bar (kept
// out of history and bookmarks).
//
// Every parameter is optional. The target agent is the source run's
// own (the runtime that registers it), unless agent= / runtime= or the
// pickers say otherwise — never simply the first one registered.
//
// The same drawer fields as the panel (prompt, tools off, model,
// thinking, input), the same POST /api/playground/runs, the same live
// result — V6: one API, two clients.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useLocation, useNavigate, useSearch } from "@tanstack/react-router"
import { useEffect, useMemo, useRef, useState   } from "react"
import type {Dispatch, SetStateAction} from "react";

import {
  ApiError,
  metaQuery,
  experimentsQuery,
  fetchRun,
  fetchTranscript,
  postExperiment,
  postFixtures,
  postApproval,
  postSteer,
  fetchCommand,
  postPlaygroundRun,
  putBreakpoints,
  runQuery,
  runtimesQuery
  
  
  
  
  
} from "@/lib/api"
import type {AgentView, CommandStatus, Message, PlaygroundRunBody, RunRow, RuntimeView} from "@/lib/api";
import { placeBatches, producedText } from "@/lib/events"
import type { TranscriptBatch } from "@/lib/events"
import { diffLines, diffSummary } from "@/lib/diff"
import type { DiffRow } from "@/lib/diff"
import { spanMs } from "@/lib/format"
import { copyText, download } from "@/lib/json"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useRunEvents } from "@/hooks/use-run-events"
import { Button } from "@/components/ui/button"

type Engine = "live" | "scripted"
type SideEffects = "substitute" | "park" | "allow"
type ThreadMode = "ephemeral" | "fork"

/** What the panel carries over (§2: run, step, current overrides —
 * every knob of the drawer, so nothing is retyped or reset). */
interface PlaygroundSearch {
  run?: string
  step?: number
  instructions?: string
  tools?: string
  model?: string
  thinking?: string
  input?: string
  engine?: Engine
  side_effects?: SideEffects
  thread?: ThreadMode
  /** The agent to run (default: the source run's own). */
  agent?: string
  /** The runtime to send the command to (default: one registering
   * the agent). */
  runtime?: string
}

/** A search value as text: the router parses values as JSON, so an
 * all-digit run id or a prompt reading `true` arrives as a number or
 * a boolean — still the text the link carried. */
function str(v: unknown): string | undefined {
  if (typeof v === "string") return v || undefined
  if (typeof v === "number" || typeof v === "boolean") return String(v)
  return undefined
}

/** The hand-off parameters, validated: the router's search (whose
 * values arrive JSON-parsed) and the fragment's (strings) both come
 * through here. */
function parseHandoff(search: Record<string, unknown>): PlaygroundSearch {
  return {
    run: str(search.run),
    step:
      typeof search.step === "number" && Number.isInteger(search.step) && search.step > 0
        ? search.step
        : undefined,
    instructions: str(search.instructions),
    tools: str(search.tools),
    model: str(search.model),
    thinking: str(search.thinking),
    input: str(search.input),
    engine:
      search.engine === "live" || search.engine === "scripted"
        ? search.engine
        : undefined,
    side_effects:
      search.side_effects === "substitute" ||
      search.side_effects === "park" ||
      search.side_effects === "allow"
        ? search.side_effects
        : undefined,
    thread:
      search.thread === "ephemeral" || search.thread === "fork"
        ? search.thread
        : undefined,
    agent: str(search.agent),
    runtime: str(search.runtime),
  }
}

/** The hand-off carried in a URL fragment (`run=…&input=…`, no
 * leading "#"): the parameters it sets, validated like the query's —
 * absent ones are left out, so they never mask the query's. */
export function handoffFromHash(hash: string): Partial<PlaygroundSearch> {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash
  if (!raw) return {}
  const rec: Record<string, unknown> = {}
  for (const [k, v] of new URLSearchParams(raw)) {
    // The query's step arrives as a number (the router parses it);
    // the fragment's is text.
    rec[k] = k === "step" && /^\d+$/.test(v) ? Number(v) : v
  }
  const parsed = parseHandoff(rec)
  const out: Partial<PlaygroundSearch> = {}
  for (const [k, v] of Object.entries(parsed) as [keyof PlaygroundSearch, unknown][])
    if (v !== undefined) (out as Record<string, unknown>)[k] = v
  return out
}

export const Route = createFileRoute("/playground")({
  validateSearch: (search: Record<string, unknown>): PlaygroundSearch => parseHandoff(search),
  component: PlaygroundPage,
})

/**
 * useHandoff is the page's hand-off: the query's parameters, with the
 * fragment's laid over them. The fragment is read once, on arrival,
 * then stripped from the address bar; it stops applying when the query
 * changes under the page (a later navigation to /playground?run=…).
 */
function useHandoff(): PlaygroundSearch {
  const query = useSearch({ from: "/playground" })
  const hash = useLocation({ select: (l) => l.hash })
  const navigate = useNavigate()
  const [arrival] = useState(() => ({
    fromHash: handoffFromHash(hash),
    query: JSON.stringify(query),
  }))
  const stripped = useRef(false)
  useEffect(() => {
    if (stripped.current || !hash) return
    stripped.current = true
    void navigate({ to: ".", search: true, hash: "", replace: true })
  }, [hash, navigate])
  const queryKey = JSON.stringify(query)
  return useMemo(
    () => (queryKey === arrival.query ? { ...query, ...arrival.fromHash } : query),
    [query, queryKey, arrival]
  )
}

function PlaygroundPage() {
  const caps = useCapabilities()
  if (caps.loading) return <p className="p-6 text-xs text-muted-foreground">loading…</p>
  if (!caps.has("playground")) return <NoPlayground />
  return <Playground caps={caps.caps} />
}

function NoPlayground() {
  return (
    <div className="mx-auto max-w-lg space-y-2 py-24 text-center">
      <p className="text-sm">This Studio has no playground.</p>
      <p className="text-xs text-muted-foreground">
        The runtime link opens with <span className="font-mono">studio.Playground(true)</span>{" "}
        and <span className="font-mono">runtime.Install(...)</span> in your app
        (WEFT-PLAYGROUND §6 rule 1).
      </p>
    </div>
  )
}

/** One experiment in flight or finished: the command's lifecycle, the
 * run it produced, the fold streaming in from the live lane. */
interface Experiment {
  commandID: string
  state: CommandStatus["state"]
  /** The run's outcome once the command finished (succeeded | failed). */
  status?: CommandStatus["status"]
  runID: string
  error: string | null
  label: string
  row: RunRow | null
  /** The decisions sent on a parked run's calls. The runtime resumes a
   * park only once every pending call has one: until then each
   * decision is held, and its command finishes under the still-parked
   * run's id — this is what the card shows as decided. */
  decided?: { runID: string; calls: Record<string, string> }
  /** The thread mode the command was issued with (fork: no run id
   * until the turn is in flight — the runtime acks again naming it). */
  thread?: ThreadMode
}

/** One variant of the experiment (§4's columns): its overrides and its
 * own run, side by side with its siblings. */
interface Variant {
  key: string
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
  result: Experiment | null
}

/** A fresh experiment for a command just issued. */
function queued(commandID: string, label: string): Experiment {
  return { commandID, state: "queued", runID: "", error: null, label, row: null }
}

/** variantA seeds the first variant from the panel's carried-over
 * context (§2: run, step, current overrides — nothing retyped). The
 * tools selection is applied when the agent's tool list is known
 * (toolsOffFor): the runtimes have not loaded when this runs. */
function variantA(search: PlaygroundSearch): Variant {
  return {
    key: "A",
    instructions: search.instructions ?? "",
    toolsOff: new Set(),
    model: search.model ?? "",
    thinking: search.thinking ?? "",
    input: search.input ?? "",
    engine: search.engine ?? "live",
    sideEffects: search.side_effects ?? "substitute",
    thread: search.thread ?? "ephemeral",
    result: null,
  }
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
  variant: Pick<Variant, "instructions" | "toolsOff" | "model" | "thinking">,
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
  return overrides
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
  variant: Variant
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

/** One transcript edit draft (§5.1's wire shape — review fix 4b): a
 * patched tool result (pinned by call_id) or a rewritten call-free
 * reply, on a kept step. */
export interface EditDraft {
  step: number
  callID?: string
  toolResult?: string
  content?: string
}

/** One editable field of a kept step, derived from the source
 * transcript. */
export interface EditField {
  step: number
  callID?: string
  /** The tool's name, or "reply". */
  name: string
  placeholder: string
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

/** wireEdits maps the drafts to §5.1's flattened wire shape — the
 * same mapping the panel's buildRunBody makes. */
export function wireEdits(drafts: EditDraft[]): unknown[] {
  return drafts.map((e) => ({
    step: e.step,
    ...(e.callID ? { call_id: e.callID } : {}),
    ...(e.toolResult ? { tool_result: e.toolResult } : {}),
    ...(e.content ? { content: e.content } : {}),
  }))
}

function Playground({ caps }: { caps: string[] }) {
  const search = useHandoff()
  // Runtimes come and go (an app restarts, a second one connects):
  // the picker follows them.
  const runtimes = useQuery({ ...runtimesQuery(), refetchInterval: 5_000 })
  const meta = useQuery(metaQuery())

  // The variants (§4): A starts as the original, more are added with
  // "+ variant"; each carries its own overrides and its own run. The
  // panel's carried-over context seeds A (nothing is retyped).
  const [variants, setVariants] = useState<Variant[]>(() => [variantA(search)])
  const [active, setActive] = useState(0)
  const variant = variants.at(active) ?? variants[0]
  const [error, setError] = useState("")
  /** A command is being posted: a second click must not issue a second
   * run (the command id is minted server-side, so a repeat is a new
   * command — and its tokens). */
  const [busy, setBusy] = useState(false)
  /** The E9 matrix: the cells' experiments, keyed variant×input. */
  const [cells, setCells] = useState<Record<string, Experiment>>({})

  const [sourceRunID, setSourceRunID] = useState(search.run ?? "")
  const [fromStep, setFromStep] = useState(search.step ?? 0)
  /** The kept prefix's edits (review fix 4b): patched tool results and
   * rewritten call-free replies — the counterfactual the fresh step
   * answers. Source-shaped, not variant-shaped, so they live here. */
  const [editDrafts, setEditDrafts] = useState<EditDraft[]>([])
  useEffect(() => {
    if (search.run) setSourceRunID(search.run)
  }, [search.run])

  // The target (§4's header): the source run's own agent on a runtime
  // that registers it — an app with several agents (any app with a
  // subagent) must not have its support turn re-run by the planner
  // because the planner registered first. agent= / runtime= and the
  // pickers override.
  const [runtimeChoice, setRuntimeChoice] = useState(search.runtime ?? "")
  const [agentChoice, setAgentChoice] = useState(search.agent ?? "")
  const source = useQuery({
    ...runQuery(sourceRunID),
    enabled: Boolean(sourceRunID),
    staleTime: 30_000,
  })
  const sourceRow =
    sourceRunID && source.data?.id === sourceRunID ? source.data : undefined
  const all = runtimes.data?.runtimes ?? []
  const target = pickTarget(all, {
    runtime: runtimeChoice || undefined,
    agent: agentChoice || sourceRow?.agent || undefined,
  })
  const runtime = target.runtime
  const agent = target.agent
  // The source row decides the agent: hold the verbs until it is read.
  const resolving = Boolean(sourceRunID) && !agentChoice && source.isFetching

  // Seed the variants from the agent once it is known, and re-seed
  // when the target agent changes: the hand-off's tools= selection on
  // A, the registered prompt wherever the prompt was not edited, and
  // no turned-off name the new agent does not have.
  const registered = agent?.instructions ?? ""
  const seeded = useRef<{ agent: string; registered: string } | null>(null)
  useEffect(() => {
    // Not while the source row is still deciding the agent: the
    // stand-in's prompt and tools are not this experiment's.
    if (!agent || resolving) return
    const prev = seeded.current
    if (prev?.agent === agent.name && prev.registered === registered) return
    seeded.current = { agent: agent.name, registered }
    const names = new Set(agent.tools.map((t) => t.name))
    setVariants((cur) =>
      cur.map((v, i) => ({
        ...v,
        toolsOff:
          i === 0 && prev?.agent !== agent.name
            ? toolsOffFor(search.tools, agent)
            : new Set([...v.toolsOff].filter((n) => names.has(n))),
        instructions:
          v.instructions === "" || (prev && v.instructions === prev.registered)
            ? registered
            : v.instructions,
        // A model the new agent does not offer is not carried over.
        model: v.model && !agent.models.includes(v.model) && prev ? "" : v.model,
      }))
    )
  }, [agent, registered, resolving, search.tools])

  const patch = (p: Partial<Variant>) =>
    setVariants((cur) => cur.map((v, i) => (i === active ? { ...v, ...p } : v)))

  /** setResult updates one variant's experiment by the variant's KEY:
   * a card belongs to its variant whichever one is being edited. */
  const setResult = (key: string, upd: SetStateAction<Experiment | null>) =>
    setVariants((cur) =>
      cur.map((v) => {
        if (v.key !== key) return v
        const next = typeof upd === "function" ? upd(v.result) : upd
        return next && next !== v.result ? { ...v, result: next } : v
      })
    )

  const sourceText = useSourceText(sourceRunID)

  /** decide answers one parked call of a variant's run with the
   * approval verbs (ADR 0007) — the panel's controls, rendered here
   * too (§2's parity rule). The resumed run replaces that variant's
   * card. text is the denial's reason or the resolve's result. */
  const decide = async (
    variantKey: string,
    runID: string,
    callID: string,
    decision: "approve" | "deny" | "resolve",
    text: string
  ) => {
    // A refusal rejects: the card that asked shows it (a 400 names
    // the calls that are pending; a 503 says the runtime is gone).
    const out = await postApproval(runID, {
      call_id: callID,
      decision,
      ...(decision === "deny" && text ? { reason: text } : {}),
      ...(decision === "resolve" ? { content: text } : {}),
    })
    setResult(variantKey, (cur) => ({
      ...queued(out.command_id, cur?.label ?? variantKey),
      thread: cur?.thread,
      decided: {
        runID,
        calls: {
          ...(cur?.decided?.runID === runID ? cur.decided.calls : {}),
          [callID]: decision,
        },
      },
    }))
  }

  const run = async () => {
    setError("")
    if (!runtime || !agent || busy) return
    const key = variant.key
    let body: PlaygroundRunBody
    try {
      body = buildRunBody({
        runtime: runtime.id,
        agent,
        variant,
        sourceRunID,
        fromStep,
        input: variant.input,
        edits: editDrafts,
        publicID: sourceRow?.public_id,
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return
    }
    setBusy(true)
    try {
      const out = await postPlaygroundRun(body)
      setResult(key, {
        ...queued(out.command_id, `${key}${sourceRunID ? ` · ${runLabel(sourceRunID)}` : ""}`),
        thread: variant.thread,
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  /** runMatrix is E9's verb: save the definition, then one command
   * per variant × input cell, all under the experiment's id (the
   * budget caps the whole matrix — §6 rule 6). Every cell is recorded
   * as it is issued: a refused one shows its reason in place and the
   * rest still run, so nothing issued is ever missing from the grid. */
  const runMatrix = async (inputs: { key: string; text: string }[], experimentID: string) => {
    setError("")
    if (!runtime || !agent || busy) return
    if (inputs.length === 0) {
      setError("the matrix needs at least one input — or a source run, whose own turn an empty row re-runs")
      return
    }
    const plan: { key: string; label: string; body: PlaygroundRunBody }[] = []
    let definition: Parameters<typeof postExperiment>[0]
    try {
      for (const v of variants)
        for (const i of inputs)
          plan.push({
            key: `${v.key}\u0000${i.key}`,
            label: `${v.key}×${i.key}`,
            body: buildRunBody({
              runtime: runtime.id,
              agent,
              variant: v,
              sourceRunID,
              fromStep: 0,
              input: i.text,
              publicID: sourceRow?.public_id,
              experimentID,
            }),
          })
      definition = {
        id: experimentID,
        name: experimentID,
        agent: agent.name,
        variants: variants.map((v) => ({ key: v.key, overrides: overridesOf(v, agent) })),
        inputs: inputs.map((i) => ({
          key: i.key,
          ...(sourceRunID ? { source_run_id: sourceRunID } : {}),
          ...(i.text ? { text: i.text } : {}),
        })),
      }
    } catch (e) {
      // Nothing was issued: a variant the form cannot express.
      setError(e instanceof Error ? e.message : String(e))
      return
    }
    setBusy(true)
    try {
      await postExperiment(definition)
      // A new matrix replaces the grid: the keys are the same cells.
      setCells({})
      for (const c of plan) {
        try {
          const out = await postPlaygroundRun(c.body)
          setCells((cur) => ({ ...cur, [c.key]: queued(out.command_id, c.label) }))
        } catch (e) {
          const refused: Experiment = {
            ...queued("", c.label),
            state: "rejected",
            error: e instanceof Error ? e.message : String(e),
          }
          setCells((cur) => ({ ...cur, [c.key]: refused }))
        }
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const ran = variants.filter((v) => v.result)
  // The cards in flight that tail their run over the live lane: a few,
  // never one per card — each stream holds one of the browser's six
  // connections to this origin, and the cards' own polls need the
  // rest. The others read their pages on the 2 s poll.
  const streamed = new Set(
    ran
      .filter((v) => v.result && !settled(v.result.state))
      .slice(0, MAX_LIVE_CARDS)
      .map((v) => v.key)
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b px-4 py-2 text-xs text-muted-foreground">
        <span>Playground ·</span>
        {runtime && agent ? (
          <>
            <select
              aria-label="agent"
              className="rounded border bg-transparent px-1 py-0.5 text-xs text-foreground"
              value={agent.name}
              onChange={(e) => setAgentChoice(e.target.value)}
            >
              {runtime.agents.map((a) => (
                <option key={a.name} value={a.name}>
                  {a.name}
                </option>
              ))}
            </select>
            <span>on</span>
            <select
              aria-label="runtime"
              className="rounded border bg-transparent px-1 py-0.5 text-xs"
              value={runtime.id}
              onChange={(e) => {
                setRuntimeChoice(e.target.value)
                // The agent stays when the new runtime has it.
                setAgentChoice(agent.name)
              }}
            >
              {all.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.service || r.id} · {r.env || "?"} · {r.host}
                </option>
              ))}
            </select>
          </>
        ) : (
          <span>
            {runtimes.isPending
              ? "connecting…"
              : runtimes.isError
                ? `the runtimes could not be read: ${runtimes.error.message}`
                : "no runtime connected — runtime.Install(...) in your app opens the link"}
          </span>
        )}
        {target.mismatch && (
          <span className="text-red-500" role="alert">
            · no connected runtime registers agent {target.mismatch}
            {sourceRow?.agent === target.mismatch ? " (the source run's)" : ""} — this
            would run {agent?.name ?? "nothing"} instead
          </span>
        )}
        {meta.data?.debug_scope && (
          <span className="text-faint">
            · breakpoints &amp; steer act on {meta.data.debug_scope} only (the app's own turns are
            viewer-only)
          </span>
        )}
      </header>
      <div className="flex min-h-0 flex-1">
        {/* The config column (§4's left half). */}
        <section className="w-80 shrink-0 space-y-3 overflow-y-auto border-r p-4">
          {/* The variant switcher (§4's "+ variant"): each column of the
              N-way view is one variant's overrides and run. */}
          <div className="flex items-center gap-1">
            {variants.map((v, i) => (
              <button
                key={v.key}
                onClick={() => setActive(i)}
                className={
                  "rounded border px-2 py-0.5 text-xs " +
                  (i === active ? "border-foreground font-medium" : "text-muted-foreground")
                }
              >
                {v.key}
              </button>
            ))}
            <button
              className="rounded border px-2 py-0.5 text-xs text-muted-foreground"
              onClick={() =>
                setVariants((cur) => [
                  ...cur,
                  {
                    ...cur[active],
                    key: String.fromCharCode("A".charCodeAt(0) + cur.length),
                    result: null,
                  },
                ])
              }
            >
              + variant
            </button>
          </div>
          <h2 className="text-sm font-medium">Variant {variant.key}</h2>
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Source run</span>
            <input
              className="w-full rounded border bg-transparent px-2 py-1 font-mono text-xs"
              value={sourceRunID}
              onChange={(e) => setSourceRunID(e.target.value)}
              placeholder="a run id, or blank for fresh input"
            />
            {sourceRunID && source.isError ? (
              <span className="block text-red-500">{source.error.message}</span>
            ) : null}
          </label>
          {sourceRunID && (
            <label className="block space-y-1">
              <span className="text-xs text-muted-foreground">Continue from step</span>
              <input
                type="number"
                min={0}
                className="w-20 rounded border bg-transparent px-2 py-1 text-xs"
                value={fromStep}
                onChange={(e) =>
                  setFromStep(Math.max(0, Math.floor(Number(e.target.value) || 0)))
                }
              />
            </label>
          )}
          {/* The kept prefix's edits (D2/D3, review fix 4b — the
              panel's per-step fields, rendered here too): when
              continuing from a step, the kept steps' tool results are
              patchable and their call-free replies rewritable. */}
          {sourceRunID && fromStep > 0 && (
            <TranscriptEdits
              runID={sourceRunID}
              fromStep={fromStep}
              drafts={editDrafts}
              setDrafts={setEditDrafts}
            />
          )}
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">System prompt</span>
            <textarea
              rows={4}
              className="w-full rounded border bg-transparent px-2 py-1 text-xs"
              value={variant.instructions}
              onChange={(e) => patch({ instructions: e.target.value })}
            />
            <button
              className="text-xs text-muted-foreground hover:underline"
              onClick={() => patch({ instructions: registered })}
            >
              ↺ reset to the registered prompt
            </button>
          </label>
          {agent?.tools.length ? (
            <div className="space-y-1">
              <span className="text-xs text-muted-foreground">Tools</span>
              {agent.tools.map((t) => (
                <label key={t.name} className="flex items-center gap-2 text-xs">
                  <input
                    type="checkbox"
                    checked={!variant.toolsOff.has(t.name)}
                    onChange={(e) => {
                      const next = new Set(variant.toolsOff)
                      if (e.target.checked) next.delete(t.name)
                      else next.add(t.name)
                      patch({ toolsOff: next })
                    }}
                  />
                  {t.name}
                  {(t.side_effects === "never" || !t.side_effects) && (
                    <span title="side-effect tool (ReplayPolicy never): substitute or park, never re-fire silently — only side effects: allow runs it for real, and only if the app opted it in">
                      ⚠
                    </span>
                  )}
                </label>
              ))}
            </div>
          ) : null}
          <div className="flex gap-2">
            <label className="block flex-1 space-y-1">
              <span className="text-xs text-muted-foreground">Model</span>
              <select
                className="w-full rounded border bg-transparent px-1 py-1 text-xs"
                value={variant.model}
                onChange={(e) => patch({ model: e.target.value })}
              >
                <option value="">(the agent's own)</option>
                {agent?.models.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </label>
            <label className="block space-y-1">
              <span className="text-xs text-muted-foreground">Thinking</span>
              <select
                className="rounded border bg-transparent px-1 py-1 text-xs"
                value={variant.thinking}
                onChange={(e) => patch({ thinking: e.target.value })}
              >
                <option value="">default</option>
                {["off", "low", "medium", "high"].map((l) => (
                  <option key={l} value={l}>
                    {l}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="flex gap-2">
            <label className="block flex-1 space-y-1">
              <span className="text-xs text-muted-foreground">Engine</span>
              <select
                className="w-full rounded border bg-transparent px-1 py-1 text-xs"
                value={variant.engine}
                onChange={(e) => patch({ engine: e.target.value as Engine })}
              >
                <option value="live">live</option>
                <option value="scripted">scripted (zero tokens)</option>
              </select>
            </label>
            <label className="block flex-1 space-y-1">
              <span className="text-xs text-muted-foreground">Side effects</span>
              <select
                className="w-full rounded border bg-transparent px-1 py-1 text-xs"
                value={variant.sideEffects}
                onChange={(e) => patch({ sideEffects: e.target.value as SideEffects })}
              >
                <option
                  value="substitute"
                  title="a side-effect call the source recorded is answered from the record; any other call parks"
                >
                  substitute — recorded results, else park
                </option>
                <option value="park" title="every side-effect call parks; nothing is answered from the record">
                  park — every side-effect call waits
                </option>
                <option value="allow" title="refused unless every tool left on is opted in or ReplaySafe">
                  allow — runs the tools this app opted in (AllowSideEffects) for real
                </option>
              </select>
            </label>
            {/* §5.4's thread mode (review fix 4a): fork continues the
                conversation in a new session with lineage — it needs a
                source turn and an input. */}
            <label className="block flex-1 space-y-1">
              <span className="text-xs text-muted-foreground">Thread</span>
              <select
                className="w-full rounded border bg-transparent px-1 py-1 text-xs"
                value={variant.thread}
                onChange={(e) => patch({ thread: e.target.value as ThreadMode })}
              >
                <option value="ephemeral">ephemeral</option>
                <option value="fork">fork (new session)</option>
              </select>
            </label>
          </div>
          {/* Rung 3 (§8.3): break on tools — PUT /api/runtimes/{id}/
              breakpoints; the runtime parks them on every run it
              starts. */}
          {caps.includes("breakpoints") && runtime && agent?.tools.length ? (
            <div className="space-y-1">
              <span className="text-xs text-muted-foreground">Break on (parks every run)</span>
              <Breakpoints
                key={runtime.id}
                runtimeID={runtime.id}
                stored={runtime.breakpoints}
                tools={[...new Set(runtime.agents.flatMap((a) => a.tools.map((t) => t.name)))]}
              />
            </div>
          ) : null}
          {fromStep === 0 && (
            <label className="block space-y-1">
              <span className="text-xs text-muted-foreground">
                Input (replaces the user message)
              </span>
              <textarea
                rows={2}
                className="w-full rounded border bg-transparent px-2 py-1 text-xs"
                value={variant.input}
                onChange={(e) => patch({ input: e.target.value })}
              />
            </label>
          )}
          {error && (
            <p className="text-xs text-red-500" role="alert">
              {error}
            </p>
          )}
          <Button
            onClick={() => void run()}
            disabled={!runtime || !agent || busy || resolving}
          >
            {busy ? "sending…" : `Run ${variant.key}`}
          </Button>
        </section>
        {/* The runs column (§4's right half): the variant's run, side
            by side with its source once P3 adds the N-way view. */}
        <section className="min-w-0 flex-1 space-y-3 overflow-y-auto p-4">
          {ran.length > 0 ? (
            <>
              {/* The N-way compare (P3): one card per variant, side by
                  side, with the tokens/latency/tool-call metrics row —
                  and the pairwise text diff of the first two below.
                  Each card follows its own command, whichever variant
                  is being edited. */}
              <div className="grid grid-cols-2 gap-3 xl:grid-cols-3">
                {ran.map((v) => (
                  <ResultCard
                    key={v.result!.commandID || v.key}
                    experiment={v.result!}
                    onUpdate={(upd) => setResult(v.key, upd)}
                    sourceText={sourceText}
                    sourceRunID={sourceRunID}
                    variant={v}
                    tools={(agent?.tools ?? [])
                      .map((t) => t.name)
                      .filter((n) => !v.toolsOff.has(n))}
                    caps={caps}
                    live={caps.includes("live") && streamed.has(v.key)}
                    decide={(runID, callID, decision, text) =>
                      decide(v.key, runID, callID, decision, text)
                    }
                  />
                ))}
              </div>
              <CompareTable variants={variants} />
              {ran.length >= 2 && <VariantDiff a={ran[0]} b={ran[1]} />}
            </>
          ) : null}
          <Matrix
            variants={variants}
            runMatrix={runMatrix}
            busy={busy || resolving || !runtime || !agent}
            cells={cells}
            setCells={setCells}
            sourceRunID={sourceRunID}
          />
          <History />
        </section>
      </div>
    </div>
  )
}

/** saveFixtures downloads the run's wefttest replay fixtures, one
 * file each (the browser writes them; Studio never touches the user's
 * tree). tools is the catalogue the run's requests carried — the
 * variant's enabled set — because the fixture key hashes it. */
async function saveFixtures(runID: string, tools: string[]) {
  if (!runID) return
  const doc = await postFixtures(runID, tools)
  for (const f of doc.files) download(f.name, f.body)
}

/** runLabel shortens a run id for the variant card's header. */
function runLabel(runID: string): string {
  const m = /-t(\d+)$/.exec(runID)
  return m ? `t${m[1]}` : runID.slice(-8)
}

/** A run's own final words: the assistant text it produced, straight
 * from the transcript (never the deltas — and never the conversation
 * it was fed: a later turn's input record carries the earlier turns'
 * replies, and an experiment's carries its source's kept prefix). */
async function finalText(runID: string): Promise<string> {
  return producedText((await fetchTranscript(runID)).batches)
}

/** The source run's final text — the diff base. */
function useSourceText(runID: string): string {
  const [text, setText] = useState("")
  useEffect(() => {
    if (!runID) {
      setText("")
      return
    }
    let alive = true
    finalText(runID)
      .then((t) => {
        if (alive) setText(t)
      })
      .catch(() => {
        if (alive) setText("")
      })
    return () => {
      alive = false
    }
  }, [runID])
  return text
}

/** The lifecycle state as shown: a finished command says how its run
 * ended — a failed run is not a success. */
function stateLabel(e: Pick<Experiment, "state" | "status">): string {
  return e.state === "finished" && e.status ? `${e.state} · ${e.status}` : e.state
}

/** Row reads after a command settled, waiting for the run's row to
 * leave running (useCommandTracking). A parked run reads succeeded. */
const ROW_SETTLE_READS = 15

/** Result cards tailing their run live at once (see `streamed`). */
const MAX_LIVE_CARDS = 3

/** True when the lifecycle is over (§10.5): nothing more will change. */
function settled(state: CommandStatus["state"]): boolean {
  return state === "finished" || state === "rejected" || state === "lost"
}

/** A command Studio no longer knows (it restarted: commands live in
 * its memory) will never answer — it reads as lost, not as a poll
 * that spins forever. */
function isUnknownCommand(e: unknown): boolean {
  return e instanceof ApiError && e.status === 404
}

/** useCommandTracking follows the command's lifecycle (§10.5): the
 * run id as soon as the ack names it, the terminal state last. */
function useCommandTracking(
  experiment: Experiment | null,
  setExperiment: Dispatch<SetStateAction<Experiment | null>>
) {
  const setRef = useRef(setExperiment)
  setRef.current = setExperiment
  const commandID = experiment?.commandID ?? ""
  useEffect(() => {
    if (!commandID) return
    let alive = true
    // Read through a function: the cleanup flips the flag while a
    // tick awaits, which control-flow analysis cannot see.
    const gone = (): boolean => !alive
    let timer: ReturnType<typeof setTimeout> | null = null
    const mine = (cur: Experiment | null): cur is Experiment =>
      cur !== null && cur.commandID === commandID
    // Reads of the row after the command settled: the finished ack can
    // land before the run's last records are exported, so the row may
    // still read running (partial usage, no finish) — it is read again
    // until it settles, a bounded number of times.
    let afterSettled = 0
    const tick = async () => {
      let st: CommandStatus
      try {
        st = await fetchCommand(commandID)
      } catch (e) {
        if (gone()) return
        if (isUnknownCommand(e)) {
          setRef.current((cur) =>
            mine(cur)
              ? { ...cur, state: "lost", error: "Studio no longer knows this command (it restarted)" }
              : cur
          )
          return
        }
        timer = setTimeout(() => void tick(), 700)
        return
      }
      if (gone()) return
      setRef.current((cur) =>
        mine(cur)
          ? { ...cur, state: st.state, status: st.status, runID: st.run_id || cur.runID, error: st.error }
          : cur
      )
      let rowSettled = false
      if (st.run_id) {
        // The metrics row (P3: tokens, latency) once the run lands.
        try {
          const row: RunRow = await fetchRun(st.run_id)
          if (gone()) return
          rowSettled = row.status !== "running"
          setRef.current((cur) => (mine(cur) ? { ...cur, row } : cur))
        } catch {
          // the row loads on the next poll
        }
      }
      if (gone()) return
      if (settled(st.state)) {
        if (!st.run_id || rowSettled || ++afterSettled > ROW_SETTLE_READS) return
        timer = setTimeout(() => void tick(), 1000)
        return
      }
      timer = setTimeout(() => void tick(), 700)
    }
    void tick()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
  }, [commandID])
}

/** One parked call's decision verbs (ADR 0007): continue runs the
 * handler for real, skip denies with a reason, resolve pastes a
 * result computed outside the process — which needs the result. */
function PendingCall({
  call,
  decided,
  onDecide,
}: {
  call: { id: string; name: string }
  /** The decision already sent and held for this call, if any (a new
   * one replaces it until the park resumes). */
  decided?: string
  onDecide: (decision: "approve" | "deny" | "resolve", text: string) => void
}) {
  const [text, setText] = useState("")
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="font-mono">{call.name}</span>
      {decided && <span className="text-emerald-500">decided: {decided}</span>}
      <button
        className="text-faint hover:underline"
        title="Approve: the handler runs for real"
        onClick={() => onDecide("approve", "")}
      >
        continue
      </button>
      <button
        className="text-faint hover:underline"
        title="Deny: the model sees a denied result (the text is the reason)"
        onClick={() => onDecide("deny", text)}
      >
        skip
      </button>
      <button
        className="text-faint hover:underline disabled:opacity-50"
        title="Resolve: the text is the tool's result, pasted from outside the process"
        disabled={!text}
        onClick={() => onDecide("resolve", text)}
      >
        resolve
      </button>
      <input
        className="min-w-24 flex-1 rounded border bg-transparent px-2 py-0.5"
        aria-label={`result or reason for ${call.name}`}
        placeholder="result to resolve with · reason to skip"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
    </div>
  )
}

/** ResultCard is one variant's run: the lifecycle, the stream — the
 * stored pages, then the live tail (the run page's own reader, so a
 * run that started, or parked, before this card subscribed is whole)
 * — and the inline diff against the source. */
function ResultCard({
  experiment,
  onUpdate,
  sourceText,
  sourceRunID,
  variant,
  tools,
  caps,
  live,
  decide,
}: {
  experiment: Experiment
  onUpdate: Dispatch<SetStateAction<Experiment | null>>
  sourceText: string
  sourceRunID: string
  variant: Variant
  tools: string[]
  caps: string[]
  /** Tail the run over the live lane (else the 2 s poll). */
  live: boolean
  decide: (
    runID: string,
    callID: string,
    decision: "approve" | "deny" | "resolve",
    text: string
  ) => Promise<void>
}) {
  const [transcriptText, setTranscriptText] = useState<string | null>(null)
  const [steerText, setSteerText] = useState("")
  const [cardErr, setCardErr] = useState("")
  const [deciding, setDeciding] = useState(false)

  useCommandTracking(experiment, onUpdate)

  // The run is over once the command settled AND its row says so: the
  // runtime's finished ack can land before the run's last records are
  // exported (a parked run's run_finish names its pending calls), so
  // the pages are read until the row settles too.
  const over =
    settled(experiment.state) &&
    (experiment.row ? experiment.row.status !== "running" : !experiment.runID)
  const stream = useRunEvents(experiment.runID, over ? "ended" : "running", { live })
  const folded = stream.folded

  // The final words come from the transcript when the run ends — its
  // row settled, not just its command (the ack can beat the export).
  useEffect(() => {
    if (!over || experiment.state !== "finished" || !experiment.runID) return
    let alive = true
    finalText(experiment.runID)
      .then((t) => {
        if (alive && t) setTranscriptText(t)
      })
      .catch(() => {
        // the fold still has the streamed words
      })
    return () => {
      alive = false
    }
  }, [over, experiment.state, experiment.runID])

  const streamedText = useMemo(
    () => folded.steps.map((s) => s.text).filter(Boolean).join("\n"),
    [folded]
  )
  const text = transcriptText ?? streamedText

  const diff = useMemo(
    () => (sourceText && text ? diffLines(sourceText, text) : null),
    [sourceText, text]
  )

  const toolCalls = useMemo(
    () => folded.steps.flatMap((st) => st.toolCalls).map((c) => c.name),
    [folded]
  )
  const decidedCalls =
    experiment.decided && experiment.decided.runID === experiment.runID
      ? experiment.decided.calls
      : {}
  const decidedHere = folded.pending.filter((c) => decidedCalls[c.id]).length

  return (
    <div className="rounded border" data-variant={variant.key}>
      <div className="flex items-center gap-2 border-b px-3 py-2 text-xs">
        <span className="font-medium">{experiment.label}</span>
        <span
          className={experiment.status === "failed" ? "text-red-500" : "text-muted-foreground"}
        >
          {stateLabel(experiment)}
        </span>
        {experiment.runID && (
          <Link
            to="/runs/$id"
            params={{ id: experiment.runID }}
            className="truncate font-mono text-faint hover:underline"
            title="open the run page"
          >
            {experiment.runID}
          </Link>
        )}
        <span className="grow" />
        {/* P4's saves: the fixture is a wefttest replay test of this
            run (D4); keep-as-prompt is the copy-the-text fallback
            (PQ2: the weft/prompt version lands with that module). */}
        <button
          className="text-faint hover:underline disabled:opacity-50"
          title="write this run's records as wefttest replay fixtures"
          disabled={!experiment.runID}
          onClick={() => {
            setCardErr("")
            saveFixtures(experiment.runID, tools).catch((e: unknown) =>
              setCardErr(e instanceof Error ? e.message : String(e))
            )
          }}
        >
          save as fixture
        </button>
        <button
          className="text-faint hover:underline"
          title="copy the edited prompt (weft/prompt versions are post-v1, PQ2)"
          onClick={() => void copyText(variant.instructions)}
        >
          keep as prompt
        </button>
      </div>
      <div className="space-y-2 p-3 text-xs">
        {experiment.error && <p className="text-red-500">{experiment.error}</p>}
        {stream.error && (
          <p className="text-red-500">event stream: {stream.error}</p>
        )}
        {experiment.state === "queued" && !experiment.runID && (
          <p className="text-muted-foreground">waiting for the runtime to ack…</p>
        )}
        {/* A fork's first accepted ack names no run (weft/runtime
            link.go): the session mints the thread turn's id at Send, and
            the runtime acks again naming it once the turn is in flight. */}
        {experiment.state === "accepted" && !experiment.runID && (
          <p className="text-muted-foreground">
            starting — the fork's turn id arrives once the turn is in flight
          </p>
        )}
        {/* The metrics row P3 names: tokens, latency, the tool calls. */}
        <div className="flex flex-wrap gap-2 text-faint">
          {experiment.row && (
            <>
              <span>
                {experiment.row.usage.input_tokens}→{experiment.row.usage.output_tokens} tok
              </span>
              <span>{latency(experiment.row)}</span>
            </>
          )}
          {toolCalls.length > 0 && <span>{toolCalls.join(", ")}</span>}
        </div>
        {text && <div className="whitespace-pre-wrap">{text}</div>}
        {/* The parked calls' decision verbs (ADR 0007) — the panel's
            controls on this surface too (§2's parity rule, review fix
            4's approvals note). */}
        {folded.pending.length > 0 && experiment.runID && (
          <div className="space-y-1 rounded border border-dashed p-2">
            <div className="text-faint">
              awaiting decision
            </div>
            {folded.pending.length > 1 && (
              <div className="text-amber-500">
                the run resumes once every parked call has a decision
                {decidedHere > 0
                  ? ` · ${decidedHere} of ${folded.pending.length} decided`
                  : ""}
              </div>
            )}
            <fieldset disabled={deciding} className="space-y-1">
              {folded.pending.map((c) => (
                <PendingCall
                  key={c.id}
                  call={c}
                  decided={decidedCalls[c.id]}
                  onDecide={(decision, reason) => {
                    // A decision completing the park resumes the run and
                    // replaces this card; one that does not is held, and
                    // the card comes back to the parked run with the call
                    // marked. A refusal (the call is no longer pending —
                    // it was decided elsewhere — or the runtime is gone)
                    // stays on the card, and the row is re-read so the
                    // card shows where the run stands now.
                    setDeciding(true)
                    setCardErr("")
                    decide(experiment.runID, c.id, decision, reason)
                      .catch((e: unknown) => {
                        setCardErr(e instanceof Error ? e.message : String(e))
                        fetchRun(experiment.runID)
                          .then((row) =>
                            onUpdate((cur) => (cur ? { ...cur, row } : cur))
                          )
                          .catch(() => {})
                      })
                      .finally(() => setDeciding(false))
                  }}
                />
              ))}
            </fieldset>
          </div>
        )}
        {/* Rung 4 (§8.4, review fix 4d): steer the in-flight run — one
            user message delivered mid-flight. */}
        {caps.includes("steer") && experiment.state === "accepted" && experiment.runID && (
          <form
            className="flex items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              const message = steerText.trim()
              if (!message) return
              setCardErr("")
              postSteer(experiment.runID, message)
                .then(() => setSteerText(""))
                .catch((err: unknown) =>
                  setCardErr(err instanceof Error ? err.message : String(err))
                )
            }}
          >
            <input
              className="flex-1 rounded border bg-transparent px-2 py-1"
              aria-label="steer message"
              placeholder="a message delivered mid-flight"
              value={steerText}
              onChange={(e) => setSteerText(e.target.value)}
            />
            <button
              type="submit"
              className="text-faint hover:underline disabled:opacity-50"
              title="POST /api/runs/{id}/steer (ADR 0019)"
              disabled={!steerText.trim()}
            >
              steer
            </button>
          </form>
        )}
        {cardErr && <p className="text-xs text-red-500">{cardErr}</p>}
        {diff && (
          <div className="rounded border border-dashed p-2">
            <div className="text-faint">
              {sourceRunID ? `diff vs ${runLabel(sourceRunID)}: ` : "diff: "}
              {diffSummary(diff)}
            </div>
            <DiffRows rows={diff} />
          </div>
        )}
      </div>
    </div>
  )
}

/** The changed lines of a diff: additions and removals. */
function DiffRows({ rows }: { rows: DiffRow[] }) {
  return (
    <>
      {rows
        .filter((r) => r.kind !== "same")
        .map((r, i) => (
          <div
            key={i}
            className={
              r.kind === "add"
                ? "whitespace-pre-wrap text-emerald-500"
                : "whitespace-pre-wrap text-amber-500 line-through"
            }
          >
            {r.kind === "add" ? "+ " : "− "}
            {r.text}
          </div>
        ))}
    </>
  )
}

/** A run's wall time, "…" until it has finished. */
function latency(row: RunRow): string {
  if (!row.finished) return "…"
  return spanMs(Math.max(0, Date.parse(row.finished) - Date.parse(row.started)))
}

/** CompareTable is P3's metrics row across the N variants: the
 * finish, tokens, latency and tool calls of each, one column each. */
function CompareTable({ variants }: { variants: Variant[] }) {
  const ran = variants.flatMap((v) => (v.result?.row ? [{ key: v.key, row: v.result.row }] : []))
  if (ran.length < 2) return null
  return (
    <div className="rounded border">
      <div className="border-b px-3 py-2 text-xs text-muted-foreground">
        compare · {ran.length} variants
      </div>
      <table className="w-full text-xs">
        <thead>
          <tr className="text-left text-faint">
            <th className="px-3 py-1 font-normal">variant</th>
            <th className="px-3 py-1 font-normal">status</th>
            <th className="px-3 py-1 font-normal">tokens</th>
            <th className="px-3 py-1 font-normal">latency</th>
            <th className="px-3 py-1 font-normal">steps</th>
          </tr>
        </thead>
        <tbody>
          {ran.map(({ key, row }) => (
            <tr key={key} className="border-t">
              <td className="px-3 py-1 font-medium">{key}</td>
              <td className="px-3 py-1">
                {row.status}
                {row.pending > 0 ? ` · ${row.pending} parked` : ""}
              </td>
              <td className="px-3 py-1">
                {row.usage.input_tokens}→{row.usage.output_tokens}
              </td>
              <td className="px-3 py-1">{latency(row)}</td>
              <td className="px-3 py-1">{row.steps}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** VariantDiff is the pairwise text diff of the first two run variants
 * (the panel's 2-way view is PQ3's closed rule; Studio's N-way page
 * shows the leading pair and the table above covers the rest). It
 * reads the transcripts once BOTH runs have finished — at the ack the
 * transcript is a prompt and no reply. */
function VariantDiff({ a, b }: { a: Variant; b: Variant }) {
  const [texts, setTexts] = useState<[string, string] | null>(null)
  // Finished means the row settled too: the transcript read at the
  // ack may not hold the run's last words yet.
  const doneID = (v: Variant) =>
    v.result?.state === "finished" && v.result.row && v.result.row.status !== "running"
      ? v.result.runID
      : ""
  const idA = doneID(a)
  const idB = doneID(b)
  useEffect(() => {
    setTexts(null)
    if (!idA || !idB) return
    let alive = true
    const read = (id: string) => finalText(id).catch(() => "")
    void Promise.all([read(idA), read(idB)]).then(([ta, tb]) => {
      if (alive) setTexts([ta, tb])
    })
    return () => {
      alive = false
    }
  }, [idA, idB])
  if (!texts) return null
  const rows = diffLines(texts[0], texts[1])
  if (rows.every((r) => r.kind === "same")) return null
  return (
    <div className="rounded border border-dashed p-3 text-xs">
      <div className="mb-1 text-faint">
        diff {a.key} ↔ {b.key}: {diffSummary(rows)}
      </div>
      <DiffRows rows={rows} />
    </div>
  )
}

/** The next matrix input's key: one past the largest in use, so a
 * removed row's number is never handed out twice (two rows sharing a
 * key share a cell — one result overwrites the other). */
export function nextInputKey(inputs: { key: string }[]): string {
  let max = 0
  for (const i of inputs) {
    const n = Number(i.key)
    if (Number.isInteger(n) && n > max) max = n
  }
  return String(max + 1)
}

/** The rows a matrix runs: with a source run an empty row re-runs the
 * source turn's own words (§5.1: the original exists by default), so
 * every row counts; without one a row needs its text. */
export function matrixInputs<T extends { text: string }>(
  inputs: T[],
  sourceRunID: string
): T[] {
  return inputs.filter((i) => i.text || sourceRunID)
}

/** Matrix is E9's variants × inputs runner: an experiment name, a row
 * per input, one "Run matrix" that saves the definition and issues
 * every cell under its id. The cells' lifecycle states render in the
 * grid; the budget cap (§6 rule 6) is the runtime's — a breach
 * rejects the next command of the experiment, and the cell says why.
 */
function Matrix({
  variants,
  runMatrix,
  busy,
  cells,
  setCells,
  sourceRunID,
}: {
  variants: Variant[]
  runMatrix: (inputs: { key: string; text: string }[], experimentID: string) => Promise<void>
  busy: boolean
  cells: Record<string, Experiment>
  setCells: React.Dispatch<React.SetStateAction<Record<string, Experiment>>>
  sourceRunID: string
}) {
  const [inputs, setInputs] = useState<{ key: string; text: string }[]>([
    { key: "1", text: "" },
  ])
  const [name, setName] = useState("")
  const qc = useQueryClient()
  // The default id is minted once per page, not per render: the id on
  // screen is the one the matrix is saved under.
  const [stamp] = useState(() => new Date().toISOString().slice(0, 19))
  const experimentID = name.trim() || `exp_${stamp}`
  /** Bumped after every poll, so the next one is scheduled even when
   * nothing changed. */
  const [polled, setPolled] = useState(0)

  // Poll the cells' lifecycle while any is queued or accepted.
  useEffect(() => {
    const pending = Object.entries(cells).filter(
      ([, c]) => c.commandID && !settled(c.state)
    )
    if (!pending.length) return
    let alive = true
    const timer = setTimeout(
      () => {
        void Promise.all(
          pending.map(async ([key, c]) => {
            try {
              return { key, id: c.commandID, st: await fetchCommand(c.commandID) }
            } catch (e) {
              // An unknown command never answers: it is lost.
              return isUnknownCommand(e)
                ? { key, id: c.commandID, st: null }
                : undefined // the next tick retries
            }
          })
        ).then((updates) => {
          if (!alive) return
          setCells((cur) => {
            let next = cur
            for (const u of updates) {
              // The cell may have been replaced by a newer matrix.
              if (!u || !(u.key in next) || next[u.key].commandID !== u.id) continue
              const cell = next[u.key]
              next = {
                ...next,
                [u.key]: u.st
                  ? {
                      ...cell,
                      state: u.st.state,
                      status: u.st.status,
                      runID: u.st.run_id || cell.runID,
                      error: u.st.error,
                    }
                  : { ...cell, state: "lost", error: "Studio no longer knows this command (it restarted)" },
              }
            }
            return next
          })
          setPolled((n) => n + 1)
          void qc.invalidateQueries({ queryKey: ["experiments"] })
        })
      },
      // A large matrix polls gently: every cell is one request.
      Math.min(3000, 800 + pending.length * 50)
    )
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [cells, polled, qc, setCells])

  const inputKeys = [...new Set(Object.keys(cells).map((k) => k.split("\u0000")[1]))]
  return (
    <div className="rounded border">
      <div className="border-b px-3 py-2 text-xs text-muted-foreground">
        Experiment matrix · {variants.length} variants × {inputs.length} inputs
        {sourceRunID ? " · over the source run" : ""}
      </div>
      <div className="space-y-2 p-3 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <input
            className="rounded border bg-transparent px-2 py-1"
            placeholder="experiment name"
            aria-label="experiment name"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <code className="text-faint">{experimentID}</code>
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => void runMatrix(matrixInputs(inputs, sourceRunID), experimentID)}
          >
            Run matrix
          </Button>
        </div>
        {inputs.map((inp, i) => (
          <div key={inp.key} className="flex items-center gap-2">
            <span className="w-6 text-faint">{inp.key}</span>
            <input
              className="flex-1 rounded border bg-transparent px-2 py-1"
              aria-label={`input ${inp.key}`}
              placeholder={
                sourceRunID
                  ? "replaces the source turn's message (blank: its own words)"
                  : "the input"
              }
              value={inp.text}
              onChange={(e) =>
                setInputs((cur) =>
                  cur.map((x, j) => (j === i ? { ...x, text: e.target.value } : x))
                )
              }
            />
            {inputs.length > 1 && (
              <button
                className="text-faint hover:underline"
                aria-label={`remove input ${inp.key}`}
                onClick={() => setInputs((cur) => cur.filter((_, j) => j !== i))}
              >
                −
              </button>
            )}
          </div>
        ))}
        <button
          className="text-faint hover:underline"
          onClick={() => setInputs((cur) => [...cur, { key: nextInputKey(cur), text: "" }])}
        >
          + input
        </button>
        {inputKeys.length > 0 && (
          <table className="w-full">
            <thead>
              <tr className="text-left text-faint">
                <th className="py-1 font-normal">input</th>
                {variants.map((v) => (
                  <th key={v.key} className="py-1 font-normal">
                    variant {v.key}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {inputKeys.map((ik) => (
                <tr key={ik} className="border-t">
                  <td className="py-1">{ik}</td>
                  {variants.map((v) => {
                    const cell = cells[`${v.key}\u0000${ik}`] as Experiment | undefined
                    return (
                      <td key={v.key} className="py-1" data-cell={`${v.key}×${ik}`}>
                        {cell ? (
                          <span
                            className={
                              cell.state === "finished" && cell.status !== "failed"
                                ? "text-emerald-500"
                                : cell.state === "rejected" ||
                                    cell.state === "lost" ||
                                    cell.status === "failed"
                                  ? "text-red-500"
                                  : "text-muted-foreground"
                            }
                          >
                            {cell.runID ? (
                              <Link
                                to="/runs/$id"
                                params={{ id: cell.runID }}
                                className="hover:underline"
                                title={cell.runID}
                              >
                                {stateLabel(cell)}
                              </Link>
                            ) : (
                              stateLabel(cell)
                            )}
                            {cell.error ? ` (${cell.error})` : ""}
                          </span>
                        ) : (
                          <span className="text-faint">—</span>
                        )}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

/** History is the experiment list (§4: "experiment history"): the
 * saved definitions with their matrix sizes. */
function History() {
  const experiments = useQuery(experimentsQuery())
  const list = experiments.data?.experiments ?? []
  if (experiments.isError)
    return (
      <p className="text-xs text-red-500">
        the experiment history could not be read: {experiments.error.message}
      </p>
    )
  if (!list.length) return null
  return (
    <div className="rounded border">
      <div className="border-b px-3 py-2 text-xs text-muted-foreground">
        Experiment history
      </div>
      <div className="divide-y text-xs">
        {list.map((e) => (
          <div key={e.id} className="flex items-center gap-2 px-3 py-1.5">
            <code className="text-faint">{e.id}</code>
            <span>{e.name || "—"}</span>
            <span className="text-faint">{e.agent}</span>
            <span className="text-faint">
              {(e.variants as unknown[] | null)?.length ?? 0}×
              {(e.inputs as unknown[] | null)?.length ?? 0}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

/** TranscriptEdits renders the kept prefix's editable fields (review
 * fix 4b): when continuing from a step, the kept steps' tool results
 * are patchable and their call-free replies rewritable — the
 * counterfactual the fresh step answers. The panel's drawer is the
 * shape; this is the same wire. */
function TranscriptEdits({
  runID,
  fromStep,
  drafts,
  setDrafts,
}: {
  runID: string
  fromStep: number
  drafts: EditDraft[]
  setDrafts: React.Dispatch<React.SetStateAction<EditDraft[]>>
}) {
  const all = useSourceEditFields(runID)
  const fields = all.filter((f) => f.step < fromStep)
  // A draft belongs to a field of the kept prefix: one left over from
  // another source run, or from a step no longer kept, would be sent
  // and refused (400) with nothing on screen to explain it.
  const fieldKeys = fields.map((f) => `${f.step}\u0000${f.callID ?? ""}`).join("\u0001")
  useEffect(() => {
    const keep = new Set(fieldKeys ? fieldKeys.split("\u0001") : [])
    setDrafts((cur) => {
      const next = cur.filter((d) => keep.has(`${d.step}\u0000${d.callID ?? ""}`))
      return next.length === cur.length ? cur : next
    })
  }, [fieldKeys, setDrafts])
  if (!fields.length) return null
  const draftOf = (f: EditField) => drafts.find((d) => d.step === f.step && d.callID === f.callID)
  const set = (f: EditField, v: string) => {
    const at = (d: EditDraft) => d.step === f.step && d.callID === f.callID
    setDrafts((cur) => {
      const i = cur.findIndex(at)
      if (v === "") return i >= 0 ? cur.filter((_, j) => j !== i) : cur
      const draft: EditDraft = f.callID
        ? { step: f.step, callID: f.callID, toolResult: v }
        : { step: f.step, content: v }
      return i >= 0 ? cur.map((d, j) => (j === i ? draft : d)) : [...cur, draft]
    })
  }
  return (
    <div className="space-y-1">
      <span className="text-xs text-muted-foreground">
        Transcript edits (steps 0..{fromStep - 1} are kept)
      </span>
      {fields.map((f) =>
        f.callID ? (
          <label key={`${f.step}:${f.callID}`} className="flex items-center gap-1 text-xs">
            <span className="shrink-0 text-faint">
              step {f.step} · {f.name} →
            </span>
            <input
              className="w-full rounded border bg-transparent px-2 py-1"
              placeholder={f.placeholder.slice(0, 60)}
              value={draftOf(f)?.toolResult ?? ""}
              onChange={(e) => set(f, e.target.value)}
            />
          </label>
        ) : (
          <label key={`${f.step}:reply`} className="block space-y-1 text-xs">
            <span className="text-faint">step {f.step} · reply</span>
            <textarea
              rows={2}
              className="w-full rounded border bg-transparent px-2 py-1"
              placeholder={f.placeholder.slice(0, 80)}
              value={draftOf(f)?.content ?? ""}
              onChange={(e) => set(f, e.target.value)}
            />
          </label>
        )
      )}
    </div>
  )
}

/** useSourceEditFields loads every editable field of the source
 * transcript (the fromStep filter is applied at render, so a changed
 * step needs no refetch). */
function useSourceEditFields(runID: string): EditField[] {
  const [fields, setFields] = useState<EditField[]>([])
  useEffect(() => {
    setFields([])
    if (!runID) return
    let alive = true
    fetchTranscript(runID)
      .then((doc) => {
        if (alive) setFields(editFieldsOf(doc.batches, Number.MAX_SAFE_INTEGER))
      })
      .catch(() => {
        if (alive) setFields([])
      })
    return () => {
      alive = false
    }
  }, [runID])
  return fields
}

/** Breakpoints is the rung-3 control (§8.3): one checkbox per tool,
 * applied on change — the runtime parks them on every run it starts
 * from then on, whatever the command asked for. The set is the
 * runtime's own: the boxes show what GET /api/runtimes reports
 * (`stored`, so a reload — or another tab's change — is reflected)
 * and, after a change, what the PUT answered; a refused change (a
 * disconnected runtime is a 503 and stores nothing) is undone and
 * says why. */
export function Breakpoints({
  runtimeID,
  tools,
  stored,
}: {
  runtimeID: string
  tools: string[]
  /** The runtime's stored set, as the runtimes view last read it. */
  stored?: string[]
}) {
  const [set, setSet] = useState<Set<string>>(() => new Set(stored ?? []))
  // Follow the server's set when it changes under us (the view is
  // re-read every few seconds) — by value, not by array identity.
  const storedKey = stored ? [...stored].sort().join("\u0000") : null
  useEffect(() => {
    if (storedKey !== null)
      setSet(new Set(storedKey ? storedKey.split("\u0000") : []))
  }, [storedKey])
  const [err, setErr] = useState("")
  const [saving, setSaving] = useState(false)
  const toggle = async (name: string, on: boolean) => {
    const prev = set
    const next = new Set(set)
    if (on) next.add(name)
    else next.delete(name)
    setSet(next)
    setErr("")
    setSaving(true)
    try {
      const out = await putBreakpoints(runtimeID, [...next].sort())
      setSet(new Set(out.tools ?? []))
    } catch (e) {
      setSet(prev)
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap gap-2 text-xs">
        {tools.map((t) => (
          <label key={t} className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={set.has(t)}
              disabled={saving}
              onChange={(e) => void toggle(t, e.target.checked)}
            />
            {t}
          </label>
        ))}
      </div>
      {err && (
        <p className="text-xs text-red-500" role="alert">
          {err}
        </p>
      )}
    </div>
  )
}
