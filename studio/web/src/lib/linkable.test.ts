// The states U21.2 made linkable (UX-F-28) and the glossary's link
// (UX-A-20): each new parameter through lib/links.ts's builders, read
// back by its page's validator — and the connect lines (UX-F-11).
import { describe, expect, it } from "vitest"

import {
  agentsLink,
  conceptLink,
  experimentLink,
  liveLink,
  path,
  pickOf,
  sessionSearch,
  sessionViewLink,
} from "./links"
import { connectLines, studioURL } from "./connect"

describe("linkable states (U21.2)", () => {
  it("a session's turn: the table view, the turn; ?turn= alone opens the table", () => {
    expect(path(sessionViewLink("s_1", { turn: 4 }))).toBe(
      "sessions/s_1?view=table&turn=4"
    )
    expect(sessionSearch({ turn: "4" })).toEqual({ view: "table", turn: 4 })
    expect(sessionSearch({ turn: 4, view: "tree" })).toEqual({
      view: "tree",
      turn: 4,
    })
    expect(sessionSearch({ turn: "-1" })).toEqual({})
    expect(sessionSearch({ turn: "x" })).toEqual({})
  })

  it("an experiment's picked cells, two at most", () => {
    expect(path(experimentLink("exp_1", undefined, ["A×1", "B×2"]))).toBe(
      `experiments/exp_1?pick=${encodeURIComponent("A×1,B×2")}`
    )
    expect(pickOf("A×1,B×2,C×3")).toEqual(["B×2", "C×3"])
    expect(pickOf("A×1,junk,,B")).toEqual(["A×1"])
    expect(pickOf(3)).toEqual([])
  })

  it("the live page's filter and agent; the agents page's card; a concept", () => {
    expect(path(liveLink({ q: "failed", agent: "orders" }))).toBe(
      "live?q=failed&agent=orders"
    )
    expect(path(liveLink())).toBe("live")
    expect(path(agentsLink("orders"))).toBe("agents?agent=orders")
    expect(path(conceptLink("session"))).toBe("settings#concept-session")
    expect(path(conceptLink())).toBe("settings#concepts")
  })
})

describe("connect an app to this Studio (U21.2)", () => {
  it("is the page's base, without the slash", () => {
    expect(studioURL("http://127.0.0.1:7349/")).toBe("http://127.0.0.1:7349")
    expect(studioURL("https://host.example/studio/runs")).toBe(
      "https://host.example/studio"
    )
  })

  it("names the URL on every line, and the token's place, never a token", () => {
    const url = "http://127.0.0.1:7349"
    const open = connectLines(url)
    expect(open.map((l) => l.key)).toEqual(["app", "panel", "mcp"])
    for (const l of open) expect(l.code).toContain(url)
    expect(open[0].code).toContain(`WEFT_STUDIO_URL=${url}`)
    expect(open[1].code).toBe(
      `<script type="module" src="${url}/panel.js" data-weft data-endpoint="${url}/"></script>`
    )
    const walled = connectLines(url, true)
    expect(walled[1].code).toContain('data-token="<token>"')
    expect(walled[1].note).toContain("<db>.token")
    expect(walled[2].code).toContain("WEFT_STUDIO_TOKEN=<token>")
    // WEFT_STUDIO_URL turns discovery off: the token goes beside it.
    expect(walled[0].code).toContain("export WEFT_STUDIO_TOKEN=<token>")
    expect(open[0].code).not.toContain("WEFT_STUDIO_TOKEN")
    expect(open[0].note).toContain("WEFT_STUDIO_TOKEN")
    // Bare `weft open --with-token` prints the root signed in (B7).
    expect(walled[1].note).toContain("`weft open --with-token`")
    // The discovery file this Studio wrote, when meta names it (B7).
    expect(open[0].note).toContain("(studio.json)")
    const named = connectLines(url, false, "/run/user/1000/weft/studio.json")
    expect(named[0].note).toContain("(/run/user/1000/weft/studio.json)")
  })
})
