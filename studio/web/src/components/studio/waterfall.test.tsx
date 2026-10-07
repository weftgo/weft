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
})
