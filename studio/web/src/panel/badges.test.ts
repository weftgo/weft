// The panel's badges (D5): one renderer over the A3 table — the span,
// data-hole, the table's words (a response's first) as its title; the
// footer's content line; the turn chips read from the run row; and no
// panel file but badges.ts spelling a hole's words.
import { readdirSync, readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { Meta } from "../lib/api"
import { CAUSES, HOLE_ORDER, HOLES } from "../lib/honesty"
import { badge, capLine, cutBadge, holeBadges, holeLine, requestCapped, requestHole, turnChips } from "./badges"
import { $, all, baseRoutes, fakeStudio, META, mount, page, runEvents, runRow, setup, teardown, text } from "./testkit"

beforeEach(setup)
afterEach(teardown)

describe("badge (the one renderer)", () => {
  it("draws every hole of the table: the span, data-hole, the label, the reason and fix as its title", () => {
    for (const h of HOLE_ORDER) {
      const b = badge(h)
      expect(b.tagName).toBe("SPAN")
      expect(b.classList.contains("weft-badge")).toBe(true)
      expect(b.getAttribute("data-hole")).toBe(h)
      expect(b.textContent).toBe(HOLES[h].label)
      const n = HOLES[h]
      expect(b.getAttribute("title")).toBe(n.fix ? `${n.reason} — fix: ${n.fix}` : n.reason)
      expect(b.className).toContain(n.tone === "loss" ? "weft-warn-badge" : "weft-info")
    }
  })

  it("a response's words win over the table's; a cause's words over the badge's", () => {
    expect(badge("hidden", { reason: "the server's own", fix: "its fix" }).getAttribute("title")).toBe(
      "the server's own — fix: its fix"
    )
    const c = CAUSES.not_recorded!.no_public_id
    expect(badge("not_recorded", { cause: "no_public_id" }).getAttribute("title")).toBe(`${c.reason} — fix: ${c.fix}`)
    // An unknown badge still renders, verbatim.
    expect(badge("future_hole").textContent).toBe("future_hole")
  })

  it("lists, lines and request holes reuse it", () => {
    const list = holeBadges([{ hole: "gap" }, { hole: "derived" }])!
    expect(Array.from(list.children).map((c) => c.getAttribute("data-hole"))).toEqual(["gap", "derived"])
    expect(holeBadges([])).toBeNull()
    const line = holeLine({ hole: "gap" })
    expect(line.querySelector('[data-hole="gap"]')).toBeTruthy()
    expect(line.textContent).toBe(`${HOLES.gap.label} — ${HOLES.gap.reason} · fix: ${HOLES.gap.fix}`)
    const [b, why] = requestHole("not_recorded")
    expect(b.textContent).toBe("request not recorded by weft v0.9.0 or earlier")
    expect(why.textContent).toBe(`${HOLES.not_recorded.reason} — fix: ${HOLES.not_recorded.fix}`)
    expect(requestHole("hidden")[0].textContent).toBe(`request: ${HOLES.hidden.label}`)
    expect(requestCapped(10000).textContent).toBe("request: truncated — first 10 000 requests")
    expect(requestCapped(10000).getAttribute("data-hole")).toBe("truncated")
  })

  it("a loop's cut is the table's: a result cap is truncated (result_cap), an unrun call max_tokens", () => {
    const cut = cutBadge({ kind: "bytes", bytes: 2048 })
    expect(cut.getAttribute("data-hole")).toBe("truncated")
    expect(cut.getAttribute("title")).toContain(CAUSES.truncated!.result_cap.reason)
    expect(cut.getAttribute("title")).toContain("2.0 KiB cut")
    expect(cut.getAttribute("title")).toContain(CAUSES.truncated!.result_cap.fix!)
    const never = cutBadge({ kind: "call", tool: "refund" })
    expect(never.getAttribute("data-hole")).toBe("max_tokens")
    expect(never.getAttribute("title")).toContain(HOLES.max_tokens.fix!)
  })
})

describe("the footer's content line", () => {
  const turn = (attrs: Record<string, unknown>[], eventHoles: Record<number, { hole: string; bytes?: number }[]> = {}) => ({
    events: attrs.map((a) => ({ attrs: a })),
    folded: { eventHoles },
  })
  const meta = (mark: string) => ({ content: { ingest: "as_received", latest: { run_id: "r", mark, note: "" } } }) as unknown as Meta

  it("on, with the recorder's cuts counted", () => {
    expect(capLine(turn([{}]), null).text).toBe("content on")
    const l = capLine(turn([{}, {}], { 1: [{ hole: "truncated", bytes: 1024 }], 3: [{ hole: "truncated", bytes: 1024 }] }), null)
    expect(l.text).toBe("content on · 2 events shortened (2.0 KiB cut)")
    expect(l.title).toBe(`${HOLES.truncated.reason} — fix: ${HOLES.truncated.fix}`)
    expect(capLine(turn([{}], { 0: [{ hole: "truncated", bytes: 10 }] }), null).text).toBe("content on · 1 event shortened (10 B cut)")
  })

  it("off, naming the cause the record marks — the turn's own first, the latest run's (meta) else", () => {
    expect(capLine(turn([{ "weft.content": "none" }]), null).text).toBe("content off · weft.Content(false)")
    expect(capLine(turn([{ "weft.content": "stripped" }]), null).text).toBe("content off · otel.NoContent()")
    expect(capLine(turn([{ "weft.content": "stripped" }]), null).title).toBe(
      `${HOLES.stripped.reason} — fix: ${HOLES.stripped.fix}`
    )
    expect(capLine(null, meta("none")).text).toBe("content off · weft.Content(false)")
    expect(capLine(null, meta("full")).text).toBe("content on")
  })

  it("replaces the old suffix in the mounted footer", async () => {
    const r = baseRoutes()
    r["runs/s_01-t1/events?after=0&limit=500"] = page(
      runEvents("s_01-t1").map((event, pos) =>
        pos === 2 ? { pos, time: "2026-10-01T09:00:00Z", event, attrs: { "weft.content.truncated_bytes": 4096 } } : event
      )
    )
    fakeStudio(r)
    const el = await mount()
    await vi.waitFor(() => expect(text(el, ".weft-footer")).toContain("content on · 1 event shortened (4.0 KiB cut)"))
    expect($(el, '.weft-footer [data-weft-cap="truncated"]')).toBeTruthy()
  })
})

describe("the turn chips (the run row's attrs)", () => {
  it("scripted, fork of, experiment of — each titled with the attribute it reads", () => {
    const label = (id: string) => (id === "s_01-t3" ? "t3" : id)
    const chips = (over: Parameters<typeof runRow>[0]) => turnChips(runRow(over), label).map((c) => [c.textContent, c.title])
    expect(chips({})).toEqual([])
    expect(chips({ model: { provider: "weft/runtime", name: "scripted" } })[0][0]).toBe("scripted (0 tokens)")
    const fork = chips({ meta: { "weft.session.forked_from": "s_01#e_3" } })
    expect(fork[0][0]).toBe("fork of s_01#e_3")
    expect(fork[0][1]).toContain("weft.session.forked_from")
    const exp = chips({ playground: true, forked_from: "s_01-t3#1", experiment_id: "exp_1" })
    expect(exp[0][0]).toBe("experiment of t3")
    expect(exp[0][1]).toContain("weft.forked_from = s_01-t3#1")
    expect(exp[0][1]).toContain("weft.experiment.id exp_1")
    expect(chips({ playground: true, experiment_id: "exp_2" })[0]).toEqual(["experiment", "weft.experiment.id = exp_2"])
  })

  it("sit on the turn rows", async () => {
    const r = baseRoutes()
    const t3 = runRow({ id: "s_01-t3", turn: 3 })
    const pg = runRow({
      id: "pg_1",
      session_id: "",
      turn: 0,
      playground: true,
      experiment_id: "exp_1",
      forked_from: "s_01-t3#0",
      model: { provider: "weft/runtime", name: "scripted" },
    })
    const fork = runRow({ id: "s_02-t1", session_id: "s_02", meta: { "weft.session.forked_from": "s_01#e_3" } })
    r["runs?public_id=pub_orders&limit=50"] = { total: 3, runs: [t3, pg, fork], next_before: null }
    fakeStudio(r, { ...META, capabilities: ["ingest", "playground"] })
    const el = await mount()
    await vi.waitFor(() => expect(all(el, "[data-weft-chip]").length).toBe(3))
    const of = (kind: string) => $(el, `[data-weft-chip="${kind}"]`)!
    expect(of("scripted").textContent).toBe("scripted (0 tokens)")
    expect(of("experiment").textContent).toBe("experiment of t3")
    expect(of("fork").textContent).toBe("fork of s_01#e_3")
    expect(of("experiment").closest('[data-key="pg_1"]')).toBeTruthy()
  })
})

describe("no panel-only badge strings", () => {
  // A hole's words are the table's (lib/honesty.ts) and are drawn by
  // badges.ts: a literal in any other panel source naming a hole —
  // prose, a label, a suffix — is a second vocabulary. A literal that
  // is exactly a hole's key (a comparison, a field value) is data.
  // styles.ts is CSS (overflow: hidden), not words.
  const WORDS =
    /(?<![-\w])(truncated|stripped|redacted|max_tokens|interrupted|not[ _]recorded|derived|compacted|shortened|gap|hidden)(?![-\w])/i
  const dir = resolve(process.cwd(), "src/panel")
  const files = readdirSync(dir).filter(
    (f) => f.endsWith(".ts") && !f.endsWith(".test.ts") && !["badges.ts", "testkit.ts", "styles.ts"].includes(f)
  )

  /** The string literals a source holds, comments dropped and a
   * template's ${…} parts left out. */
  function literals(src: string): string[] {
    const code = src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:\\])\/\/.*$/gm, "$1")
    const out: string[] = []
    for (const m of code.matchAll(/"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|`(?:[^`\\]|\\.)*`/g)) {
      const lit = m[0].slice(1, -1)
      out.push(m[0][0] === "`" ? lit.replace(/\$\{[^}]*\}/g, "") : lit)
    }
    return out
  }

  it("the scan reads literals and skips keys", () => {
    expect(literals(`const a = "truncated 3 bytes" // stripped\n/* gap */ x === "gap"; \`n \${"hidden"} m\``)).toEqual([
      "truncated 3 bytes",
      "gap",
      "n  m",
    ])
  })

  it("no panel source but badges.ts spells a hole's words", () => {
    expect(files).toContain("element.ts")
    const found: string[] = []
    for (const f of files)
      for (const lit of literals(readFileSync(resolve(dir, f), "utf8")))
        if (!(HOLE_ORDER as string[]).includes(lit) && WORDS.test(lit)) found.push(`${f}: ${JSON.stringify(lit)}`)
    expect(found).toEqual([])
  })
})
