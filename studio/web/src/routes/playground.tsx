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
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useSearch } from "@tanstack/react-router"
import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react"

import {
  apiBase,
  asTranscript,
  fetchCommand,
  postPlaygroundRun,
  runtimesQuery,
  type AgentView,
  type CommandStatus,
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
  return <Playground />
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
    result: null,
  }
}

function Playground() {
  const search = useSearch({ from: "/playground" })
  const runtimes = useQuery(runtimesQuery())

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

  const [sourceRunID, setSourceRunID] = useState(search.run ?? "")
  const [fromStep, setFromStep] = useState(search.step ?? 0)

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
      thread: "ephemeral",
    }
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
          </div>
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
                      key={v.key}
                      experiment={v.result!}
                      sourceText={sourceText}
                      sourceRunID={sourceRunID}
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
            </>
          ) : (
            <p className="text-xs text-muted-foreground">
              Configure a variant and run it. The run executes in your app through the
              runtime link; the result streams here (WEFT-PLAYGROUND §5.2). Add variants
              to compare them side by side on text, tool calls, tokens and latency.
            </p>
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
}: {
  experiment: Experiment
  sourceText: string
  sourceRunID: string
}) {
  const [events, setEvents] = useState<{ pos: number; event: WireEvent }[]>([])
  const [transcriptText, setTranscriptText] = useState<string | null>(null)

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
