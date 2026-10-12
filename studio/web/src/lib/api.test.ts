// The client's contract edges (S4.3/S4.6): the token reaches the API
// however the reader supplied it, every URL resolves under the mount,
// and a transcript body is read as data — never trusted to be shaped.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { FakeStudio } from "../test/fake-studio"
import {
  adoptTokenFromLocation,
  ApiError,
  clearTabToken,
  tabTokenActive,
  tabTokenDropped,
  asTranscript,
  fetchRuns,
  fetchStep,
  isRequestRow,
  nextCursor,
  putBreakpoints,
  runsSearch,
  setStudioToken,
  studioToken,
} from "./api"

const ok = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })

describe("the token (S4.6, setup B)", () => {
  beforeEach(() => setStudioToken(""))
  afterEach(() => {
    setStudioToken("")
    window.history.replaceState(null, "", "/")
  })

  // The studio binary prints a dev token and serves the UI open: the
  // only way in used to be typing localStorage.setItem in the console.
  // A token in the query already reached Studio in a URL when the page
  // loaded (plan C5): it is stripped from the address bar, never kept.
  it("strips ?token= from the page URL without adopting it", () => {
    window.history.replaceState(null, "", "/runs?token=dev-secret&agent=orders")
    expect(adoptTokenFromLocation()).toBe(false)
    expect(studioToken()).toBe("")
    expect(window.location.search).toBe("?agent=orders")
  })

  it("adopts the fragment's token and strips both forms when a link carries both", () => {
    window.history.replaceState(
      null,
      "",
      "/runs?token=query-secret&agent=a#token=frag-secret&t=1"
    )
    expect(adoptTokenFromLocation()).toBe(true)
    expect(studioToken()).toBe("frag-secret")
    expect(window.location.search).toBe("?agent=a")
    expect(window.location.hash).toBe("#t=1")
  })

  it("adopts #token= (never sent to the server) and strips it", () => {
    window.history.replaceState(null, "", "/runs#token=frag-secret")
    expect(adoptTokenFromLocation()).toBe(true)
    expect(studioToken()).toBe("frag-secret")
    expect(window.location.hash).toBe("")
  })

  // The hand-off (/api/panel-handoff) lands with a 5-minute read token
  // scoped to one public id: it serves this tab alone and never
  // replaces the stored dev token, which every other tab keeps using.
  it("uses a panel token from the fragment for this tab only, the stored token kept", () => {
    const claims = btoa(
      JSON.stringify({ scope: "read", pub: "pub_1" })
    ).replace(/=+$/, "")
    const panel = `weft_pt.${claims}.sig`
    setStudioToken("dev-secret")
    window.history.replaceState(null, "", `/runs/r1#token=${panel}`)
    expect(adoptTokenFromLocation()).toBe(true)
    expect(studioToken()).toBe(panel)
    expect(localStorage.getItem("studio.token")).toBe("dev-secret")
    expect(window.location.hash).toBe("")
  })

  // Setup C's reader reloads within the token's 5 minutes: the tab
  // token is in this tab's sessionStorage, so a fresh module (a reload:
  // the fragment already stripped) still reads it; localStorage keeps
  // the dev token untouched.
  it("keeps a panel token from the fragment across a reload (sessionStorage), the stored token untouched", async () => {
    const claims = btoa(
      JSON.stringify({ scope: "read", pub: "pub_1" })
    ).replace(/=+$/, "")
    const panel = `weft_pt.${claims}.sig`
    setStudioToken("dev-secret")
    window.history.replaceState(null, "", `/runs/r1#token=${panel}`)
    expect(adoptTokenFromLocation()).toBe(true)
    vi.resetModules()
    const reloaded = await import("./api")
    expect(reloaded.adoptTokenFromLocation()).toBe(false)
    expect(reloaded.studioToken()).toBe(panel)
    expect(reloaded.tabTokenActive()).toBe(true)
    expect(localStorage.getItem("studio.token")).toBe("dev-secret")
    // Dropping it leaves the stored token serving.
    reloaded.clearTabToken()
    expect(reloaded.studioToken()).toBe("dev-secret")
    expect(sessionStorage.length).toBe(0)
  })

  it("holds the panel token in memory when sessionStorage throws", () => {
    const claims = btoa(
      JSON.stringify({ scope: "read", pub: "pub_1" })
    ).replace(/=+$/, "")
    const panel = `weft_pt.${claims}.sig`
    const origGet = Storage.prototype.getItem
    const origSet = Storage.prototype.setItem
    const get = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(function (this: Storage, k: string) {
        if (this === sessionStorage) throw new Error("denied")
        return origGet.call(this, k)
      })
    const set = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(function (this: Storage, k: string, v: string) {
        if (this === sessionStorage) throw new Error("denied")
        origSet.call(this, k, v)
      })
    try {
      window.history.replaceState(null, "", `/runs/r1#token=${panel}`)
      expect(adoptTokenFromLocation()).toBe(true)
      expect(studioToken()).toBe(panel)
      expect(tabTokenActive()).toBe(true)
      clearTabToken()
      expect(tabTokenActive()).toBe(false)
    } finally {
      get.mockRestore()
      set.mockRestore()
    }
  })

  it("stores a dev token from the fragment, replacing the stored one", () => {
    setStudioToken("old-secret")
    window.history.replaceState(null, "", "/runs#token=new-secret")
    expect(adoptTokenFromLocation()).toBe(true)
    expect(studioToken()).toBe("new-secret")
    expect(localStorage.getItem("studio.token")).toBe("new-secret")
  })

  it("leaves a URL without a token alone", () => {
    window.history.replaceState(null, "", "/playground?run=r1#t=3")
    expect(adoptTokenFromLocation()).toBe(false)
    expect(studioToken()).toBe("")
    expect(window.location.search + window.location.hash).toBe("?run=r1#t=3")
  })
})

describe("a hand-off token refused", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    setStudioToken("")
    window.history.replaceState(null, "", "/")
  })

  // The hand-off's read token lives 5 minutes; when Studio refuses it,
  // the tab falls back to the stored dev token instead of walling a
  // reader whose stored token is fine.
  it("a 401 under an active tab token drops it and retries with the stored dev token", async () => {
    const claims = btoa(
      JSON.stringify({ scope: "read", pub: "pub_1" })
    ).replace(/=+$/, "")
    const panel = `weft_pt.${claims}.sig`
    setStudioToken("dev-secret")
    window.history.replaceState(null, "", `/runs#token=${panel}`)
    adoptTokenFromLocation()
    const fetchMock = vi.fn(
      async (_u: RequestInfo | URL, init?: RequestInit) =>
        new Headers(init?.headers).get("Authorization") === "Bearer dev-secret"
          ? ok({ total: 0, runs: [], next_before: null })
          : ok(
              {
                error: {
                  code: "unauthorized",
                  message: "bad or expired token",
                },
              },
              401
            )
    )
    vi.stubGlobal("fetch", fetchMock)
    await fetchRuns({})
    const auth = fetchMock.mock.calls.map((c) =>
      new Headers(c[1]?.headers).get("Authorization")
    )
    expect(auth).toEqual([`Bearer ${panel}`, "Bearer dev-secret"])
    expect(tabTokenActive()).toBe(false)
    expect(tabTokenDropped()).toBe(true)
    expect(studioToken()).toBe("dev-secret")
    expect(localStorage.getItem("studio.token")).toBe("dev-secret")
  })

  it("two requests refused together under the tab token both retry with the stored token", async () => {
    const claims = btoa(
      JSON.stringify({ scope: "read", pub: "pub_1" })
    ).replace(/=+$/, "")
    const panel = `weft_pt.${claims}.sig`
    setStudioToken("dev-secret")
    window.history.replaceState(null, "", `/runs#token=${panel}`)
    adoptTokenFromLocation()
    const fetchMock = vi.fn(
      async (_u: RequestInfo | URL, init?: RequestInit) =>
        new Headers(init?.headers).get("Authorization") === "Bearer dev-secret"
          ? ok({ total: 0, runs: [], next_before: null })
          : ok(
              {
                error: {
                  code: "unauthorized",
                  message: "bad or expired token",
                },
              },
              401
            )
    )
    vi.stubGlobal("fetch", fetchMock)
    const settled = await Promise.allSettled([fetchRuns({}), fetchRuns({})])
    expect(settled.map((s) => s.status)).toEqual(["fulfilled", "fulfilled"])
    const auth = fetchMock.mock.calls.map((c) =>
      new Headers(c[1]?.headers).get("Authorization")
    )
    expect(auth.filter((a) => a === `Bearer ${panel}`)).toHaveLength(2)
    expect(auth.filter((a) => a === "Bearer dev-secret")).toHaveLength(2)
    expect(tabTokenActive()).toBe(false)
    expect(studioToken()).toBe("dev-secret")
  })

  it("with no stored token the 401 stands and the tab token is dropped", async () => {
    const claims = btoa(
      JSON.stringify({ scope: "read", pub: "pub_1" })
    ).replace(/=+$/, "")
    const panel = `weft_pt.${claims}.sig`
    window.history.replaceState(null, "", `/runs#token=${panel}`)
    adoptTokenFromLocation()
    const fetchMock = vi.fn(async () =>
      ok(
        { error: { code: "unauthorized", message: "bad or expired token" } },
        401
      )
    )
    vi.stubGlobal("fetch", fetchMock)
    await expect(fetchRuns({})).rejects.toMatchObject({ status: 401 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(tabTokenActive()).toBe(false)
    expect(tabTokenDropped()).toBe(true)
  })
})

describe("requests under the mount and the wall", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    setStudioToken("")
    document.querySelector("base")?.remove()
  })

  it("resolves every route under <base href> (setup A's /studio/)", async () => {
    const base = document.createElement("base")
    base.href = "/studio/"
    document.head.appendChild(base)
    setStudioToken("tok")
    const fetchMock = vi.fn(async (_u: RequestInfo | URL, _i?: RequestInit) =>
      ok({ total: 0, runs: [], next_before: null })
    )
    vi.stubGlobal("fetch", fetchMock)
    await fetchRuns({ agent: "orders" })
    expect(new URL(String(fetchMock.mock.calls[0][0])).pathname).toBe(
      "/studio/api/runs"
    )

    fetchMock.mockImplementation(async () => ok({ tools: ["refund"] }))
    await putBreakpoints("rt_1", ["refund"])
    const [url, init] = fetchMock.mock.calls[1]
    // The control used to PUT api/api/runtimes/…: a 404 nobody read.
    expect(new URL(String(url)).pathname).toBe(
      "/studio/api/runtimes/rt_1/breakpoints"
    )
    expect(init?.method).toBe("PUT")
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer tok")
    expect(init?.body).toBe(JSON.stringify({ tools: ["refund"] }))
  })

  it("surfaces a refused write as an ApiError with the server's words", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        ok(
          {
            error: {
              code: "unavailable",
              message: "runtime rt_1 is not connected",
            },
          },
          503
        )
      )
    )
    const err = await putBreakpoints("rt_1", []).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(503)
    expect((err as ApiError).message).toBe("runtime rt_1 is not connected")
  })

  it("carries limit and tags in the runs query", () => {
    expect(
      runsSearch({ playground: true, public_id: "pub_1", limit: 500 })
    ).toBe("?public_id=pub_1&playground=true&limit=500")
  })
})

describe("asTranscript", () => {
  it("reads any stored body as a list of messages", () => {
    const doc = asTranscript({
      batches: [
        { index: 0, step: 0, messages: "a bare JSON string" },
        { index: 1, step: 0, messages: null },
        {
          index: 2,
          step: 0,
          messages: [null, 3, { role: "assistant", content: null }],
        },
        {
          index: 3,
          step: 0,
          input: true,
          messages: [{ role: "user", content: [{ type: "text", text: "hi" }] }],
        },
      ],
    })
    expect(doc.batches.map((b) => b.messages.length)).toEqual([0, 0, 1, 1])
    expect(doc.batches[2].messages[0].content).toEqual([])
    expect(doc.batches[3].input).toBe(true)
    expect(doc.batches[0].input).toBeUndefined()
  })

  it("survives a document with no batches", () => {
    expect(asTranscript({} as never).batches).toEqual([])
  })

  // The exact cursor (the server's next_before_id): a group of rows
  // sharing one timestamp is paged by id, so inside it the time stays
  // and only the id moves — that must not read as a stuck cursor.
  it("pages on the exact cursor and ends only when the pair stops moving", () => {
    const t = "2026-10-01T09:00:00.5Z"
    expect(
      nextCursor({ next_before: null, next_before_id: "x" })
    ).toBeUndefined()
    expect(nextCursor({ next_before: t })).toEqual({ before: t })
    expect(nextCursor({ next_before: t, next_before_id: null })).toEqual({
      before: t,
    })
    const a = nextCursor({ next_before: t, next_before_id: "r_a" })
    expect(a).toEqual({ before: t, before_id: "r_a" })
    expect(nextCursor({ next_before: t, next_before_id: "r_b" }, a)).toEqual({
      before: t,
      before_id: "r_b",
    })
    expect(
      nextCursor({ next_before: t, next_before_id: "r_a" }, a)
    ).toBeUndefined()
    expect(runsSearch({ before: t, before_id: "r_a" })).toBe(
      `?before=${encodeURIComponent(t)}&before_id=r_a`
    )
    // An id without its time is not a cursor.
    expect(runsSearch({ before_id: "r_a" })).toBe("")
  })
})

// GET runs/{id}/steps/{n} (plan A7): the client reads the server's own
// goldens — every block present or badged, the request a row or a hole.
describe("fetchStep", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("reads a step whole: attempts, the answering model, children, compaction", async () => {
    const fake = new FakeStudio()
      .withSteps("r_steps", ["0", "1", "2"])
      .install()
    const s0 = await fetchStep("r_steps", 0)
    expect(fake.calls("GET runs/r_steps/steps/0")).toHaveLength(1)
    expect(s0.model).toMatchObject({ requested: "glm-a", answered: "glm-b" })
    expect(
      s0.attempts.map((a) => [a.model, a.outcome, a.error_type ?? ""])
    ).toEqual([
      ["glm-a", "error", "stream_idle"],
      ["glm-b", "error", "stream_idle"],
      ["glm-a", "error", "stream_idle"],
      ["glm-b", "ok", ""],
    ])
    expect(s0.request && isRequestRow(s0.request)).toBe(true)
    const s1 = await fetchStep("r_steps", 1)
    expect(s1.children.map((c) => c.id)).toEqual(["r_steps/1/c_sub"])
    expect(s1.tool_calls[0].child_run_id).toBe("r_steps/1/c_sub")
    const s2 = await fetchStep("r_steps", 2)
    expect(s2.status).toBe("parked")
    expect(s2.compaction?.badge).toBe("compacted")
    expect(s2.holes.map((h) => h.hole)).toEqual(["compacted"])
    await expect(fetchStep("r_steps", 3)).rejects.toMatchObject({
      status: 404,
      code: "not_found",
    })
  })

  it("carries every missing block as a badge", async () => {
    new FakeStudio()
      .withSteps("old1", ["not-recorded"])
      .withSteps("r_off", ["stripped"])
      .withSteps("r_tok", ["0", "hidden"])
      .install()
    const old = await fetchStep("old1", 0)
    expect(old.request && !isRequestRow(old.request) && old.request.badge).toBe(
      "not_recorded"
    )
    expect(old.attempts_badge?.badge).toBe("not_recorded")
    expect(old.holes.map((h) => h.hole)).toEqual([
      "gap",
      "not_recorded",
      "derived",
    ])
    const off = await fetchStep("r_off", 0)
    expect(off.messages_in.badge).toBe("stripped")
    expect(
      off.request && isRequestRow(off.request) && off.request.content
    ).toBe("stripped")
    const hidden = await fetchStep("r_tok", 1)
    expect(
      hidden.request && !isRequestRow(hidden.request) && hidden.request.badge
    ).toBe("hidden")
    expect(hidden.children).toHaveLength(1)
  })
})
