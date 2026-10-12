// G2: one title shape per page, through one helper.
import { describe, expect, it } from "vitest"

import {
  clip,
  NO_CONTENT,
  pageTitle,
  runTitle,
  sessionTitle,
  shortId,
  turnTitle,
} from "./title"

describe("pageTitle", () => {
  it.each([
    [
      { page: "run", id: "s_01J9ZKQ3M5X7AB-t3", status: "succeeded" },
      "run s_…-t3 · succeeded · weft studio",
    ],
    [
      { page: "run", id: "r_ok", status: "running" },
      "run r_ok · running · weft studio",
    ],
    [{ page: "run", id: "r_ok" }, "run r_ok · weft studio"],
    [
      {
        page: "run",
        id: "r_ok",
        status: "succeeded",
        agent: "director",
        title: "How many units of SKU A-17 are in stock right now?",
      },
      "director · How many units of SKU A-17 are in stock… · succeeded · weft studio",
    ],
    [
      { page: "run", id: "r_ok", status: "failed", title: NO_CONTENT },
      `${NO_CONTENT} · failed · weft studio`,
    ],
    [
      { page: "session", id: "s_1", title: "Acme support desk", turns: 9 },
      "Acme support desk · 9 turns · weft studio",
    ],
    [
      { page: "trace", id: "0af7651916cd43dd8448eb211c80319c" },
      "trace 0af76519…319c · weft studio",
    ],
    [{ page: "session", id: "s_1" }, "session s_1 · weft studio"],
    [
      { page: "compare", a: "r_a", b: ["r_b", "r_c"] },
      "compare r_a ↔ r_b ↔ r_c · weft studio",
    ],
    [{ page: "compare" }, "compare · weft studio"],
    [{ page: "playground" }, "playground · weft studio"],
    [
      { page: "playground", experiment: "exp_1" },
      "playground exp_1 · weft studio",
    ],
    [{ page: "runs" }, "runs · weft studio"],
    [{ page: "sessions" }, "sessions · weft studio"],
    [{ page: "live" }, "live · weft studio"],
    [{ page: "agents" }, "agents · weft studio"],
    [{ page: "settings" }, "settings · weft studio"],
  ] as const)("%o → %s", (place, want) => {
    expect(pageTitle(place)).toBe(want)
  })
})

describe("shortId", () => {
  it("keeps a short id whole", () => {
    expect(shortId("s_1-t3")).toBe("s_1-t3")
    expect(shortId("a".repeat(16))).toBe("a".repeat(16))
  })
  it("keeps a session turn's prefix and turn", () => {
    expect(shortId("s_01J9ZKQ3M5X7AB-t12")).toBe("s_…-t12")
  })
  it("keeps another long id's head and tail", () => {
    expect(shortId("run-0123456789abcdef")).toBe("run-0123…cdef")
  })
})

// U6: titles, not ids — what each row and page leads with.
describe("runTitle / sessionTitle / turnTitle", () => {
  const base = { id: "b885b219a8374c208a9e94cb861744e8", event_count: 6 }
  it("a run: its prompt's first line, else the agent, else content-off, else the id", () => {
    expect(
      runTitle({
        ...base,
        prompt_preview: "\n refund order 8812\nplease",
        message_count: 2,
      })
    ).toEqual({
      text: "refund order 8812",
      full: "\n refund order 8812\nplease",
      kind: "prompt",
    })
    expect(
      runTitle({ ...base, agent: "director", message_count: 2 })
    ).toMatchObject({
      text: "director",
      kind: "agent",
    })
    expect(
      runTitle({ ...base, agent: "director", message_count: 0 })
    ).toMatchObject({
      text: `director · ${NO_CONTENT}`,
      kind: "agent",
    })
    expect(runTitle({ ...base, message_count: 0 })).toMatchObject({
      text: NO_CONTENT,
      kind: "absent",
    })
    // A derived run stored no messages and is not content-off.
    expect(
      runTitle({ ...base, message_count: 0, derived: true })
    ).toMatchObject({ text: base.id, kind: "id" })
  })
  it("a session: its title, else the first prompt, else content-off (its first turn), else the id", () => {
    expect(
      sessionTitle({
        id: "s_1",
        title: "Acme support desk",
        first_prompt: "hi",
      }).text
    ).toBe("Acme support desk")
    expect(sessionTitle({ id: "s_1", first_prompt: "hi" })).toMatchObject({
      text: "hi",
      kind: "prompt",
    })
    expect(
      sessionTitle(
        { id: "s_1" },
        { id: "s_1-t1", message_count: 0, event_count: 4 }
      ).text
    ).toBe(NO_CONTENT)
    expect(sessionTitle({ id: "s_1" })).toMatchObject({
      text: "s_1",
      kind: "id",
    })
  })
  it("a resume turn: the previous turn parked and this one carries no prompt of its own", () => {
    const t1 = {
      id: "s-t1",
      turn: 1,
      pending: 1,
      prompt_preview: "book it",
      message_count: 2,
      event_count: 4,
    }
    const t2 = {
      id: "s-t2",
      turn: 2,
      pending: 0,
      prompt_preview: "book it",
      message_count: 2,
      event_count: 4,
    }
    expect(turnTitle(t1, t2)).toMatchObject({
      text: "resume of t1 · 1 decided call",
      kind: "resume",
    })
    expect(
      turnTitle({ ...t1, pending: 2 }, { ...t2, prompt_preview: "" }).text
    ).toBe("resume of t1 · 2 decided calls")
    // A new prompt after a park is that prompt; no park, no resume.
    expect(turnTitle(t1, { ...t2, prompt_preview: "cancel it" }).text).toBe(
      "cancel it"
    )
    expect(turnTitle({ ...t1, pending: 0 }, t2).text).toBe("book it")
    expect(turnTitle(undefined, t2).text).toBe("book it")
  })
  it("clip: the first line, a visible cut", () => {
    expect(clip("x".repeat(40))).toBe("x".repeat(40))
    expect(clip("x".repeat(41))).toBe(`${"x".repeat(39)}…`)
    expect(clip("é".repeat(50), 10)).toBe(`${"é".repeat(9)}…`)
    expect(clip("\n\n  two\nthree")).toBe("two")
  })
})
