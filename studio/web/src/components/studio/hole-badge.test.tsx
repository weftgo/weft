// The run page's honesty badge (D5 review): the table's words are in
// the accessible text, not only the pointer's title; a cause's words
// win; the loop's cuts are the table's badges (truncated/result_cap,
// max_tokens with no fix for an unrun call).
import { cleanup, render, screen } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, describe, expect, it, vi } from "vitest"

import { HoleBadge } from "@/components/studio/hole-badge"
import { TruncationBadge } from "@/components/studio/truncation-badge"
import { UNRUN_CALL_REASON } from "@/lib/events"
import { CAUSES, HOLES, resultCapReason } from "@/lib/honesty"

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe("HoleBadge", () => {
  it("names itself by the label and describes itself by the reason and fix, said once", () => {
    const { container } = render(<HoleBadge hole="gap" />)
    const b = container.querySelector<HTMLElement>('[data-hole="gap"]')!
    const words = `${HOLES.gap.reason} — fix: ${HOLES.gap.fix}`
    expect(b.getAttribute("title")).toBe(words)
    // The accessible name is the label alone; the words are the
    // description, through aria-describedby (which a title never
    // doubles: a described element's title is not read again).
    expect(b.textContent).toBe(HOLES.gap.label)
    expect(screen.getByRole("generic", { description: words })).toBe(b)
    const desc = document.getElementById(b.getAttribute("aria-describedby")!)!
    expect(desc.hidden).toBe(true)
    expect(desc.textContent).toBe(words)
  })

  it("axe: zero violations, plain and with a note", async () => {
    // axe probes a canvas for contrast; jsdom has none.
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
    const { container } = render(
      <main>
        <HoleBadge hole="gap" />
        <TruncationBadge content={"ok\n…[truncated 2048 bytes]"} />
      </main>
    )
    const res = await axe.run(container, { resultTypes: ["violations"] })
    expect(res.violations.map((v) => v.id)).toEqual([])
  })

  it("with detail the words are visible beside it, said once", () => {
    const { container } = render(<HoleBadge hole="gap" detail />)
    expect(container.querySelector("[aria-describedby]")).toBeNull()
    expect(container.querySelector("[data-hole-words]")).toBeNull()
    expect(container.textContent).toContain(HOLES.gap.reason)
  })

  it("a cause's words win over the badge's", () => {
    const { container } = render(<HoleBadge hole="not_recorded" cause="no_public_id" />)
    const c = CAUSES.not_recorded!.no_public_id
    expect(container.querySelector("[data-hole]")!.getAttribute("title")).toBe(`${c.reason} — fix: ${c.fix}`)
  })
})

describe("TruncationBadge (the loop's cuts as the table's badges)", () => {
  it("a result cap's cut is truncated, cause result_cap, the bytes named", () => {
    const { container } = render(<TruncationBadge content={"ok\n…[truncated 2048 bytes]"} />)
    const b = container.querySelector('[data-hole="truncated"]')!
    expect(b.getAttribute("title")).toBe(`${resultCapReason(2048)} — fix: ${CAUSES.truncated!.result_cap.fix}`)
    // The bytes cut are visible, the table's label first.
    expect(b.firstChild?.textContent).toBe(HOLES.truncated.label)
    expect(b.textContent).toBe(`${HOLES.truncated.label} · 2.0 KiB cut`)
  })

  it("an unrun call is max_tokens with no fix (the loop already retried)", () => {
    const { container } = render(
      <TruncationBadge content="tool call refund was not executed: the response hit the output token limit" />
    )
    const b = container.querySelector('[data-hole="max_tokens"]')!
    expect(b.getAttribute("title")).toBe(UNRUN_CALL_REASON)
    expect(b.firstChild?.textContent).toBe(HOLES.max_tokens.label)
    expect(b.textContent).toBe(`${HOLES.max_tokens.label} · call never executed`)
  })
})
