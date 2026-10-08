// Plan D3: the keyed renderer and the keyboard it keeps — a draw
// patches the nodes on screen (keyed by run id and step ordinal), so
// a streaming step touches only its own card, and focus, caret and
// scroll stay without a restore; the turn list's roving tabindex; the
// focus trap of a focused float.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { h, on, patch } from "./render"
import {
  $,
  all,
  baseRoutes,
  click,
  FakeEventSource,
  fakeStudio,
  mount,
  page,
  runEvents,
  runRow,
  settle,
  setup,
  T0,
  teardown,
  transcript,
  user,
} from "./testkit"
import type { WeftDevtools } from "./element"
import { PANEL_CSS } from "./styles"

beforeEach(setup)
afterEach(() => {
  vi.restoreAllMocks()
  teardown()
})

const BASE = { "data-endpoint": "http://studio.test/studio/", "data-public-id": "pub_orders", "data-open": "true" }

function key(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent("keydown", { bubbles: true, composed: true, cancelable: true, ...init })
  target.dispatchEvent(e)
  return e
}

describe("patch", () => {
  it("keeps keyed nodes across a reorder, inserts and removes, and writes only what differs", () => {
    const box = h("div")
    const row = (k: string, t: string) => h("p", { "data-key": k }, t)
    patch(box, [row("a", "1"), row("b", "2"), row("c", "3")])
    const [a, b, c] = Array.from(box.children)
    const seen: MutationRecord[] = []
    const mo = new MutationObserver((m) => seen.push(...m))
    mo.observe(box, { subtree: true, childList: true, attributes: true, characterData: true })
    patch(box, [row("z", "0"), row("a", "1"), row("b", "2"), row("c", "3")])
    expect(Array.from(box.children).slice(1)).toEqual([a, b, c])
    patch(box, [row("c", "3"), row("a", "one")])
    expect(Array.from(box.children)).toEqual([c, a])
    expect(a.textContent).toBe("one")
    expect(b.isConnected).toBe(false)
    seen.push(...mo.takeRecords())
    mo.disconnect()
    // Nothing about b's or c's content was written.
    expect(seen.some((m) => m.type === "characterData" && m.target.parentNode === c)).toBe(false)
    expect(seen.some((m) => m.type === "attributes")).toBe(false)
  })

  it("a kept node answers with the newest build's handler, given the live node", () => {
    const box = h("div")
    const got: string[] = []
    let live: HTMLElement | null = null
    const build = (tag: string) => {
      const b = h("button", { type: "button" }, "go")
      on(b, "click", (_, n) => got.push(`${tag}:${n === live}`))
      return b
    }
    patch(box, [build("first")])
    live = box.firstElementChild as HTMLElement
    patch(box, [build("second")])
    expect(box.firstElementChild).toBe(live)
    live.click()
    patch(box, [h("button", { type: "button" }, "go")]) // a build without the handler
    live.click()
    expect(got).toEqual(["second:true"])
  })

  it("a field's typed value and caret survive a build that says the same", () => {
    const box = h("div")
    document.body.appendChild(box)
    const field = (v: string) => {
      const i = h("input") as HTMLInputElement
      i.value = v
      return i
    }
    patch(box, [field("")])
    const live = box.firstElementChild as HTMLInputElement
    live.focus()
    live.value = "hello"
    live.setSelectionRange(2, 2)
    patch(box, [field("hello")])
    expect(document.activeElement).toBe(live)
    expect(live.selectionStart).toBe(2)
    patch(box, [field("")]) // the build clears it (a send): written
    expect(live.value).toBe("")
  })
})

describe("a streaming step re-renders only its own card", () => {
  function liveTurn() {
    const routes = baseRoutes()
    const running = runRow({ id: "s_01-t1", status: "running", finished: null, steps: 2 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [running], next_before: null }
    routes["runs/s_01-t1"] = { ...running, children: [] }
    routes["runs/s_01-t1/events?after=0&limit=500"] = page(
      [...runEvents("s_01-t1").slice(0, 3), { type: "step_start", run_id: "s_01-t1", index: 1 }],
      { done: false }
    )
    routes["runs/s_01-t1/transcript"] = transcript([user("where is my order #4411?")], [{ role: "assistant", content: [{ type: "text", text: "Let me check." }] }])
    return routes
  }

  it("deltas mutate the streaming card and nothing outside it; its text is the one live region", async () => {
    fakeStudio(liveTurn())
    const el = await mount(BASE)
    const tail = FakeEventSource.last("run=s_01-t1")!
    tail.emit("record", { run_id: "s_01-t1", kind: "delta", pos: 0, time: T0, event: { type: "text_delta", run_id: "s_01-t1", text: "Found" } })
    await settle()
    const card = $(el, '.weft-step[data-weft-step="1"]')!
    const other = $(el, '.weft-step[data-weft-step="0"]')!
    expect(card.querySelector(".weft-stream")?.textContent).toBe("Found")
    const inside: MutationRecord[] = []
    const outside: MutationRecord[] = []
    const mo = new MutationObserver((ms) => {
      for (const m of ms) (card.contains(m.target) ? inside : outside).push(m)
    })
    mo.observe(el.shadowRoot!, { subtree: true, childList: true, attributes: true, characterData: true })
    for (let i = 0; i < 6; i++) {
      tail.emit("record", { run_id: "s_01-t1", kind: "delta", pos: i + 1, time: T0, event: { type: "text_delta", run_id: "s_01-t1", text: ` w${i}` } })
      await settle(30)
    }
    await settle(150)
    for (const m of mo.takeRecords()) (card.contains(m.target) ? inside : outside).push(m)
    mo.disconnect()
    expect(card.querySelector(".weft-stream")?.textContent).toBe("Found w0 w1 w2 w3 w4 w5")
    expect(inside.length).toBeGreaterThan(0)
    expect(outside.map((m) => `${m.type} ${m.target.nodeType === 1 ? (m.target as Element).className : m.target.nodeName}`)).toEqual([])
    // The same cards, kept: the earlier step was never rebuilt.
    expect($(el, '.weft-step[data-weft-step="0"]')).toBe(other)
    // aria-live on the streaming text only — not the dock, not step 0.
    expect(all(el, "[aria-live]")).toEqual([card.querySelector(".weft-stream")])
    expect(card.querySelector(".weft-stream")?.getAttribute("aria-live")).toBe("polite")
  })
})

describe("focus on a turn row", () => {
  function twoTurns() {
    const routes = baseRoutes()
    const t2 = runRow({ id: "s_01-t2", turn: 2 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
    routes["runs/s_01-t2"] = { ...t2, children: [] }
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2"))
    routes["runs/s_01-t2/spans"] = { spans: [] }
    routes["runs/s_01-t2/transcript"] = transcript([user("and the refund?")], [{ role: "assistant", content: [{ type: "text", text: "Issued." }] }])
    return routes
  }
  const rows = (el: WeftDevtools) => all(el, ".weft-turn") as HTMLElement[]

  it("a focused row stays focused across a redraw — the same node, no refocus", async () => {
    fakeStudio(twoTurns())
    const el = await mount(BASE)
    const byId = (id: string) => rows(el).find((r) => r.textContent.includes(id))!
    const second = byId("s_01-t1")
    second.focus()
    const focus = vi.spyOn(HTMLElement.prototype, "focus")
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 7 }) })
    await settle()
    expect(byId("s_01-t1").textContent).toContain("7 steps")
    expect(byId("s_01-t1")).toBe(second)
    expect(el.shadowRoot!.activeElement).toBe(second)
    expect(focus).not.toHaveBeenCalled()
  })

  it("the list is a list of listitems with one tab stop; arrows move it, j/k still select", async () => {
    fakeStudio(twoTurns())
    const el = await mount(BASE)
    expect($(el, ".weft-rows")?.getAttribute("role")).toBe("list")
    expect(all(el, ".weft-rows [role=listitem] > .weft-turn")).toHaveLength(2)
    const tabs = () => rows(el).map((r) => r.getAttribute("tabindex"))
    expect(tabs()).toEqual(["0", "-1"]) // the selected (newest) turn
    // A narrow panel hides the list for its dropdown (D1), wrapper and all.
    expect(PANEL_CSS).toContain(".weft-narrow .weft-rows { display: none; }")
    rows(el)[0].focus()
    expect(key(rows(el)[0], { key: "ArrowDown" }).defaultPrevented).toBe(true)
    expect(el.shadowRoot!.activeElement).toBe(rows(el)[1])
    expect(tabs()).toEqual(["-1", "0"])
    await settle()
    expect(tabs()).toEqual(["-1", "0"]) // a redraw keeps the roving stop
    key(rows(el)[1], { key: "ArrowUp" })
    expect(el.shadowRoot!.activeElement).toBe(rows(el)[0])
    expect(tabs()).toEqual(["0", "-1"])
    // j/k (D1) select, as before.
    key(rows(el)[0], { key: "j" })
    await settle()
    expect(rows(el)[1].classList.contains("weft-sel")).toBe(true)
    expect(rows(el)[1].getAttribute("aria-current")).toBe("true")
  })
})

describe("the focus trap: a focused float only", () => {
  const stops = (el: WeftDevtools) =>
    (all(el, ".weft-dock button, .weft-dock a[href], .weft-dock select, .weft-dock input, .weft-dock textarea, .weft-dock [tabindex]") as HTMLElement[]).filter(
      (n) => n.tabIndex >= 0
    )

  it("Tab wraps from the last control to the first, Shift+Tab back; Esc lets go", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    expect($(el, ".weft-dock")?.classList.contains("weft-float")).toBe(true)
    const s = stops(el)
    const first = s[0]
    const last = s.at(-1)!
    last.focus()
    expect(key(last, { key: "Tab" }).defaultPrevented).toBe(true)
    expect(el.shadowRoot!.activeElement).toBe(first)
    expect(key(first, { key: "Tab", shiftKey: true }).defaultPrevented).toBe(true)
    expect(el.shadowRoot!.activeElement).toBe(last)
    // In the middle, Tab is the browser's.
    s[1].focus()
    expect(key(s[1], { key: "Tab" }).defaultPrevented).toBe(false)
    key($(el, ".weft-dock")!, { key: "Escape" })
    await settle()
    expect($(el, ".weft-dock")).toBeNull()
    const fab = $(el, ".weft-fab") as HTMLElement
    expect(key(fab, { key: "Tab" }).defaultPrevented).toBe(false)
  })

  it("docked: no trap", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE, "data-position": "right-dock" })
    const last = stops(el).at(-1)!
    last.focus()
    expect(key(last, { key: "Tab" }).defaultPrevented).toBe(false)
  })
})

describe("ARIA", () => {
  it("the dock is a labelled complementary region; every icon button has an aria-label", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    expect($(el, ".weft-dock")?.getAttribute("role")).toBe("complementary")
    expect($(el, ".weft-dock")?.getAttribute("aria-label")).toBe("weft devtools")
    click(all(el, "button").find((b) => b.textContent === "✎ Experiment"))
    await settle()
    const controls = all(el, "button, a")
    expect(controls.length).toBeGreaterThan(5)
    // An icon control is one whose text names nothing (no letter or digit).
    const icons = controls.filter((b) => !/[\p{L}\p{N}]/u.test(b.textContent))
    expect(icons.map((b) => b.textContent).sort()).toEqual(expect.arrayContaining(["–", "⇆", "⤢", "↺"]))
    expect(icons.filter((b) => !b.getAttribute("aria-label")).map((b) => b.outerHTML)).toEqual([])
  })

  it("aria-expanded follows the expanders: raw and how to scope (a child run's: subagents.test)", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    const raw = () => all(el, "button").find((b) => b.textContent === "raw")!
    expect(raw().getAttribute("aria-expanded")).toBe("false")
    click(raw())
    await settle()
    expect(raw().getAttribute("aria-expanded")).toBe("true")
    expect($(el, ".weft-raw")).not.toBeNull()
    // The fallback's how-to-scope expander.
    document.body.innerHTML = ""
    const dev = await mount({ "data-endpoint": "http://studio.test/studio/", "data-open": "true" })
    const how = all(dev, "button").find((b) => b.textContent === "how to scope")
    expect(how?.getAttribute("aria-expanded")).toBe("false")
    click(how)
    await settle()
    expect(all(dev, "button").find((b) => b.textContent === "how to scope")?.getAttribute("aria-expanded")).toBe("true")
  })
})

describe("patch, a reorder around focus", () => {
  it("the focused node is not moved (a move would blur it): the others step past it", () => {
    const box = h("div")
    document.body.appendChild(box)
    const row = (k: string) => h("button", { "data-key": k }, k)
    patch(box, [row("a"), row("b"), row("c")])
    const [a, b, c] = Array.from(box.children) as HTMLElement[]
    c.focus()
    patch(box, [row("c"), row("a"), row("b")])
    expect(Array.from(box.children)).toEqual([c, a, b])
    expect(document.activeElement).toBe(c)
    box.remove()
  })
})

// ── Review fixes (D3) ─────────────────────────────────────────────

type Inside = { render: (s: unknown) => void; model: { state: Record<string, unknown> } | null }
const inside = (el: WeftDevtools) => el as unknown as Inside

function viewport(w: number, hgt: number) {
  Object.defineProperty(window, "innerWidth", { value: w, configurable: true, writable: true })
  Object.defineProperty(window, "innerHeight", { value: hgt, configurable: true, writable: true })
  window.dispatchEvent(new Event("resize"))
}

function streamingTurn() {
  const routes = baseRoutes()
  const running = runRow({ id: "s_01-t1", status: "running", finished: null, steps: 2 })
  routes["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [running], next_before: null }
  routes["runs/s_01-t1"] = { ...running, children: [] }
  routes["runs/s_01-t1/events?after=0&limit=500"] = page(
    [...runEvents("s_01-t1").slice(0, 3), { type: "step_start", run_id: "s_01-t1", index: 1 }],
    { done: false }
  )
  routes["runs/s_01-t1/transcript"] = transcript([user("where is my order #4411?")], [{ role: "assistant", content: [{ type: "text", text: "Let me check." }] }])
  return routes
}

/** watch records the shadow root's mutations, split by whether they
 * fall inside the node in(). */
function watch(el: WeftDevtools, within: (n: Node) => boolean) {
  const yes: MutationRecord[] = []
  const no: MutationRecord[] = []
  const mo = new MutationObserver((ms) => {
    for (const m of ms) (within(m.target) ? yes : no).push(m)
  })
  mo.observe(el.shadowRoot!, { subtree: true, childList: true, attributes: true, characterData: true })
  return {
    yes,
    no,
    stop() {
      for (const m of mo.takeRecords()) (within(m.target) ? yes : no).push(m)
      mo.disconnect()
    },
  }
}
const said = (ms: MutationRecord[]) => ms.map((m) => `${m.type} ${m.target.nodeType === 1 ? (m.target as Element).className : m.target.nodeName}`)

describe("review fixes: what a draw touches", () => {
  it("tool_start and tool_finish on the running step change only its card", async () => {
    fakeStudio(streamingTurn())
    const el = await mount({ ...BASE, "data-position": "right-dock" })
    const tail = FakeEventSource.last("run=s_01-t1")!
    const card = $(el, '.weft-step[data-weft-step="1"]')!
    const w = watch(el, (n) => card.contains(n))
    tail.emit("record", { run_id: "s_01-t1", kind: "event", pos: 4, time: T0, event: { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: { id: "4411" } } })
    await settle(150)
    tail.emit("record", { run_id: "s_01-t1", kind: "event", pos: 5, time: T0, event: { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: "shipped", is_error: false } })
    await settle(150)
    w.stop()
    expect(card.querySelector(".weft-call")?.textContent).toContain("shipped")
    expect(w.yes.length).toBeGreaterThan(0)
    expect(said(w.no)).toEqual([])
  })

  it("another turn's status flip changes only that turn's row", async () => {
    const routes = baseRoutes()
    const t2 = runRow({ id: "s_01-t2", turn: 2 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
    routes["runs/s_01-t2"] = { ...t2, children: [] }
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2"))
    routes["runs/s_01-t2/spans"] = { spans: [] }
    routes["runs/s_01-t2/transcript"] = transcript([user("and the refund?")], [{ role: "assistant", content: [{ type: "text", text: "Issued." }] }])
    fakeStudio(routes)
    const el = await mount({ ...BASE, "data-position": "right-dock" })
    const item = $(el, '[role=listitem][data-key="s_01-t1"]')!
    // That turn is also an option of the narrow dropdown: its option,
    // and the dropdown or the list itself for a move.
    const opt = $(el, '.weft-turn-pick option[data-key="s_01-t1"]')
    const w = watch(el, (n) => item.contains(n) || !!opt?.contains(n) || n === $(el, ".weft-rows") || n === $(el, ".weft-turn-pick"))
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ status: "failed", err: "boom" }) })
    await settle()
    w.stop()
    expect(item.textContent).toContain("boom")
    expect(w.yes.length).toBeGreaterThan(0)
    expect(said(w.no)).toEqual([])
  })

  it("redraws add no listener to a node on screen: the handlers ride the kept nodes", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    const add = EventTarget.prototype.addEventListener
    const live: string[] = []
    vi.spyOn(EventTarget.prototype, "addEventListener").mockImplementation(function (this: EventTarget, ...a: Parameters<EventTarget["addEventListener"]>) {
      if ((this as Node).isConnected) live.push(`${(this as Element).className} ${a[0]}`)
      return add.apply(this, a)
    })
    for (let i = 2; i < 7; i++) {
      FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: i }) })
      await settle()
    }
    expect(all(el, ".weft-turn")[0].textContent).toContain("6 steps")
    expect(live).toEqual([])
  })
})

describe("review fixes: the patch's edges", () => {
  it("attributes the build no longer has are removed: boolean, data-*, style", () => {
    const box = h("div")
    const b = h("button", { disabled: "", "data-x": "1", style: "color: red", "aria-expanded": "true" }, "go")
    patch(box, [b])
    const live = box.firstElementChild as HTMLButtonElement
    patch(box, [h("button", { "aria-expanded": "false" }, "go")])
    expect(box.firstElementChild).toBe(live)
    expect(live.disabled).toBe(false)
    expect(live.hasAttribute("data-x")).toBe(false)
    expect(live.hasAttribute("style")).toBe(false)
    expect(live.getAttribute("aria-expanded")).toBe("false")
  })

  it("a <select> keeps its node and its value across an option reorder; the options move, not rewritten", () => {
    const box = h("div")
    const sel = (order: string[], v: string) => {
      const s = h("select", { "aria-label": "x" }, order.map((k) => h("option", { value: k, "data-key": k }, k))) as HTMLSelectElement
      s.value = v
      return s
    }
    patch(box, [sel(["a", "b", "c"], "b")])
    const live = box.firstElementChild as HTMLSelectElement
    const opts = Array.from(live.options)
    const seen: MutationRecord[] = []
    const mo = new MutationObserver((m) => seen.push(...m))
    mo.observe(box, { subtree: true, characterData: true, attributes: true, childList: true })
    patch(box, [sel(["c", "a", "b"], "b")])
    seen.push(...mo.takeRecords())
    mo.disconnect()
    expect(box.firstElementChild).toBe(live)
    expect(live.value).toBe("b")
    expect(Array.from(live.options)).toEqual([opts[2], opts[0], opts[1]])
    expect(seen.filter((m) => m.type !== "childList")).toEqual([])
  })

  it("a key repeated under one parent pairs in order: both nodes kept", () => {
    const box = h("div")
    patch(box, [h("p", { "data-key": "k" }, "one"), h("p", { "data-key": "k" }, "two")])
    const [a, b] = Array.from(box.children)
    patch(box, [h("p", { "data-key": "k" }, "one"), h("p", { "data-key": "k" }, "two!")])
    expect(Array.from(box.children)).toEqual([a, b])
    expect(b.textContent).toBe("two!")
  })

  it("two unkeyed-alike lines, keyed by call: focus in the second survives the first's removal", () => {
    const box = h("div")
    document.body.appendChild(box)
    // The build carries what was typed (the panel's scratch), as the panel does.
    const typed: Record<string, string> = {}
    const line = (id: string) => {
      const i = h("input", { class: "weft-resolve" }) as HTMLInputElement
      i.value = typed[id] ?? ""
      return h("div", { class: "weft-call", "data-key": id }, [i])
    }
    patch(box, [line("c1"), line("c2")])
    const second = box.querySelectorAll("input")[1]
    second.focus()
    second.value = typed.c2 = "abc"
    second.setSelectionRange(1, 1)
    patch(box, [line("c2")])
    expect(box.querySelector("input")).toBe(second)
    expect(document.activeElement).toBe(second)
    expect(second.selectionStart).toBe(1)
    box.remove()
  })
})

describe("review fixes: the live region and the step-end status", () => {
  it("the streaming text is aria-busy; the step's end is said once on a status line that stays", async () => {
    fakeStudio(streamingTurn())
    const el = await mount({ ...BASE, "data-position": "right-dock" })
    const tail = FakeEventSource.last("run=s_01-t1")!
    tail.emit("record", { run_id: "s_01-t1", kind: "delta", pos: 0, time: T0, event: { type: "text_delta", run_id: "s_01-t1", text: "Found it here" } })
    await settle()
    const stream = $(el, ".weft-stream")!
    expect(stream.getAttribute("aria-live")).toBe("polite")
    expect(stream.getAttribute("aria-busy")).toBe("true")
    const status = $(el, ".weft-sr[role=status]")!
    expect(status.textContent).toBe("")
    const writes = watch(el, (n) => status.contains(n))
    tail.emit("record", { run_id: "s_01-t1", kind: "event", pos: 4, time: T0, event: { type: "step_finish", run_id: "s_01-t1", index: 1, reason: "tool_calls", usage: { input_tokens: 1, output_tokens: 1 } } })
    tail.emit("record", { run_id: "s_01-t1", kind: "event", pos: 5, time: T0, event: { type: "step_start", run_id: "s_01-t1", index: 2 } })
    await settle(150)
    expect(status.textContent).toBe("step 1 finished · 3 words")
    // Busy moved with the stream: step 1's text is no longer busy.
    expect($(el, '.weft-step[data-weft-step="1"] [aria-busy]')).toBeNull()
    expect($(el, '.weft-step[data-weft-step="2"] .weft-stream')?.getAttribute("aria-busy")).toBe("true")
    for (let i = 0; i < 3; i++) {
      tail.emit("record", { run_id: "s_01-t1", kind: "delta", pos: 6 + i, time: T0, event: { type: "text_delta", run_id: "s_01-t1", text: ` w${i}` } })
      await settle(120)
    }
    writes.stop()
    expect(writes.yes).toHaveLength(1) // written once, then left alone
    expect($(el, ".weft-sr[role=status]")).toBe(status)
  })
})

describe("review fixes: the narrow dropdown and the trap's stops", () => {
  afterEach(() => viewport(1024, 768))

  it("a re-sort moves the turn dropdown's options, never rewrites them", async () => {
    viewport(400, 768)
    const routes = baseRoutes()
    const t2 = runRow({ id: "s_01-t2", turn: 2 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
    routes["runs/s_01-t2"] = { ...t2, children: [] }
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2"))
    routes["runs/s_01-t2/spans"] = { spans: [] }
    routes["runs/s_01-t2/transcript"] = transcript([user("q")], [])
    fakeStudio(routes)
    const el = await mount(BASE)
    const pick = $(el, ".weft-turn-pick") as HTMLSelectElement
    const before = Array.from(pick.options)
    const ids = before.map((o) => o.value)
    const w = watch(el, () => true)
    const s = inside(el).model!.state
    inside(el).render({ ...s, turns: [...(s.turns as unknown[])].reverse() })
    w.stop()
    expect($(el, ".weft-turn-pick")).toBe(pick)
    expect(Array.from(pick.options).map((o) => o.value)).toEqual([...ids].reverse())
    expect(Array.from(pick.options)).toEqual([...before].reverse())
    expect(w.yes.filter((m) => m.type === "characterData" && pick.contains(m.target))).toEqual([])
  })

  it("Tab wraps between the first and last stops Tab can reach: not the narrow float's hidden list, not a closed details' insides", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    const dock = $(el, ".weft-dock") as HTMLElement
    expect(dock.classList.contains("weft-float") && dock.classList.contains("weft-narrow")).toBe(true)
    // A closed <details> at the end: its summary is reachable, its button is not.
    const d = document.createElement("details")
    d.innerHTML = "<summary>more</summary><button type=button>inside</button>"
    dock.appendChild(d)
    const summary = d.querySelector("summary")!
    const visible = (all(el, ".weft-dock button, .weft-dock a[href], .weft-dock select, .weft-dock input, .weft-dock textarea, .weft-dock summary, .weft-dock [tabindex]") as HTMLElement[]).filter(
      (n) => n.tabIndex >= 0 && !(n as HTMLButtonElement).disabled && !n.closest(".weft-rows") && !(n.closest("details") && n.closest("details") !== d.parentElement && n !== summary && !(n.closest("details") as HTMLDetailsElement).open)
    )
    const first = visible[0]
    expect(first.closest(".weft-rows")).toBeNull()
    expect(visible.at(-1)).toBe(summary)
    summary.focus()
    expect(key(summary, { key: "Tab" }).defaultPrevented).toBe(true)
    expect(el.shadowRoot!.activeElement).toBe(first)
    expect(key(first, { key: "Tab", shiftKey: true }).defaultPrevented).toBe(true)
    expect(el.shadowRoot!.activeElement).toBe(summary)
    // The hidden list's tab stop is not a stop: Shift+Tab from it is the browser's.
    const row = $(el, ".weft-rows .weft-turn") as HTMLElement
    row.focus()
    expect(key(row, { key: "Tab", shiftKey: true }).defaultPrevented).toBe(false)
  })
})
