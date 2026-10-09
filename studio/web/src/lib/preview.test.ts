// The preview's rows (plan F2) from studio/testdata/api/playground-
// preview.golden.json — the server's diff read, never re-derived.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import { messageLine, previewView } from "./preview"
import type { PreviewDoc } from "./preview"

const PREVIEW = JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/api/playground-preview.golden.json"), "utf8")) as PreviewDoc

describe("previewView", () => {
  it("aligns the rows by the server's ops, a changed row with both sides", () => {
    const v = previewView(PREVIEW)
    expect(v.rows!.map((r) => [r.op, r.role])).toEqual([
      ["changed", "user"],
      ["changed", "assistant"],
      ["same", "tool"],
      ["added", "user"],
      ["same", "assistant"],
      ["changed", "tool"],
      ["added", "user"],
    ])
    expect(v.rows![0]).toEqual({ op: "changed", role: "user", was: "where are orders 42 and 43?", will: "where are orders 7 and 43?" })
    expect(v.rows![1].will).toBe('lookup_order({"order_id":"7"})')
    expect(v.rows![2]).toEqual({ op: "same", role: "tool", was: "", will: "c1 → order 42 shipped" })
    expect(v.rows![3]).toEqual({ op: "added", role: "user", was: "", will: "also check 43" })
    expect(v.system).toBe("changed")
    expect(v.changed).toEqual(["params"])
    expect(v.added).toEqual([])
    expect(v.holes).toEqual([])
    expect(v.warnings.map((w) => w.kind)).toEqual(["prepare_step", "instructions"])
    expect(v.unchecked).toBe("")
  })

  it("says what is hidden as its badge, and what only a runtime checks", () => {
    const hidden: PreviewDoc = {
      ...PREVIEW,
      will_send: { ...PREVIEW.will_send, system: null, system_badge: "hidden", tools: null, tools_badge: "hidden", messages: null, messages_badge: "hidden" },
      was_sent: { ...PREVIEW.was_sent, system: null, system_badge: "hidden", tools: null, tools_badge: "hidden", badge: "derived" },
      diff: { ...PREVIEW.diff, system: "hidden", tools: null, messages: null },
      unchecked: ["model", "max_steps"],
    }
    const v = previewView(hidden)
    expect(v.rows).toBeNull()
    expect(v.system).toBe("hidden")
    expect(v.holes.map((h) => h.hole)).toEqual(["hidden", "derived"])
    expect(v.unchecked).toBe("unchecked until a runtime registers acme-support: model, max_steps")
  })

  it("a message in one line", () => {
    expect(messageLine({ role: "tool", content: [{ type: "tool_result", call_id: "c1", name: "x", content: "y".repeat(200), is_error: false }] })).toHaveLength(160)
    expect(messageLine(undefined)).toBe("")
  })
})
