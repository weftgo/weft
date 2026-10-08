// Plan C5's Done line, for both clients: no request to Studio carries
// a bearer token in a URL. Every place either client builds a request
// URL or constructs an EventSource is enumerated below and driven under
// a configured token; each URL it produced must hold no `token=` and
// no token value, and the only credential may travel in the
// Authorization header or as a live grant's sig. Beside it, the static
// half: `token=` appears in no client source but the one place a
// client reads its own page's address (lib/api's
// adoptTokenFromLocation, the fragment reader — the page URL, never a
// request); no source sets a "token" search parameter in any form; and
// the files that make raw requests (fetch(, new EventSource(,
// sendBeacon, XMLHttpRequest) are exactly the four below — a new one
// fails until it is enumerated here.
//
// The call sites (grep apiUrl, fetch(, new EventSource, token in
// studio/web/src):
//   panel/client.ts  panelGet    — fetchMeta, fetchRuns, fetchRun, fetchEvents,
//                                  fetchTranscript, fetchRequests, fetchSpans,
//                                  fetchSessions, fetchSession, fetchPublic,
//                                  fetchRuntimes, fetchCommand
//                    panelPost   — postPlaygroundRun, postApproval, postSteer
//                    panelPut    — putBreakpoints
//                    openPanelLive — POST live-grant, then new EventSource
//   panel/config.ts  discoverEndpoint — GET panel-config.json (no credential at all)
//   lib/api.ts       request()   — every fetch*/post*/put* and *Query queryFn
//   lib/live.ts      openLive    — POST live-grant (requestLiveGrant), then new EventSource
import { readdirSync, readFileSync, statSync } from "node:fs"
import { join, relative, resolve } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import * as api from "@/lib/api"
import { openLive } from "@/lib/live"
import * as panel from "@/panel/client"
import { discoverEndpoint } from "@/panel/config"
import { FakeEventSource } from "@/test/fake-event-source"
import { grantLedger, withLiveGrant } from "@/test/fake-live-grant"

const SERVER = "tok_SECRET+/=value"
const PANEL = `weft_pt.${btoa(
  JSON.stringify({ public_id: "pub_1", scope: "playground", exp: new Date(Date.now() + 3600_000).toISOString() })
).replace(/=+$/, "")}.c2ln`

interface Seen {
  site: string
  url: string
  auth: string | null
}

let seen: Seen[] = []
let site = ""

function install() {
  const inner = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://studio.test/")
    // Studio's rule (studio/auth.go, plan C5): ?token= is a 401 everywhere.
    if (url.searchParams.has("token")) return new Response("{}", { status: 401 })
    return new Response(JSON.stringify({ error: { code: "not_found", message: "" } }), {
      status: 404,
      headers: { "content-type": "application/json" },
    })
  })
  const granted = withLiveGrant(inner)
  vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
    seen.push({ site, url: String(input), auth: new Headers(init?.headers).get("Authorization") })
    return granted(input, init)
  })
  vi.stubGlobal("EventSource", FakeEventSource)
}

/** settle lets the grant requests travel and the streams construct. */
const settle = () => new Promise((r) => setTimeout(r, 20))

async function drive(name: string, fn: () => unknown) {
  site = name
  try {
    await fn()
  } catch {
    // the fake answers 404: what matters is the request that was made
  }
}

function assertClean(token: string) {
  expect(seen.length).toBeGreaterThan(0)
  const enc = [token, encodeURIComponent(token), new URLSearchParams({ t: token }).toString().slice(2)]
  for (const s of seen) {
    expect(s.url, s.site).not.toContain("token=")
    for (const v of enc) expect(s.url, s.site).not.toContain(v)
    if (s.site === "discoverEndpoint") expect(s.auth, s.site).toBeNull()
    else expect(s.auth, s.site).toBe(`Bearer ${token}`)
  }
  expect(FakeEventSource.instances.length).toBeGreaterThan(0)
  for (const es of FakeEventSource.instances) {
    expect(es.url).not.toContain("token=")
    for (const v of enc) expect(es.url).not.toContain(v)
    const sig = new URL(es.url).searchParams.get("sig")
    // The stream's one credential: a sig the bearer bought in a header.
    expect(grantLedger.find((g) => g.sig === sig)?.bearer).toBe(token)
    expect(es.refused).toBeNull()
  }
}

beforeEach(() => {
  seen = []
  FakeEventSource.reset()
  install()
})
afterEach(() => {
  vi.unstubAllGlobals()
  api.setStudioToken("")
})

describe("no client request carries the token in a URL", () => {
  for (const [name, token] of [
    ["a server token", SERVER],
    ["a panel token", PANEL],
  ] as const) {
    it(`the Studio app, under ${name}`, async () => {
      api.setStudioToken(token)
      await drive("fetchRuns", () => api.fetchRuns({ agent: "a", limit: 5 }))
      await drive("fetchRun", () => api.fetchRun("r/1"))
      await drive("fetchTranscript", () => api.fetchTranscript("r1"))
      await drive("fetchEventsPage", () => api.fetchEventsPage("r1", 0, 10))
      await drive("fetchRequests", () => api.fetchRequests("r1"))
      await drive("fetchTools", () => api.fetchTools("r1"))
      await drive("fetchStep", () => api.fetchStep("r1", 0))
      await drive("fetchLogs", () => api.fetchLogs("r1", { from: 1 }))
      await drive("fetchSessions", () => api.fetchSessions({}))
      await drive("fetchCommand", () => api.fetchCommand("c1"))
      await drive("postPlaygroundRun", () => api.postPlaygroundRun({} as Parameters<typeof api.postPlaygroundRun>[0]))
      await drive("postFixtures", () => api.postFixtures("r1", []))
      await drive("postSteer", () => api.postSteer("r1", "hi"))
      await drive("putBreakpoints", () => api.putBreakpoints("rt1", []))
      await drive("postExperiment", () => api.postExperiment({} as Parameters<typeof api.postExperiment>[0]))
      await drive("postApproval", () => api.postApproval("r1", { call_id: "c", decision: "approve" }))
      await drive("metaQuery", () => (api.metaQuery().queryFn as () => unknown)())
      site = "openLive"
      const live = openLive({ selector: { run: "r1" }, kinds: ["event", "delta", "run"] })
      await settle()
      live.close()
      assertClean(token)
    })

    it(`the devtools panel, under ${name}`, async () => {
      const ep = { base: "http://studio.test/studio/", token }
      await drive("fetchMeta", () => panel.fetchMeta(ep))
      await drive("fetchRuns", () => panel.fetchRuns(ep, { public_id: "pub_1" }))
      await drive("fetchRun", () => panel.fetchRun(ep, "r1"))
      await drive("fetchEvents", () => panel.fetchEvents(ep, "r1", 0))
      await drive("fetchTranscript", () => panel.fetchTranscript(ep, "r1"))
      await drive("fetchRequests", () => panel.fetchRequests(ep, "r1"))
      await drive("fetchSpans", () => panel.fetchSpans(ep, "r1"))
      await drive("fetchSessions", () => panel.fetchSessions(ep, { public_id: "pub_1" }))
      await drive("fetchSession", () => panel.fetchSession(ep, "s1"))
      await drive("fetchPublic", () => panel.fetchPublic(ep, "pub_1"))
      await drive("fetchRuntimes", () => panel.fetchRuntimes(ep))
      await drive("fetchCommand", () => panel.fetchCommand(ep, "c1"))
      await drive("postPlaygroundRun", () => panel.postPlaygroundRun(ep, {}))
      await drive("postApproval", () => panel.postApproval(ep, "r1", { call_id: "c", decision: "deny" }))
      await drive("putBreakpoints", () => panel.putBreakpoints(ep, "rt1", []))
      await drive("postSteer", () => panel.postSteer(ep, "r1", "hi"))
      await drive("discoverEndpoint", () => discoverEndpoint("http://studio.test/studio/panel-config.json"))
      site = "openPanelLive"
      const h = panel.openPanelLive(ep, { selector: { public_id: "pub_1" }, kinds: ["run"] })
      await settle()
      h.close()
      assertClean(token)
    })
  }
})

// ── The static half ───────────────────────────────────────────────

const SRC = resolve(process.cwd(), "src")

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) return p === join(SRC, "test") ? [] : sources(p)
    if (!/\.(ts|tsx)$/.test(name) || /\.test\.tsx?$/.test(name) || name === "testkit.ts") return []
    return [p]
  })
}

/** The one exempt site: adoptTokenFromLocation in lib/api.ts, its doc
 * comment and its body — the fragment reader: the client reading its
 * own page's `#token=` (and stripping a `?token=` unread), never a
 * request. */
function exemptLines(file: string, text: string): Set<number> {
  const out = new Set<number>()
  if (relative(SRC, file) !== join("lib", "api.ts")) return out
  const lines = text.split("\n")
  const fn = lines.findIndex((l) => l.startsWith("export function adoptTokenFromLocation("))
  if (fn < 0) return out
  let start = fn
  while (start > 0 && !lines[start - 1].startsWith("/**")) start--
  let end = fn
  while (end < lines.length && lines[end] !== "}") end++
  for (let i = start - 1; i <= end; i++) out.add(i)
  return out
}

describe("no client source spells token= outside adoptTokenFromLocation", () => {
  it("grep -rn 'token=' studio/web/src (tests and testkits aside)", () => {
    const files = sources(SRC)
    expect(files.length).toBeGreaterThan(20)
    const hits: string[] = []
    let exempt = 0
    for (const f of files) {
      const text = readFileSync(f, "utf8")
      const skip = exemptLines(f, text)
      text.split("\n").forEach((line, i) => {
        if (!line.includes("token=")) return
        if (skip.has(i)) exempt++
        else hits.push(`${relative(SRC, f)}:${i + 1}: ${line.trim()}`)
      })
    }
    expect(hits).toEqual([])
    // The exemption is real: the fragment reader is where it says.
    expect(exempt).toBeGreaterThan(0)
  })
})

/** A "token" search-parameter key, in any form a URL is built with:
 * .set("token", …) / .append("token", …) on search params, and a
 * URLSearchParams literal ({ token: … } or [["token", …]]). */
const TOKEN_KEY = [
  /\.(set|append)\(\s*["'`]token["'`]/,
  /URLSearchParams\(\s*\{[^}]*\btoken\s*:/,
  /URLSearchParams\(\s*\[[^\]]*\[\s*["'`]token["'`]/,
]

describe("no client source sets a token search parameter", () => {
  it("no .set/.append(\"token\", …) and no URLSearchParams literal with a token key", () => {
    const hits: string[] = []
    for (const f of sources(SRC)) {
      readFileSync(f, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (TOKEN_KEY.some((re) => re.test(line))) hits.push(`${relative(SRC, f)}:${i + 1}: ${line.trim()}`)
        })
    }
    expect(hits).toEqual([])
  })

  it("the patterns catch the forms the clients used before plan C5", () => {
    for (const line of [
      'if (ep.token) params.set("token", ep.token)',
      'if (tok) url.searchParams.set("token", tok)',
      "q.append('token', t)",
      "new URLSearchParams({ run, token: tok })",
      'new URLSearchParams([["token", tok]])',
    ])
      expect(TOKEN_KEY.some((re) => re.test(line)), line).toBe(true)
    // Reading or stripping the page's own parameter is not setting one.
    expect(TOKEN_KEY.some((re) => re.test('url.searchParams.delete("token")'))).toBe(false)
  })
})

/** The raw request primitives: a file holding one is a call site. */
const RAW = [/\bfetch\(/, /new EventSource\(/, /\bsendBeacon\b/, /\bXMLHttpRequest\b/]

describe("the call sites above are every raw request site", () => {
  it("the files with fetch(, new EventSource(, sendBeacon or XMLHttpRequest are exactly the enumerated four", () => {
    const found = sources(SRC)
      .filter((f) => RAW.some((re) => re.test(readFileSync(f, "utf8"))))
      .map((f) => relative(SRC, f).split("\\").join("/"))
      .sort()
    expect(found).toEqual(["lib/api.ts", "lib/live.ts", "panel/client.ts", "panel/config.ts"])
  })
})
