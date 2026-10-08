// <weft-devtools> — the panel custom element (§5): shadow DOM, no
// host-framework dependency (V1, the Dv0 decision), the §5.2
// attributes and defaults. The element owns presentation only;
// state.ts owns the data. Rung 1 is a viewer (§8.1); rung 2's
// experiment drawer and approval controls render only when
// meta.capabilities reports the playground (§8.5 item 3).
import type { RunCompaction, RunRow, ToolCallPart, Transcript, Usage } from "../lib/api"
import { isHoleRef } from "../lib/api"
import { holeWords, mergeHoles, rowHoles, statusHoles, USAGE_AT_FINISH, usageKnown } from "../lib/honesty"
import type { HoleMark } from "../lib/honesty"
import { paramsLine, REQUEST_NOT_RECORDED_LABEL, REQUEST_NOT_STORED, shortHash } from "../lib/requests"
import { MAX_REQUEST_PAGES, REQUEST_PAGE } from "./client"
import { diffLines, diffSummary } from "../lib/diff"
import { callState, runHoles, stepHoles, truncation } from "../lib/events"
import {
  compactionLine,
  compactionsOf,
  isSessionMarker,
  messageLine,
  originalOf,
  replacementNote,
  SESSION_LABEL,
  sessionNote,
} from "../lib/compaction"
import { attemptLine, attemptsHole, factsFromRows, timingLine } from "../lib/attempts"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "../lib/events"
import { duration, relativeTime, tokens } from "../lib/format"
import { discoverEndpoint, readConfig, tokenScope } from "./config"
import type { MountOptions, PanelConfig } from "./config"
import { el, fmtJSON, waterfall } from "./render"
import { PANEL_CSS } from "./styles"
import {
  emptyPanelState,
  MAX_EVENT_PAGES,
  PanelModel,
  strippedContent,
  TURNS_LIMIT,
} from "./state"
import type { ChildView, PanelRequests, PanelState, TurnView } from "./state"
import { foldedWords, turnPromptOf } from "./playground"
import type { ExperimentDraft, TurnWords } from "./playground"
import { panelStudioVersion } from "./version"

/** hasCapability reports whether meta lists the named capability (the
 * panel renders a control only when the server reports it, §8.5). */
export function hasCapability(s: PanelState, name: string): boolean {
  return s.meta?.capabilities.includes(name) ?? false
}

/** statusChip maps a row to the §2 turn-list status word: parked is
 * how the panel shows succeeded with pending calls. */
export function statusChip(r: RunRow): string {
  return r.status === "succeeded" && r.pending > 0 ? "parked" : r.status
}

/** studioLink builds the ⤢ deep link (§2: "open in Studio" with the
 * context carried over; Dv3 adds the step). The run id is data (a
 * foreign SDK's ids are any string): it travels as one encoded path
 * segment, the way Studio's own links carry it, so a `/`, `?` or `#`
 * in it cannot rewrite the URL. The panel's token is never part of a
 * link. */
export function studioLink(endpoint: string, runId: string, step?: number): string {
  const u = new URL(`runs/${encodeURIComponent(runId)}`, endpoint)
  if (step !== undefined) {
    u.searchParams.set("step", String(step))
    u.searchParams.set("view", "story")
  }
  return u.toString()
}

/** The diff is plain LCS over lines (lib/diff.ts): its table is
 * lines × lines, built inside the host page. Past this many cells the
 * panel hands the comparison to Studio instead of freezing the tab. */
export const DIFF_CELLS = 250_000

/** One stylesheet for every panel in the document, adopted by each
 * shadow root: a constructed sheet is exempt from a host page's
 * style-src policy, where an inline <style> would be refused and the
 * dock would land unstyled in the page's own flow. The <style>
 * element is the fallback where constructed sheets do not exist. */
let sheet: CSSStyleSheet | null = null
function attachStyles(root: ShadowRoot): void {
  try {
    if ("adoptedStyleSheets" in root && typeof CSSStyleSheet === "function") {
      if (!sheet) {
        const made = new CSSStyleSheet()
        made.replaceSync(PANEL_CSS)
        sheet = made
      }
      root.adoptedStyleSheets = [sheet]
      return
    }
  } catch {
    // no constructed sheets here (or another document's): fall back
  }
  root.append(el("style", undefined, PANEL_CSS))
}

const quiet = () => {}

/** How the renderers remember which <details> the user opened. */
interface OpenState {
  keys: Set<string>
  scope: string
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

  /** Set by the entry module on the dock it mounts itself: only that
   * node is the panel's to remove (§5.3). An element the page wrote —
   * markup a framework may own — goes inert in place instead, because
   * a node removed behind a framework's back breaks its next unmount. */
  autoMounted = false

  /** Configuration rung 1 (plan C2): what mount(opts) passed; above
   * the element's own attributes, field by field. */
  options: MountOptions | null = null

  private cfg: PanelConfig
  private shadow: ShadowRoot
  private model: PanelModel | null = null
  /** What the running model was started with. */
  private conn: { endpoint: string; token: string; publicId: string } | null = null
  /** The endpoint the running connection talks to: cfg.endpoint, or
   * what panel-config.json named (rung 5). The deep links use it. */
  private base = ""
  /** The endpoint that did not answer, for the not-reachable line of
   * a mount the host made (null: no line). */
  private unreachable: string | null = null
  private startSeq = 0
  private scheduled = false
  /** Studio did not answer and the node is not ours to remove. */
  private dormant = false
  private open: boolean
  /** data-open was adopted (on first connect); toggles own it after. */
  private opened = false
  /** The ? shortcuts overlay (Dv3). */
  private keys = false
  private body!: HTMLElement
  private last: PanelState = emptyPanelState()
  /** A press or an IME composition is in progress inside the dock:
   * redrawing now would take the pressed button from under the
   * pointer (no click follows) or end the composition. The redraw
   * waits for the release. */
  private held = false
  private composing = false
  private dirty = false
  private holdTimer: ReturnType<typeof setTimeout> | null = null
  /** What the user typed into fields the model does not hold (the
   * steer message, a resolve result), by field key. */
  private scratch = new Map<string, string>()
  /** The <details> the user opened, by key (survives redraws). */
  private openKeys = new Set<string>()
  private onKey = (e: KeyboardEvent) => this.keydown(e)
  private onRelease = () => this.release()

  constructor() {
    super()
    this.cfg = readConfig(this)
    this.open = this.cfg.open
    this.shadow = this.attachShadow({ mode: "open" })
    this.body = el("div", "weft-root")
    attachStyles(this.shadow)
    this.shadow.append(this.body)
    this.shadow.addEventListener("pointerdown", () => this.hold())
    this.shadow.addEventListener("compositionstart", () => {
      this.composing = true
    })
    this.shadow.addEventListener("compositionend", () => {
      this.composing = false
      this.flush()
    })
    // The failsafe for a composition whose end is never reported: the
    // field lost focus — whatever the IME did with it, nothing is being
    // composed in the dock any more, and a dock that waits for it would
    // stay frozen.
    this.shadow.addEventListener("focusout", () => {
      if (!this.composing) return
      this.composing = false
      this.flush()
    })
    this.render(this.last)
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
    // in an input — the host page's or the panel's own.
    window.addEventListener("keydown", this.onKey)
    window.addEventListener("pointerup", this.onRelease, true)
    window.addEventListener("pointercancel", this.onRelease, true)
    this.schedule()
  }

  disconnectedCallback() {
    window.removeEventListener("keydown", this.onKey)
    window.removeEventListener("pointerup", this.onRelease, true)
    window.removeEventListener("pointercancel", this.onRelease, true)
    if (this.holdTimer) clearTimeout(this.holdTimer)
    this.holdTimer = null
    this.held = this.composing = false
    this.startSeq++ // a start still in flight is no longer this element's
    this.model?.dispose()
    this.model = null
    this.conn = null
  }

  /** keydown is the whole keyboard surface. The window sees a key
   * pressed inside a shadow tree as coming from its host, so the real
   * target is read off the composed path — typing r or ? into the
   * drawer's own fields is typing, not a shortcut. Bare keys only
   * (Ctrl+R stays the browser's reload), never during an IME
   * composition, never a key the page already handled, and only when
   * focus is in the panel or nowhere: a key aimed at one of the host
   * page's own widgets is the page's. preventDefault only on a key
   * that was handled. */
  private keydown(e: KeyboardEvent): void {
    if (e.defaultPrevented || e.isComposing) return
    const path = typeof e.composedPath === "function" ? e.composedPath() : []
    const t = (path[0] ?? e.target) as HTMLElement | null
    if (
      t &&
      (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" ||
        t.isContentEditable)
    )
      return
    const toggleCombo =
      (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && e.code === "KeyW") || // Q4's pick
      (e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") // where delivered
    if (toggleCombo) {
      e.preventDefault()
      this.keys = false
      this.toggle()
      return
    }
    if (e.ctrlKey || e.metaKey || e.altKey) return
    if (!this.open) return
    // Nothing focused: the key came from the window, the document or
    // the page's body, not from an element of the page's own. Esc
    // included — the page's own dialog closes on it, not the dock.
    const idle = !t || t.nodeType !== 1 || t.tagName === "BODY" || t.tagName === "HTML"
    if (!idle && !path.includes(this)) return
    if (e.key === "Escape") {
      if (this.keys) {
        this.keys = false
        this.render(this.last)
      } else this.toggle()
      return
    }
    if (e.key === "?") {
      e.preventDefault()
      this.keys = !this.keys
      this.render(this.last)
      return
    }
    if ((e.key === "r" || e.key === "R") && this.model) {
      e.preventDefault()
      this.toggleRaw()
    }
  }

  /** quiet reports that the model has no draw scheduled (a draw held
   * back by a press or a composition waits for the user, not time). */
  quiet(): boolean {
    return !(this.model?.drawPending() ?? false)
  }

  /** rescan re-reads the configuration from outside the element's own
   * attributes — window.__WEFT__ changed (main.ts's setter) — and
   * applies it like an attribute change: an explicit data-public-id
   * still wins (readConfig). */
  rescan() {
    this.cfg = readConfig(this)
    if (this.isConnected) this.schedule()
  }

  attributeChangedCallback(_name: string, old: string | null, value: string | null) {
    if (old === value) return
    this.cfg = readConfig(this)
    if (!this.isConnected) return
    this.schedule()
  }

  /** schedule coalesces connect and attribute changes into one apply:
   * an upgraded element hears every attribute, then the connect, in
   * one turn — that is one start, one meta request. */
  private schedule() {
    if (this.scheduled) return
    this.scheduled = true
    queueMicrotask(() => {
      this.scheduled = false
      if (this.isConnected) this.apply()
    })
  }

  /** apply brings the model in line with the attributes: a new
   * endpoint or token is a new connection; a new public id rescopes
   * the one there is (the §5.2 setter path); position only redraws. */
  private apply() {
    const cfg = this.cfg
    const conn = this.conn
    if (this.model && conn && conn.endpoint === cfg.endpoint && conn.token === cfg.token) {
      if (conn.publicId !== cfg.publicId) {
        conn.publicId = cfg.publicId
        // What was typed for the previous conversation (an unsent steer,
        // a resolve result) is not offered under the next one.
        this.scratch.clear()
        void this.model.rescope(cfg.publicId).catch(quiet)
      }
      this.render(this.last)
      return
    }
    void this.start().catch(quiet)
  }

  /** start (re)connects: one meta request (after panel-config.json
   * when no rung named the endpoint, C2's rung 5). Where Studio does
   * not answer, the dock the entry mounted itself removes itself
   * silently — no console, no retries (§5.3); a mount the host made
   * (its markup, mount(opts), data-auto=false) shows one quiet line
   * instead: "Studio not reachable at … · retry". Only the newest
   * start decides: one that was superseded (the attributes changed
   * while a request was in flight) must not take the panel down. */
  private async start() {
    const seq = ++this.startSeq
    const cfg = this.cfg
    this.model?.dispose()
    this.model = null
    this.conn = null
    this.dormant = false
    this.unreachable = null
    this.scratch.clear()
    // The previous connection's dock is not this one's.
    this.render(emptyPanelState())
    let ok = false
    let endpoint = cfg.endpoint
    if (cfg.configURL) {
      // Rung 5: the endpoint the Studio beside the script names, or
      // the script's directory (rung 6) when it names none.
      endpoint = (await discoverEndpoint(cfg.configURL)) || cfg.endpoint
      if (seq !== this.startSeq) return
    }
    if (endpoint) {
      const model = new PanelModel(
        { base: endpoint, token: cfg.token },
        cfg.publicId,
        (s) => this.render(s)
      )
      this.model = model
      this.conn = { endpoint: cfg.endpoint, token: cfg.token, publicId: cfg.publicId }
      this.base = endpoint
      this.last = model.state
      try {
        ok = await model.start()
      } catch {
        ok = false
      }
      if (seq !== this.startSeq || this.model !== model) return
    }
    if (ok) return
    // No Studio answered.
    this.model?.dispose()
    this.model = null
    this.conn = null
    // The panel's own dock: this page does not want a panel (V4).
    if (this.autoMounted) this.remove()
    else {
      // The host wrote the mount: it wants to see why nothing is there.
      this.dormant = true
      this.unreachable = endpoint
      this.render(this.last)
    }
  }

  /** retry is the not-reachable line's control: probe again. */
  private retry() {
    this.cfg = readConfig(this)
    void this.start().catch(quiet)
  }

  toggle() {
    this.open = !this.open
    this.render(this.last)
  }

  toggleRaw() {
    this.model?.toggleRaw()
  }

  // ── Rendering ───────────────────────────────────────────────────

  private hold() {
    this.held = true
    // A release the window never reports (the pointer left the
    // document) must not freeze the dock.
    if (this.holdTimer) clearTimeout(this.holdTimer)
    this.holdTimer = setTimeout(() => this.unhold(), 1_500)
  }

  /** release ends a press — after the click it produces has been
   * delivered to the button that was pressed. */
  private release() {
    if (!this.held) return
    if (this.holdTimer) clearTimeout(this.holdTimer)
    this.holdTimer = setTimeout(() => this.unhold(), 0)
  }

  private unhold() {
    this.holdTimer = null
    this.held = false
    this.flush()
  }

  private flush() {
    if (this.dirty && !this.held && !this.composing) this.render(this.last)
  }

  /** render never throws into the host page: the model calls it from
   * an animation frame, over records that were stored as ingested. A
   * state that cannot be drawn leaves the previous frame standing. */
  private render(s: PanelState) {
    this.last = s
    // Whether anyone is looking: the dev list is polled only then.
    this.model?.watch(this.open && !this.dormant)
    if (this.held || this.composing) {
      this.dirty = true
      return
    }
    this.dirty = false
    try {
      this.draw(s)
    } catch {
      // fail silent (§5.3)
    }
  }

  private draw(s: PanelState) {
    const root = this.body
    // Build first, swap second: a throw while building leaves the dock
    // as it was.
    const next: Node[] = []
    if (this.dormant) {
      // Studio did not answer a mount the host made: one quiet line.
      if (this.unreachable !== null) {
        const line = el("div", "weft-unreachable", undefined, { role: "status" })
        line.append(
          this.unreachable ? "Studio not reachable at " : "Studio not reachable: no http(s) endpoint",
          ...(this.unreachable ? [el("span", "weft-unreachable-at", this.unreachable)] : []),
          " · "
        )
        const again = el("button", "weft-retry", "retry", { type: "button", title: "ask Studio again" })
        again.addEventListener("click", () => this.retry())
        line.appendChild(again)
        next.push(line)
      }
    } else if (!this.open) {
      const fab = el("button", `weft-fab weft-fab-${this.cfg.position}`, "devtools", {
        title: "weft devtools — Alt+W",
      })
      fab.addEventListener("click", () => this.toggle())
      next.push(fab)
    } else if (!s.gone) {
      const dock = el("div", `weft-dock weft-${this.cfg.position} weft-open`)
      dock.appendChild(this.header(s))
      const cols = el("div", "weft-cols")
      cols.appendChild(this.turnList(s))
      cols.appendChild(this.main(s))
      dock.appendChild(cols)
      dock.appendChild(this.footer(s))
      // The overlays live inside the dock: it is their containing
      // block. Outside it they would be laid over the host page.
      if (s.raw && s.turn) dock.appendChild(this.rawView(s.turn))
      if (this.keys) dock.appendChild(this.shortcuts())
      next.push(dock)
    }
    // The whole dock re-renders per state change; the panes' scroll
    // positions, the field the user is typing in and its caret are the
    // user's, not the data's — keep them.
    const scrollOf = (sel: string): number =>
      (root.querySelector(sel))?.scrollTop ?? 0
    const scrolls = [scrollOf(".weft-turns"), scrollOf(".weft-main")]
    const active = this.shadow.activeElement as (HTMLElement & Partial<HTMLInputElement>) | null
    const focusKey = active?.getAttribute("data-weft-k") ?? null
    let caret: [number, number] | null = null
    try {
      if (focusKey && typeof active?.selectionStart === "number")
        caret = [active.selectionStart, active.selectionEnd ?? active.selectionStart]
    } catch {
      // not a text field
    }
    while (root.firstChild) root.removeChild(root.firstChild)
    for (const n of next) root.appendChild(n)
    root.querySelectorAll(".weft-turns, .weft-main").forEach((n, i) => {
      if (scrolls[i]) (n as HTMLElement).scrollTop = scrolls[i]
    })
    if (focusKey) {
      for (const n of Array.from(root.querySelectorAll("[data-weft-k]"))) {
        if (n.getAttribute("data-weft-k") !== focusKey) continue
        const field = n as HTMLInputElement
        try {
          field.focus({ preventScroll: true })
          if (caret) field.setSelectionRange(caret[0], caret[1])
        } catch {
          // a field that takes no selection
        }
        break
      }
    }
  }

  /** go runs one of the model's async verbs; a rejection stays here
   * (the host page's unhandledrejection is not the panel's log). */
  private go(p: Promise<unknown> | undefined) {
    p?.catch(quiet)
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
    const agent = s.session?.agent ?? s.turns.at(0)?.agent ?? ""
    const scope = this.cfg.publicId || s.session?.public_id || ""
    const title = scope ? `${agent ? agent + " · " : ""}${scope}` : "latest (dev)"
    h.appendChild(el("span", "weft-title", title, { title }))
    h.appendChild(el("span", "weft-grow"))
    const inTok = s.turns.reduce((n, r) => n + r.usage.input_tokens, 0)
    const outTok = s.turns.reduce((n, r) => n + r.usage.output_tokens, 0)
    const stats = `${s.turns.length}${s.turnsCapped ? "+" : ""} turns · ${tokens(inTok)}→${tokens(outTok)} tok`
    h.appendChild(el("span", undefined, stats, { title: stats }))
    if (s.turns.length && s.selected) {
      const a = el("a", "weft-btn", "⤢", {
        href: studioLink(this.base, s.selected, s.selectedStep ?? undefined),
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
    if (!s.turns.length && !s.experiments.size) {
      list.appendChild(
        el("div", "weft-splash", this.cfg.publicId ? "no turns yet — run your app" : "no runs yet (dev)")
      )
      return list
    }
    const shown = new Set<string>()
    for (const r of s.turns) {
      shown.add(r.id)
      list.appendChild(this.turnRow(r, s.selected))
      // The experiment slot (§2): runs that forked from this turn nest
      // under it; empty without the playground capability.
      const expts = s.experiments.get(r.id) ?? []
      if (expts.length) {
        const box = el("div", "weft-expts")
        for (const x of expts) box.appendChild(this.turnRow(x, s.selected))
        list.appendChild(box)
      }
    }
    // Experiments whose source turn is not in the list (older than the
    // page, or a run of another experiment) are still runs of this
    // conversation: listed, not dropped.
    const orphans: RunRow[] = []
    for (const [key, rows] of s.experiments) if (!shown.has(key)) orphans.push(...rows)
    if (orphans.length) {
      const box = el("div", "weft-expts")
      for (const x of orphans) box.appendChild(this.turnRow(x, s.selected))
      list.appendChild(box)
    }
    if (s.turnsCapped) {
      list.appendChild(
        el("div", "weft-note", `the newest ${TURNS_LIMIT} runs — older ones are in Studio (⤢)`)
      )
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
    const usage = r.usage
    const row2 = el("div", "weft-row2", [
      el("span", undefined, r.model.name ? `${r.model.provider}/${r.model.name}` : ""),
      el("span", undefined, `${r.steps} steps`),
      el("span", undefined, `${tokens(usage.input_tokens)}→${tokens(usage.output_tokens)}`),
      el("span", undefined, duration(r.started, r.finished) || "…"),
    ])
    row.append(row1, row2)
    if (r.err) row.appendChild(el("div", "weft-reason", r.err))
    row.addEventListener("click", () => this.go(this.model?.select(r.id)))
    return row
  }

  private main(s: PanelState): HTMLElement {
    const main = el("div", "weft-main")
    // One delegated listener for every <details> the turn view draws.
    // toggle does not bubble, so it is heard on the way down
    // (capture). The open state lives outside the DOM — the model's
    // for the lazy subagent loader (Dv3: [data-weft-child]), openKeys
    // for the rest — so a re-render never collapses what the user
    // opened.
    main.addEventListener(
      "toggle",
      (e) => {
        if (!(e.target instanceof HTMLElement)) return
        const t = e.target as HTMLDetailsElement
        const isOpen = t.open
        const key = t.getAttribute("data-weft-open")
        if (key && this.openKeys.has(key) !== isOpen) {
          if (isOpen) this.openKeys.add(key)
          else this.openKeys.delete(key)
          this.render(this.last)
        }
        const child = t.getAttribute("data-weft-child")
        if (!child) return
        const turn = this.model?.state.turn
        if (!turn) return
        if (isOpen) {
          turn.expanded.add(child)
          this.go(this.model?.expandChild(child))
        } else {
          turn.expanded.delete(child)
          turn.tried.delete(child) // reopening asks again
        }
      },
      true
    )
    // A step card click marks the step the user is reading — what ⤢
    // carries into Studio (Dv3).
    main.addEventListener("click", (e) => {
      if (!(e.target instanceof Element)) return
      const target = e.target.closest("[data-weft-step]")
      if (!target) return
      const n = Number(target.getAttribute("data-weft-step"))
      if (Number.isFinite(n)) this.model?.selectStep(n)
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
   * Rendered only when the server reports the playground capability,
   * and only under the turn they belong to: a drawer opened on one
   * turn is that turn's (its kept steps, its diff base), whichever
   * turn is being read. */
  private playgroundArea(s: PanelState): HTMLElement {
    const box = el("div")
    const here = s.turn?.id ?? ""
    if (s.result && s.result.sourceRunID === here) box.appendChild(this.experimentResult(s))
    if (!this.canAct(s)) return box
    if (s.drawer && s.drawer.runId === here) box.appendChild(this.drawer(s))
    return box
  }

  /** canAct: the write verbs are drawn when the server reports the
   * playground AND the page's token may use them — a read-scoped
   * panel token (§6: read-only unless minted with "playground": true)
   * is refused on every one, so they are not offered. */
  private canAct(s: PanelState): boolean {
    return hasCapability(s, "playground") && tokenScope(this.cfg.token) !== "read"
  }

  /** field marks an input the redraw must give focus and caret back
   * to. */
  private field<T extends HTMLElement>(n: T, key: string): T {
    n.setAttribute("data-weft-k", key)
    return n
  }

  /** drawer is §3's edit form, pre-filled from the run's registered
   * config: prompt, tools off, model, thinking, input — and the ⚠ on
   * every side-effect tool (ReplayPolicy never or unannotated). Text
   * fields patch the draft without a redraw (the field already shows
   * what was typed). */
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
    const ta = this.field(el("textarea", "weft-input") as HTMLTextAreaElement, "prompt")
    ta.rows = 3
    ta.value = d.instructions
    ta.addEventListener("input", () => this.model?.setDraft({ instructions: ta.value }, true))
    const reset = el("button", "weft-btn", "↺", { title: "reset to the registered prompt" })
    reset.addEventListener("click", () => {
      this.model?.setDraft({ instructions: d.registeredInstructions })
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
          this.model?.setDraft({
            tools: { ...(this.model.state.drawer?.tools ?? d.tools), [t.name]: cb.checked },
          })
        )
        const lab = el("label", "weft-tool", [cb, el("span", undefined, t.name)])
        if (t.side_effects === "never" || !t.side_effects) {
          lab.appendChild(
            el("span", "weft-badge weft-warn-badge", "⚠", {
              title:
                "side-effect tool (ReplayPolicy never): its calls substitute or park — never re-fire silently; only side effects: allow runs it for real, and only if the app opted it in",
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
    const own = s.turn?.doc?.model.name ?? ""
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
      const inTa = this.field(el("textarea", "weft-input") as HTMLTextAreaElement, "input")
      inTa.rows = 2
      inTa.value = d.input
      inTa.addEventListener("input", () => this.model?.setDraft({ input: inTa.value }, true))
      inputRow.appendChild(inTa)
      body.appendChild(inputRow)
    }

    // The side-effect mode (§6 rule 3): substitute (the default — a
    // recorded side-effect call answers from the record, a miss parks),
    // park (every side-effect call waits), allow (the tools the app
    // opted in with AllowSideEffects run for real — only here; a
    // ReplaySafe tool runs in every mode).
    const seRow = el("div", "weft-fields")
    const seSel = el("select", "weft-input") as HTMLSelectElement
    seSel.title =
      "How side-effect tools behave in the re-run. ReplaySafe tools always run; the others substitute, park, or — under allow, if the app opted them in — run for real."
    const seLabel = el("option", undefined, "side effects: substitute", {
      title: "a side-effect call the source recorded is answered from the record; any other call parks for you",
    }) as unknown as HTMLOptionElement
    seLabel.value = ""
    seSel.appendChild(seLabel)
    const parkOpt = el("option", undefined, "park", {
      title: "every side-effect call parks for you; nothing is answered from the record",
    }) as unknown as HTMLOptionElement
    parkOpt.value = "park"
    seSel.appendChild(parkOpt)
    const allowOpt = el("option", undefined, "allow — runs the tools this app opted in (AllowSideEffects) for real", {
      title: "refused unless every tool left on is opted in or ReplaySafe",
    }) as unknown as HTMLOptionElement
    allowOpt.value = "allow"
    seSel.appendChild(allowOpt)
    seSel.value = d.sideEffects === "substitute" ? "" : d.sideEffects
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
    // Fork mode (§5.4, review fix 4a — P4 renders in both surfaces):
    // ephemeral (an experiment, never a turn) or fork (a new session
    // with lineage, the input its next turn). Fork needs an input —
    // the runtime refuses the command otherwise.
    const thrSel = el("select", "weft-input") as HTMLSelectElement
    const thrEphemeral = el("option", undefined, "thread: ephemeral") as unknown as HTMLOptionElement
    thrEphemeral.value = "ephemeral"
    thrSel.appendChild(thrEphemeral)
    const thrFork = el("option", undefined, "fork (new session)") as unknown as HTMLOptionElement
    thrFork.value = "fork"
    thrSel.appendChild(thrFork)
    thrSel.value = d.thread
    thrSel.title = "fork continues the conversation in a new session (needs an input)"
    thrSel.addEventListener("change", () => this.model?.setDraft({ thread: thrSel.value as ExperimentDraft["thread"] }))
    seRow.appendChild(thrSel)
    body.appendChild(seRow)

    // Rung 3 in the panel (§8.3, review fix 4c — "all in the panel"
    // per WEFT-DEVTOOLS §10's rung-3 gate): the tools every run the
    // runtime starts parks on. Rendered only when meta reports the
    // breakpoints capability. The rule is the runtime's: the app's own
    // turns are not touched by it (PQ7), and the label says so.
    // Runtime-wide, so no panel token may set it (the server token, or
    // setup A's open API).
    if (hasCapability(s, "breakpoints") && tokenScope(this.cfg.token) === "" && agent?.tools.length) {
      const brkRow = el("div", "weft-field")
      brkRow.appendChild(
        el("span", undefined, "Break on (parks every run)", {
          title: "applies to runs this runtime starts — the app's own turns are viewer-only (PQ7)",
        })
      )
      for (const t of agent.tools) {
        const cb = el("input") as HTMLInputElement
        cb.type = "checkbox"
        cb.checked = s.breakpoints.includes(t.name)
        cb.addEventListener("change", () => {
          const set = this.model?.state.breakpoints ?? s.breakpoints
          const next = set.filter((n) => n !== t.name)
          if (cb.checked) next.push(t.name)
          next.sort()
          this.go(this.model?.setBreakpoints(next))
        })
        brkRow.appendChild(el("label", "weft-tool", [cb, el("span", undefined, t.name)]))
      }
      body.appendChild(brkRow)
    }

    // The transcript edits (D2/D3): when continuing from a step, the
    // kept steps' results are patchable and their call-free replies
    // rewritable — the counterfactual the fresh step answers.
    const t = s.turn
    if (d.step > 0 && t) {
      const draft = (): ExperimentDraft => this.model?.state.drawer ?? d
      const editsBox = el("div", "weft-field")
      editsBox.appendChild(el("span", undefined, `Transcript edits (steps 0..${d.step - 1} are kept)`))
      // Steps are numbered the way from_step and the edits count them
      // on the wire: over the run's own steps, in order (stepOrdinal).
      for (const [n, step] of t.folded.steps.entries()) {
        if (n >= d.step) break
        for (const call of step.toolCalls) {
          if (!call.result) continue
          const lab = el("label", "weft-edit")
          lab.appendChild(el("span", undefined, `step ${n} · ${call.name} →`))
          const inp = this.field(
            el("input", "weft-input") as HTMLInputElement,
            `edit:${n}:${call.callId}`
          )
          inp.placeholder = String(call.result.content).slice(0, 60)
          const editOf = () => draft().edits.find((e) => e.step === n && e.callID === call.callId)
          inp.value = editOf()?.toolResult ?? ""
          inp.addEventListener("input", () => {
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            if (inp.value === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, toolResult: inp.value, step: n, callID: call.callId }
            } else {
              next.push({ step: n, callID: call.callId, toolResult: inp.value })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.appendChild(inp)
          editsBox.appendChild(lab)
        }
        if (step.text && !step.toolCalls.length) {
          const lab = el("label", "weft-edit")
          lab.appendChild(el("span", undefined, `step ${n} · reply`))
          const reply = this.field(
            el("textarea", "weft-input") as HTMLTextAreaElement,
            `edit:${n}`
          )
          reply.rows = 2
          const editOf = () => draft().edits.find((e) => e.step === n && !e.callID)
          reply.value = editOf()?.content ?? ""
          reply.addEventListener("input", () => {
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            if (reply.value === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, content: reply.value, step: n }
            } else {
              next.push({ step: n, content: reply.value })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.appendChild(reply)
          editsBox.appendChild(lab)
        }
      }
      if (editsBox.childElementCount > 1) body.appendChild(editsBox)
    }

    const run = el("button", "weft-run-btn", "Run experiment ▶", {
      title: "POST /api/playground/runs — the runtime in your app executes it",
    })
    run.addEventListener("click", () => this.go(this.model?.runExperiment()))
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
    // P4's saves (§3's row): keep-as-prompt is the copy-the-text
    // fallback (PQ2: the weft/prompt version lands with that module);
    // save-as-fixture hands off to Studio (the files download there).
    const keep = el("button", "weft-btn", "keep as prompt ⤴", {
      title: "copy the edited prompt (weft/prompt versions are post-v1, PQ2)",
    })
    keep.addEventListener("click", () => {
      const text = this.model?.state.drawer?.instructions ?? ""
      // The clipboard refuses without focus or permission: not the
      // host page's error.
      try {
        // undefined outside a secure context
        const clip = navigator.clipboard as Clipboard | undefined
        clip?.writeText(text).catch(quiet)
      } catch {
        // no clipboard here
      }
    })
    head.appendChild(keep)
    const fixtureURL = new URL("playground", this.base)
    if (r.runID) fixtureURL.hash = new URLSearchParams({ run: r.runID }).toString()
    const fixture = el("a", "weft-btn", "save as fixture", {
      href: fixtureURL.toString(),
      target: "_blank",
      rel: "noopener",
      title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)",
    })
    fixture.style.textDecoration = "none"
    head.appendChild(fixture)
    const compare = el("a", "weft-btn", "compare in Studio", {
      title: "open the Studio playground with this run, step and the current overrides carried over",
    })
    // Built when it is used: the drawer's text fields patch the draft
    // without a redraw, and the link must carry what they say now.
    const link = () => {
      const draft = this.model?.state.drawer ?? null
      const mine = draft && draft.runId === r.sourceRunID ? draft : null
      // The step Studio continues from: the drawer's own, else the one
      // being read — as from_step counts it.
      const step =
        mine && mine.step > 0 ? mine.step : s.turn ? stepOrdinal(s.turn.folded, s.selectedStep) : -1
      compare.setAttribute("href", studioPlaygroundLink(this.base, mine, step))
    }
    link()
    compare.setAttribute("target", "_blank")
    compare.setAttribute("rel", "noopener")
    for (const ev of ["pointerdown", "focus", "click", "contextmenu"]) compare.addEventListener(ev, link)
    compare.style.textDecoration = "none"
    head.appendChild(compare)
    const discard = el("button", "weft-btn", "discard", { title: "clear the result pane" })
    discard.addEventListener("click", () => this.model?.discardResult())
    head.appendChild(discard)
    card.appendChild(head)
    const body = el("div", "weft-step-b")
    if (r.error) body.appendChild(el("div", "weft-note weft-warn", r.error))
    const status = r.row?.status ?? (r.ready ? "succeeded" : "running")
    for (const step of r.folded.steps) {
      if (step.text) body.appendChild(el("div", undefined, step.text))
      for (const call of step.toolCalls) body.appendChild(renderCall(call, status))
    }
    if (!r.folded.steps.length && !r.error && r.state === "queued")
      body.appendChild(el("div", "weft-note", "queued — waiting for the runtime to ack…"))
    else if (!r.folded.steps.length && !r.error && r.state === "accepted" && !r.runID)
      // A fork's accepted row names no run: the finished one names the
      // session's new turn, and the pane moves to it then.
      body.appendChild(el("div", "weft-note", "accepted — the fork's turn is running in its new session…"))

    // The 2-way compare (P3, PQ3 — the panel stays 2-way): the diff's
    // other side is the source turn by default, or any sibling of it.
    const siblings = [
      { id: "", label: sourceLabel(r.label) },
      ...(s.experiments.get(r.sourceRunID) ?? [])
        .filter((x) => x.id !== r.runID)
        .map((x) => ({ id: x.id, label: shortId(x.id) })),
    ]
    if (siblings.length > 1) {
      const cmp = el("select", "weft-input") as HTMLSelectElement
      for (const sb of siblings) {
        const o = el("option", undefined, `compare vs ${sb.label || "source"}`) as unknown as HTMLOptionElement
        o.value = sb.id
        cmp.appendChild(o)
      }
      cmp.value = r.compareWith
      cmp.addEventListener("change", () => this.go(this.model?.setCompare(cmp.value)))
      body.appendChild(cmp)
    }

    // The inline diff (§3: `diff vs t3:`), once there is final text —
    // on text, and on the tool calls beside it. Both sides are whole
    // turns read the same way (turnWordsOf), and only an ended run is
    // diffed: a table per streamed delta is the host page's frame
    // budget, and half an answer is not a difference.
    const mine: TurnWords | null = r.ready ? (r.words ?? foldedWords(r.folded)) : null
    const other: TurnWords | undefined = r.compareWith
      ? this.model?.compareWords.get(r.compareWith)
      : r.source
    const otherLabel = r.compareWith ? shortId(r.compareWith) : sourceLabel(r.label)
    if (mine && other && mine.text && other.text) {
      const diffBox = el("div", "weft-diff")
      this.diffInto(diffBox, `diff vs ${otherLabel}:`, other.text, mine.text)
      // The tool-call diff beside the text: name(args) lines.
      if (other.calls.join("\n") !== mine.calls.join("\n"))
        this.diffInto(diffBox, "tool calls:", other.calls.join("\n"), mine.calls.join("\n"))
      body.appendChild(diffBox)
    }

    // The parked calls' controls: the runtime-started run's approval
    // verbs (§8.2), through POST /api/runs/{id}/approvals. Offered
    // only once the pane holds this run's own stored pending set
    // (ready) — a call still streaming, or left over from an earlier
    // leg of a chain, is not this run's to decide.
    if (r.ready && r.folded.pending.length && r.runID && this.canAct(s))
      body.appendChild(this.decisions(r.folded.pending, r.decided))
    // Rung 4 (§8.4): steer the running experiment — one user message
    // delivered mid-flight into the run the runtime holds (an ephemeral
    // run, or a fork's turn once the runtime's ack names it).
    if (hasCapability(s, "steer") && this.canAct(s) && r.state === "accepted" && r.runID) {
      const box = el("div", "weft-step")
      box.appendChild(el("div", "weft-step-h", [el("span", undefined, "steer this run")]))
      const b = el("div", "weft-step-b")
      const input = this.field(el("input", "weft-input") as HTMLInputElement, "steer")
      input.placeholder = "a message delivered mid-flight"
      input.value = this.scratch.get("steer") ?? ""
      input.addEventListener("input", () => this.scratch.set("steer", input.value))
      const send = el("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" })
      send.addEventListener("click", () => {
        if (!input.value) return
        this.go(this.model?.steer(input.value))
        this.scratch.delete("steer")
        input.value = ""
      })
      b.append(input, send)
      box.appendChild(b)
      body.appendChild(box)
    }
    card.appendChild(body)
    return card
  }

  /** diffInto appends one before → after diff under its header, or
   * the note that sends a diff too large for the dock to Studio. */
  private diffInto(box: HTMLElement, title: string, before: string, after: string) {
    const cells = (before.split("\n").length + 1) * (after.split("\n").length + 1)
    if (cells > DIFF_CELLS) {
      box.appendChild(el("div", "weft-diff-h", `${title}  too large for the panel — compare in Studio`))
      return
    }
    const rows = diffLines(before, after)
    box.appendChild(el("div", "weft-diff-h", `${title}  ${diffSummary(rows)}`))
    for (const row of rows) {
      if (row.kind === "same") continue
      box.appendChild(
        el("div", `weft-diff-row weft-diff-${row.kind}`, `${row.kind === "add" ? "+" : "−"} ${row.text}`)
      )
    }
  }

  /** decisions draws the parked calls of the result's run with the
   * approval verbs. resolve pastes the result typed beside it — never
   * an empty one. Each call is decided on its own (POST
   * /api/runs/{id}/approvals carries one), and the runtime holds the
   * decisions until every parked call has one, then resumes the run
   * once: the pane shows which are in and how many it still waits
   * for. A held decision can be changed until the last one lands. */
  private decisions(pending: ToolCallPart[], decided: Record<string, string>): HTMLElement {
    const approvals = el("div", "weft-step")
    approvals.appendChild(el("div", "weft-step-h", [el("span", undefined, "awaiting decision")]))
    const abody = el("div", "weft-step-b")
    const left = pending.filter((c) => !decided[c.id]).length
    if (left < pending.length) {
      abody.appendChild(
        el(
          "div",
          "weft-note",
          `waiting for ${left} more decision${left === 1 ? "" : "s"} — the run resumes once every parked call is decided`
        )
      )
    }
    const verbs: Record<string, string> = { approve: "continue", deny: "skip", resolve: "resolve" }
    for (const call of pending) {
      const line = el("div", "weft-call")
      const head = el("div", "weft-call-h", [
        el("span", "weft-name", call.name),
        el("span", "weft-args", call.args === undefined ? "(…)" : fmtJSON(call.args)),
      ])
      if (decided[call.id])
        head.appendChild(el("span", "weft-badge weft-info", `decided: ${verbs[decided[call.id]] ?? decided[call.id]}`))
      line.appendChild(head)
      const ctl = el("div", "weft-res")
      const key = `resolve:${call.id}`
      const paste = this.field(el("input", "weft-input weft-resolve") as HTMLInputElement, key)
      paste.placeholder = "the result to resolve with"
      paste.value = this.scratch.get(key) ?? ""
      paste.addEventListener("input", () => this.scratch.set(key, paste.value))
      const mk = (label: string, title: string, act: () => void) => {
        const b = el("button", "weft-btn", label, { title })
        b.addEventListener("click", act)
        return b
      }
      ctl.append(
        mk("continue", "Approve: the handler runs for real", () =>
          this.go(this.model?.decide(call.id, "approve"))
        ),
        mk("skip", "Deny: the model sees a denied result", () =>
          this.go(this.model?.decide(call.id, "deny"))
        ),
        paste,
        mk("resolve…", "Resolve: the model sees the result typed here; the handler never runs", () => {
          if (!paste.value) {
            paste.focus()
            return
          }
          this.go(this.model?.decide(call.id, "resolve", paste.value))
        })
      )
      line.appendChild(ctl)
      abody.appendChild(line)
    }
    approvals.appendChild(abody)
    return approvals
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
    const prompt = turnPromptOf(t.transcript)
    if (prompt) wrap.appendChild(el("div", "weft-note", prompt))
    // thread's session markers (A9.2) at the top of the turn they are
    // filed under; a run from before A9 names none and draws nothing.
    const comps = compactionsOf(t.doc)
    for (const c of comps.filter(isSessionMarker))
      wrap.appendChild(compactionBox(c, comps, t.transcript, { keys: this.openKeys, scope: t.id }))
    const row = this.rowOf(t.id)
    wrap.appendChild(
      renderFolded(
        t.folded,
        row?.status ?? t.doc?.status ?? "running",
        t,
        s.selectedStep,
        { keys: this.openKeys, scope: t.id },
        { endpoint: this.base }
      )
    )
    if (t.folded.pending.length) wrap.appendChild(this.approvals(t.folded.pending, !!row?.playground))
    if (this.canAct(s)) wrap.appendChild(this.actions(s))
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
    experiment.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, 0)))
    row.appendChild(experiment)
    const rerun = el("button", "weft-btn", "↻ Re-run", {
      title: "re-run the whole turn with the drawer's current edits",
    })
    rerun.addEventListener("click", () => this.go(this.model?.rerun(t.id)))
    row.appendChild(rerun)
    const from = stepOrdinal(t.folded, s.selectedStep)
    if (from > 0) {
      const cont = el("button", "weft-btn", `⎇ Continue from step ${from}`, {
        title: "keep the transcript through the previous step (edits apply) and run this step fresh",
      })
      cont.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, from)))
      row.appendChild(cont)
    }
    return row
  }

  /** notes renders the honesty rules (§2): truncation badges live on
   * the calls themselves (renderCall); these are the turn's holes from
   * the shared table (lib/honesty.ts, as the run page's header) — the
   * run document's (not_recorded, interrupted, derived, stripped,
   * gap), the recorder's cuts, the gaps the walk saw, a max_tokens
   * finish, content-off spans — each a badge with its reason and fix,
   * and a story longer than the panel reads. */
  private notes(t: TurnView): HTMLElement {
    const box = el("div")
    box.setAttribute("data-weft-turn-holes", "")
    const row = this.rowOf(t.id)
    const holes = turnHoles(t, row)
    for (const m of holes) {
      const w = holeWords(m)
      const note = el("div", `weft-note${w.tone === "loss" ? " weft-warn" : ""}`, `${w.label} — ${w.reason}${w.fix ? ` · fix: ${w.fix}` : ""}`)
      note.setAttribute("data-weft-hole", m.hole)
      box.appendChild(note)
    }
    if (t.capped) {
      box.appendChild(
        el(
          "div",
          "weft-note weft-warn",
          `a long run: the first ${MAX_EVENT_PAGES * 500} events are shown — the whole story is in Studio (⤢)`
        )
      )
    }
    return box
  }

  /** approvals shows the open turn's parked calls read-only (§2): the
   * decision verbs act on runs a runtime started (§8.2), from the
   * result pane that ran them — the app's own turns are viewer-only
   * (D7, PQ7), and the note says so. */
  private approvals(pending: { id: string; name: string; args?: unknown }[], experiment: boolean): HTMLElement {
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
      line.appendChild(
        el(
          "div",
          "weft-res",
          experiment
            ? "an experiment's run — its decision controls are in the result pane of the turn that ran it"
            : "the app's own turns are viewer-only (PQ7) — decide from your app"
        )
      )
      body.appendChild(line)
    }
    box.appendChild(body)
    return box
  }

  private footer(s: PanelState): HTMLElement {
    const line = "prompts, args and results from your app, via your Studio"
    if (strippedContent(s.turn?.folded)) {
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

/** turnHoles is a turn's holes, from the shared table: the run
 * document's and the fold's (runHoles, the run page's header), the
 * row's interrupted status and max_tokens stop, the walk's gaps. */
export function turnHoles(t: TurnView, row?: RunRow): HoleMark[] {
  return mergeHoles(
    runHoles(t.doc, t.folded),
    statusHoles({
      status: row?.status ?? t.doc?.status,
      stop_reason: row?.stop_reason ?? t.doc?.stop_reason,
      gaps: t.gaps,
    })
  )
}

/** stepOrdinal is the step being read as source.from_step counts it:
 * its place among the run's own steps (0-based) — the runtime and
 * Studio cut the transcript at the Nth assistant message the run
 * produced, whatever index the step's events carry. -1 when none. */
export function stepOrdinal(view: FoldedRun, selected: number | null): number {
  return selected == null ? -1 : view.steps.findIndex((st) => st.index === selected)
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

/** Where a fold is drawn: the Studio endpoint (the hand-off links),
 * and — inside a subagent expander — the child's own view, whose
 * request record and holes its steps show, and below which nothing
 * nests inline (one level, plan A10). */
export interface FoldCtx {
  endpoint?: string
  child?: ChildView
}

/** renderFolded draws the turn view (§2). */
export function renderFolded(
  view: FoldedRun,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null,
  open?: OpenState,
  ctx?: FoldCtx
): HTMLElement {
  const wrap = el("div")
  if (view.model?.name) {
    wrap.appendChild(el("div", "weft-reason", `${view.model.provider}/${view.model.name}`))
  }
  for (const step of view.steps)
    wrap.appendChild(renderStep(step, runStatus, t, selectedStep, open, ctx))
  return wrap
}

function renderStep(
  step: FoldedStep,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null,
  open?: OpenState,
  ctx?: FoldCtx
): HTMLElement {
  const card = el("div", "weft-step")
  card.setAttribute("data-weft-step", String(step.index))
  if (selectedStep === step.index) card.style.outline = "1px solid var(--w-accent)"
  const head = el("div", "weft-step-h", [
    el("span", undefined, `step ${step.index}`),
    el("span", "weft-grow"),
  ])
  // A child's step reads the child's record (by the child's id).
  const req = ctx?.child ? ctx.child.requests : t?.requests
  // The attempt and timing line (plan A4): the request rows already
  // read for the Request line, the folded step_finish's timing — no
  // fetch of its own; the same words as the run page (lib/attempts).
  const rows = req?.steps.get(step.index)?.rows ?? []
  // A record cut at the page cap holds every row of the steps before
  // its last row's step; that step's rows and later ones may be short
  // — no total and no pre-A4 reading from them.
  const complete = !req?.error && (!req?.truncated || step.index < lastStep(req))
  if (step.finish) {
    head.appendChild(el("span", undefined, step.finish.reason))
    head.appendChild(el("span", undefined, usageLine(step.finish.usage)))
  }
  // A step whose tools started has answered (tools run only after a
  // successful model call), finished or not.
  const line = complete ? attemptLine(factsFromRows(rows, !!step.finish || step.toolCalls.length > 0, runStatus === "running")) : null
  if (line) head.appendChild(el("span", "weft-badge weft-info", line, { "data-weft-attempts": "" }))
  if (step.finish) {
    const timing = timingLine(step.finish.latencyMs, step.finish.ttftMs, "ttft")
    if (timing) head.appendChild(el("span", undefined, timing, { "data-weft-timing": "" }))
  }
  const own = stepHoles(step, ctx?.child ? ctx.child.doc?.holes : t?.doc?.holes)
  const old = complete ? attemptsHole(step.finish, rows.length) : null
  const holes = holeBadges(old ? mergeHoles(own, [old]) : own)
  if (holes) head.appendChild(holes)
  card.appendChild(head)
  const body = el("div", "weft-step-b")
  // The step's run-scope compaction views (A9.2), on its step line.
  const doc = ctx?.child ? ctx.child.doc : t?.doc
  const comps = compactionsOf(doc)
  for (const c of comps)
    if (!isSessionMarker(c) && c.step === step.index)
      body.appendChild(compactionBox(c, comps, ctx?.child ? ctx.child.transcript : t?.transcript, open))
  if (req) body.appendChild(requestLine(step.index, req, runStatus, open))
  if (step.reasoning) {
    const d = el("details", "weft-collapsible")
    if (open) {
      // Collapsed by default (§2); what the user opened stays open
      // across redraws.
      const key = `${open.scope}\u0000reasoning\u0000${step.index}`
      d.setAttribute("data-weft-open", key)
      if (open.keys.has(key)) d.setAttribute("open", "")
    }
    d.appendChild(el("summary", undefined, "reasoning"))
    d.appendChild(el("div", undefined, step.reasoning))
    body.appendChild(d)
  }
  if (step.text) body.appendChild(el("div", undefined, step.text))
  if (step.steer) body.appendChild(el("div", "weft-note", `steered: ${step.steer.text}`))
  for (const call of step.toolCalls) body.appendChild(renderCall(call, runStatus, t, open, ctx))
  card.appendChild(body)
  return card
}

/** compactionBox is one compaction marker (plan A9.2, the run page's
 * words from lib/compaction): the line, the `compacted` badge, and
 * "show original" collapsed — for a view, the replaced transcript
 * messages read from the growth records the turn already holds (a gap
 * badge when they cannot be placed); for a session marker, where the
 * replaced context lives. Never a fetch. The open state is keyed by
 * a view's index (two views can share a hash: the same insertion at
 * the same place every step) and by a session marker's hash. */
function compactionBox(
  c: RunCompaction,
  all: RunCompaction[],
  transcript: Transcript | null | undefined,
  open?: OpenState
): HTMLElement {
  const session = isSessionMarker(c)
  const box = el("div", "weft-note")
  box.setAttribute("data-weft-compaction", session ? "session" : String(c.step ?? ""))
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", session ? SESSION_LABEL : "compaction"),
    el("span", "weft-args", compactionLine(c)),
  ])
  const badges = holeBadges([{ hole: "compacted" }])
  if (badges) head.appendChild(badges)
  box.appendChild(head)
  const d = el("details", "weft-collapsible")
  if (open) {
    const key = `${open.scope}\u0000compaction\u0000${session ? `session\u0000${c.hash}` : `view\u0000${c.index ?? ""}`}`
    d.setAttribute("data-weft-open", key)
    if (open.keys.has(key)) d.setAttribute("open", "")
  }
  d.appendChild(el("summary", undefined, "show original"))
  if (session) {
    d.appendChild(el("div", "weft-res", sessionNote(c)))
  } else {
    const o = originalOf(c, transcript, all)
    if ("loading" in o) d.appendChild(el("div", "weft-res", "loading the transcript…"))
    else if ("gap" in o) {
      const gap = holeBadges([{ hole: "gap", reason: o.gap }])
      if (gap) d.appendChild(gap)
      d.appendChild(el("div", "weft-reason", o.gap))
    }
    else if (!o.messages.length) d.appendChild(el("div", "weft-res", `nothing replaced: inserted at message ${o.from}`))
    else
      for (const [i, m] of o.messages.entries())
        d.appendChild(el("div", "weft-res", messageLine(m), { "data-weft-original": String(o.from + i) }))
    d.appendChild(el("div", "weft-reason", replacementNote(c)))
  }
  box.appendChild(d)
  return box
}

/** lastStep is the highest step the request record holds a row of. */
function lastStep(req: PanelRequests): number {
  let n = -1
  for (const k of req.steps.keys()) if (k > n) n = k
  return n
}

/** A request hole's badge as the panel words it: the shared honesty
 * table's label (lib/honesty.ts — the run page reads the same table),
 * prefixed where the label does not name the request. */
function holeLabel(badge: string): string {
  const label = badge === "not_recorded" ? REQUEST_NOT_RECORDED_LABEL : holeWords({ hole: badge }).label
  return label.startsWith("request") ? label : `request: ${label}`
}

/** holeNote draws one hole: the badge, then its reason and fix as
 * words (the response's when it gave them, the shared table's else). */
function holeNote(box: HTMLElement, badge: string, reason?: string, fix?: string): void {
  const w = holeWords({ hole: badge, reason, fix })
  box.appendChild(el("span", "weft-badge weft-info", holeLabel(badge)))
  box.appendChild(el("div", "weft-reason", [w.reason, w.fix && `fix: ${w.fix}`].filter(Boolean).join(" — ")))
}

/** holeBadges draws a list of holes (a step's, a turn's) from the
 * shared table: one badge each, its reason and fix as the title. */
export function holeBadges(holes: HoleMark[]): HTMLElement | null {
  if (!holes.length) return null
  const box = el("span", "weft-holes")
  for (const m of holes) {
    const w = holeWords(m)
    const b = el("span", `weft-badge ${w.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, w.label, {
      title: w.fix ? `${w.reason} — fix: ${w.fix}` : w.reason,
    })
    b.setAttribute("data-weft-hole", m.hole)
    box.appendChild(b)
  }
  return box
}

/** requestLine is the step's request (ADR 0028 §10, the Studio run
 * page's section in one line): the attempts and the changed marks,
 * the system prompt collapsed, the catalog's names, the params. A hole
 * is its badge with its reason and fix, nothing else — a hidden one
 * never holds a byte of prompt, because a read-scoped token never
 * asked for it. */
function requestLine(step: number, req: PanelRequests, runStatus: string, open?: OpenState): HTMLElement {
  const box = el("div", "weft-req")
  box.setAttribute("data-weft-request", String(step))
  if (req.badge) {
    holeNote(box, req.badge, req.reason, req.fix)
    return box
  }
  if (req.error) {
    box.appendChild(el("span", "weft-badge weft-err", `request could not be read: ${req.error}`))
    return box
  }
  const mine = req.steps.get(step)
  const row = mine?.rows[mine.rows.length - 1]
  if (!mine || !row) {
    box.appendChild(
      el(
        "span",
        "weft-badge",
        runStatus === "running"
          ? `request: ${REQUEST_NOT_STORED}`
          : req.truncated
            ? `request: truncated — first ${(MAX_REQUEST_PAGES * REQUEST_PAGE).toLocaleString("en-US").replace(",", " ")} requests`
            : "request: no record for this step"
      )
    )
    return box
  }
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", "request"),
    el("span", "weft-args", mine.rows.map((r) => `attempt ${r.attempt}`).join(" · ")),
  ])
  if (mine.promptChanged) head.appendChild(el("span", "weft-badge weft-info", "prompt changed at this step"))
  if (mine.catalogChanged) head.appendChild(el("span", "weft-badge weft-info", "catalog changed at this step"))
  if (row.content && row.content !== "stripped") head.appendChild(el("span", "weft-badge", row.content))
  box.appendChild(head)
  if (row.content === "stripped") holeNote(box, "stripped")
  const p = row.prompt
  if (p && !isHoleRef(p)) {
    const d = el("details", "weft-collapsible")
    if (open) {
      const key = `${open.scope}\u0000request\u0000${step}`
      d.setAttribute("data-weft-open", key)
      if (open.keys.has(key)) d.setAttribute("open", "")
    }
    const text = p.text
    d.appendChild(el("summary", undefined, `system prompt · ${text.length} chars`))
    d.appendChild(el("div", "weft-res", text))
    box.appendChild(d)
  } else if (row.system_hash) {
    box.appendChild(
      el("div", "weft-res", `system prompt ${shortHash(row.system_hash)}${p ? ` · ${p.badge === "stripped" ? "stripped" : holeLabel(p.badge)}` : ""}`)
    )
  }
  const names = row.body.tools.names
  box.appendChild(el("div", "weft-res", `tools: ${names.length ? names.join(", ") : "none"}`))
  box.appendChild(el("div", "weft-res", `params: ${paramsLine(row)}`))
  return box
}

function renderCall(
  call: FoldedToolCall,
  runStatus: string,
  t?: TurnView,
  open?: OpenState,
  ctx?: FoldCtx
): HTMLElement {
  const box = el("div", "weft-call")
  const state = callState(call, runStatus)
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", call.name),
    el("span", "weft-args", argsText(call)),
  ])
  box.appendChild(head)
  if (call.childRunId && (t || ctx?.child)) {
    head.appendChild(el("span", "weft-badge weft-info", "subagent", { title: call.childRunId }))
    // One level inline: a grandchild is its badge and the hand-off.
    if (ctx?.child) {
      if (ctx.endpoint) head.appendChild(handOff(ctx.endpoint, call.childRunId))
    } else if (t) box.appendChild(childBlock(call.childRunId, t, open, ctx?.endpoint))
  }
  if (call.result) {
    const ms = spanMs(t, call)
    if (ms) head.appendChild(el("span", "weft-badge weft-info", ms))
    const trunc = truncation(String(call.result.content))
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
    const holes = holeBadges(call.holes ?? [])
    if (holes) head.appendChild(holes)
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
  // The call's own span by its call id; a span without one (a foreign
  // SDK's) matches by tool name.
  const tools = t.spans.filter((sp) => sp.name === "execute_tool")
  const span =
    tools.find((sp) => sp.attrs["gen_ai.tool.call.id"] === call.callId) ??
    tools.find(
      (sp) =>
        sp.attrs["gen_ai.tool.call.id"] === undefined &&
        sp.attrs["gen_ai.tool.name"] === call.name
    )
  if (!span) return ""
  const ms = Date.parse(span.end) - Date.parse(span.start)
  if (!Number.isFinite(ms) || ms < 0) return ""
  return `${Math.round(ms)}ms`
}

/** childBlock is the lazy subagent expander (§2, Dv3): the call's
 * child run folds on demand and nests under the call. The element
 * delegates the toggle through [data-weft-child]. */
function childBlock(childId: string, t: TurnView, open?: OpenState, endpoint?: string): HTMLElement {
  const child = t.children.get(childId)
  const row = t.doc?.children.find((c) => c.id === childId)
  const details = el("details", "weft-collapsible")
  details.setAttribute("data-weft-child", childId)
  if (t.expanded.has(childId)) details.setAttribute("open", "")
  // The nested row (A10): agent, status, usage — the parent's row of
  // the child, before it is opened; its usage only once the record
  // has it, its holes (interrupted, stripped…) as badges.
  const summary = el(
    "summary",
    undefined,
    row
      ? `subagent ${row.agent || shortId(childId)} · ${row.status} · ${
          usageKnown(row.status) ? usageLine(row.usage) : row.status === "running" ? USAGE_AT_FINISH : "—"
        }`
      : `subagent ${shortId(childId)}`
  )
  const holes = row && holeBadges(child?.doc?.holes ?? rowHoles(row))
  if (holes) summary.appendChild(holes)
  details.appendChild(summary)
  if (endpoint) details.appendChild(handOff(endpoint, childId))
  if (!child) {
    details.appendChild(el("div", undefined, "loading the subagent's turn…"))
  } else {
    // The child's own status (the doc lists it): a child still running
    // shows its open calls as running, not as never completed.
    const status = row?.status ?? "succeeded"
    details.appendChild(
      renderFolded(child.folded, status, undefined, undefined, open && { keys: open.keys, scope: childId }, {
        endpoint,
        child,
      })
    )
    if (child.capped)
      details.appendChild(el("div", "weft-note weft-warn", "a long run: its first events are shown"))
  }
  return details
}

/** handOff is "open in Studio" for a child run: today's runs/<id>
 * page (studioLink). G1: the deep-link scheme replaces this URL. */
function handOff(endpoint: string, runId: string): HTMLElement {
  return el("a", "weft-btn", "open in Studio ⤢", {
    href: studioLink(endpoint, runId),
    target: "_blank",
    rel: "noopener",
    title: `open the child run ${runId} in Studio`,
    "data-weft-handoff": runId,
  })
}

function shortId(id: string): string {
  const parts = id.split("/")
  return parts[parts.length - 1] || id
}

/** studioPlaygroundLink builds the §2 hand-off: "compare in Studio"
 * with the context carried over — the run, the step being read, and
 * the drawer's current overrides — so nothing is retyped (the parity
 * rule's documented hand-off for the Studio-only surfaces). Every
 * parameter rides the URL fragment (/playground#run=…): a prompt can be
 * long and is the app's content — the fragment is never sent to a
 * server, never in its logs or a Referer, and no request line bounds
 * it. Studio's /playground reads the fragment (then strips it). */
export function studioPlaygroundLink(
  endpoint: string,
  draft: ExperimentDraft | null,
  step: number | null
): string {
  const u = new URL("playground", endpoint)
  const p = new URLSearchParams()
  if (draft) {
    if (draft.runId) p.set("run", draft.runId)
    if (step != null && step > 0) p.set("step", String(step))
    // Only what changed (§10.1): Studio pre-fills the registered
    // prompt itself, and a prompt is long for a URL.
    if (draft.instructions && draft.instructions !== draft.registeredInstructions)
      p.set("instructions", draft.instructions)
    const on = Object.entries(draft.tools)
      .filter(([, enabled]) => enabled)
      .map(([name]) => name)
    if (on.length && on.length < Object.keys(draft.tools).length)
      p.set("tools", on.join(","))
    if (draft.model) p.set("model", draft.model)
    if (draft.thinking) p.set("thinking", draft.thinking)
    if (draft.input && draft.step === 0) p.set("input", draft.input)
    // The run's shape rides along too (the non-default values): a
    // scripted, parked or forked experiment must not open in Studio
    // as a live substitute ephemeral one.
    if (draft.engine === "scripted") p.set("engine", draft.engine)
    if (draft.sideEffects && draft.sideEffects !== "substitute")
      p.set("side_effects", draft.sideEffects)
    if (draft.thread === "fork") p.set("thread", draft.thread)
    // …and who runs it: the agent and the runtime the drawer opened on.
    if (draft.agent) p.set("agent", draft.agent)
    if (draft.runtimeId) p.set("runtime", draft.runtimeId)
  }
  const hash = p.toString()
  if (hash) u.hash = hash
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

/** mount is the programmatic mount (plan C2's rung 1; the npm entry,
 * C1, exports it — the script-tag bundle stays a side-effect module):
 * a <weft-devtools> configured by opts, field by field above every
 * other source, appended to target (default document.body). It is the
 * host's mount: where Studio does not answer it shows the
 * not-reachable line, never removes itself. */
export function mount(opts: MountOptions & { target?: Element } = {}): WeftDevtools {
  if (!customElements.get("weft-devtools")) customElements.define("weft-devtools", WeftDevtools)
  const { target, ...options } = opts
  const node = document.createElement("weft-devtools") as WeftDevtools
  node.options = options
  ;(target ?? document.body).appendChild(node)
  return node
}
