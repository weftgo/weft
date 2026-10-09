// The run page's honesty badge (D5 review): the table's words are in
// the accessible text, not only the pointer's title; a cause's words
// win; the loop's cuts are the table's badges (truncated/result_cap,
// max_tokens with no fix for an unrun call).
import { render } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { HoleBadge } from "@/components/studio/hole-badge"
import { TruncationBadge } from "@/components/studio/truncation-badge"
import { UNRUN_CALL_REASON } from "@/lib/events"
import { CAUSES, HOLES, resultCapReason } from "@/lib/honesty"

describe("HoleBadge", () => {
  it("carries the reason and fix in its accessible text, visually hidden", () => {
    const { container } = render(<HoleBadge hole="gap" />)
    const b = container.querySelector<HTMLElement>('[data-hole="gap"]')!
    const words = `${HOLES.gap.reason} — fix: ${HOLES.gap.fix}`
    expect(b.getAttribute("title")).toBe(words)
    expect(b.querySelector(".sr-only")?.textContent).toBe(` — ${words}`)
    expect(b.textContent).toBe(`${HOLES.gap.label} — ${words}`)
  })

  it("with detail the words are visible beside it, said once", () => {
    const { container } = render(<HoleBadge hole="gap" detail />)
    expect(container.querySelector(".sr-only")).toBeNull()
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
  })

  it("an unrun call is max_tokens with no fix (the loop already retried)", () => {
    const { container } = render(
      <TruncationBadge content="tool call refund was not executed: the response hit the output token limit" />
    )
    const b = container.querySelector('[data-hole="max_tokens"]')!
    expect(b.getAttribute("title")).toBe(UNRUN_CALL_REASON)
  })
})
