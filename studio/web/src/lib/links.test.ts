// The deep-link scheme (plan G1): every builder, step as the ordinal,
// ids as one encoded segment, and never a token in a link.
import { describe, expect, it } from "vitest"

import {
  canonical,
  compareLink,
  experimentLink,
  href,
  path,
  playgroundLink,
  playgroundSearch,
  playgroundStateLink,
  rawFromSearch,
  rawSearch,
  runLink,
  sessionLink,
  traceLink,
} from "./links"

const BASE = "http://studio.test/studio/"

describe("runLink", () => {
  it("is the run page alone without options", () => {
    expect(runLink("r_1")).toEqual({
      to: "/runs/$id",
      params: { id: "r_1" },
      search: {},
    })
    expect(href(BASE, runLink("r_1"))).toBe(`${BASE}runs/r_1`)
  })

  it("carries the step as the ordinal, then the view", () => {
    expect(path(runLink("r_1", { step: 2, view: "story" }))).toBe(
      "runs/r_1?step=2&view=story"
    )
    // step 0 is a step.
    expect(path(runLink("r_1", { step: 0 }))).toBe("runs/r_1?step=0")
    // The trace is the default view: never written.
    expect(path(runLink("r_1", { step: 1, view: "trace" }))).toBe(
      "runs/r_1?step=1"
    )
    expect(path(runLink("r_1", { view: "raw" }))).toBe("runs/r_1?view=raw")
  })

  it("drops a step that is not an ordinal", () => {
    for (const step of [-1, 1.5, Number.NaN, Infinity])
      expect(runLink("r_1", { step }).search.step).toBeUndefined()
  })

  it("names a call with its step (call ids repeat across steps), or as the resumed call", () => {
    expect(runLink("r_1", { step: 3, call: "c1" }).search.sel).toBe("c:3:c1")
    expect(runLink("r_1", { call: "c1", resumed: true }).search.sel).toBe(
      "c:resume:c1"
    )
    // A call with neither names no span: an ambiguous key is never written.
    expect(runLink("r_1", { call: "c1" }).search.sel).toBeUndefined()
    expect(path(runLink("r_1", { step: 3, call: "c1" }))).toBe(
      "runs/r_1?step=3&sel=c%3A3%3Ac1"
    )
  })

  it("selects an OTel span, the axis and the replay position", () => {
    expect(runLink("r_1", { span: "ab12" }).search.sel).toBe("t:ab12")
    expect(path(runLink("r_1", { axis: "time", t: 7 }))).toBe(
      "runs/r_1?axis=time&t=7"
    )
    expect(runLink("r_1", { t: -3 }).search.t).toBeUndefined()
  })

  it("encodes the id as one path segment", () => {
    expect(href(BASE, runLink("a/b?x=1#frag"))).toBe(
      `${BASE}runs/a%2Fb%3Fx%3D1%23frag`
    )
    expect(href(BASE, runLink("../../evil"))).toBe(`${BASE}runs/..%2F..%2Fevil`)
    expect(new URL(href(BASE, runLink("javascript:alert(1)"))).protocol).toBe(
      "http:"
    )
  })

  it("writes a search string the router reads back as the same string", () => {
    // TanStack parses search values as JSON: a span id of digits
    // would arrive as a number unless quoted.
    const u = new URL(href(BASE, runLink("r_1", { span: "123" })))
    expect(u.searchParams.get("sel")).toBe("t:123")
    const s = new URL(href(BASE, traceLink("tr", { span: "123" })))
    expect(s.searchParams.get("span")).toBe('"123"')
    expect(JSON.parse(s.searchParams.get("span")!)).toBe("123")
  })
})

describe("the other builders", () => {
  it("sessionLink is the session page", () => {
    expect(sessionLink("s_01")).toEqual({
      to: "/sessions/$id",
      params: { id: "s_01" },
      search: {},
    })
    expect(href(BASE, sessionLink("s/01"))).toBe(`${BASE}sessions/s%2F01`)
  })

  it("traceLink is the trace page, a span optionally selected", () => {
    expect(href(BASE, traceLink("0102"))).toBe(`${BASE}traces/0102`)
    expect(href(BASE, traceLink("0102", { span: "ab12" }))).toBe(
      `${BASE}traces/0102?span=ab12`
    )
  })

  it("playgroundLink carries the hand-off in the fragment, in a fixed order", () => {
    const link = playgroundLink({
      run: "r_1",
      step: 2,
      instructions: "be brief",
      engine: "scripted",
    })
    expect(link.search).toEqual({})
    const u = new URL(href(BASE, link))
    expect(u.pathname).toBe("/studio/playground")
    expect(u.search).toBe("")
    expect(u.hash).toBe("#run=r_1&step=2&instructions=be+brief&engine=scripted")
    expect(href(BASE, playgroundLink())).toBe(`${BASE}playground`)
    // A step that is not an ordinal, an empty value: not carried.
    expect(
      new URL(href(BASE, playgroundLink({ run: "r", step: -1, model: "" })))
        .hash
    ).toBe("#run=r")
  })

  it("experimentLink is the saved experiment in the playground's history", () => {
    expect(experimentLink("exp_1")).toEqual({
      to: "/playground",
      search: { experiment: "exp_1" },
    })
    expect(href(BASE, experimentLink("exp_1"))).toBe(
      `${BASE}playground?experiment=exp_1`
    )
  })
})

describe("compareLink", () => {
  it("is the compare page: the base run a and the runs compared with it, as the router reads them back", () => {
    expect(compareLink("r_1", ["r_2", "", "r_1", "r_2", "r_3"])).toEqual({
      to: "/compare",
      search: { a: "r_1", b: ["r_2", "r_3"] },
    })
    expect(compareLink("r_1")).toEqual({ to: "/compare", search: { a: "r_1" } })
    const u = new URL(href(BASE, compareLink("123", ["r/2"])))
    expect(u.pathname).toBe("/studio/compare")
    expect(JSON.parse(u.searchParams.get("a")!)).toBe("123")
    expect(JSON.parse(u.searchParams.get("b")!)).toEqual(["r/2"])
  })

  it("lands on a step: the ordinal, dropped when it is not one", () => {
    expect(compareLink("r_1", ["r_2"], 3)).toEqual({ to: "/compare", search: { a: "r_1", b: ["r_2"], step: 3 } })
    expect(compareLink("r_1", ["r_2"], -1).search).toEqual({ a: "r_1", b: ["r_2"] })
    expect(compareLink("r_1", [], 1.5).search).toEqual({ a: "r_1" })
    expect(new URL(href(BASE, compareLink("r_1", ["r_2"], 3))).searchParams.get("step")).toBe("3")
  })
})

describe("no link ever carries a token", () => {
  it("whatever the base or the values, no builder writes token=", () => {
    const links = [
      runLink("r_1", { step: 1, call: "c", view: "story", axis: "time", t: 3 }),
      runLink("r_1", { span: "s", resumed: true, call: "c" }),
      sessionLink("s_1"),
      traceLink("t_1", { span: "s" }),
      playgroundLink({
        run: "r_1",
        step: 1,
        instructions: "x",
        agent: "a",
        runtime: "rt",
      }),
      experimentLink("e_1"),
      compareLink("r_1", ["r_2"]),
    ]
    for (const l of links) {
      // A base the token was handed over on (S4.6's ?token= / #token=)
      // is resolved against, never copied: the base's query and
      // fragment are not the link's.
      for (const base of [
        BASE,
        `${BASE}?token=sekrit`,
        `${BASE}#token=sekrit`,
      ]) {
        const u = href(base, l)
        expect(u).not.toMatch(/token/i)
        expect(u).not.toContain("sekrit")
      }
    }
  })
})

describe("G2: page state as search keys", () => {
  it("the trace page's span and view", () => {
    expect(path(traceLink("t_1", { span: "ab12", view: "chat" }))).toBe(
      "traces/t_1?span=ab12&view=chat"
    )
    // The tree is the default view: never written.
    expect(traceLink("t_1", { view: "tree" }).search).toEqual({})
  })

  it("the raw view's filters and open event", () => {
    expect(
      path(runLink("r_1", { view: "raw", raw: { q: "refund", hide: ["delta", "step"], ev: 4 } }))
    ).toBe("runs/r_1?view=raw&q=refund&hide=step%2Cdelta&ev=4")
    // Empty state writes nothing; a bad position is dropped.
    expect(runLink("r_1", { raw: { q: "", hide: [], ev: -1 } }).search).toEqual({})
    // rawSearch names every key, so a merge clears what was turned off.
    expect(rawSearch({})).toEqual({ q: undefined, hide: undefined, ev: undefined })
  })

  it("reads the raw state back as the router parsed it", () => {
    expect(rawFromSearch({ q: 42, hide: "delta,bogus,step", ev: 3 })).toEqual({
      q: "42",
      hide: ["step", "delta"],
      ev: 3,
    })
    expect(rawFromSearch({ ev: 1.5 })).toEqual({ q: "", hide: [], ev: undefined })
    // A search of digits survives the router's JSON round trip.
    const u = new URL(href(BASE, runLink("r_1", { raw: { q: "123" } })))
    expect(u.searchParams.get("q")).toBe('"123"')
  })

  it("the playground's own state rides the query, never a prompt", () => {
    expect(
      path(playgroundStateLink({ run: "r_1", step: 2, agent: "orders", runtime: "rt_1", engine: "scripted" }))
    ).toBe("playground?run=r_1&step=2&agent=orders&runtime=rt_1&engine=scripted")
    // Step 0 and the live engine are the defaults: never written.
    expect(playgroundSearch({ run: "r_1", step: 0, engine: "live" })).toEqual({ run: "r_1" })
    expect(playgroundSearch({}, "exp_1")).toEqual({ experiment: "exp_1" })
    expect(playgroundStateLink({ run: "r_1" })).not.toHaveProperty("hash")
  })
})

describe("canonical (the copy link)", () => {
  it("keeps the page's state and drops the fragment", () => {
    expect(canonical(`${BASE}runs/r_1?step=2&sel=c%3A2%3Ac1#token=abc`)).toBe(
      `${BASE}runs/r_1?step=2&sel=c%3A2%3Ac1`
    )
  })

  it("never carries a token or a panel scope", () => {
    const u = canonical(`${BASE}runs/r_1?token=sekrit&view=raw&sig=x&weft_scope=pub_1&access_token=y`)
    expect(u).toBe(`${BASE}runs/r_1?view=raw`)
  })

  it("never carries prompt text, even from an old hand-off's query", () => {
    const u = canonical(
      `${BASE}playground?run=r_1&instructions=You%20are%20careful.&input=refund%20order%2042&agent=orders`
    )
    expect(u).toBe(`${BASE}playground?run=r_1&agent=orders`)
  })

  it("leaves a query with nothing to drop byte for byte", () => {
    const u = `${BASE}traces/t_1?span=%22123%22&view=chat`
    expect(canonical(u)).toBe(u)
  })
})
