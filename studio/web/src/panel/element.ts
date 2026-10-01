// <weft-devtools> — the panel custom element (§5): shadow DOM, no
// host-framework dependency (V1, the Dv0 decision), the §5.2
// attributes and defaults. The element owns presentation only;
// state.ts owns the data. Rung 1 is a viewer (§8.1); rung 2's
// experiment drawer and approval controls render only when
// meta.capabilities reports the playground (§8.5 item 3).
import type { RunRow, Usage } from "../lib/api"
import { diffLines, diffSummary } from "../lib/diff"
import { callState, truncation, type FoldedRun, type FoldedStep, type FoldedToolCall } from "../lib/events"
import { duration, relativeTime, tokens } from "../lib/format"
import { readConfig, type PanelConfig } from "./config"
import { el, fmtJSON, waterfall } from "./render"
import { PANEL_CSS } from "./styles"
import { emptyPanelState, PanelModel, strippedContent, type PanelState, type TurnView } from "./state"
import type { ExperimentDraft } from "./playground"
import { panelStudioVersion } from "./version"

/** hasCapability reports whether meta lists the named capability (the
 * panel renders a control only when the server reports it, §8.5). */
export function hasCapability(s: PanelState, name: string): boolean {
  return s.meta?.capabilities?.includes(name) ?? false
}

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
  /** The ? shortcuts overlay (Dv3). */
  private keys = false
  private body!: HTMLElement
  private onKey = (e: KeyboardEvent) => this.keydown(e)

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
    // §5.2's keyboard: Alt+W (and Ctrl+Shift+W where the browser
    // delivers it — Q4) toggles the dock, ? lists the keys, r flips
    // the raw JSON, Esc closes. Keys never fire while the user types
    // in the host page's own inputs.
    window.addEventListener("keydown", this.onKey)
    this.start()
  }

  disconnectedCallback() {
    window.removeEventListener("keydown", this.onKey)
    this.model?.dispose()
    this.model = null
  }

  /** keydown is the whole keyboard surface; bare presses only. */
  private keydown(e: KeyboardEvent): void {
    const t = e.target as HTMLElement | null
    if (
      t &&
      (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" ||
        t.isContentEditable)
    )
      return
    const toggleCombo =
      (e.altKey && !e.ctrlKey && !e.shiftKey && e.code === "KeyW") || // Q4's pick
      (e.ctrlKey && e.shiftKey && !e.altKey && e.code === "KeyW") // where delivered
    if (toggleCombo) {
      e.preventDefault()
      this.keys = false
      this.toggle()
      return
    }
    if (e.key === "Escape") {
      if (this.keys) {
        this.keys = false
        this.render(this.model?.state ?? emptyPanelState())
      } else if (this.open) this.toggle()
      return
    }
    if (!this.open) return
    if (e.key === "?") {
      e.preventDefault()
      this.keys = !this.keys
      this.render(this.model?.state ?? emptyPanelState())
      return
    }
    if (e.key === "r" || e.key === "R") {
      e.preventDefault()
      this.toggleRaw()
    }
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
    if (this.keys) root.appendChild(this.shortcuts())
  }

  /** shortcuts is §5.2's ? overlay: the keys, and the collision Q4
   * records (Alt+W is the primary because Chrome eats Ctrl+Shift+W
   * as close-window before any page can see it). */
  private shortcuts(): HTMLElement {
    const box = el("div", "weft-keys")
    const dl = el("dl")
    for (const [k, v] of [
      ["Alt+W", "toggle the dock (Ctrl+Shift+W too, where the browser delivers it)"],
      ["r", "raw JSON of the open turn"],
      ["Esc", "close"],
      ["?", "this list"],
    ] as const) {
      dl.appendChild(el("dt", undefined, k))
      dl.appendChild(el("dd", undefined, v))
    }
    box.appendChild(dl)
    return box
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
        href: studioLink(this.cfg.endpoint, s.selected, s.selectedStep ?? undefined),
        target: "_blank",
        rel: "noopener",
        title: "open in Studio (run, and the step you are reading)",
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
    // A step card click marks the step the user is reading — what ⤢
    // carries into Studio (Dv3).
    main.addEventListener("click", (e) => {
      const target = (e.target as HTMLElement).closest?.("[data-weft-step]")
      if (!target) return
      const n = Number(target.getAttribute("data-weft-step"))
      if (Number.isFinite(n)) void this.model?.selectStep(n)
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
    main.appendChild(this.playgroundArea(s))
    return main
  }

  /** playgroundArea draws rung 2 (§8.2) under the turn view: the
   * experiment drawer, and — once a command was posted — the result
   * streaming in place with its inline diff and approval controls.
   * Rendered only when the server reports the playground capability. */
  private playgroundArea(s: PanelState): HTMLElement {
    const box = el("div")
    if (s.result) box.appendChild(this.experimentResult(s))
    if (!hasCapability(s, "playground")) return box
    if (s.drawer) box.appendChild(this.drawer(s))
    return box
  }

  /** drawer is §3's edit form, pre-filled from the run's registered
   * config: prompt, tools off, model, thinking, input — and the ⚠ on
   * every side-effect tool (ReplayPolicy never or unannotated). */
  private drawer(s: PanelState): HTMLElement {
    const d = s.drawer
    if (!d) return el("div")
    const rt = s.runtimes.find((r) => r.id === d.runtimeId)
    const agent = rt?.agents.find((a) => a.name === d.agent)
    const card = el("div", "weft-step weft-drawer")
    const head = el("div", "weft-step-h", [
      el("span", undefined, `Experiment · ${d.agent}${d.step > 0 ? ` · continue from step ${d.step}` : ""}`),
      el("span", "weft-grow"),
    ])
    const close = el("button", "weft-btn", "–", { title: "close the drawer" })
    close.addEventListener("click", () => this.model?.closeExperiment())
    head.appendChild(close)
    card.appendChild(head)
    const body = el("div", "weft-step-b")

    // System prompt, with the registered config one reset away.
    const promptRow = el("label", "weft-field", [el("span", undefined, "System prompt")])
    const ta = el("textarea", "weft-input") as HTMLTextAreaElement
    ta.rows = 3
    ta.value = d.instructions
    ta.addEventListener("input", () => this.model?.setDraft({ instructions: ta.value }))
    const reset = el("button", "weft-btn", "↺", { title: "reset to the registered prompt" })
    reset.addEventListener("click", () => {
      ta.value = agent?.instructions ?? ""
      this.model?.setDraft({ instructions: ta.value })
    })
    promptRow.append(ta, reset)
    body.appendChild(promptRow)

    // Tools: turning off is narrowing; ⚠ marks the side-effect class.
    if (agent?.tools.length) {
      const toolsRow = el("div", "weft-field", [el("span", undefined, "Tools")])
      for (const t of agent.tools) {
        const cb = el("input") as HTMLInputElement
        cb.type = "checkbox"
        cb.checked = d.tools[t.name] ?? true
        cb.addEventListener("change", () =>
          this.model?.setDraft({ tools: { ...d.tools, [t.name]: cb.checked } })
        )
        const lab = el("label", "weft-tool", [cb, el("span", undefined, t.name)])
        if (t.side_effects === "never" || !t.side_effects) {
          lab.appendChild(
            el("span", "weft-badge weft-warn-badge", "⚠", {
              title: "side-effect tool (ReplayPolicy never): its calls substitute or park, never re-fire silently",
            })
          )
        }
        toolsRow.appendChild(lab)
      }
      body.appendChild(toolsRow)
    }

    // Model, thinking, input — the row of small selects.
    const opts = el("div", "weft-fields")
    const modelSel = el("select", "weft-input") as HTMLSelectElement
    const own = s.turn?.doc?.model?.name ?? ""
    const ownOpt = el("option", undefined, `model: ${own || "—"}`) as unknown as HTMLOptionElement
    ownOpt.value = ""
    modelSel.appendChild(ownOpt)
    for (const m of agent?.models ?? []) {
      if (m === own) continue
      const o = el("option", undefined, m) as unknown as HTMLOptionElement
      o.value = m
      modelSel.appendChild(o)
    }
    modelSel.value = d.model
    modelSel.addEventListener("change", () => this.model?.setDraft({ model: modelSel.value }))
    opts.appendChild(modelSel)
    const thinkSel = el("select", "weft-input") as HTMLSelectElement
    const defOpt = el("option", undefined, "thinking: default") as unknown as HTMLOptionElement
    defOpt.value = ""
    thinkSel.appendChild(defOpt)
    for (const lvl of ["off", "low", "medium", "high"]) {
      const o = el("option", undefined, lvl) as unknown as HTMLOptionElement
      o.value = lvl
      thinkSel.appendChild(o)
    }
    thinkSel.value = d.thinking
    thinkSel.addEventListener("change", () => this.model?.setDraft({ thinking: thinkSel.value }))
    opts.appendChild(thinkSel)
    body.appendChild(opts)

    // Input replaces the turn's user message (a whole-turn re-run).
    if (d.step === 0) {
      const inputRow = el("label", "weft-field", [el("span", undefined, "Input (replaces the user message)")])
      const inTa = el("textarea", "weft-input") as HTMLTextAreaElement
      inTa.rows = 2
      inTa.value = d.input
      inTa.addEventListener("input", () => this.model?.setDraft({ input: inTa.value }))
      inputRow.appendChild(inTa)
      body.appendChild(inputRow)
    }

    // The side-effect mode (§6 rule 3): substitute (the default — a
    // recorded call answers from the record, a miss parks), park
    // (always), allow (only tools the runtime opted in).
    const seRow = el("div", "weft-fields")
    const seSel = el("select", "weft-input") as HTMLSelectElement
    const seLabel = el("option", undefined, "side effects: substitute") as unknown as HTMLOptionElement
    seLabel.value = ""
    seSel.appendChild(seLabel)
    const parkOpt = el("option", undefined, "park") as unknown as HTMLOptionElement
    parkOpt.value = "park"
    seSel.appendChild(parkOpt)
    const allowOpt = el("option", undefined, "allow (opted-in tools only)") as unknown as HTMLOptionElement
    allowOpt.value = "allow"
    seSel.appendChild(allowOpt)
    seSel.value = d.sideEffects
    seSel.addEventListener("change", () => this.model?.setDraft({ sideEffects: seSel.value as ExperimentDraft["sideEffects"] }))
    seRow.appendChild(seSel)
    // The engine (§5.5): live, or scripted — the source run's recorded
    // turns at zero tokens, refused with an instructions/model
    // override (the prompt trap).
    const engSel = el("select", "weft-input") as HTMLSelectElement
    const engLive = el("option", undefined, "engine: live") as unknown as HTMLOptionElement
    engLive.value = "live"
    engSel.appendChild(engLive)
    const engScripted = el("option", undefined, "scripted (zero tokens)") as unknown as HTMLOptionElement
    engScripted.value = "scripted"
    engSel.appendChild(engScripted)
    engSel.value = d.engine
    engSel.addEventListener("change", () => this.model?.setDraft({ engine: engSel.value as ExperimentDraft["engine"] }))
    seRow.appendChild(engSel)
    body.appendChild(seRow)

    // The transcript edits (D2/D3): when continuing from a step, the
    // kept steps' results are patchable and their call-free replies
    // rewritable — the counterfactual the fresh step answers.
    if (d.step > 0 && this.model?.state.turn) {
      const t = this.model.state.turn
      const editsBox = el("div", "weft-field")
      editsBox.appendChild(el("span", undefined, `Transcript edits (steps 0..${d.step - 1} are kept)`))
      for (const step of t.folded.steps) {
        if (step.index >= d.step) break
        for (const call of step.toolCalls) {
          if (!call.result) continue
          const lab = el("label", "weft-edit")
          lab.appendChild(el("span", undefined, `step ${step.index} · ${call.name} →`))
          const inp = el("input", "weft-input") as HTMLInputElement
          inp.placeholder = call.result.content.slice(0, 60)
          const editOf = () => d.edits.find((e) => e.step === step.index && e.callID === call.callId)
          inp.value = editOf()?.toolResult ?? ""
          inp.addEventListener("input", () => {
            const cur = editOf()
            const next = [...d.edits]
            const i = cur ? next.indexOf(cur) : -1
            if (inp.value === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, toolResult: inp.value, step: step.index, callID: call.callId }
            } else {
              next.push({ step: step.index, callID: call.callId, toolResult: inp.value })
            }
            this.model?.setDraft({ edits: next })
          })
          lab.appendChild(inp)
          editsBox.appendChild(lab)
        }
        if (step.text && !step.toolCalls.length) {
          const lab = el("label", "weft-edit")
          lab.appendChild(el("span", undefined, `step ${step.index} · reply`))
          const ta = el("textarea", "weft-input") as HTMLTextAreaElement
          ta.rows = 2
          const editOf = () => d.edits.find((e) => e.step === step.index && !e.callID)
          ta.value = editOf()?.content ?? ""
          ta.addEventListener("input", () => {
            const cur = editOf()
            const next = [...d.edits]
            const i = cur ? next.indexOf(cur) : -1
            if (ta.value === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, content: ta.value, step: step.index }
            } else {
              next.push({ step: step.index, content: ta.value })
            }
            this.model?.setDraft({ edits: next })
          })
          lab.appendChild(ta)
          editsBox.appendChild(lab)
        }
      }
      if (editsBox.childElementCount > 1) body.appendChild(editsBox)
    }

    const run = el("button", "weft-run-btn", "Run experiment ▶", {
      title: "POST /api/playground/runs — the runtime in your app executes it",
    })
    run.addEventListener("click", () => void this.model?.runExperiment())
    body.appendChild(run)
    card.appendChild(body)
    return card
  }

  /** experimentResult is §3's Result pane: the label (`t3·x1`), the
   * stream in place, the inline diff against the source turn, and —
   * when the run parked — the approval controls (continue / skip /
   * resolve, the runtime-started run's own verbs). */
  private experimentResult(s: PanelState): HTMLElement {
    const r = s.result
    if (!r) return el("div")
    const card = el("div", "weft-step weft-xres")
    const usage = r.row?.usage
    const stats = [
      r.state,
      usage ? `${tokens(usage.input_tokens)}→${tokens(usage.output_tokens)} tok` : "",
      r.row ? duration(r.row.started, r.row.finished) : "",
    ]
      .filter(Boolean)
      .join(" · ")
    const head = el("div", "weft-step-h", [
      el("span", undefined, `Result · ${r.label}`),
      el("span", undefined, stats),
      el("span", "weft-grow"),
    ])
    const compare = el("a", "weft-btn", "compare in Studio", {
      href: studioPlaygroundLink(this.cfg.endpoint, s.drawer, s.selectedStep ?? null),
      target: "_blank",
      rel: "noopener",
      title: "open the Studio playground with this run, step and the current overrides carried over",
    })
    compare.style.textDecoration = "none"
    head.appendChild(compare)
    const discard = el("button", "weft-btn", "discard", { title: "clear the result pane" })
    discard.addEventListener("click", () => this.model?.discardResult())
    head.appendChild(discard)
    card.appendChild(head)
    const body = el("div", "weft-step-b")
    if (r.error) body.appendChild(el("div", "weft-note weft-warn", r.error))
    for (const step of r.folded.steps) {
      if (step.text) body.appendChild(el("div", undefined, step.text))
      for (const call of step.toolCalls) body.appendChild(renderCall(call, r.row?.status ?? "running"))
    }
    if (!r.folded.steps.length && !r.error) body.appendChild(el("div", "weft-note", "queued — waiting for the runtime to ack…"))

    // The 2-way compare (P3, PQ3 — the panel stays 2-way): the diff's
    // other side is the source turn by default, or any sibling of it.
    const siblings = [
      { id: "", label: sourceLabel(r.label) },
      ...(s.experiments.get(s.drawer?.runId ?? "") ?? []).map((x) => ({
        id: x.id,
        label: x.id.startsWith("pg_") ? shortId(x.id) : x.id,
      })),
    ]
    if (siblings.length > 1) {
      const cmp = el("select", "weft-input") as HTMLSelectElement
      for (const sb of siblings) {
        const o = el("option", undefined, `compare vs ${sb.label || "source"}`) as unknown as HTMLOptionElement
        o.value = sb.id
        cmp.appendChild(o)
      }
      cmp.value = r.compareWith
      cmp.addEventListener("change", () => void this.model?.setCompare(cmp.value))
      body.appendChild(cmp)
    }

    // The inline diff (§3: `diff vs t3:`), once there is final text —
    // on text, and on the tool calls beside it.
    const text = r.folded.steps.map((st) => st.text).filter(Boolean).join("\n")
    const otherText = r.compareWith
      ? this.model?.compareText.get(r.compareWith) ?? ""
      : r.sourceText
    const otherLabel = r.compareWith
      ? shortId(r.compareWith)
      : sourceLabel(r.label)
    if (text && otherText) {
      const rows = diffLines(otherText, text)
      const summary = diffSummary(rows)
      const diffBox = el("div", "weft-diff")
      diffBox.appendChild(el("div", "weft-diff-h", `diff vs ${otherLabel}:  ${summary}`))
      for (const row of rows) {
        if (row.kind === "same") continue
        diffBox.appendChild(
          el("div", `weft-diff-row weft-diff-${row.kind}`, `${row.kind === "add" ? "+" : "−"} ${row.text}`)
        )
      }
      // The tool-call diff beside the text: name(args) lines.
      const otherCalls = r.compareWith
        ? this.model?.compareCalls.get(r.compareWith) ?? []
        : (this.model?.state.turn?.folded.steps ?? []).flatMap((st) => st.toolCalls)
            .map((c) => `${c.name}(${c.args === undefined ? "" : JSON.stringify(c.args)})`)
      const myCalls = r.folded.steps
        .flatMap((st) => st.toolCalls)
        .map((c) => `${c.name}(${c.args === undefined ? "" : JSON.stringify(c.args)})`)
      const callRows = diffLines(otherCalls.join("\n"), myCalls.join("\n"))
      if (callRows.some((row) => row.kind !== "same")) {
        diffBox.appendChild(el("div", "weft-diff-h", `tool calls:  ${diffSummary(callRows)}`))
        for (const row of callRows) {
          if (row.kind === "same") continue
          diffBox.appendChild(
            el("div", `weft-diff-row weft-diff-${row.kind}`, `${row.kind === "add" ? "+" : "−"} ${row.text}`)
          )
        }
      }
      body.appendChild(diffBox)
    }

    // The parked calls' controls: the runtime-started run's approval
    // verbs (§8.2), through POST /api/runs/{id}/approvals.
    if (r.folded.pending.length && r.runID) {
      const approvals = el("div", "weft-step")
      approvals.appendChild(el("div", "weft-step-h", [el("span", undefined, "awaiting decision")]))
      const abody = el("div", "weft-step-b")
      for (const call of r.folded.pending) {
        const line = el("div", "weft-call")
        line.appendChild(
          el("div", "weft-call-h", [
            el("span", "weft-name", call.name),
            el("span", "weft-args", call.args === undefined ? "(…)" : fmtJSON(call.args)),
          ])
        )
        const ctl = el("div", "weft-res")
        const mk = (label: string, decision: "approve" | "deny" | "resolve", title: string, content?: string) => {
          const b = el("button", "weft-btn", label, { title })
          b.addEventListener("click", () => void this.model?.decide(call.id, decision, content))
          return b
        }
        ctl.append(
          mk("continue", "approve", "Approve: the handler runs for real"),
          mk("skip", "deny", "Deny: the model sees a denied result"),
          mk("resolve…", "resolve", "Resolve with the recorded result pasted outside the process", "")
        )
        line.appendChild(ctl)
        abody.appendChild(line)
      }
      approvals.appendChild(abody)
      body.appendChild(approvals)
    }
    card.appendChild(body)
    return card
  }

  /** turnView draws the step story (§2): the prompt, per step the
   * model text (reasoning collapsed), tool calls name(args) → result,
   * finish reason and usage with cached/reasoning splits, pending
   * approvals read-only, and the honesty notes. With the playground
   * capability on, the experiment actions follow (§3's row). */
  private turnView(s: PanelState): HTMLElement {
    const t = s.turn
    if (!t) return el("div")
    const wrap = el("div")
    wrap.appendChild(this.notes(t))
    const wf = waterfall(t.spans ?? [])
    if (wf.length) wrap.appendChild(renderWaterfall(wf))
    const prompt = promptText(t)
    if (prompt) wrap.appendChild(el("div", "weft-note", prompt))
    wrap.appendChild(renderFolded(t.folded, this.rowOf(t.id)?.status ?? "running", t, s.selectedStep))
    if (t.folded.pending.length) wrap.appendChild(this.approvals(t.folded.pending))
    if (hasCapability(s, "playground")) wrap.appendChild(this.actions(s))
    return wrap
  }

  /** actions is §3's row: ✎ Experiment (the drawer), ↻ Re-run (the
   * whole turn with the drawer's current edits), ⎇ Continue from the
   * step being read. */
  private actions(s: PanelState): HTMLElement {
    const t = s.turn
    if (!t) return el("div")
    const row = el("div", "weft-actions")
    const experiment = el("button", "weft-btn", "✎ Experiment", {
      title: "open the experiment drawer, pre-filled from the registered config",
    })
    experiment.addEventListener("click", () => void this.model?.openExperiment(t.id, 0))
    row.appendChild(experiment)
    const rerun = el("button", "weft-btn", "↻ Re-run", {
      title: "re-run the whole turn with the drawer's current edits",
    })
    rerun.addEventListener("click", () => void this.model?.openExperiment(t.id, 0))
    row.appendChild(rerun)
    if (s.selectedStep != null && s.selectedStep > 0) {
      const cont = el("button", "weft-btn", `⎇ Continue from step ${s.selectedStep}`, {
        title: "keep the transcript through the previous step (edits apply) and run this step fresh",
      })
      cont.addEventListener("click", () => void this.model?.openExperiment(t.id, s.selectedStep ?? 0))
      row.appendChild(cont)
    }
    return row
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

  /** approvals shows the app's own parked calls read-only (§2): the
   * decision verbs act on runs a runtime started (§8.2) — the app's
   * own turns are viewer-only (D7, PQ7), and the note says so. */
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
      line.appendChild(el("div", "weft-res", "the app's own turns are viewer-only (PQ7) — decide from your app"))
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

/** renderWaterfall draws §2's timing mini-waterfall: one bar per
 * span over the run's own window, wall milliseconds beside. */
export function renderWaterfall(bars: { name: string; left: number; width: number; ms: number }[]): HTMLElement {
  const box = el("div", "weft-wf")
  for (const b of bars) {
    const row = el("div", "weft-wf-row")
    row.appendChild(el("span", "weft-wf-name", b.name, { title: b.name }))
    const track = el("span", "weft-wf-track")
    const bar = el("span", "weft-wf-bar")
    bar.style.left = `${(b.left * 100).toFixed(2)}%`
    bar.style.width = `${(b.width * 100).toFixed(2)}%`
    track.appendChild(bar)
    row.appendChild(track)
    row.appendChild(el("span", "weft-wf-ms", `${b.ms}ms`))
    box.appendChild(row)
  }
  return box
}

/** renderFolded draws the turn view (§2). */
export function renderFolded(
  view: FoldedRun,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null
): HTMLElement {
  const wrap = el("div")
  if (view.model?.name) {
    wrap.appendChild(el("div", "weft-reason", `${view.model.provider}/${view.model.name}`))
  }
  for (const step of view.steps)
    wrap.appendChild(renderStep(step, runStatus, t, selectedStep))
  return wrap
}

function renderStep(
  step: FoldedStep,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null
): HTMLElement {
  const card = el("div", "weft-step")
  card.setAttribute("data-weft-step", String(step.index))
  if (selectedStep === step.index) card.style.outline = "1px solid var(--w-accent)"
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

/** studioPlaygroundLink builds the §2 hand-off: "compare in Studio"
 * with the context carried over — the run, the step being read, and
 * the drawer's current overrides — so nothing is retyped (the parity
 * rule's documented hand-off for the Studio-only surfaces). */
export function studioPlaygroundLink(
  endpoint: string,
  draft: ExperimentDraft | null,
  step: number | null
): string {
  const u = new URL("playground", endpoint)
  if (draft) {
    if (draft.runId) u.searchParams.set("run", draft.runId)
    if (step != null && step > 0) u.searchParams.set("step", String(step))
    if (draft.instructions) u.searchParams.set("instructions", draft.instructions)
    const on = Object.entries(draft.tools)
      .filter(([, enabled]) => enabled)
      .map(([name]) => name)
    if (on.length && on.length < Object.keys(draft.tools).length)
      u.searchParams.set("tools", on.join(","))
    if (draft.model) u.searchParams.set("model", draft.model)
    if (draft.thinking) u.searchParams.set("thinking", draft.thinking)
    if (draft.input && draft.step === 0) u.searchParams.set("input", draft.input)
  }
  return u.toString()
}

/** sourceLabel takes the turn part back out of a `t3·x1` label (the
 * diff header reads "diff vs t3"). */
function sourceLabel(label: string): string {
  return label.split("·")[0] || label
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
