// The Request tab (plan E1.2): the panel half of E1, mirroring the
// Studio step card's Request pane (components/studio/step-request.tsx)
// with the same pure readings (lib/request-pane.ts, lib/requests.ts,
// lib/diff.ts) — no React, the keyed patch's nodes. Per step: the
// chips the hashes decide ("changed by PrepareStep", the neutral
// "prompt changed at this step", "overridden by experiment", "catalog
// changed at this step"), the system prompt with its bounded diff
// (vs the previous step; at the first step vs the registered
// instructions, only when the panel can read the manifest — a
// verified one decides, an unverified one is captioned so), the
// messages sent (count · bytes, the last three inline, the rest in
// D4's tree), the tool catalog (names → description, policy chips,
// the schema in D4's tree), params with "adapter default", tool
// choice, thinking, the attempts, a subagent call's child request
// (A10) and the step's compaction marker (A9). The provider wire pair
// (plan A6) is not drawn here: a step whose row carries raw gets one
// "wire ↗" link to the Studio step card's Wire tab (G1's runLink,
// pane wire) — the panel never draws a body.
// Every absent block is a badge with its reason; a run-wide hole
// (hidden for a read-scoped token, not_recorded before A1) is that
// badge and nothing else — a read token never asked for a byte.
import { skippedLine } from "../lib/onion"
import { plural } from "../lib/format"
import type { Manifest, RequestRow, RunCompaction, ToolEntry } from "../lib/api"
import { isHoleRef } from "../lib/api"
import { attemptLine, factsFromRows } from "../lib/attempts"
import { attemptTag, preparedChip, thinkingWords } from "../lib/cards"
import { compactionsOf, isSessionMarker, messageLine } from "../lib/compaction"
import { diffLinesBounded } from "../lib/diff"
import type { BoundedDiff } from "../lib/diff"
import type { FoldedToolCall } from "../lib/events"
import {
  baselineCaption,
  CHIP_CATALOG,
  CHIP_EXPERIMENT,
  CHIP_PREPARE_STEP,
  CHIP_PROMPT_CHANGED,
  composedFromInstructions,
  composeSystem,
  messagesSent,
  overrideOf,
  previousRows,
  promptText,
  registeredAgent,
  snippetsOf,
  toolSetMayExplain,
} from "../lib/request-pane"
import type { MessagesSent, PromptBaseline } from "../lib/request-pane"
import {
  hasWire,
  paramFields,
  REQUEST_DIFF_CUT_LABEL,
  REQUEST_MESSAGES_GAP_REASON,
  REQUEST_NO_INDEX_REASON,
  REQUEST_NO_RECORD_REASON,
  REQUEST_NOT_RECORDED_LABEL,
  REQUEST_NOT_STORED,
  shortHash,
} from "../lib/requests"
import { bytes } from "../lib/summarize"
import { badge, requestCapped, requestHole } from "./badges"
import type { BadgeNote } from "./badges"
import { MAX_REQUEST_PAGES, REQUEST_PAGE } from "./client"
import { el, NEW_TAB, on } from "./render"
import { href, runPage } from "../lib/links"
import type { PanelRequests, TurnView } from "./state"
import { treeView } from "./tree"
import type { TreeCtx, TreeState } from "./tree"

/** Lines of system prompt shown before "show all". */
const PROMPT_LINES = 12

/** What the tab reads beside the turn: all of it the element's. */
export interface RequestTabDeps {
  t: TurnView
  /** The step the user is reading (J/K, a click, C4.2's select). */
  step: number | undefined
  running: boolean
  /** The manifest: undefined while unasked or in flight (asking is
   * the call's side effect), null when the panel cannot read one. */
  manifest: () => Manifest | null | undefined
  /** Keys of what the user opened (the element's openKeys). */
  keys: Set<string>
  /** One tree state per tree (the schema of a tool, the earlier
   * messages), by key. */
  tree: (key: string) => TreeState
  cx: TreeCtx
  select: (step: number) => void
  /** The attempt the user picked per step (default: the last). */
  pick: Map<number, number>
  redraw: () => void
  compaction: (c: RunCompaction) => HTMLElement
  /** A subagent call's child request (its step 0, A10). */
  child: (call: FoldedToolCall) => HTMLElement
  /** Why the server serves no request record, in its words
   * (/api/meta's capabilities_off.requests), when it says. */
  notServed?: string
  /** The Studio the wire link opens (the element's endpoint). */
  base: string
}

/** A hole inside the pane: D5's badge (its reason and fix the title). */
const hole = (name: string, note?: BadgeNote): HTMLElement =>
  el("div", "weft-rq-hole", [badge(name, note)], { "data-weft-rq-hole": name })
/** A record the run does not hold (a HoleRef), worded as the run
 * page's HoleRefView: not_recorded names the version, stripped carries
 * the tools route's reason and fix. */
const refHole = (name: string, d: RequestTabDeps): HTMLElement =>
  hole(
    name,
    name === "not_recorded"
      ? { label: REQUEST_NOT_RECORDED_LABEL }
      : name === "stripped"
        ? d.t.requests?.stripped
        : undefined
  )
/** A run-wide hole: the badge, then its reason and fix as words. */
const runHole = (name: string, note?: BadgeNote): HTMLElement =>
  el("div", "weft-rq-hole", requestHole(name, note), {
    "data-weft-rq-hole": name,
  })

const row = (label: string, kids: Node[], key: string) =>
  el(
    "div",
    "weft-rq-row",
    [el("span", "weft-rq-k", label), el("div", "weft-rq-v", kids)],
    { "data-key": key, "data-weft-rq": key }
  )

const mono = (text: string, cls = "weft-res") => el("span", cls, text)

/** A chip of the tab's header (the Studio pane's Mark). */
const chip = (mark: string, text: string) =>
  el("span", "weft-badge weft-info", text, { "data-weft-mark": mark })

/** The composition checks, by their inputs (sha256 is async): a
 * pending one draws no chip and redraws when it answers. Bounded: past
 * COMPOSED_MAX the oldest goes (a Map iterates in insertion order). */
export const composedCache = new Map<string, boolean | null | "pending">()
export const COMPOSED_MAX = 64

/** What the hashes say about one step's prompt (step-request.tsx's
 * usePromptFacts, without the hooks). */
interface Facts {
  prompt?: "prepare_step" | "either"
  experiment: boolean
  base?: PromptBaseline
  /** The first step's text differs from the configured instructions
   * and the panel holds no manifest to say why. */
  noManifest: boolean
}

/** The diffs drawn, by row: a redraw diffs nothing again. */
const diffMemo = new WeakMap<RequestRow, { base: string; diff: BoundedDiff }>()

/** stepsOf: every step the turn ran or the record names, in order —
 * the tab's picker, and what J/K walk while it is open. */
export function stepsOf(t: TurnView, req: PanelRequests | null): number[] {
  const s = new Set<number>(t.folded.steps.map((x) => x.index))
  for (const k of req?.steps.keys() ?? []) s.add(k)
  return [...s].sort((a, b) => a - b)
}

function remember(key: string, v: boolean | null | "pending") {
  composedCache.set(key, v)
  if (composedCache.size > COMPOSED_MAX)
    composedCache.delete(composedCache.keys().next().value!)
}

const DIFF_MARK = { add: "+ ", del: "− ", same: "  " }

function diffView(
  r: RequestRow,
  base: PromptBaseline,
  text: string
): HTMLElement {
  const box = el(
    "div",
    "weft-diff",
    [el("div", "weft-diff-h", baselineCaption(base))],
    { "data-weft-prompt-diff": base.kind }
  )
  if (cut(r) || base.truncated) {
    // A cut tail would read as lines a PrepareStep removed.
    box.append(hole("truncated", { label: REQUEST_DIFF_CUT_LABEL }))
    return box
  }
  let m = diffMemo.get(r)
  if (!m || m.base !== base.text)
    diffMemo.set(
      r,
      (m = { base: base.text, diff: diffLinesBounded(base.text, text) })
    )
  const diff = m.diff
  if ("tooLarge" in diff) {
    box.append(
      el(
        "div",
        "weft-reason",
        `too large to diff (${diff.tooLarge.before} → ${diff.tooLarge.after} lines)`,
        { "data-weft-diff-too-large": "" }
      )
    )
    return box
  }
  for (const d of diff.rows)
    box.append(
      el(
        "div",
        `weft-diff-row weft-diff-${d.kind}`,
        `${DIFF_MARK[d.kind]}${d.text}`,
        { "data-weft-diff": d.kind }
      )
    )
  return box
}

function messagesView(d: RequestTabDeps, r: RequestRow, step: number): Node[] {
  const comps = compactionsOf(d.t.doc)
  let memo = sentMemo.get(r)
  // running: a running run's batch not yet landed is no_transcript, not
  // gap — the run page's reading (messagesSent's fifth argument).
  if (
    !memo ||
    memo.tr !== d.t.transcript ||
    memo.comps !== d.t.doc ||
    memo.running !== d.running
  )
    sentMemo.set(
      r,
      (memo = {
        tr: d.t.transcript,
        comps: d.t.doc,
        running: d.running,
        m: messagesSent(r, d.t.transcript, comps, undefined, d.running),
      })
    )
  const m = memo.m
  const n = plural(m.count, "message")
  const out: Node[] = [
    el(
      "div",
      "weft-res",
      m.bytes !== undefined ? `${n} · ${bytes(m.bytes)}` : n,
      { "data-weft-messages-line": "" }
    ),
  ]
  // compacted: the request saw a compaction view (the marker above);
  // gap: the transcript does not hold what the record counts; no index:
  // capture was off (stripped), or the view could not be recorded.
  if (m.hole === "no_transcript")
    out.push(el("div", "weft-reason", "bytes when the transcript is read"))
  else if (m.hole)
    out.push(
      m.hole === "compacted" ||
        (m.hole === "no_index" && r.content === "stripped")
        ? hole(m.hole === "compacted" ? m.hole : "stripped")
        : hole("gap", {
            reason:
              m.hole === "gap"
                ? REQUEST_MESSAGES_GAP_REASON
                : REQUEST_NO_INDEX_REASON,
          })
    )
  if (m.last.length) {
    const list = el("div", "", "", {
      "data-weft-messages-last": "",
    })
    for (const msg of m.last)
      list.append(el("div", "weft-res", messageLine(msg)))
    out.push(list)
  }
  if (m.before.length) {
    const key = `${d.t.id}\u0000rq-earlier\u0000${step}`
    const [b, open] = toggle(
      d,
      key,
      (o) =>
        `${o ? "▾" : "▸"} ${plural(m.before.length, "earlier message")} (raw)`
    )
    out.push(b)
    if (open)
      out.push(
        el(
          "div",
          "",
          [
            treeView(
              m.before,
              d.tree(`earlier\u0000${step}`),
              `step${step}-messages`,
              d.cx
            ),
          ],
          { "data-weft-messages-earlier": "" }
        )
      )
  }
  return out
}

function factsOf(
  d: RequestTabDeps,
  req: PanelRequests,
  prev: RequestRow | undefined,
  step: number,
  r: RequestRow
): Facts {
  const doc = d.t.doc
  const first = prev === undefined
  const insHash = doc?.instructions_hash
  const override = overrideOf(d.t.spans ?? undefined, d.t.id)
  const experiment = first && override?.instructions === true
  const differs = first && !!insHash && r.system_hash !== insHash
  const changed = !first && (req.steps.get(step)?.promptChanged ?? false)
  const names = r.body.tools.names
  const prevNames = prev?.body.tools.names ?? []
  const toolsMoved =
    changed &&
    (names.length !== prevNames.length ||
      names.some((n, i) => n !== prevNames[i]))
  const need = differs || experiment || toolsMoved
  const manifest = need ? d.manifest() : undefined
  const settled = !need || manifest !== undefined
  const reg = doc?.agent
    ? registeredAgent(
        manifest ?? undefined,
        doc.agent,
        doc.manifest_hash || undefined
      )
    : undefined
  const known = snippetsOf(names, reg?.agent)
  const snippets = reg?.verified ? known : undefined
  const text = promptText(r)
  if (!first) {
    const before = promptText(prev)
    return {
      prompt: !changed
        ? undefined
        : toolsMoved && (!settled || toolSetMayExplain(prevNames, names, reg))
          ? "either"
          : "prepare_step",
      experiment: false,
      noManifest: false,
      base:
        changed && before !== undefined && text !== undefined
          ? {
              kind: "previous",
              step: prev.step,
              text: before,
              truncated: cut(prev),
            }
          : undefined,
    }
  }
  const isComposed =
    differs && settled ? composed(r, insHash, snippets, d.redraw) : undefined
  const registered =
    reg && reg.agent.instructions !== undefined && known
      ? composeSystem(reg.agent.instructions, known)
      : undefined
  return {
    prompt: isComposed === false ? "prepare_step" : undefined,
    experiment,
    noManifest: (differs || experiment) && manifest === null,
    base:
      registered !== undefined && text !== undefined && registered !== text
        ? {
            kind: "registered",
            text: registered,
            verified: reg?.verified,
            overridden: experiment,
          }
        : undefined,
  }
}

function promptView(
  d: RequestTabDeps,
  r: RequestRow,
  facts: Facts,
  scope: string
): Node[] {
  const p = r.prompt
  if (!r.system_hash) return [mono("no system prompt")]
  if (!p || isHoleRef(p))
    return [
      mono(shortHash(r.system_hash), "weft-rq-hash"),
      ...(p ? [refHole(p.badge, d)] : []),
    ]
  // A record whose body did not parse keeps its hash, not its text:
  // its badge, and no prompt box (an empty one would read as no
  // prompt) — nor a diff over it (promptText says undefined).
  if (p.content === "derived")
    return [mono(shortHash(p.hash), "weft-rq-hash"), hole("derived")]
  const lines = p.text.split("\n")
  const long = lines.length > PROMPT_LINES
  const [more, all] = long
    ? toggle(d, `${scope}\u0000rq-prompt`, (o) =>
        o ? "show less" : `show all ${lines.length} lines`
      )
    : [null, true]
  const out: Node[] = [
    el(
      "div",
      "weft-res weft-rq-prompt",
      long && !all ? `${lines.slice(0, PROMPT_LINES).join("\n")}\n…` : p.text,
      { "data-weft-prompt": "" }
    ),
  ]
  const foot = el("div", "weft-holes", [
    mono(shortHash(p.hash), "weft-rq-hash"),
  ])
  if (more) foot.append(more)
  out.push(foot)
  if (p.truncated_bytes > 0)
    out.push(hole("truncated", { bytes: p.truncated_bytes }))
  if (facts.base && facts.base.text !== p.text)
    out.push(diffView(r, facts.base, p.text))
  return out
}

function composed(
  r: RequestRow,
  insHash: string,
  snippets: string[] | undefined,
  redraw: () => void
): boolean | undefined {
  const key = [
    r.system_hash,
    insHash,
    r.body.tools.names.join(","),
    snippets?.join("\u0000") ?? "\u0001",
  ].join("|")
  const got = composedCache.get(key)
  if (got === "pending") return undefined
  if (got !== undefined) return got ?? undefined
  remember(key, "pending")
  composedFromInstructions(r, insHash, snippets).then(
    (v) => {
      remember(key, v ?? null)
      redraw()
    },
    () => remember(key, null)
  )
  return undefined
}

/** messagesSent per row and the records it read: a redraw resolves
 * nothing again, and the earlier messages' tree keeps its root. */
const sentMemo = new WeakMap<
  RequestRow,
  { tr: unknown; comps: unknown; running: boolean; m: MessagesSent }
>()

function catalogView(
  d: RequestTabDeps,
  r: RequestRow,
  scope: string,
  step: number
): Node[] {
  const c = r.tools
  const names = r.body.tools.names
  if (!r.catalog_hash) return [mono("no tools offered")]
  if (!c || isHoleRef(c)) {
    const out: Node[] = []
    if (names.length) out.push(mono(names.join(", ")))
    out.push(
      c ? refHole(c.badge, d) : mono(shortHash(r.catalog_hash), "weft-rq-hash")
    )
    return out
  }
  if (c.content === "derived")
    return [mono(shortHash(c.hash), "weft-rq-hash"), hole("derived")]
  const list = el("div", "weft-rq-tools")
  for (const t of c.tools) {
    const key = `${scope}\u0000rq-tool\u0000${t.name}`
    const [b, open] = toggle(d, key, (o) => `${o ? "▾" : "▸"} ${t.name}`)
    b.className = "weft-btn weft-rq-tool"
    const item = el("div", "", [b], {
      "data-key": `tool:${t.name}`,
      "data-weft-tool": t.name,
    })
    if (open) {
      item.append(
        el("div", "weft-res", t.description),
        toolChips(t),
        treeView(
          t.schema,
          d.tree(`schema\u0000${step}\u0000${t.name}`),
          `${t.name}-schema`,
          d.cx
        )
      )
    }
    list.append(item)
  }
  // The tools an MCP listing skipped (O8): never offered, greyed —
  // lib/onion.ts's words, the run page's.
  for (const k of c.skipped ?? [])
    list.append(el("div", "weft-note", skippedLine(k)))
  const out: Node[] = [
    list,
    el("div", "weft-holes", [mono(shortHash(c.hash), "weft-rq-hash")]),
  ]
  if (c.truncated_bytes > 0)
    out.push(hole("truncated", { bytes: c.truncated_bytes }))
  return out
}

/** picker is a row of pressed/unpressed buttons (the steps, a step's
 * attempts). */
function picker(
  label: string,
  items: number[],
  cur: number,
  pick: (n: number) => void,
  words = (n: number) => `${label} ${n}`
): HTMLElement {
  const box = el("div", "weft-holes weft-rq-pick", "", {
    role: "group",
    "aria-label": label,
    "data-key": label,
  })
  for (const n of items) {
    const b = el(
      "button",
      `weft-btn${n === cur ? " weft-active" : ""}`,
      words(n),
      {
        type: "button",
        "aria-pressed": String(n === cur),
        [`data-weft-rq-${label}`]: String(n),
      }
    )
    on(b, "click", () => pick(n))
    box.append(b)
  }
  return box
}

/** A toggle the user opened, kept in the element's open keys. */
function toggle(
  d: RequestTabDeps,
  key: string,
  label: (open: boolean) => string
): [HTMLElement, boolean] {
  const open = d.keys.has(key)
  const b = el("button", "weft-btn weft-rq-more", label(open), {
    type: "button",
    "aria-expanded": String(open),
  })
  on(b, "click", () => {
    if (d.keys.has(key)) d.keys.delete(key)
    else d.keys.add(key)
    d.redraw()
  })
  return [b, open]
}

/** renderRequestTab is the Request tab's panel content. */
export function renderRequestTab(d: RequestTabDeps): HTMLElement {
  const t = d.t
  const req = t.requests
  const box = el("div", "weft-rq")
  // A run-wide hole: the badge and its words, nothing else (a
  // read-scoped token's hidden; a run older than the record).
  if (req?.badge) {
    box.append(runHole(req.badge, { reason: req.reason, fix: req.fix }))
    return box
  }
  if (!req) {
    box.append(
      t.doc?.requests_badge
        ? runHole(t.doc.requests_badge)
        : runHole("not_recorded", { cause: "not_served", reason: d.notServed })
    )
    return box
  }
  if (req.error) {
    box.append(
      el(
        "span",
        "weft-badge weft-err",
        `request could not be read: ${req.error}`
      )
    )
    return box
  }
  const steps = stepsOf(t, req)
  const step =
    d.step !== undefined && steps.includes(d.step) ? d.step : (steps[0] ?? 0)
  // The step picker: J/K move it too (D1), a click selects (⤢ carries it).
  box.append(picker("step", steps, step, d.select))
  const pane = el("div", "weft-rq-pane", "", {
    "data-key": `rq${step}`,
    "data-weft-rq-pane": String(step),
  })
  box.append(pane)

  // The compaction markers this step's request saw (A9): the session
  // marker at the first step (the run started on a compacted context),
  // a run-scope view on its own step.
  const comps = compactionsOf(t.doc)
  for (const c of comps)
    if (isSessionMarker(c) ? step === steps[0] : c.step === step)
      pane.append(d.compaction(c))

  const mine = req.steps.get(step)
  const rows = mine?.rows ?? []
  if (!rows.length) {
    pane.append(
      d.running
        ? el("div", "weft-reason", REQUEST_NOT_STORED)
        : req.truncated
          ? requestCapped(MAX_REQUEST_PAGES * REQUEST_PAGE)
          : hole("gap", { reason: REQUEST_NO_RECORD_REASON })
    )
    return box
  }
  const prev = previousRows([...req.steps.values()].flatMap((s) => s.rows))
  const first = rows[0]
  const picked = d.pick.get(step)
  const r = rows.find((x) => x.attempt === picked) ?? rows[rows.length - 1]
  const facts = factsOf(d, req, prev.get(step), step, first)

  // The header: model, the attempts, the chips.
  const head = el("div", "weft-holes weft-rq-head", "", {
    "data-key": "head",
  })
  const model = [r.body.model.provider, r.body.model.name]
    .filter(Boolean)
    .join("/")
  if (model) head.append(mono(model))
  const folded = t.folded.steps.find((s) => s.index === step)
  const line = attemptLine(
    factsFromRows(
      rows,
      !!folded?.finish || (folded?.toolCalls.length ?? 0) > 0,
      d.running
    )
  )
  if (line)
    head.append(
      el("span", "weft-badge weft-info", line, { "data-weft-attempts": "" })
    )
  if (facts.prompt)
    head.append(
      chip(
        "prompt",
        facts.prompt === "either" ? CHIP_PROMPT_CHANGED : CHIP_PREPARE_STEP
      )
    )
  // The step's PrepareStep parts (O9) and the run's overridden knobs
  // (UX-S5b-03), the run page's chips.
  const prep = preparedChip(first.body.prepared)
  if (prep) head.append(chip("prepared", prep))
  if (
    !prev.get(step) &&
    overrideOf(d.t.spans ?? undefined, d.t.id)?.fields.length
  )
    head.append(chip("experiment", CHIP_EXPERIMENT))
  if (mine?.catalogChanged) head.append(chip("catalog", CHIP_CATALOG))
  // The wire pair lives in Studio (A6.2): one link, when a row has it.
  if (rows.some(hasWire))
    head.append(
      el("a", "weft-btn", "wire ↗", {
        href: href(
          d.base,
          runPage(t.id, { step, view: "story", pane: "wire" })
        ),
        ...NEW_TAB,
        title: "the step's wire request and response, in Studio",
        "data-weft-wire-link": "",
      })
    )
  pane.append(head)
  // The attempts: each one's request, picked here (a retry may differ).
  if (rows.length > 1)
    pane.append(
      picker(
        "attempt",
        rows.map((x) => x.attempt),
        r.attempt,
        (n) => {
          d.pick.set(step, n)
          d.redraw()
        },
        (n) => attemptTag(d.t.spans, d.t.id, step, n)
      )
    )

  if (r.content)
    pane.append(
      hole(r.content, r.content === "stripped" ? req.stripped : undefined)
    )
  if (facts.noManifest)
    pane.append(
      el(
        "div",
        "weft-reason",
        "no manifest readable here: no diff vs the registered instructions",
        { "data-weft-no-manifest": "" }
      )
    )

  const scope = `${t.id}\u0000${step}`
  const b = r.body
  // The diff compares each step's first attempt; a retry sent the same
  // prompt unless its hash says otherwise.
  const f =
    facts.base && r.system_hash === first.system_hash
      ? facts
      : { ...facts, base: undefined }
  pane.append(
    row("system", promptView(d, r, f, scope), "system"),
    row("messages", messagesView(d, r, step), "messages"),
    row("tools", catalogView(d, r, scope, step), "tools")
  )
  const params = el("div", "weft-rq-params")
  for (const [k, v] of paramFields(r))
    params.append(
      el(
        "div",
        "",
        [
          el("span", "weft-rq-pk", k),
          el("span", v === "adapter default" ? "weft-rq-def" : "weft-res", v),
        ],
        { "data-weft-param": k }
      )
    )
  pane.append(
    row("params", [params], "params"),
    row(
      "tool choice",
      [
        mono(
          b.tool_choice
            ? `${b.tool_choice.mode}${b.tool_choice.name ? ` (${b.tool_choice.name})` : ""}`
            : "adapter default"
        ),
      ],
      "tool_choice"
    ),
    row("thinking", [mono(thinkingWords(b.thinking))], "thinking")
  )
  // The step's subagent calls: the child's own step-0 request (A10).
  const calls = folded?.toolCalls.filter((c) => c.childRunId) ?? []
  if (calls.length)
    pane.append(
      row(
        "subagents",
        calls.map((c) => d.child(c)),
        "subagents"
      )
    )
  return box
}

function cut(r: RequestRow | undefined): boolean {
  const p = r?.prompt
  return !!p && !isHoleRef(p) && p.truncated_bytes > 0
}

/** A tool's policy chips (the Studio pane's ToolChips). */
function toolChips(t: ToolEntry): HTMLElement {
  const box = el("div", "weft-holes")
  for (const c of [
    `timeout ${t.timeout_ms > 0 ? `${t.timeout_ms} ms` : "none"}`,
    `approval ${t.approval ? "required" : "no"}`,
    `replay ${t.replay || "never"}`,
    `result cap ${t.max_result_bytes > 0 ? bytes(t.max_result_bytes) : "off"}`,
    t.sequential ? "sequential" : "",
    `source ${t.source || "?"}`,
  ])
    if (c) box.append(el("span", "weft-chip", c))
  return box
}
