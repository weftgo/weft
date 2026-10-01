// <weft-devtools> — the panel custom element (§5): shadow DOM, no
// host-framework dependency (V1, the Dv0 decision), the §5.2
// attributes and defaults. The element owns presentation only;
// state.ts owns the data. Rung 1 is a viewer (§8.1): every control
// the playground would add waits on meta.capabilities, so nothing
// write-shaped renders until the server reports it.
import type { RunRow, Usage } from "../lib/api"
import { callState, truncation, type FoldedRun, type FoldedStep, type FoldedToolCall } from "../lib/events"
import { duration, relativeTime, tokens } from "../lib/format"
import { readConfig, type PanelConfig } from "./config"
import { el, fmtJSON } from "./render"
import { PANEL_CSS } from "./styles"
import { emptyPanelState, PanelModel, strippedContent, type PanelState, type TurnView } from "./state"
import { panelStudioVersion } from "./version"

/** statusChip maps a row to the §2 turn-list status word: parked is
 * how the panel shows succeeded with pending calls. */
export function statusChip(r: RunRow): string {
  return r.status === "succeeded" && r.pending > 0 ? "parked" : r.status
}

/** studioLink builds the ⤢ deep link (§2: "open in Studio" with the
 * context carried over; Dv3 adds the step). */
export function studioLink(endpoint: string, runId: string, step?: number): string {
  const u = new URL(`runs/${runId}`, endpoint)
  if (step !== undefined) {
    u.searchParams.set("step", String(step))
    u.searchParams.set("view", "story")
  }
  return u.toString()
}

export class WeftDevtools extends HTMLElement {
  static observedAttributes = [
    "data-endpoint",
    "data-public-id",
    "data-token",
    "data-position",
    "data-open",
    "data-auto",
  ]

  private cfg: PanelConfig
  private shadow: ShadowRoot
  private model: PanelModel | null = null
  private open: boolean
  /** data-open was adopted (on first connect); toggles own it after. */
  private opened = false
  private body!: HTMLElement

  constructor() {
    super()
    this.cfg = readConfig(this)
    this.open = this.cfg.open
    this.shadow = this.attachShadow({ mode: "open" })
    this.body = el("div", "weft-root")
    this.shadow.append(el("style", undefined, PANEL_CSS), this.body)
    this.render(emptyPanelState())
  }

  connectedCallback() {
    // Attributes set before the upgrade land here, not in the
    // constructor: re-read before connecting, and adopt data-open
    // once (the user's toggles own it afterwards).
    this.cfg = readConfig(this)
    if (!this.opened) {
      this.opened = true
      this.open = this.cfg.open
    }
    this.start()
  }

  disconnectedCallback() {
    this.model?.dispose()
    this.model = null
  }

  attributeChangedCallback() {
    this.cfg = readConfig(this)
    if (!this.isConnected) return
    this.start()
  }

  /** start (re)connects: one meta request; a failure removes the
   * panel silently — no console, no retries (§5.3). */
  private async start() {
    this.model?.dispose()
    const model = new PanelModel(
      { base: this.cfg.endpoint, token: this.cfg.token },
      this.cfg.publicId,
      (s) => this.render(s)
    )
    this.model = model
    const ok = await model.start()
    if (!ok && this.cfg.auto) {
      // No Studio answered: this page does not want a panel (V4).
      this.remove()
    }
  }

  toggle() {
    this.open = !this.open
    this.render(this.model?.state ?? emptyPanelState())
  }

  toggleRaw() {
    this.model?.toggleRaw()
  }

  // ── Rendering ───────────────────────────────────────────────────

  private render(s: PanelState) {
    const root = this.body
    // The whole dock re-renders per state change; the panes' scroll
    // positions are the user's, not the data's — keep them.
    const keepScroll = (sel: string): number => {
      const n = root.querySelector(sel) as HTMLElement | null
      return n?.scrollTop ?? 0
    }
    const scrolls = [keepScroll(".weft-turns"), keepScroll(".weft-main")]
    while (root.firstChild) root.removeChild(root.firstChild)
    if (!this.open) {
      const fab = el("button", "weft-fab", "devtools", {
        title: "weft devtools — Alt+W",
      })
      fab.addEventListener("click", () => this.toggle())
      root.appendChild(fab)
      return
    }
    if (s.gone) return // removed by start(); nothing to draw
    const dock = el("div", `weft-dock weft-${this.cfg.position} weft-open`)
    dock.appendChild(this.header(s))
    const cols = el("div", "weft-cols")
    cols.appendChild(this.turnList(s))
    cols.appendChild(this.main(s))
    dock.appendChild(cols)
    dock.appendChild(this.footer(s))
    root.appendChild(dock)
    const restore = root.querySelectorAll(".weft-turns, .weft-main")
    restore.forEach((n, i) => {
      if (scrolls[i]) (n as HTMLElement).scrollTop = scrolls[i]
    })
    if (s.raw && s.turn) root.appendChild(this.rawView(s.turn))
  }

  private header(s: PanelState): HTMLElement {
    const h = el("div", "weft-head")
    const running = s.turns.some((r) => r.status === "running")
    h.appendChild(
      el("span", `weft-dot${s.live ? (running ? " weft-run" : " weft-on") : ""}`, undefined, {
        title: s.live ? "live" : "history",
      })
    )
    const agent = s.session?.agent ?? s.turns[0]?.agent ?? ""
    const scope = this.cfg.publicId || s.session?.public_id || ""
    const title = scope ? `${agent ? agent + " · " : ""}${scope}` : "latest (dev)"
    h.appendChild(el("span", "weft-title", title, { title }))
    h.appendChild(el("span", "weft-grow"))
    const inTok = s.turns.reduce((n, r) => n + (r.usage?.input_tokens ?? 0), 0)
    const outTok = s.turns.reduce((n, r) => n + (r.usage?.output_tokens ?? 0), 0)
    const stats = `${s.turns.length} turns · ${tokens(inTok)}→${tokens(outTok)} tok`
    h.appendChild(el("span", undefined, stats, { title: stats }))
    if (s.turns.length && s.selected) {
      const a = el("a", "weft-btn", "⤢", {
        href: studioLink(this.cfg.endpoint, s.selected),
        target: "_blank",
        rel: "noopener",
        title: "open in Studio",
      })
      a.style.textDecoration = "none"
      h.appendChild(a)
    }
    const raw = el("button", `weft-btn${s.raw ? " weft-active" : ""}`, "raw", {
      title: "the JSON, one keypress away (r)",
    })
    raw.addEventListener("click", () => this.toggleRaw())
    h.appendChild(raw)
    const close = el("button", "weft-btn", "–", { title: "collapse (Alt+W)" })
    close.addEventListener("click", () => this.toggle())
    h.appendChild(close)
    return h
  }

  private turnList(s: PanelState): HTMLElement {
    const list = el("div", "weft-turns")
    if (!s.turns.length) {
      list.appendChild(
        el("div", "weft-splash", this.cfg.publicId ? "no turns yet — run your app" : "no runs yet (dev)")
      )
      return list
    }
    for (const r of s.turns) {
      list.appendChild(this.turnRow(r, s.selected))
      // The experiment slot (§2): runs that forked from this turn nest
      // under it. Empty until step 8's playground capability.
      const expts = s.experiments.get(r.id) ?? []
      if (expts.length) {
        const box = el("div", "weft-expts")
        for (const x of expts) box.appendChild(this.turnRow(x, s.selected))
        list.appendChild(box)
      }
    }
    return list
  }

  private turnRow(r: RunRow, selected: string): HTMLElement {
    const chip = statusChip(r)
    const row = el("button", `weft-turn${r.id === selected ? " weft-sel" : ""}`)
    const row1 = el("div", "weft-row1", [
      el("span", `weft-chip weft-${chip}`, chip),
      el("span", "weft-id", r.id, { title: r.id }),
      el("span", "weft-when", relativeTime(r.last_seen || r.started)),
    ])
    const usage = r.usage ?? { input_tokens: 0, output_tokens: 0 }
    const row2 = el("div", "weft-row2", [
      el("span", undefined, r.model?.name ? `${r.model.provider}/${r.model.name}` : ""),
      el("span", undefined, `${r.steps} steps`),
      el("span", undefined, `${tokens(usage.input_tokens)}→${tokens(usage.output_tokens)}`),
      el("span", undefined, duration(r.started, r.finished) || "…"),
    ])
    row.append(row1, row2)
    if (r.err) row.appendChild(el("div", "weft-reason", r.err))
    row.addEventListener("click", () => void this.model?.select(r.id))
    return row
  }

  private main(s: PanelState): HTMLElement {
    const main = el("div", "weft-main")
    // The lazy subagent loader (Dv3): one delegated listener for every
    // [data-weft-child] expander the turn view renders. The open state
    // lives in the model, so a re-render never collapses what the user
    // opened.
    main.addEventListener("toggle", (e) => {
      const t = e.target as HTMLElement
      const child = t.getAttribute?.("data-weft-child")
      if (!child) return
      const turn = this.model?.state.turn
      if (!turn) return
      if ((t as HTMLDetailsElement).open) {
        turn.expanded.add(child)
        void this.model?.expandChild(child)
      } else {
        turn.expanded.delete(child)
      }
    })
    if (s.tooNew && s.meta) {
      main.appendChild(
        el("div", "weft-note weft-warn", [
          el("span", "weft-warn", "Studio is newer than this panel; update panel.js"),
          el(
            "span",
            undefined,
            `studio_version ${s.meta.studio_version} · panel built for ${panelStudioVersion()}`
          ),
        ])
      )
      return main
    }
    if (!s.turn) {
      main.appendChild(el("div", "weft-splash", "select a turn"))
      return main
    }
    main.appendChild(this.turnView(s))
    return main
  }

  /** turnView draws the step story (§2): the prompt, per step the
   * model text (reasoning collapsed), tool calls name(args) → result,
   * finish reason and usage with cached/reasoning splits, pending
   * approvals read-only, and the honesty notes. */
  private turnView(s: PanelState): HTMLElement {
    const t = s.turn
    if (!t) return el("div")
    const wrap = el("div")
    wrap.appendChild(this.notes(t))
    const prompt = promptText(t)
    if (prompt) wrap.appendChild(el("div", "weft-note", prompt))
    wrap.appendChild(renderFolded(t.folded, this.rowOf(t.id)?.status ?? "running", t))
    if (t.folded.pending.length) wrap.appendChild(this.approvals(t.folded.pending))
    return wrap
  }

  /** notes renders the honesty rules (§2): truncation badges live on
   * the calls themselves (renderCall); these are the run-level notes —
   * interrupted, gaps, stripped content, and a max_tokens finish. */
  private notes(t: TurnView): HTMLElement {
    const box = el("div")
    const row = this.rowOf(t.id)
    if (row?.status === "interrupted") {
      box.appendChild(el("div", "weft-note weft-warn", "never completed — this run was interrupted"))
    }
    if (t.gaps.length) {
      box.appendChild(
        el("div", "weft-note weft-warn", `${t.gaps.length} events missing (positions skipped)`, {
          title: t.gaps.join(", "),
        })
      )
    }
    if (row?.stop_reason === "max_tokens") {
      box.appendChild(el("div", "weft-note weft-warn", "stopped at the output token limit (max_tokens)"))
    }
    if (strippedContent(t.spans)) {
      box.appendChild(el("div", "weft-note", "content not captured by this app (weft.content = stripped)"))
    }
    return box
  }

  /** approvals shows parked calls read-only (§2): continue / skip /
   * resolve need the playground verbs (rung 2+, §8.2) — the panel
   * renders controls only for capabilities meta reports, and v1
   * reports none. */
  private approvals(pending: { id: string; name: string; args?: unknown }[]): HTMLElement {
    const box = el("div", "weft-step")
    box.appendChild(el("div", "weft-step-h", [el("span", undefined, "awaiting decision (read-only)")]))
    const body = el("div", "weft-step-b")
    for (const call of pending) {
      const line = el("div", "weft-call")
      line.appendChild(
        el("div", "weft-call-h", [
          el("span", "weft-name", call.name),
          el("span", "weft-args", call.args === undefined ? "(…)" : fmtJSON(call.args)),
          el("span", "weft-badge weft-info", "parked"),
        ])
      )
      line.appendChild(el("div", "weft-res", "continue / skip / resolve need the playground capability"))
      body.appendChild(line)
    }
    box.appendChild(body)
    return box
  }

  private footer(s: PanelState): HTMLElement {
    const line = "prompts, args and results from your app, via your Studio"
    if (strippedContent(s.turn?.spans ?? null)) {
      return el("div", "weft-footer", [
        el("span", undefined, line),
        el("span", undefined, " · content is stripped for this destination"),
      ])
    }
    return el("div", "weft-footer", line)
  }

  private rawView(t: TurnView): HTMLElement {
    return el("pre", "weft-raw", fmtJSON({ doc: t.doc, events: t.events, transcript: t.transcript }))
  }

  private rowOf(id: string): RunRow | undefined {
    return this.model?.rowOf(id)
  }
}

/** promptText lifts the turn's input (the transcript's user messages)
 * so the step story starts where the user did. */
function promptText(t: TurnView): string {
  if (!t.transcript) return ""
  return t.transcript.batches
    .flatMap((b) => b.messages)
    .filter((m) => m.role === "user")
    .flatMap((m) => m.content)
    .filter((p): p is Extract<typeof p, { type: "text" }> => p.type === "text")
    .map((p) => p.text)
    .join("\n")
}

/** renderFolded draws the turn view (§2). */
export function renderFolded(view: FoldedRun, runStatus: string, t?: TurnView): HTMLElement {
  const wrap = el("div")
  if (view.model?.name) {
    wrap.appendChild(el("div", "weft-reason", `${view.model.provider}/${view.model.name}`))
  }
  for (const step of view.steps) wrap.appendChild(renderStep(step, runStatus, t))
  return wrap
}

function renderStep(step: FoldedStep, runStatus: string, t?: TurnView): HTMLElement {
  const card = el("div", "weft-step")
  const head = el("div", "weft-step-h", [
    el("span", undefined, `step ${step.index}`),
    el("span", "weft-grow"),
  ])
  if (step.finish) {
    head.appendChild(el("span", undefined, step.finish.reason))
    head.appendChild(el("span", undefined, usageLine(step.finish.usage)))
  }
  card.appendChild(head)
  const body = el("div", "weft-step-b")
  if (step.reasoning) {
    const d = el("details", "weft-collapsible")
    d.appendChild(el("summary", undefined, "reasoning"))
    d.appendChild(el("div", undefined, step.reasoning))
    body.appendChild(d)
  }
  if (step.text) body.appendChild(el("div", undefined, step.text))
  if (step.steer) body.appendChild(el("div", "weft-note", `steered: ${step.steer.text}`))
  for (const call of step.toolCalls) body.appendChild(renderCall(call, runStatus, t))
  card.appendChild(body)
  return card
}

function renderCall(call: FoldedToolCall, runStatus: string, t?: TurnView): HTMLElement {
  const box = el("div", "weft-call")
  const state = callState(call, runStatus)
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", call.name),
    el("span", "weft-args", argsText(call)),
  ])
  box.appendChild(head)
  if (call.childRunId && t) {
    head.appendChild(el("span", "weft-badge weft-info", "subagent", { title: call.childRunId }))
    box.appendChild(childBlock(call.childRunId, t))
  }
  if (call.result) {
    const ms = spanMs(t, call)
    if (ms) head.appendChild(el("span", "weft-badge weft-info", ms))
    const trunc = truncation(call.result.content)
    if (trunc) {
      head.appendChild(
        el(
          "span",
          "weft-badge",
          trunc.kind === "bytes"
            ? `truncated ${trunc.bytes} bytes`
            : "not executed (max_tokens)"
        )
      )
    }
    if (call.result.isError) head.appendChild(el("span", "weft-badge weft-err", "error"))
    box.appendChild(el("div", "weft-res", call.result.content))
  } else if (state === "running") {
    box.appendChild(el("div", "weft-res", "running…"))
  } else {
    box.appendChild(el("div", "weft-res weft-warn", "never completed"))
  }
  return box
}

/** spanMs reads the tool call's wall time off its execute_tool span
 * when the spans are loaded (the waterfall's numbers, in line). */
function spanMs(t: TurnView | undefined, call: FoldedToolCall): string {
  if (!t?.spans) return ""
  const span = t.spans.find(
    (sp) => sp.name === "execute_tool" && sp.attrs?.["gen_ai.tool.name"] === call.name
  )
  if (!span) return ""
  const ms = Date.parse(span.end) - Date.parse(span.start)
  if (!Number.isFinite(ms) || ms < 0) return ""
  return `${Math.round(ms)}ms`
}

/** childBlock is the lazy subagent expander (§2, Dv3): the call's
 * child run folds on demand and nests under the call. The element
 * delegates the toggle through [data-weft-child]. */
function childBlock(childId: string, t: TurnView): HTMLElement {
  const child = t.children.get(childId)
  const details = el("details", "weft-collapsible")
  details.setAttribute("data-weft-child", childId)
  if (t.expanded.has(childId)) details.setAttribute("open", "")
  details.appendChild(el("summary", undefined, `subagent ${shortId(childId)}`))
  if (!child) {
    details.appendChild(el("div", undefined, "loading the subagent's turn…"))
  } else {
    details.appendChild(renderFolded(child.folded, "succeeded"))
  }
  return details
}

function shortId(id: string): string {
  const parts = id.split("/")
  return parts[parts.length - 1] || id
}

function argsText(call: FoldedToolCall): string {
  if (call.args !== undefined) {
    try {
      return `(${JSON.stringify(call.args)})`
    } catch {
      return "(?)"
    }
  }
  return call.streamedArgs ? `(${call.streamedArgs}…)` : "(…)"
}

/** usageLine renders usage with the cached/reasoning splits §2 names. */
function usageLine(u: Usage): string {
  const parts = [`${tokens(u.input_tokens)}→${tokens(u.output_tokens)} tok`]
  if (u.cached_input_tokens) parts.push(`${tokens(u.cached_input_tokens)} cached`)
  if (u.reasoning_tokens) parts.push(`${tokens(u.reasoning_tokens)} reasoning`)
  if (u.cache_write_tokens) parts.push(`${tokens(u.cache_write_tokens)} cache-write`)
  return parts.join(" · ")
}
