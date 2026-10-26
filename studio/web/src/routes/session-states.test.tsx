// U16 (UX-B-15, UX-B-16, UX-B-28, UX-F-02): the sessions list's states
// — "⏸ awaiting a decision" on a session whose newest turn is an open
// park, the outcome strip, failed turns red and busy-policy ends
// neutral ("2✗ interrupted" on the warroom shape: interrupts are not
// failures), exchanges beside turns, a fork's lineage, the state filter
// chips — the session header's "decide…" and its conversation link, and
// the conversation page /conversations/$id: every run of a public id
// with its experiments nested, for a public id no session holds.
import { cleanup, configure, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunRow, RunsPage, SessionDoc, SessionRow } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"
import { AWAITING } from "@/lib/status"
import { CONVERSATION, splitConversation } from "@/lib/conversation"
import {
  awaitingWords,
  cancelledWords,
  endedWords,
  exchangesOfRow,
  failedWords,
  sinceOf,
  stripLabel,
} from "@/lib/session-states"
import { conversationLink, path } from "@/lib/links"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const row = (over: Partial<RunRow>): RunRow => ({ ...rOK, ...over })

const sessionRow = (over: Partial<SessionRow>): SessionRow => ({
  id: "s_x",
  public_id: "",
  agent: "acme-support",
  turns: 1,
  first_seen: rOK.started,
  last_seen: rOK.last_seen,
  status: "succeeded",
  usage: { input_tokens: 20, output_tokens: 10 },
  cost_usd: null,
  cost_known: false,
  ...over,
})

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", golden<Record<string, unknown>>("meta"))
    .on("GET runs", { total: 0, runs: [], next_before: null })
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

describe("the words (lib/session-states.ts)", () => {
  it("awaiting, failed apart from ended, exchanges, the strip's name", () => {
    expect(awaitingWords(1)).toBe(`⏸ ${AWAITING} · 1 call`)
    expect(failedWords(2)).toBe("2✗ failed")
    expect(endedWords(2)).toBe("2✗ ended by a newer message")
    expect(failedWords(0) + endedWords(undefined)).toBe("")
    expect(cancelledWords(1)).toBe("1⊘ cancelled")
    expect(cancelledWords(0)).toBe("")
    expect(sinceOf("1h", Date.parse("2026-10-01T10:00:00Z"))).toBe(
      "2026-10-01T09:00:00.000Z"
    )
    expect(sinceOf(undefined)).toBeUndefined()
    expect(exchangesOfRow({ turns: 5, resumes: 2 })).toBe(
      "3 exchanges · 5 turns"
    )
    expect(exchangesOfRow({ turns: 1 })).toBe("1 turn")
    expect(stripLabel({ turns: 3, outcomes: ["succeeded", "ended"] })).toBe(
      "outcomes of the newest 2 turns, oldest first: succeeded, ended by a newer message — 1 older not shown"
    )
  })
  it("conversationLink is the public id's page, the id one segment", () => {
    expect(path(conversationLink("arena:cli-1:rw.support-desk"))).toBe(
      "conversations/arena%3Acli-1%3Arw.support-desk"
    )
  })
  it("splitConversation: turns oldest first, experiments under their source, orphans apart", () => {
    const t1 = row({ id: "a", started: "2026-10-01T09:00:00Z" })
    const t2 = row({ id: "b", started: "2026-10-01T09:05:00Z" })
    const x = row({ id: "pg_1", playground: true, forked_from: "a#1" })
    const o = row({ id: "pg_2", playground: true, forked_from: "zz#0" })
    const s = splitConversation([t2, x, t1, o])
    expect(s.turns.map((r) => r.id)).toEqual(["a", "b"])
    expect(s.experiments.get("a")?.map((r) => r.id)).toEqual(["pg_1"])
    expect(s.orphans.map((r) => r.id)).toEqual(["pg_2"])
  })
})

describe("the sessions list's states", () => {
  it("⏸ on a parked session, the strip and the counts apart, a fork's lineage", async () => {
    studio.on("GET sessions", {
      total: 3,
      sessions: [
        sessionRow({ id: "s_park", pending: 1, outcomes: ["parked"] }),
        sessionRow({
          id: "s_war",
          agent: "incident-analyst",
          turns: 7,
          ended_turns: 2,
          outcomes: [
            "succeeded",
            "succeeded",
            "succeeded",
            "ended",
            "succeeded",
            "ended",
            "succeeded",
          ],
        }),
        sessionRow({
          id: "s_fork",
          turns: 4,
          resumes: 1,
          failed_turns: 1,
          cancelled_turns: 1,
          forked_from_session: "s_war#e9",
          outcomes: ["resumed", "succeeded", "failed", "succeeded"],
        }),
      ],
      next_before: null,
    })
    const { container } = renderApp("/sessions")
    await screen.findByText(awaitingWords(1))
    const war = container.querySelector('[data-session-ended="2"]')
    expect(war?.textContent).toBe("2✗ ended by a newer message")
    expect(war?.className).toContain("text-muted-foreground") // neutral, never red
    expect(
      container.querySelector('[data-session-failed="1"]')?.className
    ).toContain("text-status-bad")
    // The war room's strip: ✓✓✓✗✓✗✓ with the ends neutral.
    const strip = container.querySelector(
      '[data-session-strip^="succeeded,succeeded,succeeded,ended"]'
    )!
    expect(strip.textContent).toBe("✓✓✓✗✓✗✓")
    expect(strip.getAttribute("aria-label")).toContain(
      "ended by a newer message"
    )
    expect(screen.getByText("3 exchanges · 4 turns")).toBeTruthy()
    // A turn cancelled for another reason: apart from the failures,
    // neutral (U16-fix).
    const cancelled = container.querySelector('[data-session-cancelled="1"]')
    expect(cancelled?.textContent).toBe("1⊘ cancelled")
    expect(cancelled?.className).toContain("text-muted-foreground")
    expect(container.querySelector('[data-fork-of="s_war"]')).not.toBeNull()
  })

  it("the state chips filter on the server: awaiting, failed, delegated, forks", async () => {
    studio.on("GET sessions", { total: 0, sessions: [], next_before: null })
    renderApp("/sessions")
    const chip = await screen.findByRole("button", { name: `⏸ ${AWAITING}` })
    expect(chip.getAttribute("aria-pressed")).toBe("false")
    chip.click()
    await vi.waitFor(() =>
      expect(
        studio.requests.some(
          (r) => r.path === "sessions" && r.query.get("awaiting") === "1"
        )
      ).toBe(true)
    )
    ;(await screen.findByRole("button", { name: "⑃ forks" })).click()
    await vi.waitFor(() =>
      expect(
        studio.requests.some(
          (r) =>
            r.path === "sessions" &&
            r.query.get("lineage") === "fork" &&
            r.query.get("awaiting") === "1"
        )
      ).toBe(true)
    )
  })

  it("the compacted, status and since chips filter on the server too (U16-fix)", async () => {
    studio.on("GET sessions", { total: 0, sessions: [], next_before: null })
    renderApp("/sessions")
    const last = () =>
      studio.requests.filter((r) => r.path === "sessions").at(-1)!.query
    // One chip at a time: each a navigation, each read on the server.
    ;(await screen.findByRole("button", { name: "⌗ compacted" })).click()
    await vi.waitFor(() => expect(last().get("compacted")).toBe("1"))
    ;(await screen.findByRole("button", { name: "✗ failed" })).click()
    await vi.waitFor(() => expect(last().get("status")).toBe("failed"))
    ;(await screen.findByRole("button", { name: "last 24 h" })).click()
    await vi.waitFor(() => {
      const q = last()
      expect(q.get("compacted")).toBe("1")
      expect(q.get("status")).toBe("failed")
      const at = Date.parse(q.get("since") ?? "")
      expect(Math.abs(Date.now() - 86_400_000 - at)).toBeLessThan(60_000)
    })
    const since = await screen.findByRole("button", { name: "last 24 h" })
    expect(since.getAttribute("aria-pressed")).toBe("true")
  })
})

describe("the session header", () => {
  it("an open park at the newest turn: decide… to the parked turn; the public id opens the conversation", async () => {
    const t1 = row({ id: "s_p-t1", session_id: "s_p", turn: 1, pending: 1 })
    const doc: SessionDoc = {
      ...sessionRow({
        id: "s_p",
        public_id: "pub_p",
        pending: 1,
        outcomes: ["parked"],
      }),
      runs: [t1],
      delegated: [],
      delegated_usage: { input_tokens: 0, output_tokens: 0 },
    }
    studio.on("GET sessions/s_p", doc)
    const { container } = renderApp("/sessions/s_p")
    const decide = await screen.findByRole("link", { name: "decide…" })
    expect(decide.getAttribute("href")).toContain("/runs/s_p-t1")
    expect(
      container
        .querySelector('[data-conversation-link="pub_p"]')
        ?.getAttribute("href")
    ).toContain("/conversations/pub_p")
  })
})

describe("the conversation page", () => {
  it("a public id with no session: its runs newest first (the panel's order, UX-S3-16), an experiment nested, live", async () => {
    const pub = "arena:cli-1:rw.support-desk"
    const t1 = row({
      id: "r1",
      session_id: "",
      public_id: pub,
      started: "2026-10-01T09:00:00Z",
      pending: 1,
    })
    const t2 = row({
      id: "r2",
      session_id: "",
      public_id: pub,
      started: "2026-10-01T09:05:00Z",
      resumed_from: "r1",
    })
    const x = row({
      id: "pg_1",
      session_id: "",
      public_id: pub,
      playground: true,
      forked_from: "r1#0",
      started: "2026-10-01T09:06:00Z",
    })
    studio
      .on("GET runs", { total: 3, runs: [x, t2, t1], next_before: null })
      .on("GET sessions", { total: 0, sessions: [], next_before: null })
    const { container } = renderApp(`/conversations/${encodeURIComponent(pub)}`)
    await screen.findByRole("heading", { name: pub })
    await screen.findByText(CONVERSATION.noSession)
    // A resume folds into the exchange it continues (U16-fix).
    expect(
      container.querySelector("[data-conversation-counts]")?.textContent
    ).toContain("1 exchange · 2 turns · 1 experiment")
    const turns = [...container.querySelectorAll("[data-turn]")].map((n) =>
      n.getAttribute("data-turn")
    )
    expect(turns).toEqual(["r2", "r1"])
    expect(
      studio.requests.some(
        (r) => r.path === "runs" && r.query.get("public_id") === pub
      )
    ).toBe(true)
  })
})

describe("the conversation header's cost (U16-fix)", () => {
  it("own and delegated apart, never folded", async () => {
    studio
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: [
          ...golden<{ capabilities: string[] }>("meta").capabilities,
          "pricing",
        ],
      })
      .on("GET runs", {
        total: 2,
        runs: [
          row({
            id: "r2",
            session_id: "",
            public_id: "pub_c",
            started: "2026-10-01T09:05:00Z",
            cost_usd: 0.02,
            cost_known: true,
            delegated_cost_usd: 0.5,
            delegated_cost_known: true,
          }),
          row({
            id: "r1",
            session_id: "",
            public_id: "pub_c",
            started: "2026-10-01T09:00:00Z",
            cost_usd: 0.01,
            cost_known: true,
            delegated_cost_usd: 0,
            delegated_cost_known: true,
          }),
        ],
        next_before: null,
      })
      .on("GET sessions", { total: 0, sessions: [], next_before: null })
    const { container } = renderApp("/conversations/pub_c")
    await screen.findByRole("heading", { name: "pub_c" })
    await vi.waitFor(() =>
      expect(
        container.querySelector("[data-conversation-cost]")?.textContent
      ).toContain("own$0.03")
    )
    const del = container.querySelector("[data-conversation-delegated]")
    expect(del?.textContent).toContain("$0.50")
    expect(del?.textContent).toContain(CONVERSATION.delegated)
  })
})
