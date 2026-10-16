// The waterfall against the subagent golden: every span is a row,
// nested rows indent, the playhead veils, selection and seeking
// answer clicks and keys.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import type { EventsPage } from "@/lib/api"
import { fold } from "@/lib/events"
import { spansFromFold } from "@/lib/trace"
import type { Span } from "@/lib/trace"
import { Waterfall } from "@/components/studio/waterfall"

const events = (
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../testdata/api/events-sub.golden.json"),
      "utf8"
    )
  ) as EventsPage
).events.map((pe) => pe.event)
const spans = spansFromFold(fold(events), events.length, "succeeded")
// jsdom has no scrollIntoView.
Element.prototype.scrollIntoView = vi.fn()

describe("Waterfall", () => {
  it("renders one row per span with its range and badge", () => {
    render(
      <Waterfall
        spans={spans}
        domain={[0, events.length - 1]}
        playhead={null}
      />
    )
    expect(screen.getAllByRole("treeitem")).toHaveLength(spans.length)
    expect(screen.getByText("research")).toBeTruthy()
    expect(screen.getByText("2–3")).toBeTruthy() // the call's positions
    expect(screen.getByText("ok")).toBeTruthy()
    expect(screen.getByText("stop")).toBeTruthy()
  })

  it("selects on click and on arrow keys", () => {
    const onSelect = vi.fn()
    render(
      <Waterfall
        spans={spans}
        domain={[0, events.length - 1]}
        playhead={null}
        onSelect={onSelect}
        selectedId={spans[1].id}
      />
    )
    fireEvent.click(screen.getByText("research"))
    expect(onSelect).toHaveBeenLastCalledWith(spans[2])
    fireEvent.keyDown(screen.getByRole("tree"), { key: "ArrowDown" })
    expect(onSelect).toHaveBeenLastCalledWith(spans[2])
    fireEvent.keyDown(screen.getByRole("tree"), { key: "ArrowUp" })
    expect(onSelect).toHaveBeenLastCalledWith(spans[0])
  })

  it("folds a subtree", () => {
    render(
      <Waterfall
        spans={spans}
        domain={[0, events.length - 1]}
        playhead={null}
      />
    )
    // Fold the step: its call disappears.
    fireEvent.click(screen.getAllByLabelText("collapse")[1])
    expect(screen.getAllByRole("treeitem")).toHaveLength(spans.length - 1)
  })
})

// A row is hidden when an ancestor is folded — found by walking the
// parent chain. A trace whose spans name each other as parents (ingest
// stores what it is sent) made that walk spin forever and hung the
// tab; the walk must terminate on any parent graph.
describe("Waterfall on a cyclic parent chain", () => {
  it("renders and terminates", () => {
    const row = (id: string, parent: string): Span => ({
      id,
      key: id,
      parent,
      depth: 1,
      kind: "tool",
      label: id,
      tone: "tool",
      from: 0,
      to: 1,
      runId: "",
    })
    render(
      <Waterfall
        spans={[row("t:x", "t:y"), row("t:y", "t:x"), row("t:self", "t:self")]}
        domain={[0, 1]}
        playhead={null}
      />
    )
    expect(screen.getAllByRole("treeitem")).toHaveLength(3)
  })

  it("the time axis reads 'ms', humanised ticks and a duration per row (UX-A-22, UX-A-23)", () => {
    const row = (id: string, from: number, to: number): Span => ({
      id,
      key: id,
      depth: 0,
      kind: "tool",
      label: id,
      tone: "tool",
      from,
      to,
      runId: "",
    })
    render(
      <Waterfall
        spans={[row("t:a", 0, 7737), row("t:b", 1934, 1934.5)]}
        domain={[0, 7737]}
        playhead={null}
        unit="ms"
      />
    )
    // The unit as-is (no "MSS"), the axis in s/ms, durations not ranges.
    expect(screen.getByText("ms")).toBeTruthy()
    expect(screen.queryByText("mss")).toBeNull()
    expect(screen.getByText("7.7s", { selector: "span" })).toBeTruthy()
    expect(screen.getByText("7.7s", { selector: "div" })).toBeTruthy()
    expect(screen.getByText("500µs")).toBeTruthy()
    expect(screen.queryByText("0–7737")).toBeNull()
    // The first tick is the origin: "0", not "0µs".
    expect(screen.queryByText("0µs")).toBeNull()
    expect(screen.getByText("0", { selector: "span" })).toBeTruthy()
  })

  it("the position axis keeps its ranges, the unit pluralised once", () => {
    render(
      <Waterfall
        spans={spans}
        domain={[0, events.length - 1]}
        playhead={null}
      />
    )
    expect(screen.getByText("events")).toBeTruthy()
  })
})
