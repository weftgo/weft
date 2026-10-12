// The honesty table against Go's (plan A3, ADR 0028 §11): the ten
// badges, in order, with obsdb.HoleNote's reason and fix word for word.
// studio/testdata/holes.golden.json is written by obsdb's
// TestHoleNotesGolden — one file both trees are checked against, so the
// two tables cannot drift.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import {
  CAUSES,
  HOLE_ORDER,
  HOLES,
  contentHoles,
  holeWords,
  isHole,
  statusHoles,
  kib,
  mergeHoles,
  APP_OWNED,
  APP_RUN,
  APP_RUN_FIX,
  CAPS_UNREAD,
  PLAYGROUND_OFF,
  PLAYGROUND_OFF_FIX,
  READ_TOKEN,
  SESSION_UNREAD,
  POOL_CHILD,
  SUBAGENT_CALL,
  appVerbWords,
  noPublicIDWords,
  parkedKind,
  refusalLine,
  refusalWords,
} from "./honesty"

interface GoHole {
  hole: string
  reason: string
  fix?: string
  causes?: Record<string, { reason: string; fix?: string }>
}

const golden = JSON.parse(
  readFileSync(resolve(process.cwd(), "../testdata/holes.golden.json"), "utf8")
) as GoHole[]

describe("the honesty table", () => {
  it("is Go's closed table: the same ten holes, in order", () => {
    expect(golden.map((g) => g.hole)).toEqual(HOLE_ORDER)
    expect(HOLE_ORDER).toEqual([
      "truncated",
      "stripped",
      "redacted",
      "max_tokens",
      "interrupted",
      "gap",
      "not_recorded",
      "derived",
      "hidden",
      "compacted",
    ])
  })

  it("words every hole as obsdb.HoleNote does, key by key", () => {
    for (const g of golden) {
      expect(isHole(g.hole)).toBe(true)
      if (!isHole(g.hole)) continue
      const note = HOLES[g.hole]
      expect(note.reason, g.hole).toBe(g.reason)
      expect(note.fix, g.hole).toBe(g.fix)
      expect(note.label, g.hole).toBeTruthy()
      expect(["loss", "note"]).toContain(note.tone)
    }
  })

  it("words every cause as obsdb.HoleNoteFor does, and a mark's cause wins over the badge's words (D5)", () => {
    for (const g of golden) {
      const mine = isHole(g.hole) ? (CAUSES[g.hole] ?? {}) : {}
      expect(Object.keys(mine).sort(), g.hole).toEqual(Object.keys(g.causes ?? {}).sort())
      for (const [cause, note] of Object.entries(g.causes ?? {})) {
        expect(mine[cause], `${g.hole}/${cause}`).toEqual(note)
        const w = holeWords({ hole: g.hole, cause })
        expect(w.reason).toBe(note.reason)
        expect(w.fix).toBe(note.fix)
      }
    }
    // A result cap's cut is the badge, not the recorder's words.
    expect(holeWords({ hole: "truncated", cause: "result_cap", bytes: 12 }).label).toBe("truncated")
    // An unknown cause reads the badge's own words.
    expect(holeWords({ hole: "gap", cause: "nope" }).reason).toBe(HOLES.gap.reason)
  })

  it("reads an event's attrs: the recorder's cut and the content-off mark", () => {
    expect(contentHoles(undefined)).toEqual([])
    expect(contentHoles({ "weft.content": "full" })).toEqual([])
    expect(contentHoles({ "weft.content": "stripped" })).toEqual([
      { hole: "stripped" },
    ])
    // The core's own capture-off mark reads the same.
    expect(contentHoles({ "weft.content": "none" })).toEqual([
      { hole: "stripped" },
    ])
    expect(
      contentHoles({ "weft.content.truncated_bytes": 12595 })
    ).toEqual([{ hole: "truncated", bytes: 12595 }])
  })

  it("says what the recorder cut, in bytes", () => {
    expect(kib(92)).toBe("92 B")
    expect(kib(12595)).toBe("12.3 KiB")
    const w = holeWords({ hole: "truncated", bytes: 12595 })
    expect(w.label).toBe("shortened by the recorder: 12.3 KiB cut")
    expect(w.reason).toBe(HOLES.truncated.reason)
    expect(w.fix).toBe(HOLES.truncated.fix)
    expect(holeWords({ hole: "stripped" }).label).toBe(
      "content not captured by this app"
    )
    // A response's words win; an unknown badge renders verbatim.
    expect(holeWords({ hole: "gap", reason: "mine" }).reason).toBe("mine")
    expect(holeWords({ hole: "brand_new" }).label).toBe("brand_new")
  })

  it("merges lists: one badge per hole, bytes summed, the table's order", () => {
    const merged = mergeHoles(
      [{ hole: "not_recorded" }, { hole: "truncated", bytes: 10 }],
      [{ hole: "truncated", bytes: 5 }, { hole: "stripped" }]
    )
    expect(merged).toEqual([
      { hole: "truncated", bytes: 15 },
      { hole: "stripped" },
      { hole: "not_recorded" },
    ])
  })
})

// One rule on both surfaces (the run page's header, the panel's turn).
describe("statusHoles", () => {
  it("interrupted, max_tokens from the stop reason, a gap only once the run is over", () => {
    expect(statusHoles({ status: "running", gaps: [4] })).toEqual([])
    expect(statusHoles({ gaps: [4] })).toEqual([])
    expect(statusHoles({ status: "failed", gaps: [4, 5] })).toEqual([
      { hole: "gap", reason: "2 events missing (positions 4, 5): a destination dropped a batch" },
    ])
    expect(statusHoles({ status: "succeeded", stop_reason: "max_tokens" })).toEqual([
      { hole: "max_tokens" },
    ])
    expect(statusHoles({ status: "interrupted" })).toEqual([{ hole: "interrupted" }])
  })
})

// The app turn's gate and words (plan F4.3) — the run page's and the
// panel's one module; studio/appturn.go and studio/manifest.go are the
// words' source, read here so the two cannot drift.
describe("the app turn's kinds and refusal words", () => {
  const turn = { id: "s-t1", session_id: "s", public_id: "pub_s", service: "app" }
  const rt = (o: Partial<{ id: string; connected: boolean; sessions: boolean }> = {}) => ({ id: "rt_1", service: "app", connected: true, sessions: true, ...o })
  const go = (f: string) => readFileSync(resolve(process.cwd(), `../${f}`), "utf8")

  it("a subagent's child run is decided on its parent turn: no verb, the parent named", () => {
    const child = { ...turn, id: "s-t1/0/call_1", parent_run_id: "s-t1" }
    expect(parkedKind(child, [rt()])).toBe("child")
    expect(refusalLine(refusalWords("child", child, [rt()], false))).toBe(`${SUBAGENT_CALL} s-t1`)
  })

  it("a thread/pool child's turn (its own session's turn under the delegating call): decided on the parent session, never the plain line", () => {
    // ADR 0022 §7: the child's requests surface on the parent session's
    // Pending() and p.Decide decides them there; pool.go runs the child
    // under the delegating call's ctx, so core stamps weft.parent.run.id.
    const child = { id: "s_pool-t1", session_id: "s_pool", public_id: "", service: "app", parent_run_id: "s-t1" }
    expect(parkedKind(child, [rt()])).toBe("pool-child")
    const w = refusalWords(parkedKind(child, [rt()]), child, [rt()], false)
    expect(refusalLine(w)).toBe(`${POOL_CHILD} s-t1`)
    expect(w.fix).toBe("")
    expect(refusalLine(w)).not.toContain(APP_RUN)
    expect(go("../thread/turn.go")).toContain(`t.runID = fmt.Sprintf("%s-t%d", s.header.ID, s.turnSeq)`)
  })

  it("another agent's Generate inside a tool handler (weft.parent.run.id, its own id): the plain line, never the parent's", () => {
    // Core stamps the parent id on every run under a tool call's context;
    // only a Subagent child's id is `<parent>/<step>/<call>` (pricing.go's subagents()).
    // Under a session turn it inherits the session id (core's run
    // metadata rides ctx), but its id is not that session's turn.
    const inherited = { ...turn, id: "r_nested", parent_run_id: "s-t1" }
    expect(parkedKind(inherited, [rt()])).toBe("plain")
    const nested = { id: "r_nested", service: "app", parent_run_id: "s-t1" }
    expect(parkedKind(nested, [rt()])).toBe("plain")
    expect(refusalLine(refusalWords(parkedKind(nested, [rt()]), nested, [rt()], false))).toBe(`${APP_RUN} — fix: ${APP_RUN_FIX}`)
    expect(go("pricing.go")).toContain(`strings.HasPrefix(k.ID, id+"/")`)
  })

  it("a session turn without a public id: appturn.go's 403 words and fix", () => {
    const row = { ...turn, public_id: "" }
    expect(parkedKind(row, [rt()])).toBe("no-public-id")
    expect(refusalWords("no-public-id", row, [rt()], false)).toEqual({ words: noPublicIDWords("s-t1"), fix: APP_RUN_FIX })
    expect(go("appturn.go")).toContain(`appOwned+" · read-only: run "+row.ID+" is not a turn of a thread session with a public id"`)
    // Outside a session it stays the plain Generate's line.
    expect(parkedKind({ ...turn, session_id: "", public_id: "" }, [rt()])).toBe("plain")
  })

  it("a Studio without the playground: capabilities_off's reason, never the closed-link line; a failed meta read said as such", () => {
    const off = /offPlayground\s*=\s*"([^"]+)"/.exec(go("manifest.go"))![1]
    const w = refusalWords("read-only", turn, null, false, undefined, "", { capabilities: ["live"], capabilities_off: { playground: off } })
    expect(w).toEqual({ words: `${APP_OWNED}: ${PLAYGROUND_OFF}`, fix: off.replace(/^the playground is off:\s*/, "") })
    expect(`the playground is off: ${PLAYGROUND_OFF_FIX}`).toBe(off)
    // No capabilities_off sent: the same words, Go's fix.
    expect(refusalWords("read-only", turn, null, false, undefined, "", { capabilities: [] }).fix).toBe(PLAYGROUND_OFF_FIX)
    expect(refusalWords("read-only", turn, null, false, undefined, "", { metaError: "boom" })).toEqual({ words: `${CAPS_UNREAD}: boom`, fix: "" })
    // A read token is told about its token first.
    expect(refusalWords("read-only", turn, null, true, undefined, "", { capabilities: [] }).words).toBe(READ_TOKEN)
  })

  it("a failed session read is said as such, never a verdict", () => {
    expect(refusalWords("decide", turn, [rt()], false, undefined, "", { capabilities: ["playground"], sessionError: "500" })).toEqual({
      words: `${SESSION_UNREAD}: 500`,
      fix: "",
    })
  })

  it("a finished steer reads steered, a finished decision decided", () => {
    expect(appVerbWords({ state: "finished" }, "r", "steer").text).toBe("steered")
    expect(appVerbWords({ state: "finished", decision: "approve" }, "r").text).toBe("decided")
    // The panel's steer carries no decision: steered without the argument.
    expect(appVerbWords({ state: "finished" }, "r").text).toBe("steered")
  })
})
