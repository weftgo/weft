// The panel's badges (D5): one renderer over the A3 table — the span,
// data-hole, the table's words (a response's first) as its title; the
// footer's content line; the turn chips read from the run row; and no
// panel file but badges.ts spelling a hole's words.
import { readdirSync, readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { Meta } from "../lib/api"
import { UNRUN_CALL_REASON } from "../lib/events"
import { CAUSES, HOLE_ORDER, HOLES, resultCapReason } from "../lib/honesty"
import type { HoleMark } from "../lib/honesty"
import { REQUEST_NO_RECORD_REASON, requestCappedWords } from "../lib/requests"
import {
  badge,
  badgeLabel,
  capLine,
  CONTENT_OFF,
  cutBadge,
  holeBadges,
  holeLine,
  noPublicIdWords,
  requestCapped,
  requestHole,
  SCRIPTED_MODEL,
  turnChips,
} from "./badges"
import { $, all, baseRoutes, fakeStudio, META, mount, page, runEvents, runRow, setup, teardown, text } from "./testkit"

beforeEach(setup)
afterEach(teardown)

describe("badge (the one renderer)", () => {
  it("draws every hole of the table: the span, data-hole, the label, the reason and fix as its title and accessible text", () => {
    for (const h of HOLE_ORDER) {
      const b = badge(h)
      expect(b.tagName).toBe("SPAN")
      expect(b.classList.contains("weft-badge")).toBe(true)
      expect(b.getAttribute("data-hole")).toBe(h)
      expect(badgeLabel(b)).toBe(HOLES[h].label)
      const n = HOLES[h]
      const words = n.fix ? `${n.reason} — fix: ${n.fix}` : n.reason
      expect(b.getAttribute("title")).toBe(words)
      // A keyboard or screen-reader user gets the words too (D5 review).
      expect(b.querySelector(".weft-sr")?.textContent).toBe(` — ${words}`)
      expect(b.textContent).toBe(`${n.label} — ${words}`)
      expect(b.className).toContain(n.tone === "loss" ? "weft-warn-badge" : "weft-info")
    }
  })

  it("a response's words win over the table's; a cause's words over the badge's; fix \"\" says none applies", () => {
    expect(badge("hidden", { reason: "the server's own", fix: "its fix" }).getAttribute("title")).toBe(
      "the server's own — fix: its fix"
    )
    const c = CAUSES.not_recorded!.no_public_id
    expect(badge("not_recorded", { cause: "no_public_id" }).getAttribute("title")).toBe(`${c.reason} — fix: ${c.fix}`)
    expect(badge("gap", { fix: "" }).getAttribute("title")).toBe(HOLES.gap.reason)
    // An unknown badge still renders, verbatim.
    expect(badgeLabel(badge("future_hole"))).toBe("future_hole")
    expect(noPublicIdWords()).toBe(`${HOLES.not_recorded.label}: ${c.reason} · fix: ${c.fix}`)
    expect(noPublicIdWords("R", "F")).toBe(`${HOLES.not_recorded.label}: R · fix: F`)
  })

  it("lists, lines and request holes reuse it; a line or request hole says its words visibly, once", () => {
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
    const [g, gw] = requestHole("gap", { reason: REQUEST_NO_RECORD_REASON })
    expect(g.textContent).toBe("request: gap")
    expect(gw.textContent).toBe(`${REQUEST_NO_RECORD_REASON} — fix: ${HOLES.gap.fix}`)
    const cap = requestCapped(10000)
    expect(badgeLabel(cap)).toBe("request: truncated — first 10 000 requests")
    expect(cap.getAttribute("data-hole")).toBe("truncated")
    const w = requestCappedWords(10000)
    expect(cap.getAttribute("title")).toBe(`${w.reason} — fix: ${w.fix}`)
  })

  it("a loop's cut is the table's: a result cap is truncated (result_cap), an unrun call max_tokens with no fix", () => {
    const cut = cutBadge({ kind: "bytes", bytes: 2048 })
    expect(cut.getAttribute("data-hole")).toBe("truncated")
    expect(cut.getAttribute("title")).toBe(`${resultCapReason(2048)} — fix: ${CAUSES.truncated!.result_cap.fix}`)
    expect(cut.getAttribute("title")).toContain("2.0 KiB cut")
    const never = cutBadge({ kind: "call", tool: "refund" })
    expect(never.getAttribute("data-hole")).toBe("max_tokens")
    // The loop already retried the step (rule 11): no fix is offered.
    expect(never.getAttribute("title")).toBe(UNRUN_CALL_REASON)
    expect(never.getAttribute("title")).not.toContain(HOLES.max_tokens.fix!)
  })
})

describe("the footer's content line (said on evidence only)", () => {
  const turn = (
    attrs: Record<string, unknown>[],
    eventHoles: Record<number, HoleMark[]> = {},
    over: { id?: string; capped?: boolean } = {}
  ) => ({
    id: over.id ?? "r1",
    capped: over.capped,
    events: attrs.map((a) => ({ attrs: a })),
    folded: { eventHoles },
  })
  const meta = (mark: string, run = "r1", note = "", fix?: string) =>
    ({ content: { ingest: "as_received", latest: { run_id: run, mark, note, fix } } }) as unknown as Meta
  const cuts = { 1: [{ hole: "truncated", bytes: 1024 }], 3: [{ hole: "truncated", bytes: 1024 }] }

  it("on: the recorder's cuts are evidence content was there; a capped walk says how far it counted", () => {
    const l = capLine(turn([{}, {}], cuts), null)!
    expect(l.text).toBe("content on · 2 events shortened (2.0 KiB cut)")
    expect(l.title).toBe(`${HOLES.truncated.reason} — fix: ${HOLES.truncated.fix}`)
    expect(capLine(turn([{}], { 0: [{ hole: "truncated", bytes: 10 }] }), null)!.text).toBe("content on · 1 event shortened (10 B cut)")
    expect(capLine(turn([{}], cuts, { capped: true }), null, 5000)!.text).toBe(
      "content on · 2 events shortened (2.0 KiB cut) · first 5000 events"
    )
    // A full mark is said for the run it names, or with no turn open.
    expect(capLine(turn([{}]), meta("full"))!.text).toBe("content on")
    expect(capLine(null, meta("full"))!.text).toBe("content on")
  })

  it("unknown says nothing: no mark, an unmarked run, the latest run's mark on another turn", () => {
    expect(capLine(turn([{}]), null)).toBeNull()
    expect(capLine(null, null)).toBeNull()
    expect(capLine(null, meta("unmarked"))).toBeNull()
    expect(capLine(turn([]), meta("full", "r_other"))).toBeNull()
    expect(capLine(turn([]), meta("none", "r_other"))).toBeNull()
  })

  it("off names the cause the record marks — none is weft.Content(false) or no content-taking destination; the response's words win", () => {
    expect(capLine(turn([{ "weft.content": "none" }]), null)!.text).toBe(
      "content off · captured none (weft.Content(false), or no destination takes content)"
    )
    expect(capLine(turn([{ "weft.content": "stripped" }]), null)!.text).toBe("content off · otel.NoContent()")
    expect(capLine(turn([{ "weft.content": "stripped" }]), null)!.title).toBe(
      `${HOLES.stripped.reason} — fix: ${HOLES.stripped.fix}`
    )
    const m = capLine(null, meta("none", "r1", "studio's note", "studio's fix"))!
    expect(m.text).toBe(`content off · ${CONTENT_OFF.none}`)
    expect(m.title).toBe("studio's note — fix: studio's fix")
  })

  it("replaces the old suffix in the mounted footer, its title in the accessible text", async () => {
    const r = baseRoutes()
    r["runs/s_01-t1/events?after=0&limit=500"] = page(
      runEvents("s_01-t1").map((event, pos) =>
        pos === 2 ? { pos, time: "2026-10-01T09:00:00Z", event, attrs: { "weft.content.truncated_bytes": 4096 } } : event
      )
    )
    fakeStudio(r)
    const el = await mount()
    await vi.waitFor(() => expect(text(el, ".weft-footer")).toContain("content on · 1 event shortened (4.0 KiB cut)"))
    const cap = $(el, '.weft-footer [data-weft-cap="truncated"]')!
    expect(cap.querySelector(".weft-sr")?.textContent).toBe(` — ${HOLES.truncated.reason} — fix: ${HOLES.truncated.fix}`)
  })

  it("says nothing in the mounted footer when nothing marks the content", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    expect($(el, ".weft-footer")).toBeTruthy()
    expect($(el, ".weft-footer .weft-cap")).toBeNull()
  })
})

describe("the turn chips (the run row's attrs)", () => {
  it("scripted (its zero only when the row's usage is zero), fork of, experiment of — each titled with the attribute it reads", () => {
    const label = (id: string) => (id === "s_01-t3" ? "t3" : id)
    const chips = (over: Parameters<typeof runRow>[0]) => turnChips(runRow(over), label).map((c) => [c.textContent, c.title])
    expect(chips({})).toEqual([])
    expect(SCRIPTED_MODEL).toEqual({ provider: "weft/runtime", name: "scripted" })
    const zero = { input_tokens: 0, output_tokens: 0 }
    expect(chips({ model: SCRIPTED_MODEL, usage: zero })[0][0]).toBe("scripted (0 tokens)")
    // A scripted parent whose Subagent child ran a real model.
    expect(chips({ model: SCRIPTED_MODEL, usage: { input_tokens: 40, output_tokens: 7 } })[0][0]).toBe("scripted")
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
      model: SCRIPTED_MODEL,
      usage: { input_tokens: 0, output_tokens: 0 },
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

  /** The lib modules the panel imports: shared by both surfaces, so a
   * word there is not panel-only — but it must be one both surfaces
   * use, named here, and no lib module draws a panel badge. */
  const panelLibs = (): string[] => {
    const libs = new Set<string>()
    for (const f of readdirSync(dir).filter((x) => x.endsWith(".ts") && !x.endsWith(".test.ts")))
      for (const m of readFileSync(resolve(dir, f), "utf8").matchAll(/from "\.\.\/lib\/([a-z-]+)"/g)) libs.add(m[1])
    return [...libs].sort()
  }
  /** The shared lib prose that names a hole (by its start), each with
   * the run page's use of it. */
  const LIB_WORDS: Record<string, string> = {
    "compaction: compacted into ": "compactionLine — the run page's compaction marker",
    "compaction:thread compacted the session context this run belongs to:  of its messages were replaced by ; the next run starts on the compacted context (":
      "sessionNote — the run page's session marker",
    "requests:not recorded (stripped)": "paramFields — the run page's params of a content-off request",
    "requests:request not recorded by weft v0.9.0 or earlier": "REQUEST_NOT_RECORDED_LABEL — the run page's request label",
  }

  it("the lib modules the panel imports spell no other hole words and draw no panel badge", () => {
    const libs = panelLibs()
    expect(libs).toContain("honesty")
    const found: string[] = []
    for (const n of libs) {
      if (n === "honesty") continue // the table itself
      const src = readFileSync(resolve(process.cwd(), `src/lib/${n}.ts`), "utf8")
      expect(src.includes("weft-badge"), n).toBe(false)
      for (const lit of literals(src))
        if (!(HOLE_ORDER as string[]).includes(lit) && WORDS.test(lit)) found.push(`${n}:${lit}`)
    }
    // Each found literal is one named (by its start), and each named one is still there.
    const keys = Object.keys(LIB_WORDS)
    expect(found.filter((f) => !keys.some((k) => f.startsWith(k)))).toEqual([])
    expect(keys.filter((k) => !found.some((f) => f.startsWith(k)))).toEqual([])
  })

  // Badges worded without a hole's name slip past the word scan (the
  // no-record line did): every weft-badge built outside badges.ts is
  // one of these, by what its line says — none of them a hole.
  const NON_HOLE_BADGES = [
    "element.ts|⚠", // a tool's side-effect class (the drawer)
    "element.ts|decided:", // the decision taken on a parked call
    "element.ts|\"parked\"", // a parked call (approvals, read-only)
    "element.ts|data-weft-attempts", // the attempt line (lib/attempts)
    "element.ts|request could not be read", // a read error, not a hole
    "element.ts|prompt changed at this step",
    "element.ts|catalog changed at this step",
    "element.ts|\"subagent\"", // the child run's link
    "element.ts|\"subagent\"",
    "element.ts|, ms)", // the call's wall time
    "element.ts|\"error\"", // a tool error result (data, rule 1)
    "request.ts|data-weft-mark", // E1.2's change chips
    "request.ts|request could not be read",
    "request.ts|data-weft-attempts",
  ]

  it("every other weft-badge is a named non-hole badge", () => {
    const found: string[] = []
    for (const f of files) {
      const lines = readFileSync(resolve(dir, f), "utf8").split("\n")
      for (const l of lines) {
        if (!/["`']weft-badge/.test(l)) continue
        const hit = NON_HOLE_BADGES.find((k) => k.startsWith(`${f}|`) && l.includes(k.slice(f.length + 1)))
        found.push(hit ?? `${f}: ${l.trim()}`)
      }
    }
    expect(found.sort()).toEqual([...NON_HOLE_BADGES].sort())
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
