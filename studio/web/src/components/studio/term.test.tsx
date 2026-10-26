// <Term> (U21.2; UX-A-20, UX-F-10): the glossary tooltip is keyboard
// reachable — Tab reaches the word, focus shows TERMS' short text (the
// word's description too), Escape hides it — and axe finds nothing with
// it open.
import { act, fireEvent, screen, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { describe, expect, it } from "vitest"

import { Term, TermWrap } from "@/components/studio/term"
import { SelectField } from "@/components/ui/select-field"
import { TERMS } from "@/lib/glossary"
import { renderWithRouter } from "@/test/render"

/** The document's tab order: what Tab visits, in order. */
function tabbable(): HTMLElement[] {
  return Array.from(
    document.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input, [tabindex]:not([tabindex="-1"])'
    )
  ).filter((e) => e.tabIndex >= 0)
}

describe("<Term> (U21.2)", () => {
  it("Tab reaches it, focus shows the short text, Escape hides it", async () => {
    await renderWithRouter(
      <p>
        <button type="button">before</button> a <Term name="session" /> of turns
      </p>
    )
    const before = screen.getByRole("button", { name: "before" })
    const word = await screen.findByText("session")
    // Tab from the button lands on the word: it is next in the order.
    const order = tabbable()
    expect(order[order.indexOf(before) + 1]).toBe(word)
    expect(word.tabIndex).toBe(0)

    // Closed, nothing of it is in the page: no stray text.
    expect(document.querySelector("[role=tooltip]")).toBeNull()
    expect(word.hasAttribute("aria-describedby")).toBe(false)

    act(() => word.focus())
    const tip = await waitFor(() => {
      const t = document.getElementById(
        word.getAttribute("aria-describedby") ?? ""
      )
      expect(t).not.toBeNull()
      return t!
    })
    expect(tip.getAttribute("role")).toBe("tooltip")
    expect(tip.textContent).toContain(TERMS.session.short)
    // "more ›" opens the Concepts entry.
    const more = tip.querySelector<HTMLAnchorElement>("[data-term-more]")!
    expect(more.getAttribute("href")).toMatch(/\/settings#concept-session$/)
    // Tab goes from the word into the tooltip's link, and it stays open.
    expect(tabbable().indexOf(more)).toBe(tabbable().indexOf(word) + 1)
    fireEvent.blur(word, { relatedTarget: more })
    expect(tip.isConnected).toBe(true)

    // The tooltip open: every rule on the paragraph that holds it.
    const res = await axe.run(word.parentElement!)
    expect(
      res.violations.map(
        (v) => `${v.id}: ${v.nodes.map((n) => n.html).join(" | ")}`
      )
    ).toEqual([])

    fireEvent.keyDown(word, { key: "Escape" })
    await waitFor(() =>
      expect(document.querySelector("[role=tooltip]")).toBeNull()
    )
    expect(word.hasAttribute("aria-describedby")).toBe(false)
    // Focus again shows it again; leaving it hides it.
    act(() => word.blur())
    act(() => word.focus())
    await waitFor(() =>
      expect(document.querySelector("[role=tooltip]")).not.toBeNull()
    )
    fireEvent.blur(word, { relatedTarget: before })
    await waitFor(() =>
      expect(document.querySelector("[role=tooltip]")).toBeNull()
    )
  })

  it("hover shows it, and Escape anywhere hides a hovered one", async () => {
    await renderWithRouter(<Term name="replay">replay</Term>)
    const word = await screen.findByText("replay")
    fireEvent.mouseEnter(word)
    await waitFor(() =>
      expect(screen.getByRole("tooltip").textContent).toContain(
        TERMS.replay.short
      )
    )
    fireEvent.keyDown(document, { key: "Escape" })
    await waitFor(() => expect(screen.queryByRole("tooltip")).toBeNull())
  })

  it("a control is its own trigger: one focusable element, the tooltip after it", async () => {
    await renderWithRouter(
      <TermWrap name="tokens" extra="sorts by tokens">
        {(t) => (
          <button type="button" {...t}>
            tokens in / out
          </button>
        )}
      </TermWrap>
    )
    const b = await screen.findByRole("button", { name: "tokens in / out" })
    expect(b.querySelector("[tabindex]")).toBeNull()
    act(() => b.focus())
    const tip = await screen.findByRole("tooltip")
    expect(b.getAttribute("aria-describedby")).toBe(tip.id)
    expect(tip.textContent).toContain(TERMS.tokens.short)
    expect(tip.textContent).toContain("sorts by tokens")
    fireEvent.keyDown(b, { key: "Escape" })
    await waitFor(() => expect(screen.queryByRole("tooltip")).toBeNull())
  })

  it("a scroll (any box, capture) or a resize hides it: it is drawn fixed", async () => {
    await renderWithRouter(
      <div data-box style={{ overflow: "auto" }}>
        <Term name="step" />
      </div>
    )
    const word = await screen.findByText("step")
    act(() => word.focus())
    await screen.findByRole("tooltip")
    fireEvent.scroll(document.querySelector("[data-box]")!)
    await waitFor(() => expect(screen.queryByRole("tooltip")).toBeNull())
    fireEvent.mouseEnter(word)
    await screen.findByRole("tooltip")
    fireEvent(window, new Event("resize"))
    await waitFor(() => expect(screen.queryByRole("tooltip")).toBeNull())
  })

  it("in a select's caption: a tap reads the word, never focuses the select, and the open tip stays out of its name", async () => {
    await renderWithRouter(
      <SelectField
        caption={
          <Term name="fork" portal>
            Write to
          </Term>
        }
        label="Write to"
        value="ephemeral"
        onValueChange={() => {}}
        options={[
          { value: "ephemeral", label: "nowhere (ephemeral)" },
          { value: "fork", label: "a new session (fork)" },
        ]}
      />
    )
    const word = await screen.findByText("Write to", {
      selector: "[data-term]",
    })
    const caption = word.closest<HTMLElement>('[id$="-caption"]')!
    const trigger = document.querySelector<HTMLElement>(
      `[aria-labelledby="${caption.id}"]`
    )!
    fireEvent.click(word)
    const tip = await screen.findByRole("tooltip")
    expect(document.activeElement).not.toBe(trigger)
    // The name the trigger reads stays the caption's words alone.
    expect(caption.contains(tip)).toBe(false)
    expect(caption.textContent).toBe("Write to")
    // The caption itself still focuses its control.
    fireEvent.click(caption)
    expect(document.activeElement).toBe(trigger)
  })
})
