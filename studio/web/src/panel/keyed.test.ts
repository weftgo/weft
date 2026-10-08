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
