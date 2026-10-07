// The client's contract edges (S4.3/S4.6): the token reaches the API
// however the reader supplied it, every URL resolves under the mount,
// and a transcript body is read as data — never trusted to be shaped.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { FakeStudio } from "../test/fake-studio"
import {
  adoptTokenFromLocation,
  ApiError,
  asTranscript,
  exportUrl,
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
  it("adopts ?token= from the page URL and strips it", () => {
    window.history.replaceState(null, "", "/runs?token=dev-secret&agent=orders")
    expect(adoptTokenFromLocation()).toBe(true)
    expect(studioToken()).toBe("dev-secret")
    expect(window.location.search).toBe("?agent=orders")
  })

  it("adopts #token= (never sent to the server) and strips it", () => {
    window.history.replaceState(null, "", "/runs#token=frag-secret")
    expect(adoptTokenFromLocation()).toBe(true)
    expect(studioToken()).toBe("frag-secret")
    expect(window.location.hash).toBe("")
  })

  it("leaves a URL without a token alone", () => {
    window.history.replaceState(null, "", "/playground?run=r1#t=3")
    expect(adoptTokenFromLocation()).toBe(false)
    expect(studioToken()).toBe("")
    expect(window.location.search + window.location.hash).toBe("?run=r1#t=3")
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
        ok({ error: { code: "unavailable", message: "runtime rt_1 is not connected" } }, 503)
      )
    )
    const err = await putBreakpoints("rt_1", []).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(503)
    expect((err as ApiError).message).toBe("runtime rt_1 is not connected")
  })

  it("carries limit and tags in the runs query", () => {
    expect(runsSearch({ playground: true, public_id: "pub_1", limit: 500 })).toBe(
      "?public_id=pub_1&playground=true&limit=500"
    )
  })
})

describe("exportUrl", () => {
  afterEach(() => {
    setStudioToken("")
    document.querySelector("base")?.remove()
  })

  it("links a run's export under the mount, the id encoded, the token as ?token=", () => {
    const base = document.createElement("base")
    base.href = "/studio/"
    document.head.appendChild(base)
    const plain = new URL(exportUrl("r_1/1/c_sub", "wefttest"))
    expect(plain.pathname).toBe("/studio/api/runs/r_1%2F1%2Fc_sub/export")
    expect(plain.searchParams.get("format")).toBe("wefttest")
    expect(plain.searchParams.has("token")).toBe(false)

    // A download link cannot carry a bearer header.
    setStudioToken("tok")
    const walled = new URL(exportUrl("r_1", "jsonl"))
    expect(walled.searchParams.get("format")).toBe("jsonl")
    expect(walled.searchParams.get("token")).toBe("tok")
  })
})

describe("asTranscript", () => {
  it("reads any stored body as a list of messages", () => {
    const doc = asTranscript({
      batches: [
        { index: 0, step: 0, messages: "a bare JSON string" },
        { index: 1, step: 0, messages: null },
        { index: 2, step: 0, messages: [null, 3, { role: "assistant", content: null }] },
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
    expect(nextCursor({ next_before: null, next_before_id: "x" })).toBeUndefined()
    expect(nextCursor({ next_before: t })).toEqual({ before: t })
    expect(nextCursor({ next_before: t, next_before_id: null })).toEqual({ before: t })
    const a = nextCursor({ next_before: t, next_before_id: "r_a" })
    expect(a).toEqual({ before: t, before_id: "r_a" })
    expect(nextCursor({ next_before: t, next_before_id: "r_b" }, a)).toEqual({ before: t, before_id: "r_b" })
    expect(nextCursor({ next_before: t, next_before_id: "r_a" }, a)).toBeUndefined()
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
    const fake = new FakeStudio().withSteps("r_steps", ["0", "1", "2"]).install()
    const s0 = await fetchStep("r_steps", 0)
    expect(fake.calls("GET runs/r_steps/steps/0")).toHaveLength(1)
    expect(s0.model).toMatchObject({ requested: "glm-a", answered: "glm-b" })
    expect(s0.attempts.map((a) => [a.model, a.outcome, a.error_type ?? ""])).toEqual([
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
    await expect(fetchStep("r_steps", 3)).rejects.toMatchObject({ status: 404, code: "not_found" })
  })

  it("carries every missing block as a badge", async () => {
    new FakeStudio()
      .withSteps("old1", ["not-recorded"])
      .withSteps("r_off", ["stripped"])
      .withSteps("r_tok", ["0", "hidden"])
      .install()
    const old = await fetchStep("old1", 0)
    expect(old.request && !isRequestRow(old.request) && old.request.badge).toBe("not_recorded")
    expect(old.attempts_badge?.badge).toBe("not_recorded")
    expect(old.holes.map((h) => h.hole)).toEqual(["gap", "not_recorded", "derived"])
    const off = await fetchStep("r_off", 0)
    expect(off.messages_in.badge).toBe("stripped")
    expect(off.request && isRequestRow(off.request) && off.request.content).toBe("stripped")
    const hidden = await fetchStep("r_tok", 1)
    expect(hidden.request && !isRequestRow(hidden.request) && hidden.request.badge).toBe("hidden")
    expect(hidden.children).toHaveLength(1)
  })
})
