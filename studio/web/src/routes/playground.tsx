// The Studio playground (WEFT-PLAYGROUND §4, P1's Studio half): the
// split view — the experiment's config on the left, the runs side by
// side on the right, the diff on top. One variant for now (P3's N-way
// compare and P5's variants × inputs matrix grow this page); the
// panel hands off into it with the context carried over (run, step,
// current overrides), so nothing is retyped (§2's parity rule).
//
//   /playground?run=<id>&step=N&instructions=…&tools=a,b&model=…&input=…
//
// The same drawer fields as the panel (prompt, tools off, model,
// thinking, input), the same POST /api/playground/runs, the same live
// result — V6: one API, two clients.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useSearch } from "@tanstack/react-router"
import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react"

import {
  apiBase,
  asTranscript,
  metaQuery,
  experimentsQuery,
  postExperiment,
  postFixtures,
  postApproval,
  postSteer,
  fetchCommand,
  postPlaygroundRun,
  runtimesQuery,
  type AgentView,
  type CommandStatus,
  type Message,
  type PlaygroundRunBody,
  type RunRow,
  type WireEvent,
  studioToken,
} from "@/lib/api"
import { fold } from "@/lib/events"
import { diffLines, diffSummary } from "@/lib/diff"
import { openLive } from "@/lib/live"
import { useCapabilities } from "@/hooks/use-capabilities"
import { Button } from "@/components/ui/button"

/** What the panel carries over (§2: run, step, current overrides). */
interface PlaygroundSearch {
  run?: string
  step?: number
  instructions?: string
  tools?: string
  model?: string
  thinking?: string
  input?: string
}

export const Route = createFileRoute("/playground")({
  validateSearch: (search: Record<string, unknown>): PlaygroundSearch => ({
    run: typeof search.run === "string" && search.run ? search.run : undefined,
    step: typeof search.step === "number" && search.step > 0 ? search.step : undefined,
    instructions:
      typeof search.instructions === "string" && search.instructions
        ? search.instructions
        : undefined,
    tools: typeof search.tools === "string" && search.tools ? search.tools : undefined,
    model: typeof search.model === "string" && search.model ? search.model : undefined,
    thinking:
      typeof search.thinking === "string" && search.thinking ? search.thinking : undefined,
    input: typeof search.input === "string" && search.input ? search.input : undefined,
  }),
  component: PlaygroundPage,
})

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
  runID: string
  error: string | null
  label: string
  row: RunRow | null
  events: { pos: number; event: unknown }[]
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
  engine: "live" | "scripted"
  sideEffects: "substitute" | "park" | "allow"
  /** §5.4's thread mode (review fix 4a): ephemeral — an experiment,
   * never a turn — or fork, a new session with lineage whose next
   * turn is the input. */
  thread: "ephemeral" | "fork"
  result: Experiment | null
}

/** variantA seeds the first variant from the panel's carried-over
 * context (§2: run, step, current overrides — nothing retyped). */
function variantA(search: PlaygroundSearch, agent?: AgentView): Variant {
  const on = search.tools ? new Set(search.tools.split(",")) : null
  return {
    key: "A",
    instructions: search.instructions ?? "",
    toolsOff: new Set(
      agent && on ? agent.tools.map((t) => t.name).filter((n) => !on.has(n)) : []
    ),
    model: search.model ?? "",
    thinking: search.thinking ?? "",
    input: search.input ?? "",
    engine: "live",
    sideEffects: "substitute",
    thread: "ephemeral",
    result: null,
  }
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
 * panel's per-step fields are the shape (element.ts's drawer). */
export function editFieldsOf(
  batches: { step: number; messages: Message[] }[],
  fromStep: number
): EditField[] {
  const steps = new Map<number, { results: { callID: string; name: string; content: string }[]; text: string; hadCalls: boolean }>()
  for (const b of batches) {
    if (b.step >= fromStep) continue
    let st = steps.get(b.step)
    if (!st) {
      st = { results: [], text: "", hadCalls: false }
      steps.set(b.step, st)
    }
    for (const m of b.messages) {
      if (m.role === "assistant") {
        for (const p of m.content) {
          if (p.type === "tool_call") st.hadCalls = true
          if (p.type === "text" && p.text) st.text += p.text
        }
      } else if (m.role === "tool") {
        for (const p of m.content) {
          if (p.type === "tool_result")
            st.results.push({ callID: p.call_id, name: p.name, content: p.content })
        }
      }
    }
  }
  const fields: EditField[] = []
  for (const [step, st] of [...steps.entries()].sort((a, b) => a[0] - b[0])) {
    for (const r of st.results)
      fields.push({ step, callID: r.callID, name: r.name || r.callID, placeholder: r.content })
    if (st.text && !st.hadCalls)
      fields.push({ step, name: "reply", placeholder: st.text })
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
  const search = useSearch({ from: "/playground" })
  const runtimes = useQuery(runtimesQuery())
  const meta = useQuery(metaQuery())

  const firstRuntime = runtimes.data?.runtimes.find((r) => r.agents.length > 0)
  const agent: AgentView | undefined = firstRuntime?.agents[0]

  // The variants (§4): A starts as the original, more are added with
  // "+ variant"; each carries its own overrides and its own run. The
  // panel's carried-over context seeds A (nothing is retyped).
  const [variants, setVariants] = useState<Variant[]>(() => [
    variantA(search, agent),
  ])
  const [active, setActive] = useState(0)
  const variant = variants[active] ?? variants[0]
  const [error, setError] = useState("")
  /** The E9 matrix: the cells' experiments, keyed variant×input. */
  const [cells, setCells] = useState<Record<string, Experiment>>({})

  const [sourceRunID, setSourceRunID] = useState(search.run ?? "")
  const [fromStep, setFromStep] = useState(search.step ?? 0)
  /** The kept prefix's edits (review fix 4b): patched tool results and
   * rewritten call-free replies — the counterfactual the fresh step
   * answers. Source-shaped, not variant-shaped, so they live here. */
  const [editDrafts, setEditDrafts] = useState<EditDraft[]>([])

  const registered = agent?.instructions ?? ""
  useEffect(() => {
    if (search.instructions === undefined && registered && !variant.instructions)
      patch({ instructions: registered })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [registered])
  useEffect(() => {
    if (search.run) setSourceRunID(search.run)
  }, [search.run])

  const patch = (p: Partial<Variant>) =>
    setVariants((cur) => cur.map((v, i) => (i === active ? { ...v, ...p } : v)))

  const sourceText = useSourceText(sourceRunID)

  /** decide answers one parked call of the variant's run with the
   * approval verbs (ADR 0007) — the panel's controls, rendered here
   * too (§2's parity rule). The resumed run replaces the card. */
  const decide = async (
    runID: string,
    callID: string,
    decision: "approve" | "deny" | "resolve"
  ) => {
    try {
      const out = await postApproval(runID, { call_id: callID, decision })
      setVariants((cur) =>
        cur.map((v, i) =>
          i === active
            ? {
                ...v,
                result: {
                  commandID: out.command_id,
                  state: "queued",
                  runID: "",
                  error: null,
                  label: v.result?.label ?? v.key,
                  row: null,
                  events: [],
                },
              }
            : v
        )
      )
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const run = async () => {
    setError("")
    if (!firstRuntime || !agent) return
    const overrides: NonNullable<PlaygroundRunBody["overrides"]> = {}
    if (variant.instructions && variant.instructions !== registered)
      overrides.instructions = variant.instructions
    const enabled = (agent?.tools ?? []).map((t) => t.name).filter((n) => !variant.toolsOff.has(n))
    if (variant.toolsOff.size) overrides.tools_enabled = enabled
    if (variant.model) overrides.model = variant.model
    if (variant.thinking) overrides.thinking = variant.thinking
    const body: PlaygroundRunBody = {
      runtime: firstRuntime.id,
      agent: agent.name,
      source: sourceRunID ? { run_id: sourceRunID, from_step: fromStep } : null,
      input: fromStep === 0 && variant.input ? variant.input : undefined,
      overrides,
      engine: variant.engine,
      side_effects: variant.sideEffects,
      thread: variant.thread,
    }
    if (fromStep > 0 && editDrafts.length) body.transcript_edits = wireEdits(editDrafts)
    try {
      const out = await postPlaygroundRun(body)
      const experiment: Experiment = {
        commandID: out.command_id,
        state: "queued",
        runID: "",
        error: null,
        label: `${variant.key}${sourceRunID ? ` · ${runLabel(sourceRunID)}` : ""}`,
        row: null,
        events: [],
      }
      setVariants((cur) => cur.map((v, i) => (i === active ? { ...v, result: experiment } : v)))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  /** runMatrix is E9's verb: save the definition, then one command
   * per variant × input cell, all under the experiment's id (the
   * budget caps the whole matrix — §6 rule 6). */
  const runMatrix = async (inputs: { key: string; text: string }[], experimentID: string) => {
    setError("")
    if (!firstRuntime || !agent) return
    const next: Record<string, Experiment> = {}
    try {
      await postExperiment({
        id: experimentID,
        name: experimentID,
        agent: agent.name,
        variants: variants.map((v) => ({
          key: v.key,
          overrides: {
            ...(v.instructions && v.instructions !== registered
              ? { instructions: v.instructions }
              : {}),
            ...(v.toolsOff.size
              ? { tools_enabled: agent.tools.map((t) => t.name).filter((n) => !v.toolsOff.has(n)) }
              : {}),
            ...(v.model ? { model: v.model } : {}),
            ...(v.thinking ? { thinking: v.thinking } : {}),
          },
        })),
        inputs: inputs.map((i) => ({
          key: i.key,
          ...(sourceRunID ? { source_run_id: sourceRunID } : {}),
          text: i.text,
        })),
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return
    }
    for (const v of variants) {
      for (const i of inputs) {
        const overrides: NonNullable<PlaygroundRunBody["overrides"]> = {}
        if (v.instructions && v.instructions !== registered)
          overrides.instructions = v.instructions
        const enabled = (agent?.tools ?? []).map((t) => t.name).filter((n) => !v.toolsOff.has(n))
        if (v.toolsOff.size) overrides.tools_enabled = enabled
        if (v.model) overrides.model = v.model
        if (v.thinking) overrides.thinking = v.thinking
        try {
          const out = await postPlaygroundRun({
            runtime: firstRuntime.id,
            agent: agent.name,
            source: sourceRunID ? { run_id: sourceRunID, from_step: 0 } : null,
            input: i.text || undefined,
            overrides,
            engine: v.engine,
            side_effects: v.sideEffects,
            thread: v.thread,
            experiment_id: experimentID,
          })
          next[`${v.key}\u0000${i.key}`] = {
            commandID: out.command_id,
            state: "queued",
            runID: "",
            error: null,
            label: `${v.key}×${i.key}`,
            row: null,
            events: [],
          }
        } catch (e) {
          setError(e instanceof Error ? e.message : String(e))
          return
        }
      }
    }
    setCells((cur) => ({ ...cur, ...next }))
  }

  useCommandTracking(variant?.result ?? null, (upd) =>
    setVariants((cur) =>
      cur.map((v, i) => {
        if (i !== active) return v
        const next = typeof upd === "function" ? upd(v.result) : upd
        return next ? { ...v, result: next } : v
      })
    )
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="border-b px-4 py-2 text-xs text-muted-foreground">
        Playground · {agent?.name ?? "no runtime connected"} ·{" "}
        {firstRuntime ? `${firstRuntime.service} · ${firstRuntime.env || "?"} · ${firstRuntime.host}` : "—"}
        {runtimes.isPending && <span className="text-faint">connecting…</span>}
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
          </label>
          {sourceRunID && (
            <label className="block space-y-1">
              <span className="text-xs text-muted-foreground">Continue from step</span>
              <input
                type="number"
                min={0}
                className="w-20 rounded border bg-transparent px-2 py-1 text-xs"
                value={fromStep}
                onChange={(e) => setFromStep(Number(e.target.value) || 0)}
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
                    <span title="side-effect tool (ReplayPolicy never): substitute or park, never re-fire silently">
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
                onChange={(e) => patch({ engine: e.target.value as "live" | "scripted" })}
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
                onChange={(e) =>
                  patch({ sideEffects: e.target.value as "substitute" | "park" | "allow" })
                }
              >
                <option value="substitute">substitute</option>
                <option value="park">park</option>
                <option value="allow">allow</option>
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
                onChange={(e) => patch({ thread: e.target.value as "ephemeral" | "fork" })}
              >
                <option value="ephemeral">ephemeral</option>
                <option value="fork">fork (new session)</option>
              </select>
            </label>
          </div>
          {/* Rung 3 (§8.3): break on tools — PUT /api/runtimes/{id}/
              breakpoints; the runtime parks them on every run it
              starts. */}
          {caps.includes("breakpoints") && agent?.tools.length ? (
            <div className="space-y-1">
              <span className="text-xs text-muted-foreground">Break on (parks every run)</span>
              <Breakpoints runtimeID={firstRuntime?.id ?? ""} tools={agent.tools.map((t) => t.name)} />
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
          {error && <p className="text-xs text-red-500">{error}</p>}
          <Button onClick={() => void run()} disabled={!firstRuntime || !agent}>
            Run {variant.key}
          </Button>
        </section>
        {/* The runs column (§4's right half): the variant's run, side
            by side with its source once P3 adds the N-way view. */}
        <section className="min-w-0 flex-1 space-y-3 overflow-y-auto p-4">
          {variants.some((v) => v.result) ? (
            <>
              {/* The N-way compare (P3): one card per variant, side by
                  side, with the tokens/latency/tool-call metrics row —
                  and the pairwise text diff of the first two below. */}
              <div className="grid grid-cols-2 gap-3 xl:grid-cols-3">
                {variants
                  .filter((v) => v.result)
                  .map((v) => (
                    <ResultCard
                      key={v.result!.commandID || v.key}
                      experiment={v.result!}
                      sourceText={sourceText}
                      sourceRunID={sourceRunID}
                      variant={v}
                      tools={agent?.tools.map((t) => t.name) ?? []}
                      caps={caps}
                      decide={decide}
                    />
                  ))}
              </div>
              <CompareTable variants={variants} />
              {variants.filter((v) => v.result).length >= 2 && (
                <VariantDiff
                  a={variants.filter((v) => v.result)[0]!}
                  b={variants.filter((v) => v.result)[1]!}
                />
              )}
              <Matrix
                variants={variants}
                runMatrix={runMatrix}
                cells={cells}
                setCells={setCells}
                sourceRunID={sourceRunID}
              />
              <History />
            </>
          ) : (
            <>
              <Matrix
                variants={variants}
                runMatrix={runMatrix}
                cells={cells}
                setCells={setCells}
                sourceRunID={sourceRunID}
              />
              <History />
            </>
          )}
        </section>
      </div>
    </div>
  )
}

/** getJSON is the raw transcript read both text effects share (the
 * final words come from the transcript, never the deltas). */
interface RawTranscript {
  batches: { index: number; step: number; messages: unknown }[]
}

async function getJSON(path: string): Promise<RawTranscript | RunRow> {
  const headers: Record<string, string> = { Accept: "application/json" }
  const tok = studioToken()
  if (tok) headers.Authorization = `Bearer ${tok}`
  const res = await fetch(new URL(path, apiBase()).toString(), { headers })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return (await res.json()) as RawTranscript | RunRow
}

/** saveFixtures downloads the run's wefttest replay fixtures, one
 * file each (the browser writes them; Studio never touches the user's
 * tree). */
async function saveFixtures(runID: string, tools: string[]) {
  if (!runID) return
  const doc = await postFixtures(runID, tools)
  for (const f of doc.files) {
    const url = URL.createObjectURL(new Blob([f.body], { type: "application/json" }))
    const a = document.createElement("a")
    a.href = url
    a.download = f.name
    a.click()
    URL.revokeObjectURL(url)
  }
}

/** runLabel shortens a run id for the variant card's header. */
function runLabel(runID: string): string {
  const m = /-t(\d+)$/.exec(runID)
  return m ? `t${m[1]}` : runID.slice(-8)
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
    void (async () => {
      try {
        const doc = asTranscript((await getJSON(`runs/${encodeURIComponent(runID)}/transcript`)) as RawTranscript)
        if (!alive) return
        // The source's own words: the assistant text parts straight
        // from the transcript (final words, never deltas).
        const parts: string[] = []
        for (const b of doc.batches)
          for (const m of b.messages)
            if (m.role === "assistant")
              for (const p of m.content)
                if (p.type === "text" && p.text) parts.push(p.text)
        setText(parts.join("\n"))
      } catch {
        if (alive) setText("")
      }
    })()
    return () => {
      alive = false
    }
  }, [runID])
  return text
}

/** useCommandTracking follows the command's lifecycle (§10.5): the
 * run id as soon as the ack names it, the terminal state last. */
function useCommandTracking(
  experiment: Experiment | null,
  setExperiment: Dispatch<SetStateAction<Experiment | null>>
) {
  const setRef = useRef(setExperiment)
  setRef.current = setExperiment
  useEffect(() => {
    if (!experiment || !experiment.commandID) return
    let alive = true
    let timer: ReturnType<typeof setTimeout> | null = null
    const tick = async () => {
      if (!alive) return
      let st: CommandStatus
      try {
        st = await fetchCommand(experiment.commandID)
      } catch {
        if (alive) timer = setTimeout(tick, 700)
        return
      }
      if (!alive) return
      setRef.current((cur) =>
        cur && cur.commandID === experiment.commandID
          ? { ...cur, state: st.state, runID: st.run_id || cur.runID, error: st.error }
          : cur
      )
      if (st.run_id) {
        // The metrics row (P3: tokens, latency) once the run lands.
        try {
          const row = (await getJSON(`runs/${encodeURIComponent(st.run_id)}`)) as unknown as RunRow
          setRef.current((cur) =>
            cur && cur.commandID === experiment.commandID ? { ...cur, row } : cur
          )
        } catch {
          // the row loads on the next poll
        }
      }
      if (st.state === "finished" || st.state === "rejected" || st.state === "lost") return
      timer = setTimeout(tick, 700)
    }
    void tick()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
  }, [experiment?.commandID])
}

/** ResultCard is one variant's run: the lifecycle, the stream in
 * place from /api/live, the inline diff against the source. */
function ResultCard({
  experiment,
  sourceText,
  sourceRunID,
  variant,
  tools,
  caps,
  decide,
}: {
  experiment: Experiment
  sourceText: string
  sourceRunID: string
  variant: Variant
  tools: string[]
  caps: string[]
  decide: (runID: string, callID: string, decision: "approve" | "deny" | "resolve") => Promise<void>
}) {
  const [events, setEvents] = useState<{ pos: number; event: WireEvent }[]>([])
  const [transcriptText, setTranscriptText] = useState<string | null>(null)
  const [steerText, setSteerText] = useState("")
  const [steerErr, setSteerErr] = useState("")

  // The live tail of the run the ack named.
  useEffect(() => {
    if (!experiment.runID) return
    const handle = openLive({
      selector: { run: experiment.runID },
      kinds: ["event", "delta", "run"],
      onRecord: (rec) =>
        setEvents((cur) => [...cur, { pos: Number(rec.pos), event: rec.event }]),
      onOverflow: () => setEvents([]),
    })
    return () => handle.close()
  }, [experiment.runID])

  // The final words come from the transcript when the run ends.
  useEffect(() => {
    if (experiment.state !== "finished" || !experiment.runID) return
    let alive = true
    void (async () => {
      try {
        const doc = asTranscript((await getJSON(`runs/${encodeURIComponent(experiment.runID)}/transcript`)) as RawTranscript)
        if (!alive) return
        const parts: string[] = []
        for (const b of doc.batches)
          for (const m of b.messages)
            if (m.role === "assistant")
              for (const p of m.content)
                if (p.type === "text" && p.text) parts.push(p.text)
        setTranscriptText(parts.join("\n"))
      } catch {
        // the fold still has the streamed words
      }
    })()
    return () => {
      alive = false
    }
  }, [experiment.state, experiment.runID])

  const folded = useMemo(() => fold(events.map((e) => e.event)), [events])
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
    () =>
      folded.steps
        .flatMap((st) => st.toolCalls)
        .map((c) => `${c.name}(${c.args === undefined ? "" : JSON.stringify(c.args)})`),
    [folded]
  )

  return (
    <div className="rounded border">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-xs">
        <span className="font-medium">{experiment.label}</span>
        <span className="text-muted-foreground">{experiment.state}</span>
        {experiment.runID && (
          <span className="font-mono text-faint">{experiment.runID}</span>
        )}
        <span className="grow" />
        {/* P4's saves: the fixture is a wefttest replay test of this
            run (D4); keep-as-prompt is the copy-the-text fallback
            (PQ2: the weft/prompt version lands with that module). */}
        <button
          className="text-faint hover:underline"
          title="write this run's records as wefttest replay fixtures"
          onClick={() => void saveFixtures(experiment.runID, tools)}
        >
          save as fixture
        </button>
        <button
          className="text-faint hover:underline"
          title="copy the edited prompt (weft/prompt versions are post-v1, PQ2)"
          onClick={() => void navigator.clipboard?.writeText(variant.instructions)}
        >
          keep as prompt
        </button>
      </div>
      <div className="space-y-2 p-3 text-xs">
        {experiment.error && <p className="text-red-500">{experiment.error}</p>}
        {experiment.state === "queued" && !experiment.runID && (
          <p className="text-muted-foreground">waiting for the runtime to ack…</p>
        )}
        {/* The metrics row P3 names: tokens, latency, the tool calls. */}
        <div className="flex flex-wrap gap-2 text-faint">
          {experiment.row && (
            <>
              <span>
                {experiment.row.usage.input_tokens}→{experiment.row.usage.output_tokens} tok
              </span>
              <span>
                {experiment.row.finished
                  ? `${Math.max(
                      0,
                      Date.parse(experiment.row.finished) - Date.parse(experiment.row.started)
                    )}ms`
                  : "…"}
              </span>
            </>
          )}
          {toolCalls.length > 0 && <span>{toolCalls.map((c) => c.split("(")[0]).join(", ")}</span>}
        </div>
        {text && <div className="whitespace-pre-wrap">{text}</div>}
        {/* The parked calls' decision verbs (ADR 0007) — the panel's
            controls on this surface too (§2's parity rule, review fix
            4's approvals note). */}
        {folded.pending.length > 0 && experiment.runID && (
          <div className="rounded border border-dashed p-2">
            <div className="mb-1 text-faint">awaiting decision</div>
            {folded.pending.map((c) => (
              <div key={c.id} className="flex items-center gap-2">
                <span className="font-mono">{c.name}</span>
                <button
                  className="text-faint hover:underline"
                  title="Approve: the handler runs for real"
                  onClick={() => void decide(experiment.runID, c.id, "approve")}
                >
                  continue
                </button>
                <button
                  className="text-faint hover:underline"
                  title="Deny: the model sees a denied result"
                  onClick={() => void decide(experiment.runID, c.id, "deny")}
                >
                  skip
                </button>
                <button
                  className="text-faint hover:underline"
                  title="Resolve with a result pasted outside the process"
                  onClick={() => void decide(experiment.runID, c.id, "resolve")}
                >
                  resolve…
                </button>
              </div>
            ))}
          </div>
        )}
        {/* Rung 4 (§8.4, review fix 4d): steer the in-flight run — one
            user message delivered mid-flight. */}
        {caps.includes("steer") && experiment.state === "accepted" && experiment.runID && (
          <div className="flex items-center gap-2">
            <input
              className="flex-1 rounded border bg-transparent px-2 py-1"
              placeholder="a message delivered mid-flight"
              value={steerText}
              onChange={(e) => setSteerText(e.target.value)}
            />
            <button
              className="text-faint hover:underline"
              title="POST /api/runs/{id}/steer (ADR 0019)"
              onClick={() => {
                const message = steerText
                setSteerText("")
                setSteerErr("")
                void postSteer(experiment.runID, message)
                  .then(() => undefined)
                  .catch((e: unknown) =>
                    setSteerErr(e instanceof Error ? e.message : String(e))
                  )
              }}
            >
              steer
            </button>
          </div>
        )}
        {steerErr && <p className="text-xs text-red-500">{steerErr}</p>}
        {diff && (
          <div className="rounded border border-dashed p-2">
            <div className="text-faint">
              {sourceRunID ? `diff vs ${runLabel(sourceRunID)}: ` : "diff: "}
              {diffSummary(diff)}
            </div>
            {diff
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
          </div>
        )}
      </div>
    </div>
  )
}


/** CompareTable is P3's metrics row across the N variants: the
 * finish, tokens, latency and tool calls of each, one column each. */
function CompareTable({ variants }: { variants: Variant[] }) {
  const ran = variants.filter((v) => v.result?.row)
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
            <th className="px-3 py-1 font-normal">tokens</th>
            <th className="px-3 py-1 font-normal">latency</th>
            <th className="px-3 py-1 font-normal">steps</th>
          </tr>
        </thead>
        <tbody>
          {ran.map((v) => {
            const row = v.result!.row!
            return (
              <tr key={v.key} className="border-t">
                <td className="px-3 py-1 font-medium">{v.key}</td>
                <td className="px-3 py-1">
                  {row.usage.input_tokens}→{row.usage.output_tokens}
                </td>
                <td className="px-3 py-1">
                  {row.finished
                    ? `${Math.max(0, Date.parse(row.finished) - Date.parse(row.started))}ms`
                    : "…"}
                </td>
                <td className="px-3 py-1">{row.steps}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

/** VariantDiff is the pairwise text+tool-call diff of the first two
 * run variants (the panel's 2-way view is PQ3's closed rule; Studio's
 * N-way page shows the leading pair and the table above covers the
 * rest). */
function VariantDiff({ a, b }: { a: Variant; b: Variant }) {
  const [texts, setTexts] = useState<[string, string] | null>(null)
  useEffect(() => {
    let alive = true
    void (async () => {
      const read = async (id: string) => {
        try {
          const doc = asTranscript((await getJSON(`runs/${encodeURIComponent(id)}/transcript`)) as RawTranscript)
          const parts: string[] = []
          for (const bt of doc.batches)
            for (const m of bt.messages)
              if (m.role === "assistant")
                for (const p of m.content)
                  if (p.type === "text" && p.text) parts.push(p.text)
          return parts.join("\n")
        } catch {
          return ""
        }
      }
      const [ta, tb] = await Promise.all([read(a.result!.runID), read(b.result!.runID)])
      if (alive) setTexts([ta, tb])
    })()
    return () => {
      alive = false
    }
  }, [a.result?.runID, b.result?.runID])
  if (!texts) return null
  const rows = diffLines(texts[0], texts[1])
  if (rows.every((r) => r.kind === "same")) return null
  return (
    <div className="rounded border border-dashed p-3 text-xs">
      <div className="mb-1 text-faint">
        diff {a.key} ↔ {b.key}: {diffSummary(rows)}
      </div>
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
    </div>
  )
}

/** Matrix is E9's variants × inputs runner: an experiment name, a row
 * per input, one "Run matrix" that saves the definition and issues
 * every cell under its id. The cells' lifecycle states render in the
 * grid; the budget cap (§6 rule 6) is the runtime's — a breach refuses
 * the next command of the experiment.
 */
function Matrix({
  variants,
  runMatrix,
  cells,
  setCells,
  sourceRunID,
}: {
  variants: Variant[]
  runMatrix: (inputs: { key: string; text: string }[], experimentID: string) => Promise<void>
  cells: Record<string, Experiment>
  setCells: React.Dispatch<React.SetStateAction<Record<string, Experiment>>>
  sourceRunID: string
}) {
  const [inputs, setInputs] = useState<{ key: string; text: string }[]>([
    { key: "1", text: "" },
  ])
  const [name, setName] = useState("")
  const qc = useQueryClient()
  const experimentID = name || `exp_${new Date().toISOString().slice(0, 16)}`

  // Poll the cells' lifecycle while any is queued or accepted.
  useEffect(() => {
    const pending = Object.values(cells).filter((c) => c.state === "queued" || c.state === "accepted")
    if (!pending.length) return
    const timer = setTimeout(async () => {
      for (const c of pending) {
        try {
          const st = await fetchCommand(c.commandID)
          setCells((cur) => ({
            ...cur,
            [Object.keys(cur).find((k) => cur[k].commandID === c.commandID) ?? ""]: {
              ...c,
              state: st.state,
              runID: st.run_id || c.runID,
              error: st.error,
            },
          }))
        } catch {
          // the next tick retries
        }
      }
      void qc.invalidateQueries({ queryKey: ["experiments"] })
    }, 800)
    return () => clearTimeout(timer)
  }, [cells, qc, setCells])

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
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <code className="text-faint">{experimentID}</code>
          <Button
            size="sm"
            variant="outline"
            onClick={() => void runMatrix(inputs.filter((i) => i.text || !sourceRunID), experimentID)}
          >
            Run matrix
          </Button>
        </div>
        {inputs.map((inp, i) => (
          <div key={i} className="flex items-center gap-2">
            <span className="w-6 text-faint">{inp.key}</span>
            <input
              className="flex-1 rounded border bg-transparent px-2 py-1"
              placeholder={sourceRunID ? "the input's text (over the source run)" : "the input"}
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
                onClick={() => setInputs((cur) => cur.filter((_, j) => j !== i))}
              >
                −
              </button>
            )}
          </div>
        ))}
        <button
          className="text-faint hover:underline"
          onClick={() =>
            setInputs((cur) => [...cur, { key: String(cur.length + 1), text: "" }])
          }
        >
          + input
        </button>
        {Object.keys(cells).length > 0 && (
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
              {[...new Set(Object.keys(cells).map((k) => k.split("\u0000")[1]))].map((ik) => (
                <tr key={ik} className="border-t">
                  <td className="py-1">{ik}</td>
                  {variants.map((v) => {
                    const cell = cells[`${v.key}\u0000${ik}`]
                    return (
                      <td key={v.key} className="py-1">
                        {cell ? (
                          <span className={cell.state === "finished" ? "text-emerald-500" : "text-muted-foreground"}>
                            {cell.state}
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
 * saved definitions, newest first, with their run counts. */
function History() {
  const experiments = useQuery(experimentsQuery())
  if (!experiments.data?.experiments.length) return null
  return (
    <div className="rounded border">
      <div className="border-b px-3 py-2 text-xs text-muted-foreground">
        Experiment history
      </div>
      <div className="divide-y text-xs">
        {experiments.data.experiments.map((e) => (
          <div key={e.id} className="flex items-center gap-2 px-3 py-1.5">
            <code className="text-faint">{e.id}</code>
            <span>{e.name || "—"}</span>
            <span className="text-faint">
              {e.variants?.length ?? 0}×{e.inputs?.length ?? 0}
            </span>
            <span className="text-faint">{e.runs ? `${e.runs.length} runs` : ""}</span>
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
      {fields.map((f, i) =>
        f.callID ? (
          <label key={i} className="flex items-center gap-1 text-xs">
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
          <label key={i} className="block space-y-1 text-xs">
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
    if (!runID) {
      setFields([])
      return
    }
    let alive = true
    void (async () => {
      try {
        const doc = asTranscript(
          (await getJSON(`runs/${encodeURIComponent(runID)}/transcript`)) as RawTranscript
        )
        if (alive) setFields(editFieldsOf(doc.batches, Number.MAX_SAFE_INTEGER))
      } catch {
        if (alive) setFields([])
      }
    })()
    return () => {
      alive = false
    }
  }, [runID])
  return fields
}


/** Breakpoints is the rung-3 control (§8.3): one checkbox per tool,
 * applied on change — the runtime parks them on every run it starts
 * from then on, whatever the command asked for. */
function Breakpoints({ runtimeID, tools }: { runtimeID: string; tools: string[] }) {
  const [set, setSet] = useState<Set<string>>(new Set())
  const toggle = async (name: string, on: boolean) => {
    const next = new Set(set)
    if (on) next.add(name)
    else next.delete(name)
    setSet(next)
    const headers: Record<string, string> = { "Content-Type": "application/json" }
    const tok = studioToken()
    if (tok) headers.Authorization = `Bearer ${tok}`
    await fetch(new URL(`api/runtimes/${encodeURIComponent(runtimeID)}/breakpoints`, apiBase()).toString(), {
      method: "PUT",
      headers,
      body: JSON.stringify({ tools: [...next].sort() }),
    })
  }
  return (
    <div className="flex flex-wrap gap-2">
      {tools.map((t) => (
        <label key={t} className="flex items-center gap-1">
          <input type="checkbox" checked={set.has(t)} onChange={(e) => void toggle(t, e.target.checked)} />
          {t}
        </label>
      ))}
    </div>
  )
}
