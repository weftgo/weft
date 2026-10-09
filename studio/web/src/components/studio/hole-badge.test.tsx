// The run page's honesty badge (D5 review): the table's words are in
// the accessible text (inline, visually hidden — the panel's form),
// not only the pointer's title; a cause's words
// win; the loop's cuts are the table's badges (truncated/result_cap,
// max_tokens with no fix for an unrun call).
import { cleanup, render } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, describe, expect, it, vi } from "vitest"

import { HoleBadge } from "@/components/studio/hole-badge"
import { TruncationBadge } from "@/components/studio/truncation-badge"
import { UNRUN_CALL_REASON } from "@/lib/events"
import { CAUSES, HOLES, resultCapReason } from "@/lib/honesty"
import { badge } from "@/panel/badges"

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe("HoleBadge", () => {
  it("carries the reason and fix inline, visually hidden, after the label — the panel's words", () => {
    const { container } = render(<HoleBadge hole="gap" />)
    const b = container.querySelector<HTMLElement>('[data-hole="gap"]')!
    const words = `${HOLES.gap.reason} — fix: ${HOLES.gap.fix}`
    expect(b.getAttribute("title")).toBe(words)
    // Part of the accessible name, as the panel's .weft-sr: no
    // aria-describedby a browse-mode reader may skip.
    expect(b.getAttribute("aria-describedby")).toBeNull()
    expect(b.firstChild?.textContent).toBe(HOLES.gap.label)
    const sr = b.querySelector<HTMLElement>("[data-hole-words]")!
    expect(sr.className).toContain("sr-only")
    expect(sr.textContent).toBe(` — ${words}`)
    expect(b.textContent).toBe(`${HOLES.gap.label} — ${words}`)
    // The panel draws the same text for the same hole.
    expect(badge("gap").textContent).toBe(b.textContent)
  })

  it("a note stays visible between the label and the hidden words", () => {
    const { container } = render(<HoleBadge hole="truncated" note="2.0 KiB cut" />)
    const b = container.querySelector<HTMLElement>('[data-hole="truncated"]')!
    expect(b.querySelector("[data-hole-note]")!.textContent).toBe(" · 2.0 KiB cut")
    expect(b.textContent.startsWith(`${HOLES.truncated.label} · 2.0 KiB cut — `)).toBe(true)
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
    expect(b.querySelector("[data-hole-note]")?.textContent).toBe(" · 2.0 KiB cut")
  })

  it("an unrun call is max_tokens with no fix (the loop already retried)", () => {
    const { container } = render(
      <TruncationBadge content="tool call refund was not executed: the response hit the output token limit" />
    )
    const b = container.querySelector('[data-hole="max_tokens"]')!
    expect(b.getAttribute("title")).toBe(UNRUN_CALL_REASON)
    expect(b.firstChild?.textContent).toBe(HOLES.max_tokens.label)
    expect(b.querySelector("[data-hole-note]")?.textContent).toBe(" · call never executed")
  })
})
