// U8.3 (UX-D-34, UX-F-18): the panel's Timeline draws Studio's run tree
// rows one level deep — on rw.deep-delegation's director run (the
// shared fixture, test/director-fixture.ts): depth indent, the kind's
// glyph and the tone's colour from the shared tables, the label rule,
// took and the offset, the critical path, the child run one row with ⤢
// to Studio's run page at that row (?sel=), untimed rows in lib's words.
import { describe, expect, it } from "vitest"

import { renderTimeline } from "./element"
import type { TurnView } from "./state"
import { DIR, FOLD, OPS, SPANS } from "../test/director-fixture"
import {
  KIND_ICON,
  UNTIMED_HOLE,
  UNTIMED_PENDING,
  waterfallRows,
} from "../lib/waterfall"
import { TONE } from "../lib/palette"
import { PANEL_CSS } from "./styles"

/** The run's own spans, as GET /spans serves them without ?tree=1. */
const OWN = SPANS.filter((s) => s.span_id.startsWith("d_"))

const turn = (over: Partial<TurnView> = {}) =>
  ({
    id: DIR,
    doc: { status: "succeeded" },
    spans: OWN,
    folded: FOLD,
    events: [{ pos: 8 }],
    ...over,
  }) as unknown as TurnView

const rows = (box: HTMLElement) =>
  Array.from(box.querySelectorAll<HTMLElement>(".weft-tl"))
const row = (box: HTMLElement, key: string) =>
  rows(box).find((n) => n.dataset.key === `tl:${key}`)!

describe("the Timeline on the director run (U8.3)", () => {
  it("draws the run tree's rows, one level deep: the child one row", () => {
    const box = renderTimeline(turn(), null, "http://studio.test/studio/")
    const keys = rows(box).map((n) => n.dataset.key!.slice(3))
    expect(keys).toEqual([
      `r:${DIR}`,
      "s0",
      "s0:m",
      "c:0:call_1",
      `r:${OPS}`,
      "t:d_http",
      "s1",
      "s1:m",
    ])
    // The same rows the model builds (Studio draws them all collapsed).
    expect(keys).toEqual(
      waterfallRows({ runId: DIR, spans: OWN, fold: FOLD }).rows.map(
        (r) => r.key
      )
    )
    // None of ops-manager's own rows: they are Studio's to draw.
    expect(keys.some((k) => k.includes(`${OPS}:`))).toBe(false)
  })

  it("labels by the label rule, kinds by their glyph, depth by indent", () => {
    const box = renderTimeline(turn())
    const label = (k: string) =>
      row(box, k).querySelector(".weft-tl-l")!.textContent
    expect(label(`r:${DIR}`)).toBe("director")
    expect(label("s0")).toBe("step 0")
    expect(label("s0:m")).toBe("model · script")
    expect(label("c:0:call_1")).toBe("ops_manager")
    expect(box.textContent).not.toContain("chat script")
    expect(box.textContent).not.toContain("invoke_agent")
    const kinds = rows(box).map((n) => n.dataset.kind)
    expect(kinds).toEqual([
      "run",
      "step",
      "model",
      "tool",
      "subagent",
      "span",
      "step",
      "model",
    ])
    expect(row(box, "s0:m").querySelector("i")!.textContent).toBe(
      KIND_ICON.model
    )
    expect(row(box, `r:${OPS}`).querySelector("i")!.textContent).toBe(
      KIND_ICON.subagent
    )
    const pad = (k: string) =>
      row(box, k).querySelector<HTMLElement>(".weft-wf-name")!.style.paddingLeft
    expect(pad(`r:${DIR}`)).toBe("0ch")
    expect(pad("s0")).toBe("2ch")
    expect(pad("c:0:call_1")).toBe("4ch")
    // The gist rides in the label cell's title, Studio's "label — sub".
    expect(
      row(box, "s0:m").querySelector(".weft-wf-name")!.getAttribute("title")
    ).toContain("model · script — ")
  })

  it("colours by tone through the shared table, marks the critical path", () => {
    const box = renderTimeline(turn())
    expect(row(box, "c:0:call_1").dataset.tone).toBe("tool")
    expect(row(box, "s0").dataset.tone).toBe("step")
    for (const [tone, t] of Object.entries(TONE))
      expect(PANEL_CSS).toContain(
        `.weft-tl[data-tone=${tone}] { --c: var(--weft-${t}); }`
      )
    const critical = rows(box)
      .filter((n) => n.hasAttribute("data-critical"))
      .map((n) => n.dataset.key!.slice(3))
    for (const k of [`r:${DIR}`, "s0", "s0:m", "c:0:call_1", `r:${OPS}`, "s1"])
      expect(critical).toContain(k)
  })

  it("took per row, the offset as its title, over a ms axis", () => {
    const box = renderTimeline(turn())
    expect(box.getAttribute("data-axis")).toBe("time")
    const ms = row(box, `r:${DIR}`).querySelector(".weft-wf-ms")!
    expect(ms.textContent).toBe("14.0ms")
    expect(ms.getAttribute("title")).toBe("+0µs")
    expect(
      row(box, "c:0:call_1").querySelector(".weft-wf-ms")!.getAttribute("title")
    ).toBe("+1.0ms")
    expect(box.querySelector(".weft-tick")!.textContent).toBe("0 ms")
  })

  it("a step's rows select it; the child row hands off with ?sel=", () => {
    const box = renderTimeline(turn(), 1, "http://studio.test/studio/")
    expect(row(box, "s0:m").dataset.weftStep).toBe("0")
    expect(row(box, "s1").dataset.weftStep).toBe("1")
    expect(row(box, "s1").classList.contains("weft-sel")).toBe(true)
    expect(row(box, "s0").classList.contains("weft-sel")).toBe(false)
    const a = row(box, `r:${OPS}`).querySelector("a")!
    expect(a.textContent).toBe("⤢")
    const u = new URL(a.getAttribute("href")!)
    expect(u.pathname).toBe(`/studio/runs/${encodeURIComponent(DIR)}`)
    expect(u.searchParams.get("sel")).toBe(`r:${OPS}`)
    // No endpoint, no hand-off.
    expect(
      row(renderTimeline(turn()), `r:${OPS}`).querySelector("a")
    ).toBeNull()
  })

  it("rows no span covers are drawn untimed, in lib's words", () => {
    // Step 1's spans dropped: the fold has the step, no span covers it.
    const spans = OWN.filter((s) => s.span_id !== "d_c1")
    const ended = renderTimeline(turn({ spans }))
    const s1 = row(ended, "s1:m")
    expect(s1.querySelector(`[data-hole="${UNTIMED_HOLE.hole}"]`)).toBeTruthy()
    expect(s1.querySelector(".weft-wf-ms")!.textContent).toBe("—")
    const live = renderTimeline(
      turn({ spans, doc: { status: "running" } as TurnView["doc"] })
    )
    expect(live.textContent).toContain(UNTIMED_PENDING)
  })
})
