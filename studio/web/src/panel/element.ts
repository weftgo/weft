// <weft-devtools> — the panel custom element (§5): shadow DOM, no
// host-framework dependency (V1), the §5.2 attributes and defaults.
// The element owns nothing but presentation; state.ts owns the data.
import type { FoldedRun, FoldedStep, FoldedToolCall } from "../lib/events"
import { callState, truncation } from "../lib/events"
import type { RunRow } from "../lib/api"
import { readConfig, type PanelConfig } from "./config"
import { el } from "./render"
import { PANEL_CSS } from "./styles"
import { SpikeRun, type SpikeState } from "./state"
import { PANEL_STUDIO_VERSION } from "./version"

const STATUS = new Set(["running", "succeeded", "failed", "interrupted", "parked"])

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
  private run: SpikeRun | null = null
  private open: boolean
  private body!: HTMLElement

  constructor() {
    super()
    this.cfg = readConfig(this)
    this.open = this.cfg.open
    this.shadow = this.attachShadow({ mode: "open" })
    const style = el("style", undefined, PANEL_CSS)
    this.shadow.append(style, this.hostRoot())
    this.renderChrome()
  }

  connectedCallback() {
    this.start()
  }

  disconnectedCallback() {
    this.run?.dispose()
    this.run = null
  }

  attributeChangedCallback() {
    if (!this.isConnected) return
    this.cfg = readConfig(this)
    this.start()
  }

  /** start (re)connects: one meta request; failure removes the panel
   * silently — no console, no retries (§5.3). */
  private async start() {
    this.run?.dispose()
    const run = new SpikeRun(
      { base: this.cfg.endpoint, token: this.cfg.token },
      this.cfg.publicId,
      (s) => this.render(s)
    )
    this.run = run
    const ok = await run.start()
    if (!ok && this.auto) {
      // No Studio answered: this page does not want a panel (V4).
      this.remove()
    }
  }

  private get auto(): boolean {
    // data-auto is about mounting without markup; an element that is
    // already in the page only removes itself when Studio is absent.
    return this.cfg.auto
  }

  // ── DOM skeleton ────────────────────────────────────────────────

  private hostRoot(): HTMLElement {
    this.body = el("div", "weft-root")
    return this.body
  }

  private renderChrome() {
    // The chrome is rebuilt per state; positions live on the classes.
  }

  toggle() {
    this.open = !this.open
    this.render(this.run?.state ?? {
      meta: null,
      tooNew: false,
      view: null,
      runs: [],
      runId: "",
    })
  }

  // ── Rendering ───────────────────────────────────────────────────

  private render(s: SpikeState) {
    const root = this.body
    while (root.firstChild) root.removeChild(root.firstChild)
    if (!this.open) {
      const fab = el("button", "weft-fab", "devtools", {
        title: "weft devtools — Alt+W",
      })
      fab.addEventListener("click", () => this.toggle())
      root.appendChild(fab)
      return
    }
    const dock = el("div", `weft-dock weft-${this.cfg.position} weft-open`)
    dock.appendChild(this.header(s))
    const cols = el("div", "weft-cols")
    cols.appendChild(this.turnList(s))
    cols.appendChild(this.main(s))
    dock.appendChild(cols)
    dock.appendChild(this.footer())
    root.appendChild(dock)
  }

  private header(s: SpikeState): HTMLElement {
    const h = el("div", "weft-head")
    const live = s.meta && !s.tooNew
    const running = s.runs.some((r) => r.status === "running")
    h.appendChild(el("span", `weft-dot${live ? (running ? " weft-run" : " weft-on") : ""}`))
    h.appendChild(
      el(
        "span",
        "weft-title",
        this.cfg.publicId ? `weft · ${this.cfg.publicId}` : "weft · latest (dev)"
      )
    )
    h.appendChild(el("span", "weft-grow"))
    const close = el("button", "weft-btn", "–", { title: "collapse (Alt+W)" })
    close.addEventListener("click", () => this.toggle())
    h.appendChild(close)
    return h
  }

  private turnList(s: SpikeState): HTMLElement {
    const list = el("div", "weft-turns")
    if (!s.runs.length) {
      list.appendChild(el("div", "weft-splash", "no turns yet — run your app"))
      return list
    }
    for (const r of s.runs) list.appendChild(this.turnRow(r, s.runId))
    return list
  }

  private turnRow(r: RunRow, selected: string): HTMLElement {
    const row = el("button", `weft-turn${r.id === selected ? " weft-sel" : ""}`)
    const parked = r.status === "succeeded" && r.pending > 0
    const chip = parked ? "parked" : r.status
    const row1 = el("div", "weft-row1", [
      el("span", `weft-chip weft-${STATUS.has(chip) ? chip : "interrupted"}`, chip),
      el("span", "weft-id", r.id),
    ])
    row.appendChild(row1)
    row.addEventListener("click", () => this.run?.watch(r.id))
    return row
  }

  private main(s: SpikeState): HTMLElement {
    const main = el("div", "weft-main")
    if (s.tooNew && s.meta) {
      const note = el("div", "weft-note weft-warn", [
        el("span", "weft-warn", "Studio is newer than this panel; update panel.js"),
        el("span", undefined, `studio_version ${s.meta.studio_version} · panel built for ${PANEL_STUDIO_VERSION}`),
      ])
      main.appendChild(note)
      return main
    }
    if (!s.view || !s.view.steps.length) {
      main.appendChild(el("div", "weft-splash", "waiting for the first turn…"))
      return main
    }
    main.appendChild(renderFolded(s.view, s.runs.find((r) => r.id === s.runId)?.status ?? "running"))
    return main
  }

  private footer(): HTMLElement {
    return el(
      "div",
      "weft-footer",
      "prompts, args and results from your app, via your Studio"
    )
  }
}

/** renderFolded draws the turn view (§2): per step the model text,
 * reasoning collapsed, tool calls name(args) → result, the finish
 * reason and usage. Shared by the spike and the rung-1 panel. */
export function renderFolded(view: FoldedRun, runStatus: string): HTMLElement {
  const wrap = el("div")
  if (view.model) wrap.appendChild(el("div", "weft-reason", `${view.model.provider}/${view.model.name}`))
  for (const step of view.steps) wrap.appendChild(renderStep(step, runStatus))
  return wrap
}

function renderStep(step: FoldedStep, runStatus: string): HTMLElement {
  const card = el("div", "weft-step")
  const head = el("div", "weft-step-h", [
    el("span", undefined, `step ${step.index}`),
    el("span", "weft-grow"),
  ])
  if (step.finish) head.appendChild(el("span", undefined, step.finish.reason))
  card.appendChild(head)
  const body = el("div", "weft-step-b")
  if (step.reasoning) {
    const d = el("details", "weft-collapsible")
    d.appendChild(el("summary", undefined, "reasoning"))
    d.appendChild(el("div", undefined, step.reasoning))
    body.appendChild(d)
  }
  if (step.text) body.appendChild(el("div", undefined, step.text))
  for (const call of step.toolCalls) body.appendChild(renderCall(call, runStatus))
  card.appendChild(body)
  return card
}

function renderCall(call: FoldedToolCall, runStatus: string): HTMLElement {
  const box = el("div", "weft-call")
  const state = callState(call, runStatus)
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", call.name),
    el("span", "weft-args", fmtCallArgs(call)),
  ])
  box.appendChild(head)
  if (call.result) {
    const res = el("div", "weft-res", call.result.content)
    const trunc = truncation(call.result.content)
    if (trunc) {
      head.appendChild(
        el(
          "span",
          "weft-badge",
          trunc.kind === "bytes" ? `truncated ${trunc.bytes} bytes` : "not executed (max_tokens)"
        )
      )
    }
    if (call.result.isError) head.appendChild(el("span", "weft-badge weft-err", "error"))
    box.appendChild(res)
  } else if (state === "running") {
    box.appendChild(el("div", "weft-res", "running…"))
  } else {
    box.appendChild(el("div", "weft-res weft-warn", "never completed"))
  }
  return box
}

function fmtCallArgs(call: FoldedToolCall): string {
  if (call.args !== undefined) {
    try {
      return `(${JSON.stringify(call.args)})`
    } catch {
      return "(?)"
    }
  }
  return call.streamedArgs ? `(${call.streamedArgs}…)` : "(…)"
}
