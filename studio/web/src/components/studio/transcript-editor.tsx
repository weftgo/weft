// The transcript editor (plan F2) on the run page's Story view: a user
// message, a call's arguments, a tool result or a call-free reply
// becomes a textarea in place (arguments are JSON, checked against the
// tool's schema from the step's catalog — lib/edits.ts's checkArgs, the
// server's rules by name; a refusal is said on the field and holds Run),
// with revert and a red "edited" chip; a boundary takes an inserted
// message. The edits accumulate into the replay drawer's one command
// (the run page holds them). The drawer's "will be sent" preview
// (POST /api/playground/preview) and the replayed run's weft.edits mark
// are drawn here too — lib/preview.ts and lib/edits.ts, the rows the
// panel draws.
import { PencilLine, Plus } from "lucide-react"
import { createContext, useContext, useEffect, useRef, useState } from "react"
import type { ReactNode } from "react"

import { ApiError, postPlaygroundPreview } from "@/lib/api"
import type { PlaygroundRunBody, RunRow } from "@/lib/api"
import { checkArgs, editable, editKey, editLine, editMarks, kindOf, markedPart, markLine, MARK_CHIPS } from "@/lib/edits"
import type { ReplayEdit, UserMessage } from "@/lib/edits"
import type { TranscriptBatch } from "@/lib/events"
import { PREVIEW_SILENT, PREVIEW_TIMEOUT_MS, previewView } from "@/lib/preview"
import type { PreviewDoc } from "@/lib/preview"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Button } from "@/components/ui/button"

/** The editor as the run page provides it: the edits so far, the
 * fields whose text is refused (by key), put (null: revert), the
 * schema a step's catalog recorded for a tool, and the highest
 * from_step the server accepts (an edit past it is not offered). */
export interface Editor {
  edits: ReplayEdit[]
  invalid: Record<string, string>
  put: (target: ReplayEdit, next: ReplayEdit | null, error?: string) => void
  schema: (tool: string) => unknown
  maxFrom: number | null
}

export const EditorContext = createContext<Editor | null>(null)
export const useEditor = () => useContext(EditorContext)

/** The text a target's edit holds. */
function valueOf(e: ReplayEdit | undefined): string | undefined {
  if (!e) return undefined
  return kindOf(e) === "tool_args" ? JSON.stringify(e.args, null, 2) : (e.toolResult ?? e.content)
}

/** The edit a field's text makes (undefined: none — empty, or the
 * recorded text again), or the refusal. */
function editOf(target: ReplayEdit, text: string, recorded: string, schema: unknown, tool: string): { edit?: ReplayEdit; error?: string; unchecked?: boolean } {
  if (!text.trim() || text === recorded) return {}
  const k = kindOf(target)
  if (k === "tool_args") {
    const r = checkArgs(tool, schema, text)
    return r.error ? { error: r.error } : { edit: { ...target, args: r.args }, unchecked: r.unchecked }
  }
  return { edit: k === "tool_result" ? { ...target, toolResult: text } : { ...target, content: text } }
}

/** offered: the editor may edit this target (the step is one a replay
 * can keep: its from_step within the server's bound). */
export function offered(ed: Editor | null, target: ReplayEdit): ed is Editor {
  return !!ed && editable(target, ed.maxFrom)
}

/**
 * Editable is one editable thing: its recorded view with an edit
 * affordance (click it, or the pencil), or — open, or edited — the
 * textarea, the chip, revert, and the refusal.
 */
export function Editable({
  target,
  recorded,
  label,
  tool = "",
  children,
}: {
  target: ReplayEdit
  recorded: string
  /** What is edited, in words ("the result of lookup_order (c1)"). */
  label: string
  tool?: string
  children?: ReactNode
}) {
  const ed = useEditor()
  const [open, setOpen] = useState(false)
  const [text, setText] = useState<string | null>(null)
  const [unchecked, setUnchecked] = useState(false)
  const key = editKey(target)
  const cur = ed?.edits.find((e) => editKey(e) === key)
  const err = ed?.invalid[key]
  // The field's state in the command: its edit and its refusal. What
  // this field last put is mine; a change from anywhere else — the
  // drawer closed (every edit dropped), another verb opened, a drop
  // from the drawer's list, the drawer's own field rewriting it —
  // resets the text to the command's, and closes the editor when the
  // command no longer holds the field.
  const held = `${cur ? JSON.stringify(cur) : ""}\u0000${err ?? ""}`
  const mine = useRef(held)
  useEffect(() => {
    if (held === mine.current) return
    mine.current = held
    setText(null)
    setUnchecked(false)
    if (held === "\u0000") setOpen(false)
  }, [held])
  if (!offered(ed, target)) return <>{children}</>
  const k = kindOf(target)
  if (!open && !cur && !err)
    return (
      <div
        className="group/ed relative"
        data-editable={k}
        onClick={(e) => {
          if ((e.target as HTMLElement).closest("button")) return
          e.stopPropagation()
          setOpen(true)
        }}
      >
        {children}
        <Button
          variant="ghost"
          size="icon-xs"
          className="absolute top-0 right-0 text-faint opacity-0 transition-opacity group-hover/ed:opacity-100 focus-visible:opacity-100"
          aria-label={`edit ${label}`}
          title={`edit ${label}`}
          data-edit-open={k}
          onClick={(e) => {
            e.stopPropagation()
            setOpen(true)
          }}
        >
          {k === "insert" ? <Plus data-slot="icon" /> : <PencilLine data-slot="icon" />}
        </Button>
      </div>
    )
  const shown = text ?? valueOf(cur) ?? recorded
  return (
    <div className="space-y-1" data-edit={k} data-edit-key={key} onClick={(e) => e.stopPropagation()}>
      <div className="flex items-center gap-2">
        <span className="eyebrow">{k === "insert" ? "inserted · user" : `edit · ${label}`}</span>
        {cur ? (
          <span
            className="rounded-sm border border-ev-error/50 px-1.5 py-px font-mono text-[10px] text-ev-error uppercase"
            data-edited
          >
            {k === "insert" ? "inserted" : "edited"}
          </span>
        ) : null}
        <button
          type="button"
          className="ml-auto text-[11px] text-faint hover:underline"
          data-edit-revert
          onClick={() => {
            mine.current = "\u0000"
            setText(null)
            setOpen(false)
            ed.put(target, null)
          }}
        >
          revert
        </button>
      </div>
      <textarea
        rows={k === "tool_args" ? 4 : 2}
        aria-label={`edit ${label}`}
        aria-invalid={err ? true : undefined}
        className={`w-full rounded border bg-transparent px-2 py-1 font-mono text-xs ${err ? "border-ev-error" : cur ? "border-ev-error/50" : ""}`}
        value={shown}
        // A fresh editor opens on its text: the textarea takes focus.
        autoFocus={!cur && !err}
        onChange={(e) => {
          const v = e.target.value
          setText(v)
          const r = editOf(target, v, recorded, ed.schema(tool), tool)
          mine.current = `${r.edit ? JSON.stringify(r.edit) : ""}\u0000${r.error ?? ""}`
          setUnchecked(Boolean(r.unchecked))
          ed.put(target, r.edit ?? null, r.error)
        }}
      />
      {unchecked ? (
        <p className="text-[11px] text-faint" data-edit-unchecked>
          this browser gives no number literals: integers and exactness are checked by the runtime
        </p>
      ) : null}
      {err ? (
        <p className="text-xs text-status-bad" role="alert" data-edit-error>
          {err}
        </p>
      ) : null}
    </div>
  )
}

/** PromptEditor is step 0's turn prompt on its card, editable (a user
 * edit of step 0, index 0) — drawn only where the editor is. */
export function PromptEditor({ user }: { user: UserMessage }) {
  const ed = useEditor()
  const target: ReplayEdit = { kind: "user", step: user.step, index: user.index }
  if (!offered(ed, target)) return null
  return (
    <div className="rounded-md border border-thread/30 px-3 py-1.5" data-prompt-editor>
      <span className="eyebrow">user · the turn's prompt</span>
      <Editable target={target} recorded={user.text} label="the turn's prompt">
        <p className="text-sm whitespace-pre-wrap">{user.text}</p>
      </Editable>
    </div>
  )
}

/** InsertHere is the boundary before step `step`'s model call: a
 * message inserted there (the ADR 0019 steer shape at rest). */
export function InsertHere({ step, last = false }: { step: number; last?: boolean }) {
  const ed = useEditor()
  if (!offered(ed, { kind: "insert", step })) return null
  const words = last ? "insert a message after the last step" : `insert a message before step ${step}`
  return (
    <div data-insert-at={step} className="pl-4">
      <Editable target={{ kind: "insert", step }} recorded="" label={`a message inserted before step ${step}`}>
        <span className="block font-mono text-[10px] text-faint">· {words}</span>
      </Editable>
    </div>
  )
}

/** EditList is the drawer's list of the command's edits, by kind, each
 * droppable. */
export function EditList({ edits, onDrop }: { edits: ReplayEdit[]; onDrop: (e: ReplayEdit) => void }) {
  if (!edits.length) return null
  return (
    <ul className="space-y-0.5 text-xs" aria-label="transcript edits" data-edit-list>
      {edits.map((e) => (
        <li key={editKey(e)} className="flex items-baseline gap-2" data-edit-kind={kindOf(e)}>
          <span className="min-w-0 flex-1 truncate font-mono">{editLine(e)}</span>
          <button type="button" className="text-faint hover:underline" onClick={() => onDrop(e)}>
            drop
          </button>
        </li>
      ))}
    </ul>
  )
}

const OP_TONE: Record<string, string> = {
  same: "text-faint",
  changed: "text-thread-ink",
  added: "text-status-ok",
  removed: "text-ev-error",
}

/** The preview's state in the drawer: the answer, or the refusal
 * (refused: a 400 — the command's own sentence, which holds Run). */
export interface PreviewState {
  doc?: PreviewDoc
  error?: string
  refused?: boolean
  /** The body changed and its answer is not in yet: Run waits. */
  pending?: boolean
}

/** How long the form rests before the preview is read again. */
export const PREVIEW_DEBOUNCE_MS = 300

/** usePreview reads the preview of a body (JSON; "" — none), debounced:
 * the last body's answer wins. */
export function usePreview(body: string): PreviewState {
  // The answer and the body it answers: a body not answered yet is
  // pending (the last answer stays drawn meanwhile).
  const [st, setSt] = useState<PreviewState & { for?: string }>({})
  useEffect(() => {
    if (!body) return
    let live = true
    let answered = false
    const t = setTimeout(() => {
      postPlaygroundPreview(JSON.parse(body) as PlaygroundRunBody).then(
        (doc) => {
          answered = true
          if (live) setSt({ doc, for: body })
        },
        (e: unknown) => {
          answered = true
          if (live) setSt({ error: e instanceof Error ? e.message : String(e), refused: e instanceof ApiError && e.status === 400, for: body })
        }
      )
    }, PREVIEW_DEBOUNCE_MS)
    // A preview that never answers does not hold Run forever: past the
    // bound it is failed, not refused.
    const bound = setTimeout(() => {
      if (!live || answered) return
      live = false
      setSt((cur) => ({ doc: cur.doc, error: PREVIEW_SILENT, for: body }))
    }, PREVIEW_DEBOUNCE_MS + PREVIEW_TIMEOUT_MS)
    return () => {
      live = false
      clearTimeout(t)
      clearTimeout(bound)
    }
  }, [body])
  if (!body) return {}
  return st.for === body ? st : { doc: st.doc, pending: true }
}

/**
 * PreviewPane is the request the replay's first step will send, beside
 * the one the source step sent (the server's diff): the system, the
 * messages with an op per row (a changed row shows was and will), the
 * tools added and removed, the knobs that changed, every hole a badge,
 * the warnings, and what only a runtime checks.
 */
export function PreviewPane({ state, fromStep }: { state: PreviewState; fromStep: number }) {
  const v = state.doc ? previewView(state.doc) : null
  return (
    <div className="space-y-1.5 rounded-md border px-3 py-2" data-preview>
      <div className="eyebrow">will be sent · step {fromStep}'s request</div>
      {state.pending && v ? (
        <p className="text-[11px] text-faint" data-preview-pending>
          reading it again for the changed command…
        </p>
      ) : null}
      {state.error ? (
        <p className="text-xs text-status-bad" role="alert" data-preview-error>
          {state.error}
        </p>
      ) : null}
      {!v ? (
        state.error ? null : <p className="text-[11px] text-faint">assembling the request…</p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
            <span className="font-mono" data-preview-system={v.system}>
              system {v.system}
            </span>
            {v.changed.map((c) => (
              <span key={c} className="rounded-sm border px-1 font-mono text-thread-ink" data-preview-changed={c}>
                {c} changed
              </span>
            ))}
            {v.holes.map((h) => (
              <HoleBadge key={h.hole} hole={h.hole} />
            ))}
          </div>
          {v.added.length || v.removed.length ? (
            <p className="font-mono text-[11px]" data-preview-tools>
              {v.added.length ? `tools added: ${v.added.join(", ")}` : ""}
              {v.added.length && v.removed.length ? " · " : ""}
              {v.removed.length ? `tools removed: ${v.removed.join(", ")}` : ""}
            </p>
          ) : null}
          {v.rows ? (
            <ol className="space-y-0.5 text-xs" aria-label="messages">
              {v.rows.map((r, i) => (
                <li key={i} className="flex gap-2" data-preview-op={r.op}>
                  <span className={`w-14 shrink-0 font-mono text-[10px] ${OP_TONE[r.op]}`}>{r.op}</span>
                  <span className="w-14 shrink-0 font-mono text-[10px] text-faint">{r.role}</span>
                  <span className="min-w-0 flex-1 break-words">
                    {r.was ? (
                      <span className="block text-muted-foreground line-through" data-preview-was>
                        {r.was}
                      </span>
                    ) : null}
                    {r.will ? <span className="block" data-preview-will>{r.will}</span> : null}
                  </span>
                </li>
              ))}
            </ol>
          ) : null}
          {v.warnings.map((w, i) => (
            <p key={i} className="text-[11px] text-muted-foreground" data-preview-warning={w.kind}>
              {w.message}
            </p>
          ))}
          {v.unchecked ? (
            <p className="text-[11px] text-faint" data-preview-unchecked>
              {v.unchecked}
            </p>
          ) : null}
        </>
      )}
    </div>
  )
}

/**
 * EditsMark is a replayed run's weft.edits read back: which of its
 * source's pairs and messages the kept prefix edited (source
 * coordinates; a call's edited args or result read from this run's
 * input by its call id). Not drawn on a child run: it inherits the
 * parent's mark, its own transcript was not edited.
 */
export function EditsMark({ row, batches }: { row: Pick<RunRow, "meta" | "parent_run_id" | "forked_from">; batches?: TranscriptBatch[] }) {
  const { marks, more } = editMarks(row)
  if (!marks.length && !more) return null
  // The replay's from_step (weft.forked_from's "#N"): where its kept
  // prefix ends, so a token's step ordinal finds its pair.
  const at = /#(\d+)$/.exec(row.forked_from)?.[1]
  const from = at === undefined ? undefined : Number(at)
  return (
    <div className="space-y-1 rounded-lg border border-ev-error/30 px-4 py-2 text-xs" data-edits-mark>
      <span className="eyebrow">kept prefix edited{row.forked_from ? ` · from ${row.forked_from}` : ""}</span>
      <ul className="space-y-0.5">
        {marks.map((m, i) => (
          <li key={i} className="flex flex-wrap items-baseline gap-2" data-edit-mark={m.what}>
            <span className="rounded-sm border border-ev-error/50 px-1.5 py-px font-mono text-[10px] text-ev-error uppercase">
              {MARK_CHIPS[m.what]}
            </span>
            <span className="font-mono">{markLine(m)}</span>
            <span className="min-w-0 truncate font-mono text-muted-foreground">{markedPart(batches, m, from)}</span>
          </li>
        ))}
      </ul>
      {more ? <p className="text-faint">+{more} more (the mark is capped at 1024 B)</p> : null}
    </div>
  )
}
