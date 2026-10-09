// The notices' words and keys (plan H5): every toast Studio raises is
// worded here, keyed by the instance it reports, and raised once per
// instance — a re-render or a refetch never repeats one.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { LIVE_RETRIES } from "./live"
import { noticeKey, noticeText, notify, resetNotices } from "./notify"
import type { Notice } from "./notify"

const sonner = vi.hoisted(() => {
  const fn = Object.assign(vi.fn(), {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
    dismiss: vi.fn(),
  })
  return { toast: fn }
})
vi.mock("sonner", () => sonner)


const { toast } = sonner

beforeEach(() => resetNotices())
afterEach(() => vi.clearAllMocks())

describe("noticeText: the words", () => {
  const cases: [Notice, string, string][] = [
    [{ kind: "experiment", commandID: "cmd_1", runID: "pg_1", status: "succeeded" }, "experiment pg_1 finished · succeeded", "success"],
    [{ kind: "experiment", commandID: "cmd_1", runID: "pg_1", status: "failed" }, "experiment pg_1 finished · failed", "error"],
    [{ kind: "experiment", commandID: "cmd_1", runID: "", status: "succeeded" }, "experiment cmd_1 finished · succeeded", "success"],
    [
      { kind: "matrix", experimentID: "exp_1", commandIDs: ["a", "b", "c"], succeeded: 2, failed: 1, other: 0 },
      "experiment exp_1 finished · 2 succeeded, 1 failed",
      "error",
    ],
    [
      { kind: "matrix", experimentID: "exp_1", commandIDs: ["a", "b"], succeeded: 2, failed: 0, other: 0 },
      "experiment exp_1 finished · 2 succeeded",
      "success",
    ],
    [
      { kind: "matrix", experimentID: "exp_1", commandIDs: ["a", ""], succeeded: 1, failed: 0, other: 1 },
      "experiment exp_1 finished · 1 succeeded, 1 not run",
      "error",
    ],
    [{ kind: "parked", runID: "pg_1", tools: ["refund"] }, "run pg_1 parked at refund", "info"],
    [{ kind: "parked", runID: "pg_1", tools: ["refund", "lookup_order"] }, "run pg_1 parked at refund, lookup_order", "info"],
    [{ kind: "runtime-connected", runtimeID: "rt_1", service: "acme-api" }, "runtime acme-api (rt_1) connected", "success"],
    [{ kind: "runtime-disconnected", runtimeID: "rt_1", service: "" }, "runtime rt_1 disconnected", "error"],
    [{ kind: "live-gave-up", stream: 1, round: 1, retry: () => {} }, "live updates stopped after 6 reconnects", "error"],
    [{ kind: "copy-link", ok: true, n: 1 }, "link copied", "success"],
    [{ kind: "copy-link", ok: false, n: 2 }, "copy failed — the browser refused the clipboard", "error"],
  ]
  for (const [n, title, tone] of cases)
    it(`${n.kind}: ${title}`, () => {
      expect(noticeText(n)).toEqual({ title, tone })
    })

  it("says the client's own reconnect count", () => {
    expect(LIVE_RETRIES).toBe(6)
  })
})

describe("noticeKey: one instance, one key", () => {
  it("keys by the command, the matrix's commands, the run, the runtime, the stream's give-up, the press", () => {
    expect(noticeKey({ kind: "experiment", commandID: "cmd_1", runID: "pg_1", status: "succeeded" })).toBe("experiment:cmd_1")
    // The matrix is its commands, in any order.
    expect(
      noticeKey({ kind: "matrix", experimentID: "e", commandIDs: ["b", "a"], succeeded: 2, failed: 0, other: 0 })
    ).toBe(noticeKey({ kind: "matrix", experimentID: "e", commandIDs: ["a", "b"], succeeded: 1, failed: 1, other: 0 }))
    expect(noticeKey({ kind: "parked", runID: "pg_1", tools: ["a"] })).toBe("parked:pg_1")
    expect(noticeKey({ kind: "runtime-connected", runtimeID: "rt_1", service: "" })).not.toBe(
      noticeKey({ kind: "runtime-disconnected", runtimeID: "rt_1", service: "" })
    )
    expect(noticeKey({ kind: "live-gave-up", stream: 3, round: 1, retry: () => {} })).not.toBe(
      noticeKey({ kind: "live-gave-up", stream: 3, round: 2, retry: () => {} })
    )
    expect(noticeKey({ kind: "copy-link", ok: true, n: 1 })).not.toBe(noticeKey({ kind: "copy-link", ok: true, n: 2 }))
  })
})

describe("notify", () => {
  it("raises one toast per instance, never again for the same key", () => {
    const n: Notice = { kind: "experiment", commandID: "cmd_1", runID: "pg_1", status: "succeeded" }
    expect(notify(n)).toBe(true)
    expect(notify({ ...n })).toBe(false)
    expect(notify({ ...n, commandID: "cmd_2" })).toBe(true)
    expect(toast.success).toHaveBeenCalledTimes(2)
    expect(toast.success.mock.calls[0][0]).toBe("experiment pg_1 finished · succeeded")
    expect(toast.success.mock.calls[0][1]).toMatchObject({ id: "experiment:cmd_1" })
  })

  it("keeps an actionable notice up; lets the rest go", () => {
    notify({ kind: "parked", runID: "pg_1", tools: ["refund"] })
    notify({ kind: "live-gave-up", stream: 1, round: 1, retry: () => {} })
    notify({ kind: "runtime-connected", runtimeID: "rt_1", service: "" })
    expect(toast.info.mock.calls[0][1]).toMatchObject({ duration: Infinity })
    expect(toast.error.mock.calls[0][1]).toMatchObject({ duration: Infinity, id: "live-gave-up" })
    expect(toast.success.mock.calls[0][1]).toMatchObject({ duration: undefined })
  })

  it("shows every copy press under one toast id: the next replaces it, never stacks", () => {
    notify({ kind: "copy-link", ok: true, n: 1 })
    notify({ kind: "copy-link", ok: false, n: 2 })
    expect(toast.success.mock.calls[0][1]).toMatchObject({ id: "copy-link" })
    expect(toast.error.mock.calls[0][1]).toMatchObject({ id: "copy-link" })
  })

  it("never words a token or a prompt: ids, tool names and counts only", () => {
    const all: Notice[] = [
      { kind: "experiment", commandID: "c", runID: "r", status: "succeeded" },
      { kind: "parked", runID: "r", tools: ["t"] },
      { kind: "copy-link", ok: true, n: 1 },
    ]
    for (const n of all) expect(noticeText(n).title).not.toMatch(/token|Bearer|weft_pt\./)
  })
})
