// Plan D4: the turn view's tabs (Story · Request · Timeline · Raw),
// the Raw tab's JSON tree (lazy, a "/" filter, copy-node, copy-all,
// download), the Timeline's two axes, and the turn list's filter and
// paging (before=/before_id=, no cap).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { patch } from "./render"
import { FILTER_MS, KIDS_CAP, newTree, STR_CAP, treeView, utf8 } from "./tree"
import { STORE_KEY } from "./layout"
import {
  $,
  all,
  baseRoutes,
  click,
  fakeStudio,
  mount,
  page,
  pause,
  runEvents,
  runRow,
  settle,
  setup,
  T0,
  teardown,
  text,
  transcript,
  user,
  assistant,
} from "./testkit"
import type { Route } from "./testkit"
import type { WeftDevtools } from "./element"

let logs: ReturnType<typeof vi.spyOn>[] = []
beforeEach(() => {
  setup()
  localStorage.clear()
  logs = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m))
})
afterEach(() => {
  // No console output, ever (§5.3).
  for (const l of logs) expect(l).not.toHaveBeenCalled()
  vi.restoreAllMocks()
  teardown()
  localStorage.clear()
})

const BASE = { "data-endpoint": "http://studio.test/studio/", "data-public-id": "pub_orders", "data-open": "true" }
/** Docked at the bottom: wide enough for the turn column (not the dropdown). */
const WIDE = { ...BASE, "data-position": "bottom-dock" }
const dock = (el: WeftDevtools) => $(el, ".weft-dock") as HTMLElement
const active = (el: WeftDevtools) => el.shadowRoot!.activeElement as HTMLElement | null
const stored = () => JSON.parse(localStorage.getItem(STORE_KEY) ?? "null") as Record<string, unknown> | null

function key(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent("keydown", { bubbles: true, composed: true, cancelable: true, ...init })
  target.dispatchEvent(e)
  return e
}
const tab = (el: WeftDevtools, name: string) => $(el, `#weft-tab-${name}`) as HTMLElement
const type = async (input: HTMLInputElement, value: string) => {
  input.value = value
  input.dispatchEvent(new Event("input", { bubbles: true }))
  await settle(FILTER_MS + 20) // the raw filter's debounce
}

/** A fake clipboard; null takes it away. */
function clipboard(writeText: ((t: string) => Promise<void>) | null) {
  Object.defineProperty(navigator, "clipboard", { value: writeText ? { writeText } : undefined, configurable: true })
}

describe("the tabs", () => {
  it("a tablist of four ARIA tabs: one tab stop, arrows/Home/End move and select, the choice stored", async () => {
    fakeStudio(baseRoutes())
    let el = await mount(BASE)
    const list = $(el, "[role=tablist]")!
    const tabs = all(el, "[role=tab]")
    expect(tabs.map((t) => t.textContent)).toEqual(["Story", "Request", "Timeline", "Raw"])
    expect(tabs.map((t) => t.getAttribute("aria-selected"))).toEqual(["true", "false", "false", "false"])
    expect(tabs.map((t) => t.getAttribute("tabindex"))).toEqual(["0", "-1", "-1", "-1"])
    // Only the drawn panel is controlled; it is labelled by its tab.
    expect(tab(el, "story").getAttribute("aria-controls")).toBe("weft-tp-story")
    expect(tab(el, "raw").hasAttribute("aria-controls")).toBe(false)
    expect($(el, "#weft-tp-story")?.getAttribute("role")).toBe("tabpanel")
    expect($(el, "#weft-tp-story")?.getAttribute("aria-labelledby")).toBe("weft-tab-story")
    expect(list.getAttribute("aria-label")).toBe("turn views")
    tab(el, "story").focus()
    expect(key(tab(el, "story"), { key: "ArrowRight" }).defaultPrevented).toBe(true)
    await settle()
    expect(tab(el, "request").getAttribute("aria-selected")).toBe("true")
    expect(active(el)).toBe(tab(el, "request"))
    expect(stored()).toMatchObject({ v: 1, tab: "request", raw: false })
    key(tab(el, "request"), { key: "End" })
    await settle()
    expect(active(el)).toBe(tab(el, "raw"))
    expect(stored()).toMatchObject({ tab: "raw", raw: true })
    key(tab(el, "raw"), { key: "Home" })
    await settle()
    expect(tab(el, "story").getAttribute("aria-selected")).toBe("true")
    key(tab(el, "story"), { key: "ArrowLeft" }) // wraps
    await settle()
    expect(tab(el, "raw").getAttribute("aria-selected")).toBe("true")
    click(tab(el, "timeline"))
    await settle()
    // Remembered: a reload opens the Timeline tab.
    el.remove()
    await settle()
    el = await mount(BASE)
    expect(tab(el, "timeline").getAttribute("aria-selected")).toBe("true")
    expect($(el, ".weft-timeline")).not.toBeNull()
  })

  it("a v1 document from before D4 (raw, no tab) opens the Raw tab; an unknown tab reads as Story", async () => {
    localStorage.setItem(STORE_KEY, JSON.stringify({ v: 1, raw: true }))
    fakeStudio(baseRoutes())
    let el = await mount(BASE)
    expect(tab(el, "raw").getAttribute("aria-selected")).toBe("true")
    el.remove()
    localStorage.setItem(STORE_KEY, JSON.stringify({ v: 1, tab: "bogus" }))
    el = await mount(BASE)
    expect(tab(el, "story").getAttribute("aria-selected")).toBe("true")
  })

  it("Request is E1.2's pane (requesttab.test.ts); without the record it says why, never empty", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    click(tab(el, "request"))
    await settle()
    expect(text(el, "#weft-tp-request")).toContain("this Studio does not serve the request record")
  })

  it("r opens Raw and again goes back to the tab before it; the raw button is a toggle (aria-pressed)", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    const raw = () => all(el, ".weft-head button").find((b) => b.textContent === "raw")!
    expect(raw().getAttribute("aria-pressed")).toBe("false")
    dock(el).focus()
    key(dock(el), { key: "r" })
    await settle()
    expect(tab(el, "raw").getAttribute("aria-selected")).toBe("true")
    expect(raw().getAttribute("aria-pressed")).toBe("true")
    expect(raw().hasAttribute("aria-expanded")).toBe(false)
    key(dock(el), { key: "r" })
    await settle()
    expect(tab(el, "story").getAttribute("aria-selected")).toBe("true")
    // From the Timeline, r and r again land back on the Timeline.
    click(tab(el, "timeline"))
    await settle()
    dock(el).focus()
    key(dock(el), { key: "r" })
    await settle()
    expect(tab(el, "raw").getAttribute("aria-selected")).toBe("true")
    key(dock(el), { key: "r" })
    await settle()
    expect(tab(el, "timeline").getAttribute("aria-selected")).toBe("true")
  })

  it("⤢ carries the step being read from any tab (G1)", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    click(tab(el, "timeline"))
    await settle()
    dock(el).focus()
    key(dock(el), { key: "J" })
    await settle()
    const a = all(el, ".weft-head a").find((n) => n.textContent === "⤢")!
    expect(a.getAttribute("href")).toContain("step=0")
  })
})

describe("the timeline", () => {
  it("a run with spans: the waterfall over a time axis, ms from the run's start", async () => {
    const routes = baseRoutes()
    routes["runs/s_01-t1/spans"] = {
      spans: [
        { name: "invoke_agent", start: "2026-10-01T09:00:00.000Z", end: "2026-10-01T09:00:02.000Z" },
        { name: "chat", start: "2026-10-01T09:00:00.500Z", end: "2026-10-01T09:00:01.500Z" },
      ],
    }
    fakeStudio(routes)
    const el = await mount(BASE)
    // The story no longer carries the waterfall: it moved.
    expect($(el, ".weft-wf")).toBeNull()
    click(tab(el, "timeline"))
    await settle()
    const tl = $(el, ".weft-timeline")!
    expect(tl.getAttribute("data-axis")).toBe("time")
    expect(all(el, ".weft-tick").map((n) => n.textContent)).toEqual(["0 ms", "500 ms", "1000 ms", "1500 ms", "2000 ms"])
    expect(all(el, ".weft-wf .weft-wf-name").map((n) => n.textContent)).toEqual(["invoke_agent", "chat"])
    expect(all(el, ".weft-wf .weft-wf-ms").map((n) => n.textContent)).toEqual(["2000ms", "1000ms"])
  })

  it("a run without spans: its steps and tool calls over the event sequence (seq)", async () => {
    const routes = baseRoutes()
    routes["runs/s_01-t1/events?after=0&limit=500"] = page([
      ...runEvents("s_01-t1").slice(0, 2),
      { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: {} },
      { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: "ok", is_error: false },
      ...runEvents("s_01-t1").slice(2),
    ])
    fakeStudio(routes)
    const el = await mount(BASE)
    click(tab(el, "timeline"))
    await settle()
    expect($(el, ".weft-timeline")!.getAttribute("data-axis")).toBe("seq")
    expect(all(el, ".weft-tick").map((n) => n.textContent)).toEqual(["seq 0", "seq 1", "seq 3", "seq 4", "seq 5"])
    expect(all(el, ".weft-wf .weft-wf-name").map((n) => n.textContent)).toEqual(["lookup_order", "step 0"])
    expect(all(el, ".weft-wf .weft-wf-ms").map((n) => n.textContent)).toEqual(["#2–3", "#1–4"])
  })

  it("review: the axis is the waterfall's own window, and a span it cannot place is said", async () => {
    const routes = baseRoutes()
    routes["runs/s_01-t1/spans"] = {
      spans: [
        { name: "chat", start: "2026-10-01T09:00:00.000Z", end: "2026-10-01T09:00:01.000Z" },
        // Ends before it starts: waterfall drops it, so the axis must too.
        { name: "bent", start: "2026-10-01T09:00:05.000Z", end: "2026-10-01T09:00:04.000Z" },
        { name: "garbled", start: "yesterday", end: "today" },
      ],
    }
    fakeStudio(routes)
    const el = await mount(BASE)
    click(tab(el, "timeline"))
    await settle()
    expect(all(el, ".weft-tick").map((n) => n.textContent)).toEqual(["0 ms", "250 ms", "500 ms", "750 ms", "1000 ms"])
    expect(all(el, ".weft-wf .weft-wf-name").map((n) => n.textContent)).toEqual(["chat"])
    expect(text(el, ".weft-timeline .weft-warn")).toBe("2 spans not placed: unreadable times, or an end before the start")
  })
})

describe("the tabs keep the story (review)", () => {
  it("the Story panel is the same node across a tab switch (hidden, not rebuilt): an opened <details> stays open", async () => {
    const routes = baseRoutes()
    routes["runs/s_01-t1/events?after=0&limit=500"] = page([
      ...runEvents("s_01-t1").slice(0, 2),
      { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: {} },
      { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: "ok", is_error: false },
      ...runEvents("s_01-t1").slice(2),
    ])
    fakeStudio(routes)
    const el = await mount(BASE)
    const story = $(el, "#weft-tp-story")!
    click(tab(el, "timeline"))
    await settle()
    expect($(el, "#weft-tp-story")).toBe(story)
    expect(story.hasAttribute("hidden")).toBe(true)
    expect($(el, "#weft-tp-timeline")).not.toBeNull()
    click(tab(el, "story"))
    await settle()
    expect($(el, "#weft-tp-story")).toBe(story)
    expect(story.hasAttribute("hidden")).toBe(false)
    expect($(el, "#weft-tp-timeline")).toBeNull()
  })

  it("⤢ from the Raw tab carries the step being read", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    click(tab(el, "raw"))
    await settle()
    dock(el).focus()
    key(dock(el), { key: "J" })
    await settle()
    expect(all(el, ".weft-head a").find((n) => n.textContent === "⤢")!.getAttribute("href")).toContain("step=0")
  })
})

describe("the raw tree", () => {
  function rawRoutes() {
    const routes = baseRoutes()
    routes["runs/s_01-t1"] = { ...runRow({ meta: { tenant: { name: "acme", tier: "gold" } } as unknown as Record<string, string> }), children: [] }
    return routes
  }
  async function openRaw() {
    fakeStudio(rawRoutes())
    const el = await mount(BASE)
    click(tab(el, "raw"))
    await settle()
    return el
  }

  it("the document is {doc, events, transcript, spans} — spans included, no requests the panel does not hold", async () => {
    const el = await openRaw()
    const top = all(el, ".weft-tree > .weft-tn")
      .filter((n) => (n as HTMLElement).style.paddingLeft === "0px")
      .map((n) => n.querySelector(".weft-tk")?.textContent)
    expect(top).toEqual(["doc: ", "events: ", "transcript: ", "spans: "])
  })

  it("/ finds a key: the path opens, the row is marked, \"1 match\"; copy-node puts that node's JSON on the clipboard", async () => {
    const writeText = vi.fn<(t: string) => Promise<void>>(async () => {})
    clipboard(writeText)
    const el = await openRaw()
    // Collapse doc first: the filter must open the path itself.
    click(all(el, ".weft-tt").find((b) => b.getAttribute("aria-label") === "collapse doc"))
    await settle()
    expect(text(el, ".weft-tree")).not.toContain("tenant")
    dock(el).focus()
    expect(key(dock(el), { key: "/" }).defaultPrevented).toBe(true)
    const q = active(el) as HTMLInputElement
    expect(q.classList.contains("weft-tree-q")).toBe(true)
    await type(q, "TENANT")
    expect(text(el, ".weft-tree-n")).toBe("1 match")
    const hits = all(el, ".weft-tn.weft-hit")
    expect(hits).toHaveLength(1)
    expect(hits[0].querySelector(".weft-tk")?.textContent).toBe("tenant: ")
    // The path to it is open: doc and meta say so.
    for (const k of ["doc", "meta"])
      expect(all(el, ".weft-tt").find((b) => b.getAttribute("aria-label")?.endsWith(` ${k}`))?.getAttribute("aria-expanded")).toBe("true")
    expect(active(el)).toBe(q) // the caret stays in the box
    click(hits[0].querySelector(".weft-tc"))
    await settle()
    expect(writeText).toHaveBeenCalledWith(JSON.stringify({ name: "acme", tier: "gold" }, null, 2))
    expect(text(el, ".weft-tree-said")).toBe("copied")
    // A value matches too, case-insensitively.
    await type(q, "GOLD")
    expect(text(el, ".weft-tree-n")).toBe("1 match")
    expect($(el, ".weft-tn.weft-hit .weft-tv")?.textContent).toBe('"gold"')
  })

  it("copy all puts the whole document on the clipboard; refused, it is shown selected to copy by hand", async () => {
    const writeText = vi.fn<(t: string) => Promise<void>>(async () => {})
    clipboard(writeText)
    const el = await openRaw()
    click(all(el, ".weft-tbar button").find((b) => b.textContent === "copy all"))
    await settle()
    const doc = JSON.parse(String(writeText.mock.calls[0]?.[0])) as Record<string, unknown>
    expect(Object.keys(doc)).toEqual(["doc", "events", "transcript", "spans"])
    clipboard(() => Promise.reject(new Error("denied")))
    click(all(el, ".weft-tbar button").find((b) => b.textContent === "copy all"))
    await settle()
    expect(text(el, ".weft-tree-said")).toContain("refused")
    expect(($(el, ".weft-copybox") as HTMLTextAreaElement).value).toContain('"transcript"')
    clipboard(null) // no clipboard at all: the same fallback, nothing thrown
    click(all(el, ".weft-tc")[0])
    await settle()
    expect(($(el, ".weft-copybox") as HTMLTextAreaElement).value).toContain('"id"')
  })

  it("download is an <a download> named <run id>.json whose href is the document", async () => {
    const el = await openRaw()
    const a = $(el, ".weft-dl") as HTMLAnchorElement
    expect(a.getAttribute("download")).toBe("s_01-t1.json")
    const stop = (e: Event) => e.preventDefault()
    document.addEventListener("click", stop)
    a.dispatchEvent(new MouseEvent("click", { bubbles: true, composed: true, cancelable: true }))
    document.removeEventListener("click", stop)
    const href = a.getAttribute("href") ?? ""
    // jsdom has no createObjectURL: the data: form (a browser gets a blob: URL).
    expect(href.startsWith("data:application/json;charset=utf-8,")).toBe(true)
    const doc = JSON.parse(decodeURIComponent(href.slice(href.indexOf(",") + 1))) as Record<string, unknown>
    expect(Object.keys(doc)).toEqual(["doc", "events", "transcript", "spans"])
  })

  /** draw renders a tree into a box the way the panel does (patched). */
  function draw(doc: unknown) {
    const box = document.createElement("div")
    const st = newTree()
    const redraw = () => patch(box, [treeView(doc, st, "x", { redraw, root: () => box })])
    redraw()
    return { box, st, redraw }
  }

  it("lazy: a 100,000-node document draws only the open rows; opening one draws KIDS_CAP and says the rest", () => {
    const doc = { big: Array.from({ length: 50_000 }, (_, i) => ({ i })) }
    const { box } = draw(doc)
    expect(box.querySelectorAll(".weft-tn")).toHaveLength(1)
    expect(box.querySelectorAll("*").length).toBeLessThan(30)
    const open = box.querySelector(".weft-tt") as HTMLElement
    expect(open.getAttribute("aria-expanded")).toBe("false")
    click(open)
    expect(box.querySelectorAll(".weft-tn")).toHaveLength(1 + KIDS_CAP)
    const more = Array.from(box.querySelectorAll(".weft-tmore")).at(-1)!
    expect(more.textContent).toBe(`… +${50_000 - KIDS_CAP} more`)
    click(more)
    expect(box.querySelectorAll(".weft-tn")).toHaveLength(1 + 2 * KIDS_CAP)
    expect(box.querySelectorAll("*").length).toBeLessThan(3_000)
  })

  it("keyboard: ArrowRight opens a node, ArrowLeft closes it (Enter/Space are the button's)", () => {
    const { box } = draw({ a: { b: 1 }, big: Array.from({ length: 400 }, (_, i) => i) })
    const t = Array.from(box.querySelectorAll<HTMLElement>(".weft-tt")).find((b) => b.getAttribute("aria-label") === "expand big")!
    key(t, { key: "ArrowRight" })
    expect(t.getAttribute("aria-expanded")).toBe("true")
    key(t, { key: "ArrowLeft" })
    expect(t.getAttribute("aria-expanded")).toBe("false")
  })

  it("a match past the cap is drawn anyway; every match is counted", () => {
    const doc = { big: Array.from({ length: 50_000 }, (_, i) => ({ i })) }
    const { box, st, redraw } = draw(doc)
    st.q = st.applied = "49999"
    redraw()
    // The key "49999" and the value 49999.
    expect(box.querySelector(".weft-tree-n")?.textContent).toBe("2 matches")
    expect(box.querySelectorAll(".weft-tn.weft-hit")).toHaveLength(2)
  })

  it("a string over 2 KiB shows its head and \"+N bytes\" (UTF-8), the rest on a click", () => {
    const { box } = draw({ s: "a".repeat(STR_CAP) + "é".repeat(1000), t: "short" })
    const v = box.querySelector(".weft-tn .weft-tv")!
    expect(v.textContent).toBe(`"${"a".repeat(STR_CAP)}…`)
    const more = box.querySelector(".weft-tmore") as HTMLElement
    expect(more.textContent).toBe("… +2000 bytes")
    click(more)
    expect(box.querySelector(".weft-tn .weft-tv")!.textContent).toBe(JSON.stringify("a".repeat(STR_CAP) + "é".repeat(1000)))
    expect(box.querySelector(".weft-tmore")).toBeNull()
  })

  it("review: past OPEN_HITS matches the count says only the first 200 are opened", () => {
    const { box, st, redraw } = draw({ a: Array.from({ length: 300 }, () => "x") })
    st.q = st.applied = "x"
    redraw()
    expect(box.querySelector(".weft-tree-n")?.textContent).toBe("300 matches · first 200 opened")
    expect(box.querySelectorAll(".weft-tn.weft-hit")).toHaveLength(200)
  })

  it("review: the cap is 2,048 characters and never splits a surrogate pair; bytes counted without allocating", () => {
    const s = "a".repeat(STR_CAP - 1) + "😀" + "b".repeat(10)
    const { box } = draw({ s, cjk: "中".repeat(3000) })
    const [v1, v2] = Array.from(box.querySelectorAll(".weft-tn .weft-tv"), (n) => n.textContent)
    expect(v1).toBe(`"${"a".repeat(STR_CAP - 1)}…`)
    expect(/[\ud800-\udbff](?![\udc00-\udfff])/.test(v1)).toBe(false)
    expect(v2).toBe(`"${"中".repeat(STR_CAP)}…`)
    expect(Array.from(box.querySelectorAll(".weft-tmore"), (n) => n.textContent)).toEqual(["… +14 bytes", `… +${(3000 - STR_CAP) * 3} bytes`])
    const mixed = "aé中😀\u{1F600}x"
    expect(utf8(mixed)).toBe(new TextEncoder().encode(mixed).length)
  })

  it("review: a redraw re-walks nothing — the byte count, the search and the default opening are kept per document", () => {
    const { box, st, redraw } = draw({ s: "z".repeat(STR_CAP + 5), o: { k: 1 } })
    st.q = st.applied = "k"
    redraw()
    const memo = st.memo
    const auto = st.auto
    st.bytes.get("/s")!.n = 999 // were it counted again, this would be overwritten
    redraw()
    expect(st.memo).toBe(memo)
    expect(st.auto).toBe(auto)
    expect(box.querySelector(".weft-tmore")?.textContent).toBe("… +999 bytes")
  })

  it("review: the filter is debounced: three keystrokes, one walk, FILTER_MS after the last", () => {
    vi.useFakeTimers()
    const { box, st } = draw({ alpha: 1, beta: 2 })
    const q = box.querySelector(".weft-tree-q") as HTMLInputElement
    for (const v of ["a", "al", "alp"]) {
      q.value = v
      q.dispatchEvent(new Event("input", { bubbles: true }))
      vi.advanceTimersByTime(FILTER_MS / 2)
    }
    expect(st.memo).toBeUndefined()
    expect(box.querySelector(".weft-tree-n")?.textContent).toBe("")
    vi.advanceTimersByTime(FILTER_MS)
    expect(st.memo?.q).toBe("alp")
    expect(box.querySelector(".weft-tree-n")?.textContent).toBe("1 match")
  })

  it("review: a redraw that changes no record re-walks nothing in the open Raw tab", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    click(tab(el, "raw"))
    await settle()
    await type($(el, ".weft-tree-q") as HTMLInputElement, "run_start")
    const tree = (el as unknown as { tree: { memo?: unknown; auto?: unknown } }).tree
    const memo = tree.memo
    const auto = tree.auto
    // Two redraws that change no record (the ? list opened and closed).
    dock(el).focus()
    key(dock(el), { key: "?" })
    await settle()
    key(dock(el), { key: "?" })
    await settle()
    expect(tree.memo).toBe(memo)
    expect(tree.auto).toBe(auto)
  })

  it("review: \"copied\" goes after 2 s or with the next action; the hand-copy box goes on blur or Esc", async () => {
    vi.useFakeTimers()
    const writeText = vi.fn<(t: string) => Promise<void>>(async () => {})
    clipboard(writeText)
    const { box } = draw({ a: { b: 1 } })
    document.body.appendChild(box)
    click(box.querySelector(".weft-tc"))
    await vi.advanceTimersByTimeAsync(0)
    expect(box.querySelector(".weft-tree-said")?.textContent).toBe("copied")
    await vi.advanceTimersByTimeAsync(2_000)
    expect(box.querySelector(".weft-tree-said")).toBeNull()
    click(box.querySelector(".weft-tc"))
    await vi.advanceTimersByTimeAsync(0)
    click(box.querySelector(".weft-tt")) // the next action
    expect(box.querySelector(".weft-tree-said")).toBeNull()
    clipboard(null)
    click(box.querySelector(".weft-tc"))
    await vi.advanceTimersByTimeAsync(0)
    const ta = box.querySelector(".weft-copybox") as HTMLTextAreaElement
    expect(document.activeElement).toBe(ta)
    ta.dispatchEvent(new Event("blur"))
    expect(box.querySelector(".weft-copybox")).toBeNull()
    click(box.querySelector(".weft-tc"))
    await vi.advanceTimersByTimeAsync(0)
    key(box.querySelector(".weft-copybox")!, { key: "Escape" })
    expect(box.querySelector(".weft-copybox")).toBeNull()
  })

  it("review: copy-node on a container past the 200-child cap copies the whole node", () => {
    const writeText = vi.fn<(t: string) => Promise<void>>(async () => {})
    clipboard(writeText)
    const big = Array.from({ length: 500 }, (_, i) => i)
    const { box } = draw({ big })
    click(Array.from(box.querySelectorAll(".weft-tc")).find((b) => b.getAttribute("aria-label") === "copy big"))
    expect(JSON.parse(String(writeText.mock.calls[0]?.[0]))).toEqual(big)
  })
})

describe("the turn list's filter", () => {
  function three() {
    const routes = baseRoutes()
    const t1 = runRow({})
    const t2 = runRow({ id: "s_01-t2", turn: 2, status: "failed", err: "model: boom", started: "2026-10-01T09:01:00Z" })
    const t3 = runRow({ id: "s_01-t3", turn: 3, status: "succeeded", pending: 1, started: "2026-10-01T09:02:00Z" })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 3, runs: [t3, t2, t1], next_before: null }
    for (const r of [t2, t3]) {
      routes[`runs/${r.id}`] = { ...r, children: [] }
      routes[`runs/${r.id}/events?after=0&limit=500`] = page(runEvents(r.id))
      routes[`runs/${r.id}/transcript`] = transcript([user(`question ${r.turn}`)], [assistant("answer")])
      routes[`runs/${r.id}/spans`] = { spans: [] }
    }
    return routes
  }
  const ids = (el: WeftDevtools) => all(el, ".weft-turn .weft-id").map((n) => n.textContent)

  it("text (id, error, an opened turn's prompt), status and has error narrow the loaded rows; the count says so", async () => {
    fakeStudio(three())
    const el = await mount(BASE)
    expect(ids(el)).toEqual(["s_01-t3", "s_01-t2", "s_01-t1"])
    expect($(el, ".weft-tq-n")).toBeNull()
    const q = $(el, ".weft-turn-q") as HTMLInputElement
    await type(q, "BOOM")
    expect(ids(el)).toEqual(["s_01-t2"])
    expect(text(el, ".weft-tq-n")).toBe("1 of 3 turns")
    await type(q, "t1")
    expect(ids(el)).toEqual(["s_01-t1"])
    // The opened turn's prompt (s_01-t3 was selected first).
    await type(q, "question 3")
    expect(ids(el)).toEqual(["s_01-t3"])
    await type(q, "")
    const status = $(el, ".weft-tq-status") as HTMLSelectElement
    status.value = "parked"
    status.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(ids(el)).toEqual(["s_01-t3"])
    status.value = ""
    status.dispatchEvent(new Event("change", { bubbles: true }))
    const err = $(el, ".weft-tq-err") as HTMLInputElement
    err.checked = true
    err.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(ids(el)).toEqual(["s_01-t2"])
    expect(text(el, ".weft-tq-n")).toBe("1 of 3 turns")
    // j/k walk the filtered rows; nothing about the filter is stored.
    expect(JSON.stringify(stored() ?? {})).not.toMatch(/boom|status|err/)
  })

  it("review: the narrow dropdown keeps the selected turn listed and runs experiments through the filter too", async () => {
    const routes = three()
    const x1 = runRow({ id: "x1", playground: true, session_id: "", forked_from: "s_01-t1#0", started: "2026-10-01T09:03:00Z" })
    const page0 = routes["runs?public_id=pub_orders&limit=50"] as { runs: unknown[] }
    page0.runs.push(x1)
    fakeStudio(routes)
    const el = await mount(BASE) // 520 px: the dropdown
    const pick = () => $(el, ".weft-turn-pick") as HTMLSelectElement
    expect(pick().value).toBe("s_01-t3")
    expect(Array.from(pick().options, (o) => o.value)).toContain("x1")
    await type($(el, ".weft-turn-q") as HTMLInputElement, "boom")
    expect(Array.from(pick().options, (o) => o.value)).toEqual(["s_01-t3", "s_01-t2"])
    expect(pick().value).toBe("s_01-t3") // still what the column shows
    expect(Array.from(pick().options, (o) => o.value)).not.toContain("x1")
  })

  it("review: / with no filter box to focus (an empty list) is left to the page", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = { total: 0, runs: [], next_before: null }
    fakeStudio(routes)
    const el = await mount(BASE)
    dock(el).focus()
    expect($(el, ".weft-turn-q")).toBeNull()
    expect(key(dock(el), { key: "/" }).defaultPrevented).toBe(false)
  })
})

describe("paging (before= / before_id=, no cap)", () => {
  const N = 200
  const at = (n: number) => new Date(Date.parse(T0) + n * 60_000).toISOString()
  const turn = (n: number) => runRow({ id: `t${n}`, turn: n, session_id: "s_01", started: at(n), last_seen: at(n), finished: at(n) })

  /** A 200-turn conversation, paged 50 at a time newest first, as
   * studio/api.go's runs route pages it (next_before, next_before_id). */
  function long(): Record<string, Route> {
    const routes = baseRoutes()
    const runs = Array.from({ length: N }, (_, i) => turn(N - i))
    for (let p = 0; p * 50 < N; p++) {
      const rows = runs.slice(p * 50, p * 50 + 50)
      const last = rows.at(-1)!
      const more = p * 50 + 50 < N
      const q: Record<string, string> = { public_id: "pub_orders", limit: "50" }
      if (p > 0) Object.assign(q, { before: runs[p * 50 - 1].started, before_id: runs[p * 50 - 1].id })
      routes[`runs?${new URLSearchParams(q)}`] = {
        total: N,
        runs: rows,
        next_before: more ? last.started : null,
        next_before_id: more ? last.id : null,
      }
    }
    for (const r of runs) {
      routes[`runs/${r.id}`] = { ...r, children: [] }
      routes[`runs/${r.id}/events?after=0&limit=500`] = page(runEvents(r.id))
      routes[`runs/${r.id}/transcript`] = transcript([user(`question ${r.turn}`)], [assistant("answer")])
      routes[`runs/${r.id}/spans`] = { spans: [] }
    }
    return routes
  }

  /** An IntersectionObserver the test fires (jsdom has none). */
  class FakeIO {
    static all: FakeIO[] = []
    targets: Element[] = []
    off = false
    constructor(
      public cb: IntersectionObserverCallback,
      public opts: IntersectionObserverInit = {}
    ) {
      FakeIO.all.push(this)
    }
    observe(n: Element) {
      this.targets.push(n)
    }
    unobserve() {}
    disconnect() {
      this.off = true
    }
    takeRecords() {
      return []
    }
    /** The sentinel scrolled into view. */
    fire() {
      this.cb(this.targets.map((target) => ({ isIntersecting: true, target }) as IntersectionObserverEntry), this as unknown as IntersectionObserver)
    }
  }

  it("a 200-turn session scrolls to its first turn: each sentinel loads the next older page, then \"all 200 turns loaded\"", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    const studio = fakeStudio(long())
    const el = await mount(WIDE)
    expect(all(el, ".weft-turn")).toHaveLength(50)
    expect(text(el, ".weft-head")).toContain("50+ turns")
    expect(text(el, ".weft-turns")).not.toContain("Studio")
    for (let i = 0; i < 10 && $(el, ".weft-older"); i++) {
      const io = FakeIO.all.at(-1)!
      // Watched inside the panel's own scrolling list, passively.
      expect(io.opts.root).toBe($(el, ".weft-turns"))
      expect(io.targets).toEqual([$(el, ".weft-older")])
      io.fire()
      await settle()
    }
    const rows = all(el, ".weft-turn .weft-id").map((n) => n.textContent)
    expect(rows).toHaveLength(N)
    expect(rows[0]).toBe("t200")
    expect(rows.at(-1)).toBe("t1") // the first turn
    expect(new Set(rows).size).toBe(N)
    expect(text(el, ".weft-all")).toBe("all 200 turns loaded")
    expect($(el, ".weft-older")).toBeNull()
    expect(text(el, ".weft-head")).toContain("200 turns")
    // The cursor the API offers: before= with before_id=, 3 older pages.
    const older = studio.gets("runs?public_id=pub_orders&limit=50&before=")
    expect(older).toHaveLength(3)
    expect(older[0].path).toContain(`before_id=t151`)
    // One observer at a time; the old ones let go; disconnected with the element.
    expect(FakeIO.all.slice(0, -1).every((io) => io.off)).toBe(true)
    el.remove()
    expect(FakeIO.all.at(-1)!.off).toBe(true)
  })

  it("j/k and the roving tabindex cross the loaded pages; a live refresh keeps the older pages", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    const routes = long()
    fakeStudio(routes)
    const el = await mount(WIDE)
    FakeIO.all.at(-1)!.fire()
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(100)
    click(all(el, ".weft-turn").find((n) => n.textContent.includes("t151")))
    await settle()
    dock(el).focus()
    key(dock(el), { key: "j" })
    await settle()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("t150") // page two
    const row = $(el, ".weft-turn.weft-sel") as HTMLElement
    row.focus()
    key(row, { key: "ArrowDown" })
    expect(active(el)?.querySelector(".weft-id")?.textContent).toBe("t149")
    // A refresh (a reconnect, a stale read) reads the first page again:
    // what was paged in stays, and so does the older cursor.
    await (el as unknown as { model: { refresh: () => Promise<void> } }).model.refresh()
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(100)
    expect($(el, ".weft-older")).not.toBeNull()
  })

  it("no observer (an old browser), or a narrow panel (the turn dropdown): the sentinel is a button that loads the next page", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    fakeStudio(long())
    const el = await mount(BASE) // the 520 px float: narrow
    expect(FakeIO.all).toHaveLength(0)
    expect(($(el, ".weft-turn-pick") as HTMLSelectElement).options).toHaveLength(50)
    click($(el, ".weft-older"))
    await settle()
    expect(($(el, ".weft-turn-pick") as HTMLSelectElement).options).toHaveLength(100)
    el.remove()
    await settle()
    vi.stubGlobal("IntersectionObserver", undefined)
    const wide = await mount(WIDE)
    expect(text(wide, ".weft-older")).toBe("older turns ↓")
    click($(wide, ".weft-older"))
    await settle()
    expect(all(wide, ".weft-turn")).toHaveLength(100)
  })

  /** A long() variant: page two is rows already listed (it adds no
   * turn) with a cursor to page three. */
  it("review: a page that adds no turn still re-arms the observer (the sentinel is keyed by the cursor)", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    const routes = long()
    const k2 = `runs?${new URLSearchParams({ public_id: "pub_orders", limit: "50", before: at(151), before_id: "t151" })}`
    const real2 = routes[k2] as { runs: unknown[]; next_before: string; next_before_id: string }
    routes[k2] = { total: N, runs: (routes["runs?public_id=pub_orders&limit=50"] as { runs: unknown[] }).runs, next_before: real2.next_before, next_before_id: real2.next_before_id }
    fakeStudio(routes)
    const el = await mount(WIDE)
    FakeIO.all.at(-1)!.fire()
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(50) // nothing new…
    const io = FakeIO.all.at(-1)!
    expect(io.off).toBe(false) // …but a new observer on the new sentinel
    expect(FakeIO.all).toHaveLength(2)
    io.fire()
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(100)
  })

  it("review: a refresh landing while an older page is read does not discard that page (cursors compared by value)", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    const routes = long()
    const k2 = `runs?${new URLSearchParams({ public_id: "pub_orders", limit: "50", before: at(151), before_id: "t151" })}`
    const body = routes[k2]
    let release = () => {}
    routes[k2] = () => new Promise((r) => (release = () => r(body)))
    fakeStudio(routes)
    const el = await mount(WIDE)
    FakeIO.all.at(-1)!.fire()
    await pause(60)
    expect(text(el, ".weft-older")).toBe("loading older turns…")
    await (el as unknown as { model: { refresh: () => Promise<void> } }).model.refresh()
    release()
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(100)
  })

  it("review: with the turn filter on, the list does not auto-load; the count says what it covers", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    fakeStudio(long())
    const el = await mount(WIDE)
    const io = FakeIO.all.at(-1)!
    await type($(el, ".weft-turn-q") as HTMLInputElement, "t15")
    expect(io.off).toBe(true)
    expect(FakeIO.all).toHaveLength(1)
    expect(text(el, ".weft-tq-n")).toBe("9 of 50 loaded · the filter applies to the loaded turns")
    expect(text(el, ".weft-older")).toBe("older turns ↓")
    click($(el, ".weft-older")) // the button still pages
    await settle()
    expect(text(el, ".weft-tq-n")).toBe("10 of 100 loaded · the filter applies to the loaded turns")
    await type($(el, ".weft-turn-q") as HTMLInputElement, "")
    expect(FakeIO.all.at(-1)!.off).toBe(false) // watched again
  })

  it("review: a refresh with new runs at the top keeps the pages read and adds the new rows", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    const routes = long()
    fakeStudio(routes)
    const el = await mount(WIDE)
    FakeIO.all.at(-1)!.fire()
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(100)
    const fresh = turn(201)
    const first = routes["runs?public_id=pub_orders&limit=50"] as { runs: ReturnType<typeof turn>[] }
    const rows = [fresh, ...first.runs.slice(0, 49)]
    routes["runs?public_id=pub_orders&limit=50"] = { total: N + 1, runs: rows, next_before: rows[49].started, next_before_id: rows[49].id }
    routes["runs/t201"] = { ...fresh, children: [] }
    await (el as unknown as { model: { refresh: () => Promise<void> } }).model.refresh()
    await settle()
    const ids = all(el, ".weft-turn .weft-id").map((n) => n.textContent)
    expect(ids).toHaveLength(101)
    expect(ids[0]).toBe("t201")
    expect(ids.at(-1)).toBe("t101")
    expect(new Set(ids).size).toBe(101)
    expect($(el, ".weft-older")).not.toBeNull()
  })

  it("review: rows tied on started across a page boundary are both listed once (before_id is the tie-breaker)", async () => {
    FakeIO.all = []
    vi.stubGlobal("IntersectionObserver", FakeIO)
    const routes = long()
    // t150 starts when t151 does: page two begins inside the tie.
    const k2 = `runs?${new URLSearchParams({ public_id: "pub_orders", limit: "50", before: at(151), before_id: "t151" })}`
    const p2 = routes[k2] as { runs: ReturnType<typeof turn>[] }
    p2.runs[0] = { ...p2.runs[0], started: at(151) }
    const studio = fakeStudio(routes)
    const el = await mount(WIDE)
    FakeIO.all.at(-1)!.fire()
    await settle()
    expect(studio.gets("runs?public_id=pub_orders&limit=50&before=").at(-1)?.path).toContain("before_id=t151")
    await (el as unknown as { model: { refresh: () => Promise<void> } }).model.refresh()
    await settle()
    const ids = all(el, ".weft-turn .weft-id").map((n) => n.textContent)
    expect(ids).toHaveLength(100)
    expect(ids).toEqual(expect.arrayContaining(["t151", "t150"]))
    expect(new Set(ids).size).toBe(100)
  })
})
