// Plan D3's accessibility budget: axe-core (a devDependency, never in
// the bundle) over the panel's shadow root in each mode — float open,
// docked right, the bottom sheet at 400 px, the pill, and (D4) the Raw
// tab's open, filtered tree and the Timeline tab, and (D5) a turn
// whose badges, cap line and chips are all drawn, and (E1.2) the
// Request tab's chips, diff and trees, and (E3.2) the experiment's
// step compare table — in both themes (D2),
// with axe's default rules; the budget is zero violations. jsdom has
// no layout, so the rules that need one (color-contrast, and
// label-content-name-mismatch's visible text) come back "incomplete",
// never as a pass: those are the browser
// gate's (studio/README.md, "The accessibility budget").
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { golden } from "../test/fake-studio"
import { baseRoutes, DIFF_META, fakeStudio, META, mount, page, runEvents, runExperiment, runRow, settle, setup, stepDiffRoutes, teardown, transcript } from "./testkit"
import type { Route } from "./testkit"
import { FILTER_MS } from "./tree"
import type { WeftDevtools } from "./element"
import { REPLAY_META, replayRoutes } from "./replaykit"

beforeEach(() => {
  setup()
  // axe probes a canvas for contrast; jsdom has none (and would say so).
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
})
afterEach(() => {
  vi.restoreAllMocks()
  teardown()
  viewport(1024, 768)
})

function viewport(w: number, h: number) {
  Object.defineProperty(window, "innerWidth", { value: w, configurable: true, writable: true })
  Object.defineProperty(window, "innerHeight", { value: h, configurable: true, writable: true })
  window.dispatchEvent(new Event("resize"))
}

const BASE = { "data-endpoint": "http://studio.test/studio/", "data-public-id": "pub_orders" }

/** The themes the budget runs in, and the attributes that pick one. */
const THEMES: Record<string, Record<string, string>> = {
  dark: { "data-theme": "dark" },
  light: { "data-theme": "light" },
}

/** D4: the Raw tab with its tree open and filtered (a marked match),
 * and the Timeline tab. */
async function rawOpen(el: WeftDevtools) {
  ;(el.shadowRoot!.querySelector("#weft-tab-raw") as HTMLElement).click()
  await settle()
  const q = el.shadowRoot!.querySelector(".weft-tree-q") as HTMLInputElement
  q.value = "run_start"
  q.dispatchEvent(new Event("input", { bubbles: true }))
  await settle(FILTER_MS + 20) // the filter's debounce
}
/** D3 fixes: the experiment drawer (its selects and fields), and the
 * Raw tab's tree with the ? shortcuts overlay over it. */
async function drawer(el: WeftDevtools) {
  ;(Array.from(el.shadowRoot!.querySelectorAll("button")).find((b) => b.textContent === "✎ Experiment") as HTMLElement).click()
  await settle()
}
/** F1: the replay verbs drawn, and the drawer a verb opened — its
 * ack preview and the pre-filled edit. */
async function replayDrawer(el: WeftDevtools) {
  ;(el.shadowRoot!.querySelector('.weft-call[data-key="c3"] [data-weft-verb="edit_result"]') as HTMLElement).click()
  await settle()
}
async function rawAndKeys(el: WeftDevtools) {
  await rawOpen(el)
  el.shadowRoot!.querySelector(".weft-dock")!.dispatchEvent(new KeyboardEvent("keydown", { key: "?", bubbles: true, composed: true, cancelable: true }))
  await settle()
}
async function timeline(el: WeftDevtools) {
  ;(el.shadowRoot!.querySelector("#weft-tab-timeline") as HTMLElement).click()
  await settle()
}

/** D5: a turn with the table's badges on its header, step and call,
 * the footer's cap line and the turn chips (a thread fork). */
function badgeRoutes(): Record<string, Route> {
  const r = baseRoutes()
  const row = runRow({ meta: { "weft.session.forked_from": "s_00#e_3" }, stop_reason: "max_tokens" })
  r["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [row], next_before: null }
  r["runs/s_01-t1"] = { ...row, children: [], holes: [{ hole: "not_recorded" }, { hole: "derived" }] }
  r["runs/s_01-t1/events?after=0&limit=500"] = page(
    runEvents("s_01-t1").map((event, pos) => (pos === 2 ? { pos, time: "2026-10-01T09:00:00Z", event, attrs: { "weft.content.truncated_bytes": 4096 } } : event)),
    { gaps: [9] }
  )
  return r
}

async function badgesDrawn(el: WeftDevtools) {
  const root = el.shadowRoot!
  for (const sel of [
    '.weft-step-h [data-hole="truncated"]',
    '[data-weft-turn-holes] [data-hole="max_tokens"]',
    '[data-weft-turn-holes] [data-hole="gap"]',
    '[data-weft-turn-holes] [data-hole="not_recorded"]',
    '[data-weft-turn-holes] [data-hole="derived"]',
    '.weft-footer [data-weft-cap="truncated"]',
  ])
    expect(root.querySelector(sel), sel).not.toBeNull()
}

/** E1.2: the Request tab over a recorded request record — step 2's
 * chip and diff, a tool expanded to its schema tree, the earlier
 * messages' tree open. */
function requestRoutes(): Record<string, Route> {
  const r = baseRoutes()
  const id = "s_01-t1"
  const evs: unknown[] = [{ type: "run_start", id, model: { provider: "wefttest", name: "script" } }]
  for (let i = 0; i < 3; i++) evs.push({ type: "step_start", run_id: id, index: i }, { type: "step_finish", run_id: id, index: i, reason: "stop", usage: { input_tokens: 1, output_tokens: 1 } })
  r[`runs/${id}/events?after=0&limit=500`] = page(evs)
  const m = (role: string) => [{ role, content: [{ type: "text", text: role }] }]
  r[`runs/${id}/transcript`] = transcript(m("user"), m("assistant"), m("tool"), m("assistant"), m("tool"))
  r[`runs/${id}/requests?limit=1000`] = golden("requests-ok")
  return r
}
async function request(el: WeftDevtools) {
  const q = (sel: string) => el.shadowRoot!.querySelector(sel) as HTMLElement
  q("#weft-tab-request").click()
  await settle()
  q('[data-weft-rq-step="2"]').click()
  await settle()
  q('[data-weft-tool="refund"] button').click()
  await settle()
  Array.from(el.shadowRoot!.querySelectorAll("#weft-tp-request button")).find((b) => b.textContent.includes("earlier messages"))?.dispatchEvent(new Event("click", { bubbles: true }))
  await settle()
  for (const sel of ['#weft-tp-request [data-weft-mark="prompt"]', '#weft-tp-request [data-weft-diff="del"]', '#weft-tp-request [data-weft-tool="refund"] .weft-tree'])
    expect(el.shadowRoot!.querySelector(sel), sel).not.toBeNull()
}

const MODES: { name: string; width: number; attrs: Record<string, string>; sel: string; act?: (el: WeftDevtools) => Promise<void>; routes?: () => Record<string, Route>; meta?: unknown }[] = [
  { name: "float, open", width: 1024, attrs: { "data-open": "true" }, sel: ".weft-dock.weft-float" },
  { name: "docked right", width: 1024, attrs: { "data-open": "true", "data-position": "right-dock" }, sel: ".weft-dock.weft-docked" },
  { name: "bottom sheet at 400 px", width: 400, attrs: { "data-open": "true" }, sel: ".weft-dock.weft-sheet" },
  { name: "the pill", width: 1024, attrs: {}, sel: ".weft-fab" },
  { name: "the Raw tab, its tree open and filtered", width: 1024, attrs: { "data-open": "true", "data-position": "bottom-dock" }, sel: ".weft-tn.weft-hit", act: rawOpen },
  { name: "the Timeline tab", width: 1024, attrs: { "data-open": "true" }, sel: ".weft-timeline", act: timeline },
  { name: "the experiment drawer open", width: 1024, attrs: { "data-open": "true", "data-position": "right-dock" }, sel: ".weft-drawer select[aria-label=thread]", act: drawer },
  { name: "the Raw tab's tree with the shortcuts overlay", width: 1024, attrs: { "data-open": "true", "data-position": "bottom-dock" }, sel: ".weft-keys", act: rawAndKeys },
  { name: "the Request tab: chips, diff, a tool's schema tree, the earlier messages", width: 1024, attrs: { "data-open": "true", "data-position": "bottom-dock" }, sel: "#weft-tp-request [data-weft-messages-earlier] .weft-tn", act: request, routes: requestRoutes, meta: { ...META, capabilities: [...META.capabilities, "requests"] } },
  { name: "the replay verbs and a verb's drawer with its ack preview", width: 1024, attrs: { "data-open": "true", "data-position": "right-dock" }, sel: ".weft-drawer [data-weft-ack] [data-verdict]", act: replayDrawer, routes: replayRoutes, meta: REPLAY_META },
  { name: "the experiment's step compare: markers, the table, marks and badges", width: 1024, attrs: { "data-open": "true", "data-position": "right-dock" }, sel: '[data-weft-step-diff] table [data-hole="hidden"]', act: runExperiment, routes: () => stepDiffRoutes(golden("diff-hidden")), meta: DIFF_META },
  { name: "badges, the cap line and the chips", width: 1024, attrs: { "data-open": "true", "data-position": "right-dock" }, sel: '[data-weft-chip="fork"]', act: badgesDrawn, routes: badgeRoutes },
]

/** The rules jsdom leaves incomplete: they need layout (the browser
 * gate's). Any other incomplete rule fails the budget. */
const LAYOUT_RULES = ["color-contrast", "label-content-name-mismatch"]

/** audit runs axe's default rules over the panel (its open shadow
 * root, through the host) and returns the violations and the rules
 * jsdom could not decide. */
async function audit(el: WeftDevtools) {
  const res = await axe.run(el, { resultTypes: ["violations", "incomplete"] })
  return {
    violations: res.violations.map((v) => `${v.id}: ${v.help} — ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`),
    incomplete: res.incomplete.map((v) => v.id),
  }
}

describe("the axe budget: zero violations", () => {
  for (const [theme, themeAttrs] of Object.entries(THEMES))
    for (const m of MODES)
      it(`${m.name} (${theme})`, async () => {
        viewport(m.width, 768)
        fakeStudio((m.routes ?? baseRoutes)(), m.meta ?? META)
        const el = await mount({ ...BASE, ...m.attrs, ...themeAttrs })
        await m.act?.(el)
        expect(el.shadowRoot!.querySelector(m.sel)).not.toBeNull()
        const { violations, incomplete } = await audit(el)
        expect(violations, violations.join("\n")).toEqual([])
        // What jsdom cannot judge is said, not passed: both need layout
        // (an exact aria-label = text match is "incomplete" here too).
        for (const id of incomplete) expect(LAYOUT_RULES).toContain(id)
      })
})

describe("the audit itself", () => {
  it("sees inside the shadow root: a control without a name there is a violation", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE, "data-open": "true" })
    el.shadowRoot!.querySelector(".weft-head")!.appendChild(document.createElement("button"))
    const { violations } = await audit(el)
    expect(violations.some((v) => v.startsWith("button-name:"))).toBe(true)
  })
})

describe("axe stays out of the bundle", () => {
  it("panel.js carries no axe", () => {
    const built = readFileSync(resolve(process.cwd(), "../dist/panel/panel.js"), "utf8")
    expect(built).not.toMatch(/axe/i)
  })
})
