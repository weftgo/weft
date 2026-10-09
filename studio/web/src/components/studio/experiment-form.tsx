// The experiment form (WEFT-PLAYGROUND §4's config column): the kept
// prefix's transcript edits, the system prompt, the tools, model,
// thinking, engine, side effects, thread, breakpoints and — at
// from_step 0 — the input. The playground page renders it per variant;
// the run page's replay drawer renders the same form (plan F1), so the
// two never drift. The step picker lists a source run's steps for
// either.
import { useQuery } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import type { Dispatch, ReactNode, SetStateAction } from "react"

import { ApiError, putBreakpoints, runQuery, transcriptQuery } from "@/lib/api"
import type { AgentView, RuntimeView } from "@/lib/api"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import {
  editFieldsOf,
  emptyLab,
  LAB_LABELS,
  LAB_NEW,
  LAB_NUMBERS,
  LAB_PREDATES,
  labDefault,
  labLacksDefaults,
  labOn,
  labProblems,
  replayBounds,
  sourceSteps,
  unmatchedDrafts,
} from "@/lib/experiment-body"
import type { EditDraft, EditField, LabFields, LabProblem, SourceStep, VariantFields } from "@/lib/experiment-body"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Badge } from "@/components/ui/badge"

export function ExperimentForm({
  variant,
  patch,
  agent,
  registered,
  runtime,
  caps,
  sourceRunID,
  fromStep,
  editDrafts,
  setEditDrafts,
  promptNote,
}: {
  variant: VariantFields
  patch: (p: Partial<VariantFields>) => void
  /** The target agent as its runtime registered it. */
  agent?: AgentView
  /** The agent's registered system prompt (the reset target). */
  registered: string
  runtime?: RuntimeView
  /** /api/meta's capabilities (breakpoints is one). */
  caps: string[]
  sourceRunID: string
  /** source.from_step: the step ordinal run fresh. */
  fromStep: number
  editDrafts: EditDraft[]
  setEditDrafts: Dispatch<SetStateAction<EditDraft[]>>
  /** Drawn above the system prompt box: where its text came from when
   * that is not what the caller asked for (a hole, badged). */
  promptNote?: ReactNode
}) {
  return (
    <>
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
        {promptNote}
        <textarea
          rows={4}
          aria-label="system prompt"
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
      <ModelField variant={variant} patch={patch} agent={agent} />
      <div className="flex gap-2">
        <label className="block flex-1 space-y-1">
          <span className="text-xs text-muted-foreground">Engine</span>
          <select
            className="w-full rounded border bg-transparent px-1 py-1 text-xs"
            value={variant.engine}
            onChange={(e) => patch({ engine: e.target.value as VariantFields["engine"] })}
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
            onChange={(e) => patch({ sideEffects: e.target.value as VariantFields["sideEffects"] })}
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
            onChange={(e) => patch({ thread: e.target.value as VariantFields["thread"] })}
          >
            <option value="ephemeral">ephemeral</option>
            <option value="fork">fork (new session)</option>
          </select>
        </label>
      </div>
      {agent ? <OptionLab variant={variant} patch={patch} agent={agent} /> : null}
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
            aria-label="input"
            className="w-full rounded border bg-transparent px-2 py-1 text-xs"
            value={variant.input}
            onChange={(e) => patch({ input: e.target.value })}
          />
        </label>
      )}
    </>
  )
}

/** ModelField is the model knob: a select over the runtime's models
 * and — when the app holds a runtime.ModelResolver — free text the app
 * resolves (its refusal comes back as the command's reason), and the
 * thinking level beside it (as it was; its default named). */
function ModelField({
  variant,
  patch,
  agent,
}: {
  variant: VariantFields
  patch: (p: Partial<VariantFields>) => void
  agent?: AgentView
}) {
  const models = agent?.models ?? []
  const listed = variant.model === "" || models.includes(variant.model)
  const thinkingDef = agent?.defaults?.thinking
  return (
    <div className="flex flex-wrap gap-2">
      <label className="block flex-1 space-y-1">
        <span className="text-xs text-muted-foreground">Model</span>
        <select
          aria-label="model"
          className={`w-full rounded border bg-transparent px-1 py-1 text-xs ${variant.model ? "border-primary text-primary" : ""}`}
          value={listed ? variant.model : ""}
          data-override={variant.model ? "" : undefined}
          onChange={(e) => patch({ model: e.target.value })}
        >
          <option value="">(the agent's own)</option>
          {models.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </label>
      {agent?.resolver ? (
        <label className="block flex-1 space-y-1">
          <span className="text-xs text-muted-foreground">or any model name</span>
          <input
            aria-label="model name the app resolves"
            data-model-free=""
            className={`w-full rounded border bg-transparent px-2 py-1 text-xs ${!listed ? "border-primary text-primary" : ""}`}
            placeholder="the app resolves it (runtime.ModelResolver)"
            value={listed ? "" : variant.model}
            onChange={(e) => patch({ model: e.target.value.trim() })}
          />
        </label>
      ) : null}
      <label className="block space-y-1">
        <span className="text-xs text-muted-foreground">Thinking</span>
        <select
          aria-label="thinking"
          className="rounded border bg-transparent px-1 py-1 text-xs"
          value={variant.thinking}
          onChange={(e) => patch({ thinking: e.target.value })}
        >
          <option value="">default{thinkingDef ? ` (${thinkingDef})` : ""}</option>
          {["off", "low", "medium", "high"].map((l) => (
            <option key={l} value={l}>
              {l}
            </option>
          ))}
        </select>
      </label>
    </div>
  )
}


/** OptionLab is plan F3's form: every narrowing or neutral run option
 * the command carries, each with the agent's default greyed (the
 * placeholder) and an override drawn in colour with a one-click reset.
 * An empty knob sends nothing. The rules are the server's, by name
 * (lib/experiment-body.ts's labOverrides): a problem is said on its
 * knob and holds the command; the server's own 400 stays authoritative
 * and is shown verbatim where the command's error goes. A registration
 * without defaults (an old runtime) shows the knobs with no default and
 * greys the ones it cannot carry. */
export function OptionLab({
  variant,
  patch,
  agent,
}: {
  variant: VariantFields
  patch: (p: Partial<VariantFields>) => void
  agent: AgentView
}) {
  const lab = variant.lab ?? emptyLab()
  const d = agent.defaults
  const old = labLacksDefaults(agent)
  const set = (p: Partial<LabFields>) => patch({ lab: { ...lab, ...p } })
  const names = agent.tools.map((t) => t.name)
  const problems = labProblems(variant, agent)
  const problemOf = (k: keyof LabFields | "lab") => problems.filter((p) => p.field === k)
  const greyed = (k: keyof LabFields) => old && LAB_NEW.includes(k)
  const reset = (k: keyof LabFields) =>
    labOn(lab, k, d) ? (
      <button
        type="button"
        className="text-[11px] text-faint hover:underline"
        aria-label={`reset ${LAB_LABELS[k]} to the agent's default`}
        data-lab-reset={k}
        onClick={() => set(k === "tool_choice" ? { tool_choice: "", tool_choice_name: "" } : { [k]: Array.isArray(lab[k]) ? [] : "" })}
      >
        ↺ default
      </button>
    ) : null
  const ring = (k: keyof LabFields) => (labOn(lab, k, d) ? "border-primary text-primary" : "")
  const tc = lab.tool_choice
  const parked = new Set(lab.park_on)
  const only = lab.only_tools.length ? new Set(lab.only_tools) : null
  const tools = (k: "park_on" | "only_tools") => (
    <fieldset className="space-y-1" data-lab={k} disabled={greyed(k)}>
      <legend className="flex items-center gap-2 text-xs text-muted-foreground">
        {LAB_LABELS[k]}
        <span className="text-faint">default: {k === "park_on" ? "none" : "every tool"}</span>
        {reset(k)}
      </legend>
      <div className="flex flex-wrap gap-2 text-xs">
        {names.map((n) => {
          // only_tools narrows tools_enabled: a tool turned off is greyed.
          const off = k === "only_tools" && variant.toolsOff.has(n)
          const on = lab[k].includes(n)
          return (
            // A span, not a label: the checkbox's name says which set
            // (the Tools boxes are the bare names).
            <span key={n} className={`flex items-center gap-1 ${off ? "opacity-50" : on ? "text-primary" : ""}`}>
              <input
                type="checkbox"
                aria-label={`${LAB_LABELS[k]}: ${n}`}
                checked={on}
                disabled={off || greyed(k)}
                onChange={(e) => set({ [k]: e.target.checked ? [...lab[k], n] : lab[k].filter((x) => x !== n) })}
              />
              {n}
            </span>
          )
        })}
      </div>
      <Problems list={problemOf(k)} />
    </fieldset>
  )
  return (
    <div className="space-y-2 rounded border border-dashed p-2" data-option-lab="">
      <span className="text-xs text-muted-foreground">Options (empty keeps the agent's default, shown greyed)</span>
      {old ? (
        <p className="text-[11px] text-faint" data-lab-old="">
          this runtime reports no defaults — {LAB_PREDATES}
        </p>
      ) : null}
      <Problems list={problemOf("lab")} />
      <div className="grid grid-cols-3 gap-2">
        {LAB_NUMBERS.map((k) => (
          <label key={k} className="block space-y-1">
            <span className="flex items-center gap-1 text-xs text-muted-foreground">
              {LAB_LABELS[k]} {reset(k)}
            </span>
            <input
              inputMode="decimal"
              aria-label={LAB_LABELS[k]}
              data-lab={k}
              data-override={labOn(lab, k, d) ? "" : undefined}
              disabled={greyed(k)}
              className={`w-full rounded border bg-transparent px-2 py-1 text-xs disabled:opacity-50 ${ring(k)}`}
              placeholder={labDefault(k, d) || (old ? "" : "adapter default")}
              value={lab[k]}
              onChange={(e) => set({ [k]: e.target.value })}
            />
            <Problems list={problemOf(k)} />
          </label>
        ))}
      </div>
      <label className="block space-y-1">
        <span className="flex items-center gap-1 text-xs text-muted-foreground">
          {LAB_LABELS.stop} {reset("stop")}
        </span>
        <textarea
          rows={2}
          aria-label="stop sequences, one per line"
          data-lab="stop"
          data-override={labOn(lab, "stop", d) ? "" : undefined}
          disabled={greyed("stop")}
          className={`w-full rounded border bg-transparent px-2 py-1 text-xs disabled:opacity-50 ${ring("stop")}`}
          placeholder={labDefault("stop", d) || (old ? "" : "none")}
          value={lab.stop}
          onChange={(e) => set({ stop: e.target.value })}
        />
        <Problems list={problemOf("stop")} />
      </label>
      <div className="space-y-1">
        <span className="flex items-center gap-1 text-xs text-muted-foreground">
          {LAB_LABELS.tool_choice} {reset("tool_choice")}
        </span>
        <div className="flex gap-2">
          <select
            aria-label="tool choice"
            data-lab="tool_choice"
            data-override={labOn(lab, "tool_choice", d) ? "" : undefined}
            disabled={greyed("tool_choice")}
            className={`rounded border bg-transparent px-1 py-1 text-xs disabled:opacity-50 ${ring("tool_choice")}`}
            value={tc}
            onChange={(e) => set({ tool_choice: e.target.value as LabFields["tool_choice"] })}
          >
            <option value="">
              default{d ? ` (${d.tool_choice.mode}${d.tool_choice.name ? ` ${d.tool_choice.name}` : ""})` : ""}
            </option>
            {(["auto", "any", "none", "named"] as const).map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
          {tc === "named" ? (
            <select
              aria-label="tool choice: the tool"
              data-lab="tool_choice_name"
              className={`flex-1 rounded border bg-transparent px-1 py-1 text-xs ${ring("tool_choice")}`}
              value={lab.tool_choice_name}
              onChange={(e) => set({ tool_choice_name: e.target.value })}
            >
              <option value="">(pick a tool)</option>
              {names.map((n) => (
                // A tool the run turns off or parks cannot be forced: greyed.
                <option key={n} value={n} disabled={(only ? !only.has(n) : variant.toolsOff.has(n)) || parked.has(n)}>
                  {n}
                </option>
              ))}
            </select>
          ) : null}
        </div>
        <Problems list={problemOf("tool_choice")} />
      </div>
      {names.length ? tools("only_tools") : null}
      {names.length ? tools("park_on") : null}
    </div>
  )
}

function Problems({ list }: { list: LabProblem[] }) {
  if (!list.length) return null
  return (
    <>
      {list.map((p) => (
        <p key={p.message} className="text-[11px] text-status-bad" role="alert" data-lab-problem={p.field}>
          {p.message}
        </p>
      ))}
    </>
  )
}

/** TranscriptEdits renders the kept prefix's editable fields (review
 * fix 4b): when continuing from a step, the kept steps' tool results
 * are patchable and their call-free replies rewritable — the
 * counterfactual the fresh step answers. The panel's drawer is the
 * shape; this is the same wire. A draft is never dropped behind the
 * user's back: one no field of the kept prefix matches (another step,
 * an unreadable transcript) is shown with a badge, and the caller holds
 * Run (unmatchedDrafts) until it is fixed or dropped here. */
export function TranscriptEdits({
  runID,
  fromStep,
  drafts,
  setDrafts,
}: {
  runID: string
  fromStep: number
  drafts: EditDraft[]
  setDrafts: Dispatch<SetStateAction<EditDraft[]>>
}) {
  const source = useSourceEditFields(runID)
  const fields = (source.fields ?? []).filter((f) => f.step < fromStep)
  const loading = source.fields === null && !source.error
  // While the transcript loads nothing is judged yet: the drafts wait
  // (Run is held by the caller), no alarm is raised.
  const orphans = loading ? [] : unmatchedDrafts(drafts, source.fields, fromStep)
  if (!loading && !fields.length && !orphans.length && !source.error) return null
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
      {loading ? (
        <p className="text-[11px] text-faint" data-edits-reading>
          reading the transcript…
        </p>
      ) : null}
      {source.hidden ? (
        <HoleBadge hole="hidden" />
      ) : source.error ? (
        <Badge
          variant="outline"
          className="font-mono text-[10px] font-normal"
          title={source.error}
          data-edits-unreadable
        >
          transcript unreadable · {source.error}
        </Badge>
      ) : null}
      {orphans.map((d) => (
        <div
          key={`orphan:${d.step}:${d.callID ?? "reply"}`}
          className="flex flex-wrap items-center gap-1 text-xs"
          data-edit-orphan={d.callID ?? "reply"}
        >
          <Badge
            variant="outline"
            className="border-ev-error/40 font-mono text-[10px] font-normal text-ev-error"
          >
            {source.fields === null ? "unchecked edit" : "no such field in the kept prefix"}
          </Badge>
          <span className="font-mono text-faint">
            step {d.step} · {d.callID ?? "reply"} → {(d.toolResult ?? d.content ?? "").slice(0, 40)}
          </span>
          <button
            type="button"
            className="text-faint hover:underline"
            onClick={() => setDrafts((cur) => cur.filter((x) => x !== d))}
          >
            drop
          </button>
        </div>
      ))}
      {fields.map((f) =>
        f.callID ? (
          <label key={`${f.step}:${f.callID}`} className="flex items-center gap-1 text-xs">
            <span className="shrink-0 text-faint">
              step {f.step} · {f.name} →
            </span>
            <input
              className="w-full rounded border bg-transparent px-2 py-1"
              aria-label={`edit the result of ${f.name} (${f.callID}) at step ${f.step}`}
              data-edit-call={f.callID}
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

/** useSourceEditFields reads every editable field of the source
 * transcript (the fromStep filter is applied by the reader, so a
 * changed step needs no refetch) through the transcript query the run
 * page shares: fields null while loading — and on a failed read, with
 * the error — never [] for "unknown". */
export function useSourceEditFields(runID: string): {
  fields: EditField[] | null
  error?: string
  /** The read was refused for the token's scope (403 hidden). */
  hidden?: boolean
} {
  const q = useQuery({ ...transcriptQuery(runID), enabled: Boolean(runID) })
  if (!runID) return { fields: [] }
  if (q.isError) return { fields: null, error: q.error.message, hidden: isHidden(q.error) }
  if (!q.data) return { fields: null }
  return { fields: editFieldsOf(q.data.batches, Number.MAX_SAFE_INTEGER) }
}

/** isHidden: a refusal for the token's scope (a 403 carrying the
 * hidden badge) — a hole, not an error. */
function isHidden(e: unknown): boolean {
  return (
    e instanceof ApiError &&
    e.status === 403 &&
    (e.doc as { badge?: string } | null | undefined)?.badge === "hidden"
  )
}

/** useSourceSteps reads a source run's own steps (the transcript and
 * the run document's compactions, through the queries the run page
 * shares): the step picker's list. */
export function useSourceSteps(runID: string): {
  steps: SourceStep[] | null
  error?: string
  hidden?: boolean
  /** The highest from_step the server accepts (replayBounds). */
  max?: number
  stepCount?: number
  /** replayBounds' answeredCalls and lastCalls (the past-end reason). */
  answeredCalls?: boolean
  lastCalls?: boolean
} {
  const transcript = useQuery({ ...transcriptQuery(runID), enabled: Boolean(runID) })
  const doc = useQuery({ ...runQuery(runID), enabled: Boolean(runID), staleTime: 30_000 })
  if (!runID) return { steps: [] }
  if (transcript.isError) {
    const e = transcript.error
    return { steps: null, error: e.message, hidden: isHidden(e) }
  }
  if (!transcript.data) return { steps: null }
  const compacted = new Set(
    compactionsOf(doc.data)
      .filter((c) => !isSessionMarker(c) && typeof c.step === "number")
      .map((c) => c.step as number)
  )
  const bounds = replayBounds(transcript.data.batches)
  return {
    steps: sourceSteps(transcript.data.batches, compacted),
    max: bounds.max,
    stepCount: bounds.stepCount,
    answeredCalls: bounds.answeredCalls,
    lastCalls: bounds.lastCalls,
  }
}

/** StepPicker is the playground's (and the replay drawer's) from_step
 * field: the source run's steps by their ordinal (from_step IS the
 * ordinal) — step 0 starts the turn over with a new input; step N
 * keeps steps 0..N-1 and runs N fresh. Past the last step only when
 * its calls are all answered (the server's rule, replayBounds: the
 * replay's first model call answers them); never past a call-free
 * last reply.
 * When the run's steps cannot be read the number field stays, with a
 * badge saying why — never an empty picker. */
export function StepPicker({
  runID,
  value,
  onChange,
  model,
}: {
  runID: string
  value: number
  onChange: (n: number) => void
  /** The source run's model, shown on each step. */
  model?: string
}) {
  const doc = useQuery({ ...runQuery(runID), enabled: Boolean(runID), staleTime: 30_000 })
  const { steps, error, hidden, max, stepCount } = useSourceSteps(runID)
  const shownModel = model ?? (doc.data ? doc.data.model.name : undefined)
  const failed = doc.data && doc.data.status === "failed" ? doc.data.err || "failed" : ""
  if (steps === null || steps.length === 0) {
    return (
      <label className="block space-y-1" data-step-picker="number">
        <span className="text-xs text-muted-foreground">Continue from step</span>
        <span className="flex items-center gap-2">
          <input
            type="number"
            min={0}
            aria-label="continue from step"
            className="w-20 rounded border bg-transparent px-2 py-1 text-xs"
            value={value}
            onChange={(e) => onChange(Math.max(0, Math.floor(Number(e.target.value) || 0)))}
          />
          {hidden ? (
            <HoleBadge hole="hidden" />
          ) : error ? (
            <Badge variant="outline" className="font-mono text-[10px] font-normal" title={error}>
              steps unreadable · {error}
            </Badge>
          ) : steps === null ? (
            <span className="text-[11px] text-faint">reading the run's steps…</span>
          ) : (
            <Badge variant="outline" className="font-mono text-[10px] font-normal">
              the run's transcript holds no steps
            </Badge>
          )}
        </span>
      </label>
    )
  }
  // A value the list lacks (a hand-off's step the run does not have) is
  // shown as itself, not silently moved.
  const offered = steps.filter((st) => st.ordinal > 0 && (max === undefined || st.ordinal <= max))
  // The step after the last, when its calls are answered (a failed
  // run's next model call, run fresh).
  const after = max !== undefined && stepCount !== undefined && max === stepCount && max > 0 ? max : null
  const known = value === 0 || value === after || offered.some((st) => st.ordinal === value)
  const last = Math.max(...steps.map((st) => st.ordinal))
  return (
    <label className="block space-y-1" data-step-picker="list">
      <span className="text-xs text-muted-foreground">Continue from step</span>
      <select
        aria-label="continue from step"
        className="w-full rounded border bg-transparent px-1 py-1 text-xs"
        value={String(value)}
        onChange={(e) => onChange(Number(e.target.value))}
      >
        <option value="0">0 · from the start (new input)</option>
        {offered.map((st) => {
          const bits = [
            `${st.ordinal}`,
            shownModel,
            st.tools.length ? `calls ${st.tools.join(", ")}` : "reply",
            st.toolError ? "tool error" : "",
            st.ordinal === last && failed ? "failed" : "",
            st.compacted ? "compacted" : "",
          ].filter(Boolean)
          return (
            <option key={st.ordinal} value={String(st.ordinal)} data-compacted={st.compacted ? "" : undefined}>
              {bits.join(" · ")}
            </option>
          )
        })}
        {after !== null ? (
          <option value={String(after)}>{after} · after the last step (answers its calls)</option>
        ) : null}
        {known ? null : <option value={String(value)}>{value} · not a step of this run</option>}
      </select>
    </label>
  )
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
