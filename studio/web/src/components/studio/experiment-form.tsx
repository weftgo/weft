// The experiment form (WEFT-PLAYGROUND §4's config column): the kept
// prefix's transcript edits, the system prompt, the tools, model,
// thinking, engine, side effects, thread, breakpoints and — at
// from_step 0 — the input. The playground page renders it per variant;
// the run page's replay drawer renders the same form (plan F1), so the
// two never drift. The step picker lists a source run's steps for
// either.
import { useQueries, useQuery } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import type { Dispatch, ReactNode, SetStateAction } from "react"

import {
  ApiError,
  putBreakpoints,
  runQuery,
  stepQuery,
  transcriptQuery,
} from "@/lib/api"
import { turnPrompt } from "@/lib/events"
import type { AgentView, ModelInfo, RuntimeView, StepDoc } from "@/lib/api"
import { EDIT_FIELD, kindOf } from "@/lib/edits"
import {
  engineHeld,
  engineOf,
  ENGINE_WORDS,
  modelLabel,
  noEdit,
  stepsOf,
} from "@/lib/engine"
import type { Engine, EngineChoice, StepCost } from "@/lib/engine"
import {
  DRAWER_TITLES,
  SIDE_EFFECT_TOOL,
  SIDE_EFFECT_WORDS,
} from "@/lib/drawer-spec"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import { bytes } from "@/lib/summarize"
import {
  callLabel,
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
import type {
  EditDraft,
  EditField,
  LabFields,
  LabProblem,
  SourceStep,
  VariantFields,
} from "@/lib/experiment-body"
import { BREAKPOINT_WORDS } from "@/lib/settings"
import { optionValue, TARGET_PICK, TARGET_WORDS } from "@/lib/target"
import type { AgentOption, TargetCheck } from "@/lib/target"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Badge } from "@/components/ui/badge"
import { SelectField } from "@/components/ui/select-field"

type FormProps = {
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
  /** The source's own model: the others are labelled live calls. */
  own?: ModelInfo
  /** The engine choice for this variant's Run (lib/engine.ts): drawn as
   * EngineRadios — the per-command rule, the user's pick in
   * variant.engine. */
  choice?: EngineChoice
}

/** ExperimentForm is the playground's per-variant form, every knob in
 * one column. The replay drawer composes the same pieces in
 * lib/drawer-spec.ts's order instead (ChangeFields, ModeFields,
 * OptionLab). */
export function ExperimentForm(props: FormProps) {
  const { variant, patch, agent, runtime, caps } = props
  return (
    <>
      <ChangeFields {...props} input={false} />
      {props.choice ? (
        <EngineRadios
          choice={props.choice}
          engine={engineOf(props.choice, variant.engine)}
          refused={engineHeld(props.choice, variant.engine)}
          onPick={(engine) => patch({ engine })}
        />
      ) : null}
      <ModeFields variant={variant} patch={patch} />
      {agent ? (
        <OptionLab variant={variant} patch={patch} agent={agent} />
      ) : null}
      {/* Rung 3 (§8.3): break on tools — PUT /api/runtimes/{id}/
          breakpoints; the runtime parks them on every run it
          starts. */}
      {caps.includes("breakpoints") && runtime && agent?.tools.length ? (
        <div className="space-y-1">
          <span className="text-xs text-muted-foreground">
            Break on (parks every run)
          </span>
          <Breakpoints
            key={runtime.id}
            runtimeID={runtime.id}
            stored={runtime.breakpoints}
            tools={[
              ...new Set(
                runtime.agents.flatMap((a) => a.tools.map((t) => t.name))
              ),
            ]}
          />
        </div>
      ) : null}
      {props.fromStep === 0 && <InputField variant={variant} patch={patch} />}
    </>
  )
}

/** ChangeFields is "what changes": the kept prefix's transcript edits,
 * the system prompt, the tools, the model and thinking — and, unless
 * input is false, the input at from_step 0. */
export function ChangeFields({
  variant,
  patch,
  agent,
  registered,
  sourceRunID,
  fromStep,
  editDrafts,
  setEditDrafts,
  promptNote,
  own,
  input = true,
}: FormProps & { input?: boolean }) {
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
                <span title={SIDE_EFFECT_TOOL}>⚠</span>
              )}
            </label>
          ))}
        </div>
      ) : null}
      <ModelField variant={variant} patch={patch} agent={agent} own={own} />
      {input && fromStep === 0 && (
        <InputField variant={variant} patch={patch} />
      )}
    </>
  )
}

/** InputField replaces the turn's user message (from_step 0). */
function InputField({ variant, patch }: Pick<FormProps, "variant" | "patch">) {
  return (
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
  )
}

/** ModeFields is the run's modes: side effects and thread (the engine
 * is EngineRadios). */
export function ModeFields({
  variant,
  patch,
}: Pick<FormProps, "variant" | "patch">) {
  return (
    <div className="flex flex-wrap gap-2">
      <div className="block min-w-0 flex-1 space-y-1">
        <SelectField
          caption="Side effects"
          label="Side effects"
          className="w-full"
          value={variant.sideEffects}
          onValueChange={(v) =>
            patch({ sideEffects: v as VariantFields["sideEffects"] })
          }
          options={[
            {
              value: "substitute",
              title:
                "a side-effect call the source recorded is answered from the record; any other call parks",
              label: SIDE_EFFECT_WORDS.substitute,
            },
            {
              value: "park",
              title:
                "every side-effect call parks; nothing is answered from the record",
              label: SIDE_EFFECT_WORDS.park,
            },
            {
              value: "allow",
              title:
                "refused unless every tool left on is opted in or ReplaySafe",
              label: SIDE_EFFECT_WORDS.allow,
            },
          ]}
        />
      </div>
      {/* §5.4's thread mode (review fix 4a): fork continues the
          conversation in a new session with lineage — it needs a
          source turn and an input. */}
      <div className="block min-w-0 flex-1 space-y-1">
        <SelectField
          caption="Thread"
          label="Thread"
          className="w-full"
          value={variant.thread}
          onValueChange={(v) => patch({ thread: v as VariantFields["thread"] })}
          options={[
            { value: "ephemeral", label: "ephemeral" },
            { value: "fork", label: "fork (new session)" },
          ]}
        />
      </div>
    </div>
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
  own,
}: {
  variant: VariantFields
  patch: (p: Partial<VariantFields>) => void
  agent?: AgentView
  own?: ModelInfo
}) {
  const models = agent?.models ?? []
  // The free text is its own state: what was typed stays as typed (a
  // name that extends a listed one, inner spaces) and the model sent is
  // its trim; the select shows a listed name only when nothing is typed.
  const [free, setFree] = useState(() =>
    variant.model && !models.includes(variant.model) ? variant.model : ""
  )
  useEffect(() => {
    // Set from outside (another agent, a hand-off): follow it.
    if (
      variant.model !== free.trim() &&
      !(free === "" && (variant.model === "" || models.includes(variant.model)))
    )
      setFree(models.includes(variant.model) ? "" : variant.model)
  }, [variant.model, free, models])
  const listed = free.trim() === ""
  const thinkingDef = agent?.defaults?.thinking
  return (
    <div className="flex flex-wrap gap-2">
      <div className="block min-w-0 flex-1 space-y-1">
        <SelectField
          caption="Model"
          label="model"
          className={`w-full ${variant.model ? "border-primary text-primary" : ""}`}
          value={listed && models.includes(variant.model) ? variant.model : ""}
          attrs={{ "data-override": variant.model ? "" : undefined }}
          onValueChange={(v) => {
            setFree("")
            patch({ model: v })
          }}
          options={[
            { value: "", label: "(the agent's own)" },
            ...models.map((m) => ({ value: m, label: modelLabel(m, own) })),
          ]}
        />
      </div>
      {agent?.resolver ? (
        <label className="block flex-1 space-y-1">
          <span className="text-xs text-muted-foreground">
            or any model name
          </span>
          <input
            aria-label="model name the app resolves"
            data-model-free=""
            className={`w-full rounded border bg-transparent px-2 py-1 text-xs ${!listed ? "border-primary text-primary" : ""}`}
            placeholder="the app resolves it (runtime.ModelResolver)"
            value={free}
            onChange={(e) => {
              setFree(e.target.value)
              patch({ model: e.target.value.trim() })
            }}
          />
        </label>
      ) : null}
      <div className="block space-y-1">
        <SelectField
          caption="Thinking"
          label="thinking"
          value={variant.thinking}
          onValueChange={(v) => patch({ thinking: v })}
          options={[
            {
              value: "",
              label: `default${thinkingDef ? ` (${thinkingDef})` : ""}`,
            },
            ...["off", "low", "medium", "high"].map((l) => ({
              value: l,
              label: l,
            })),
          ]}
        />
      </div>
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
  const problemOf = (k: keyof LabFields | "lab") =>
    problems.filter((p) => p.field === k)
  const greyed = (k: keyof LabFields) => old && LAB_NEW.includes(k)
  const reset = (k: keyof LabFields) =>
    labOn(lab, k, d) ? (
      <button
        type="button"
        className="text-[11px] text-faint hover:underline"
        aria-label={`reset ${LAB_LABELS[k]} to the agent's default`}
        data-lab-reset={k}
        onClick={() =>
          set(
            k === "tool_choice"
              ? { tool_choice: "", tool_choice_name: "" }
              : { [k]: Array.isArray(lab[k]) ? [] : "" }
          )
        }
      >
        ↺ default
      </button>
    ) : null
  const ring = (k: keyof LabFields) =>
    labOn(lab, k, d) ? "border-primary text-primary" : ""
  const tc = lab.tool_choice
  const parked = new Set(lab.park_on)
  const only = lab.only_tools.length ? new Set(lab.only_tools) : null
  const tools = (k: "park_on" | "only_tools") => (
    <fieldset className="space-y-1" data-lab={k} disabled={greyed(k)}>
      <legend className="flex items-center gap-2 text-xs text-muted-foreground">
        {LAB_LABELS[k]}
        <span className="text-faint">
          default: {k === "park_on" ? "none" : "every tool"}
        </span>
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
            <span
              key={n}
              className={`flex items-center gap-1 ${off ? "opacity-50" : on ? "text-primary" : ""}`}
            >
              <input
                type="checkbox"
                aria-label={`${LAB_LABELS[k]}: ${n}`}
                checked={on}
                // A checked box stays clearable after its tool is turned off.
                disabled={greyed(k) || (off && !on)}
                onChange={(e) =>
                  set({
                    [k]: e.target.checked
                      ? [...lab[k], n]
                      : lab[k].filter((x) => x !== n),
                  })
                }
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
    <div
      className="space-y-2 rounded border border-dashed p-2"
      data-option-lab=""
    >
      <span className="text-xs text-muted-foreground">
        Options (empty keeps the agent's default, shown greyed)
      </span>
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
        <div className="flex flex-wrap gap-2">
          <SelectField
            label="tool choice"
            attrs={{
              "data-lab": "tool_choice",
              "data-override": labOn(lab, "tool_choice", d) ? "" : undefined,
            }}
            disabled={greyed("tool_choice")}
            className={`disabled:opacity-50 ${ring("tool_choice")}`}
            value={tc}
            onValueChange={(v) =>
              set({ tool_choice: v as LabFields["tool_choice"] })
            }
            options={[
              {
                value: "",
                label: `default${d ? ` (${d.tool_choice.mode}${d.tool_choice.name ? ` ${d.tool_choice.name}` : ""})` : ""}`,
              },
              ...(["auto", "any", "none", "named"] as const).map((m) => ({
                value: m,
                label: m,
              })),
            ]}
          />
          {tc === "named" ? (
            <SelectField
              label="tool choice: the tool"
              attrs={{ "data-lab": "tool_choice_name" }}
              className={`min-w-0 flex-1 ${ring("tool_choice")}`}
              value={lab.tool_choice_name}
              onValueChange={(v) => set({ tool_choice_name: v })}
              options={[
                { value: "", label: "(pick a tool)" },
                // A tool the run turns off or parks cannot be forced: greyed.
                ...names.map((n) => ({
                  value: n,
                  label: n,
                  disabled:
                    (only ? !only.has(n) : variant.toolsOff.has(n)) ||
                    parked.has(n),
                })),
              ]}
            />
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
      {list.map((p, i) => (
        <p
          key={`${i}:${p.message}`}
          className="text-[11px] text-status-bad"
          role="alert"
          data-lab-problem={p.field}
        >
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
  const orphans = loading
    ? []
    : unmatchedDrafts(drafts, source.fields, fromStep)
  if (!loading && !fields.length && !orphans.length && !source.error)
    return null
  // A field is a result patch or a reply rewrite: the Story's other
  // kinds on the same call (its args) are not this field's.
  const at = (f: EditField) => (d: EditDraft) =>
    d.step === f.step &&
    d.callID === f.callID &&
    kindOf(d) === (f.callID ? "tool_result" : "reply")
  const draftOf = (f: EditField) => drafts.find(at(f))
  const set = (f: EditField, v: string) => {
    setDrafts((cur) => {
      const i = cur.findIndex(at(f))
      // The recording itself is no edit (U17): the draft goes, so the
      // implied from_step and the Story's marks forget it too.
      if (noEdit(v, f.placeholder))
        return i >= 0 ? cur.filter((_, j) => j !== i) : cur
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
            {source.fields === null
              ? "unchecked edit"
              : "no such field in the kept prefix"}
          </Badge>
          <span className="font-mono text-faint">
            step {String(d.step)} · {String(d.callID ?? "reply")} →{" "}
            {String(d.toolResult ?? d.content ?? "").slice(0, 40)}
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
      {fields.map((f) => {
        // The recorded value is a muted line, not the placeholder
        // (UX-C-47): an empty field reads empty, an edited one "edited".
        if (f.callID)
          return (
            <ResultField
              key={`${f.step}:${f.callID}`}
              field={f}
              value={draftOf(f)?.toolResult}
              onChange={(v) => set(f, v)}
            />
          )
        return (
          <ReplyField
            key={`${f.step}:reply`}
            field={f}
            value={draftOf(f)?.content}
            onChange={(v) => set(f, v)}
          />
        )
      })}
    </div>
  )
}

/** useFieldText is a kept field's text as typed: the draft's, else
 * what the user typed that is no draft — the recording itself ("start
 * from recorded", or typed back), whose draft was dropped. A draft
 * dropped from elsewhere (the drawer's list, a reset) empties it. */
function useFieldText(
  value: string | undefined,
  recorded: string
): [string, (v: string) => void] {
  const [text, setText] = useState(value ?? "")
  useEffect(() => {
    if (value !== undefined) setText(value)
    else setText((t) => (t === recorded ? t : ""))
  }, [value, recorded])
  return [text, setText]
}

/** ReplyField is one kept call-free reply's editor. */
function ReplyField({
  field: f,
  value,
  onChange,
}: {
  field: EditField
  value: string | undefined
  onChange: (v: string) => void
}) {
  const [text, setText] = useFieldText(value, f.placeholder)
  return (
    <label className="block space-y-1 text-xs">
      <span className="flex items-center gap-1 text-faint">
        step {f.step} · reply
        {value !== undefined ? <EditedChip /> : null}
      </span>
      <RecordedLine text={f.placeholder} />
      <textarea
        rows={2}
        className="w-full rounded border bg-transparent px-2 py-1"
        aria-label={`edit the reply at step ${f.step}`}
        data-edit-reply={f.step}
        placeholder={EDIT_FIELD.empty}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          onChange(e.target.value)
        }}
      />
    </label>
  )
}

/** EngineRadios is the engine block (U17; UX-C-01, UX-C-05): two
 * radios, one line each — scripted's at 0 tokens, live's naming the
 * model and the estimate; scripted disabled with its reason when it
 * cannot answer, and the refusal said inline while it is the pick. */
export function EngineRadios({
  choice,
  engine,
  refused,
  onPick,
  note,
}: {
  choice: EngineChoice
  engine: Engine
  refused: string
  onPick: (e: Engine) => void
  /** UX-C-09: what live calls here when it is not a provider. */
  note?: string
}) {
  const off = !choice.scripted.enabled
  return (
    <fieldset
      className="space-y-1"
      aria-label="Engine"
      data-drawer-section="engine"
      data-engine={engine}
      data-value={engine}
    >
      <legend className="eyebrow">{DRAWER_TITLES.engine}</legend>
      <label
        className={`flex items-baseline gap-2 text-xs ${off ? "text-faint" : ""}`}
        data-engine-option="scripted"
        data-disabled={off ? "" : undefined}
      >
        <input
          type="radio"
          name="replay-engine"
          value="scripted"
          checked={engine === "scripted"}
          disabled={off}
          // A click is a pick, the checked option's too (it then stays
          // put whatever changes); a disabled option is no pick.
          onChange={() => {}}
          onClick={() => !off && onPick("scripted")}
        />
        <span>
          {ENGINE_WORDS.scripted}
          <span className="block text-[11px] text-faint" data-engine-why>
            {off ? choice.scripted.reason : ENGINE_WORDS.scriptedFor}
          </span>
        </span>
      </label>
      <label
        className="flex items-baseline gap-2 text-xs"
        data-engine-option="live"
      >
        <input
          type="radio"
          name="replay-engine"
          value="live"
          checked={engine === "live"}
          onChange={() => {}}
          onClick={() => onPick("live")}
        />
        <span data-engine-live>
          {ENGINE_WORDS.live(choice.live.model, choice.live.estimate)}
        </span>
      </label>
      {note ? (
        <p className="text-[11px] text-faint" data-engine-script>
          {note}
        </p>
      ) : null}
      {refused ? (
        <p className="text-xs text-status-bad" role="alert" data-engine-refused>
          {refused}
        </p>
      ) : null}
    </fieldset>
  )
}

/**
 * TargetCheckBlock is the target's check under its line (U18.2,
 * lib/target.ts): the build line, why Run is held, the agent picker
 * when the source names no agent or one nobody registers (the agents
 * whose tools match the recording first), and the one escape a click
 * takes — "replay as <agent> anyway", "replay on today's build
 * anyway". The replay drawer and the playground draw the same block.
 */
export function TargetCheckBlock({
  check,
  options,
  picker,
  picked,
  onPick,
  onEscape,
}: {
  check: TargetCheck
  options: AgentOption[]
  /** Draw the picker (the source names no registered agent, or a pick
   * was made). */
  picker: boolean
  picked?: string
  onPick: (o: AgentOption) => void
  onEscape: () => void
}) {
  return (
    <>
      {check.build ? (
        <p
          className={`text-[11px] [overflow-wrap:anywhere] ${check.held ? "text-status-bad" : "text-faint"}`}
          role={check.held ? "alert" : undefined}
          data-target-build
        >
          ! {check.build}
        </p>
      ) : null}
      {check.held && check.kind !== "build" ? (
        <p
          className="text-xs text-status-bad"
          role="alert"
          data-target-held={check.kind}
        >
          {check.held}
        </p>
      ) : null}
      {picker && options.length ? (
        <div className="space-y-0.5" data-target-picker>
          <span className="text-[11px] text-faint">{TARGET_PICK}</span>
          <ul className="flex flex-wrap gap-1.5">
            {options.map((o) => (
              <li key={optionValue(o)}>
                <button
                  type="button"
                  className="rounded border px-2 py-0.5 text-xs hover:bg-muted aria-pressed:border-foreground"
                  aria-pressed={picked === optionValue(o)}
                  data-target-option={o.agent}
                  title={`runtime ${o.runtime}`}
                  onClick={() => onPick(o)}
                >
                  {o.label}
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {check.escape ? (
        <button
          type="button"
          className="text-xs text-thread-ink hover:underline"
          data-target-escape={check.kind}
          onClick={onEscape}
        >
          {check.escape}
        </button>
      ) : null}
    </>
  )
}

/** MissLine is a finished command's miss after Run (U18.2): the
 * server's sentence and, for a scripted miss, "run again on live" — a
 * choice that puts the same command on live with its estimate, never
 * a retry by itself. */
export function MissLine({
  text,
  again,
  onLive,
}: {
  text: string
  again: boolean
  onLive?: () => void
}) {
  return (
    <p className="text-status-bad" data-replay-miss>
      {text}
      {again && onLive ? (
        <>
          {" "}
          <button
            type="button"
            className="text-thread-ink hover:underline"
            data-replay-live-again
            onClick={onLive}
          >
            {TARGET_WORDS.again}
          </button>
        </>
      ) : null}
    </p>
  )
}

/** RecordedLine is a kept field's recorded value as a muted line above
 * it (UX-C-47), never its placeholder: an empty field reads empty. */
function RecordedLine({ text }: { text: string }) {
  return (
    <span
      className="block truncate font-mono text-[11px] text-faint"
      title={text}
      data-edit-recorded
    >
      {EDIT_FIELD.recorded}
      {text.slice(0, 120)}
    </span>
  )
}

/** EditedChip marks a kept field holding a draft (UX-C-47). */
function EditedChip() {
  return (
    <span
      className="shrink-0 rounded-sm border border-thread/60 px-1 font-mono text-[10px] text-thread-ink"
      data-edit-edited
    >
      {EDIT_FIELD.edited}
    </span>
  )
}

/** utf8Bytes is a text's size on the wire. */
function utf8Bytes(s: string): number {
  return new TextEncoder().encode(s).length
}

/** ResultField is one kept tool result's editor (UX-C-25): labelled by
 * its call — the tool with its arguments and the call id, so parallel
 * calls of one tool read apart — a textarea sized to the text (a
 * multi-KB result is not a one-line input), "start from recorded" to
 * edit the recorded text rather than retype it, "reset to recorded" to
 * drop the edit, and a size line. Its selector is step + call id
 * (UX-C-24: call ids repeat across steps). An empty value is no edit. */
function ResultField({
  field: f,
  value,
  onChange,
}: {
  field: EditField
  value: string | undefined
  onChange: (v: string) => void
}) {
  const recorded = f.placeholder
  const [shown, setShown] = useFieldText(value, recorded)
  const put = (v: string) => {
    setShown(v)
    onChange(v)
  }
  const lines = (shown || recorded).split("\n").length
  return (
    <div
      className="space-y-1 text-xs"
      data-edit-field={`${f.step}:${f.callID}`}
    >
      <label className="block space-y-1">
        <span className="block text-faint">
          step {f.step} ·{" "}
          <span className="font-mono">{callLabel(f.name, f.args)}</span> ·{" "}
          <span className="font-mono [overflow-wrap:anywhere]">{f.callID}</span>{" "}
          →
        </span>
        <RecordedLine text={recorded} />
        <textarea
          rows={Math.min(8, Math.max(2, lines))}
          className="w-full rounded border bg-transparent px-2 py-1 font-mono"
          aria-label={`edit the result of ${f.name} (${f.callID}) at step ${f.step}`}
          data-edit-step={f.step}
          data-edit-call={f.callID}
          placeholder={EDIT_FIELD.empty}
          value={shown}
          onChange={(e) => put(e.target.value)}
        />
      </label>
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-faint">
        {shown && shown === recorded ? (
          <span data-edit-same>{EDIT_FIELD.same}</span>
        ) : value ? (
          <EditedChip />
        ) : null}
        <span data-edit-size>
          recorded {bytes(utf8Bytes(recorded))}
          {value ? ` · edited ${bytes(utf8Bytes(value))}` : ""}
        </span>
        {shown ? (
          <button
            type="button"
            className="hover:underline"
            data-edit-reset
            onClick={() => put("")}
          >
            reset to recorded
          </button>
        ) : recorded ? (
          <button
            type="button"
            className="hover:underline"
            data-edit-from-recorded
            onClick={() => put(recorded)}
          >
            start from recorded
          </button>
        ) : null}
      </div>
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
  if (q.isError)
    return { fields: null, error: q.error.message, hidden: isHidden(q.error) }
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
/** useEngineContext is what the engine's choice reads from the source
 * (lib/engine.ts's factsOf): its step count, its own input, and the
 * step routes' costs from from_step on (stepsOf) — Studio's replay
 * drawer and the playground read it alike. */
export function useEngineContext(
  runID: string,
  fromStep: number,
  fork: boolean,
  caps: string[]
): {
  steps?: number
  sourceInput?: string
  costs: (StepCost | null)[] | null
  costWhy: string
  /** from_step's step doc (its request's catalog: the recorded tools). */
  step?: StepDoc
} {
  const { stepCount } = useSourceSteps(runID)
  const at = stepsOf(fromStep, fork ? undefined : stepCount)
  const qs = useQueries({
    queries: at.map((n) => ({
      ...stepQuery(runID, Math.max(n, 0)),
      enabled: !!runID && caps.includes("steps") && n >= 0,
      retry: false,
    })),
  })
  const tr = useQuery({ ...transcriptQuery(runID), enabled: !!runID })
  const first = qs.at(0)
  return {
    steps: stepCount,
    step: at[0] >= 0 ? first?.data : undefined,
    sourceInput: tr.data
      ? (turnPrompt(tr.data.batches) ?? undefined)
      : undefined,
    costs:
      stepCount === undefined && !fork
        ? null
        : qs.map((q, i) => (at[i] < 0 ? null : (q.data ?? null))),
    costWhy: !caps.includes("steps")
      ? "no step route"
      : first?.isError
        ? `step unreadable: ${first.error.message}`
        : "reading…",
  }
}

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
  const transcript = useQuery({
    ...transcriptQuery(runID),
    enabled: Boolean(runID),
  })
  const doc = useQuery({
    ...runQuery(runID),
    enabled: Boolean(runID),
    staleTime: 30_000,
  })
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
  const doc = useQuery({
    ...runQuery(runID),
    enabled: Boolean(runID),
    staleTime: 30_000,
  })
  const { steps, error, hidden, max, stepCount } = useSourceSteps(runID)
  const shownModel = model ?? (doc.data ? doc.data.model.name : undefined)
  const failed =
    doc.data && doc.data.status === "failed" ? doc.data.err || "failed" : ""
  if (steps === null || steps.length === 0) {
    return (
      <label className="block space-y-1" data-step-picker="number">
        <span className="text-xs text-muted-foreground">
          Continue from step
        </span>
        <span className="flex items-center gap-2">
          <input
            type="number"
            min={0}
            aria-label="continue from step"
            className="w-20 rounded border bg-transparent px-2 py-1 text-xs"
            value={value}
            onChange={(e) =>
              onChange(Math.max(0, Math.floor(Number(e.target.value) || 0)))
            }
          />
          {hidden ? (
            <HoleBadge hole="hidden" />
          ) : error ? (
            <Badge
              variant="outline"
              className="font-mono text-[10px] font-normal"
              title={error}
            >
              steps unreadable · {error}
            </Badge>
          ) : steps === null ? (
            <span className="text-[11px] text-faint">
              reading the run's steps…
            </span>
          ) : (
            <Badge
              variant="outline"
              className="font-mono text-[10px] font-normal"
            >
              the run's transcript holds no steps
            </Badge>
          )}
        </span>
      </label>
    )
  }
  // A value the list lacks (a hand-off's step the run does not have) is
  // shown as itself, not silently moved.
  const offered = steps.filter(
    (st) => st.ordinal > 0 && (max === undefined || st.ordinal <= max)
  )
  // The step after the last, when its calls are answered (a failed
  // run's next model call, run fresh).
  const after =
    max !== undefined && stepCount !== undefined && max === stepCount && max > 0
      ? max
      : null
  const known =
    value === 0 || value === after || offered.some((st) => st.ordinal === value)
  const last = Math.max(...steps.map((st) => st.ordinal))
  return (
    <div className="block space-y-1" data-step-picker="list">
      <SelectField
        caption="Continue from step"
        label="continue from step"
        className="w-full"
        value={String(value)}
        onValueChange={(v) => onChange(Number(v))}
        options={[
          { value: "0", label: "0 · from the start (new input)" },
          ...offered.map((st) => {
            const bits = [
              `${st.ordinal}`,
              shownModel,
              st.tools.length ? `calls ${st.tools.join(", ")}` : "reply",
              st.toolError ? "tool error" : "",
              st.ordinal === last && failed ? "failed" : "",
              st.compacted ? "compacted" : "",
            ].filter(Boolean)
            return {
              value: String(st.ordinal),
              label: bits.join(" · "),
              data: { "data-compacted": st.compacted ? "" : undefined },
            }
          }),
          ...(after !== null
            ? [
                {
                  value: String(after),
                  label: `${after} · after the last step (answers its calls)`,
                },
              ]
            : []),
          ...(known
            ? []
            : [
                {
                  value: String(value),
                  label: `${value} · not a step of this run`,
                },
              ]),
        ]}
      />
    </div>
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
  const put = async (next: Set<string>) => {
    const prev = set
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
  const toggle = (name: string, on: boolean) => {
    const next = new Set(set)
    if (on) next.add(name)
    else next.delete(name)
    return put(next)
  }
  // UX-A-33: every tool on parks every call — said, not left to a count.
  const all = tools.length > 1 && tools.every((t) => set.has(t))
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
        {set.size > 0 ? (
          <button
            type="button"
            className="font-mono text-thread-ink hover:underline disabled:opacity-50"
            title={BREAKPOINT_WORDS.clearTitle}
            disabled={saving}
            onClick={() => void put(new Set())}
            data-breakpoints-clear
          >
            {BREAKPOINT_WORDS.clear}
          </button>
        ) : null}
      </div>
      {all ? (
        <p className="text-xs text-status-int" data-breakpoints-all>
          {BREAKPOINT_WORDS.all}
        </p>
      ) : null}
      {err && (
        <p className="text-xs text-destructive" role="alert">
          {err}
        </p>
      )}
    </div>
  )
}
