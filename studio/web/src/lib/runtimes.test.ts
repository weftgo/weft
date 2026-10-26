// lib/runtimes.ts and lib/runs-in.ts (U20.1): the runtimes page's
// reading and the "Runs in" line — three runtimes of one service told
// apart, a restored one said so, two links of one process one process,
// the manifest marks, what Studio may do and why not.
import { describe, expect, it } from "vitest"

import type { Meta, RuntimeView } from "./api"
import { APP_CANCEL, OWN_RUNTIME, READ_TOKEN } from "./honesty"
import { RUNS_IN, registering, runsInLine, runtimeLabel } from "./runs-in"
import {
  breakpointsBy,
  countLine,
  debugScopeWords,
  manifestMark,
  mayDo,
  missingWords,
  overlapping,
  processGroups,
  processHead,
  runManifestMark,
  runtimeState,
  STATE_WORDS,
  steerOffLine,
  steerWhyNot,
} from "./runtimes"
import { BREAKPOINT_WORDS } from "./settings"

const NOW = Date.parse("2026-10-26T10:00:00Z")
const ago = (s: number) => new Date(NOW - s * 1000).toISOString()

const rt = (over: Partial<RuntimeView>): RuntimeView => ({
  id: "rt_x",
  host: "box",
  pid: 1,
  service: "weft-arena-dev",
  env: "",
  connected_since: ago(3600),
  last_seen: ago(2),
  connected: true,
  agents: [],
  ...over,
})
const ag = (name: string, manifest_hash?: string) => ({
  name,
  manifest_hash,
  models: [],
  tools: [],
})

// The arena's three runtimes, as the audit found them (UX-C-11): one
// service, three registrations — here three processes, three cwds.
const batch = rt({
  id: "rt_21bffa1a0000",
  pid: 2901,
  cwd: "/w/arena",
  agents: [ag("chat"), ag("director"), ag("planner"), ag("storyteller")],
  last_seen: ago(20),
})
const fleet = rt({
  id: "rt_0396552a0000",
  pid: 2902,
  cwd: "/w/arena-fleet",
  agents: [ag("analyst"), ag("director"), ag("writer")],
  last_seen: ago(2),
})
const lab = rt({
  id: "rt_dddce66f0000",
  pid: 2903,
  cwd: "/w/thread-lab",
  agents: [ag("trip-planner")],
})

describe("the runtimes page's reading (U20.1)", () => {
  it("three runtimes of one service read apart by pid and id; a restored one says so", () => {
    const labels = [batch, fleet, lab].map(runtimeLabel)
    expect(new Set(labels).size).toBe(3)
    expect(labels[0]).toBe(
      "weft-arena-dev · (no env) · pid 2901 · 4 agents · rt_21bf…"
    )
    expect(runsInLine(fleet, NOW)).toBe(
      "rt_0396… weft-arena-dev (pid 2902, seen just now)"
    )
    const restored = rt({ id: "rt_old", connected: false, restored: true })
    const stale = rt({ id: "rt_gone", connected: false })
    expect([batch, restored, stale].map(runtimeState)).toEqual([
      "connected",
      "restored",
      "stale",
    ])
    expect(STATE_WORDS.restored.word).toBe("restored after restart")
    expect(countLine([batch, fleet, restored, stale])).toBe(
      "2 connected · 1 restored after restart · 1 stale"
    )
    expect(countLine([])).toBe("0 connected")
  })

  it("two links of one process are one process, their shared agents named (UX-E-56)", () => {
    const a = rt({ id: "rt_a", pid: 7, agents: [ag("director"), ag("chat")] })
    const b = rt({ id: "rt_b", pid: 7, agents: [ag("director")] })
    const c = rt({ id: "rt_c", pid: 8, agents: [ag("x")] })
    const groups = processGroups([a, b, c])
    expect(groups.map((g) => g.runtimes.map((r) => r.id))).toEqual([
      ["rt_a", "rt_b"],
      ["rt_c"],
    ])
    expect(processHead(groups[0])).toBe(
      "one process · box · pid 7 · 2 runtime links"
    )
    expect(overlapping(groups[0])).toEqual(["director"])
    // Only connected links group, and only within one cwd (U20.1-fix):
    // two stale links that share a host and a (reused) pid are two
    // runtimes; so are two connected ones in different directories.
    const s1 = rt({ id: "rt_s1", pid: 7, connected: false })
    const s2 = rt({ id: "rt_s2", pid: 7, connected: false })
    expect(processGroups([s1, s2]).map((g) => g.runtimes.length)).toEqual([
      1, 1,
    ])
    const d1 = rt({ id: "rt_d1", pid: 9, cwd: "/a" })
    const d2 = rt({ id: "rt_d2", pid: 9, cwd: "/b" })
    expect(processGroups([d1, d2])).toHaveLength(2)
  })

  it("a command goes to the connected registration, then the newest seen — never the first listed", () => {
    const old = rt({
      id: "rt_old",
      agents: [ag("director")],
      last_seen: ago(600),
    })
    const gone = rt({
      id: "rt_gone",
      agents: [ag("director")],
      connected: false,
      last_seen: ago(1),
    })
    expect(
      registering([old, gone, batch, fleet], "director").map((r) => r.id)
    ).toEqual([fleet.id, batch.id, old.id, "rt_gone"])
    expect(registering([batch], "nobody")).toEqual([])
  })

  it("the refusal says what is connected, the fix and the Studio asked", () => {
    expect(RUNS_IN.none("studio-local")).toBe(
      "✗ no connected runtime registers studio-local"
    )
    expect(RUNS_IN.connected([lab])).toBe(
      "connected: weft-arena-dev (trip-planner)"
    )
    expect(RUNS_IN.connected([])).toBe("connected: none")
    expect(RUNS_IN.fix("orders")).toContain("runtime.Agents(orders)")
    expect(RUNS_IN.asked("http://127.0.0.1:7341")).toBe(
      "asked http://127.0.0.1:7341"
    )
  })

  it("what Studio may do, and why not (UX-S4-06)", () => {
    const caps = ["playground", "steer", "cancel", "breakpoints"]
    const plain = mayDo(rt({}), caps)
    expect(plain.map((m) => [m.verb, m.started.yes, m.own.why])).toEqual([
      ["approve", true, "runtime.Sessions not installed"],
      ["steer", true, "runtime.Sessions not installed"],
      ["cancel", true, APP_CANCEL],
      ["breakpoints", true, "no agent built with runtime.Breakpoints()"],
    ])
    const own = mayDo(rt({ sessions: true, breakpoints_own: true }), caps)
    expect(own.map((m) => m.own.yes)).toEqual([true, true, false, true])
    const gone = mayDo(rt({ connected: false, sessions: true }), caps)
    expect(gone.every((m) => !m.started.yes)).toBe(true)
    expect(gone[0].started.why).toBe("not connected")
    expect(mayDo(rt({}), ["playground"])[1].started.why).toBe(
      "capability steer is off"
    )
    expect(debugScopeWords(OWN_RUNTIME)).toContain("runtime.Sessions")
    expect(debugScopeWords("runtime-started runs")).toContain("view-only")
  })

  it("the manifest marks: a registered manifest against the latest run (UX-S4-01), a run against the registered one", () => {
    const check: NonNullable<Meta["manifest_check"]> = {
      agents: 3,
      checked: 2,
      stale: ["director"],
      latest: { director: "d2151022aaaa", writer: "w1" },
    }
    expect(manifestMark("director", "e3c29c1e4b80", check)).toEqual({
      mark: "stale",
      latest: "d2151022aaaa",
    })
    expect(manifestMark("writer", "w1", check).mark).toBe("current")
    expect(manifestMark("analyst", "a1", check).mark).toBe("unchecked")
    // A weft.json agent has no hash of its own (U20.1-fix): the stale
    // list decides, never "other".
    expect(manifestMark("writer", undefined, check).mark).toBe("current")
    expect(manifestMark("director", undefined, check).mark).toBe("stale")
    // Another registered version of a name that is not stale.
    expect(manifestMark("writer", "w0", check).mark).toBe("other")
    // An older Studio: stale names but no latest.
    expect(
      manifestMark("director", "x", {
        agents: 1,
        checked: 1,
        stale: ["director"],
      }).mark
    ).toBe("stale")

    expect(runManifestMark("writer", "w1", check)).toBe("current")
    expect(runManifestMark("director", "d2151022aaaa", check)).toBe("stale")
    // An older run of a current agent ran another manifest: stale.
    expect(runManifestMark("writer", "w0", check)).toBe("stale")
    // An older run of a stale agent: the check cannot tell.
    expect(runManifestMark("director", "old", check)).toBe("")
    expect(runManifestMark("analyst", "a", check)).toBe("")
  })

  it("who set the breakpoints", () => {
    expect(
      breakpointsBy({ breakpoints_by: "studio", breakpoints_set_at: ago(120) })
    ).toMatch(/^set .+ through this Studio$/)
    expect(breakpointsBy({ breakpoints_by: "runtime" })).toContain(
      "the runtime's own registration carried it"
    )
    expect(breakpointsBy({ breakpoints_by: "store" })).toBe(
      "restored from Studio's store after a restart"
    )
    expect(breakpointsBy({})).toBe(BREAKPOINT_WORDS.setAtUnknown)
  })

  it("the missing-agent explainer names a disconnected runtime that registers it", () => {
    const gone = rt({ id: "rt_gone", connected: false, agents: [ag("orders")] })
    const lines = missingWords("orders", [gone, batch])
    expect(lines[0]).toContain("No connected runtime registers orders")
    expect(lines[1]).toBe(
      "1 runtime that is not connected registers it: rt_gone."
    )
    expect(missingWords("orders", [batch])).toHaveLength(1)
  })

  it("why a running app turn offers no steer (UX-S4-10, U20.1-fix)", () => {
    const f = {
      kind: "read-only" as const,
      steer: true,
      mayAct: true,
      service: "app",
    }
    expect(steerWhyNot(f)).toBe(
      "no connected runtime of service app registered runtime.Sessions"
    )
    expect(steerWhyNot({ ...f, kind: "decide" })).toBe("")
    expect(steerWhyNot({ ...f, kind: "decide", steer: false })).toBe(
      "capability steer is off"
    )
    expect(steerWhyNot({ ...f, mayAct: false })).toBe(READ_TOKEN)
    expect(steerWhyNot({ ...f, kind: "experiment" })).toBe("")
    expect(steerOffLine("x", "runtime-started runs")).toContain(APP_CANCEL)
  })
})
