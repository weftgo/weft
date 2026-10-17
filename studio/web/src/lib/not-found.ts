// One not-found state (UX-F-31): a run, session, experiment or trace
// the API answers 404 for, and a Studio page that does not exist, say
// the same thing the same way — "No <kind> <id> in this Studio", the
// way back to the list, and for a run "search for it" (the ⌘K palette
// finds a run by id prefix, public id or tag, U26). A 404 offers no
// retry (UX-A-29): reading it again answers the same. Pure words; the
// component is components/studio/not-found.tsx.
import { ApiError } from "./api"
import { experimentsLink, runsLink, sessionsLink, tracesLink } from "./links"

export type NotFoundKind = "run" | "session" | "experiment" | "trace" | "page"

/** isNotFound is the API's 404: the thing is not in this Studio. */
export function isNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404
}

/** The list each kind goes back to. */
const BACK: Record<NotFoundKind, { to: string; label: string }> = {
  run: { to: runsLink().to, label: "back to runs" },
  session: { to: sessionsLink().to, label: "back to sessions" },
  experiment: { to: experimentsLink().to, label: "back to experiments" },
  trace: { to: tracesLink().to, label: "back to traces" },
  page: { to: runsLink().to, label: "back to runs" },
}

/** notFoundBack is a kind's way back to its list — the not-found
 * state's and a failed read's (LoadFailed) alike. */
export function notFoundBack(kind: NotFoundKind): {
  to: string
  label: string
} {
  return BACK[kind]
}

/** The ⌘K hand-off's verb, a run's only. */
export const SEARCH_FOR_IT = "search for it"

/** The window event the app shell opens the ⌘K palette on, its input
 * prefilled with detail.q (components/studio/app-shell.tsx). */
export const PALETTE_EVENT = "weft:palette"

/** openPalette opens the ⌘K palette with q typed in. */
export function openPalette(q: string): void {
  window.dispatchEvent(new CustomEvent(PALETTE_EVENT, { detail: { q } }))
}

/** notFoundWords: the heading, the line under it and the way back. */
export function notFoundWords(
  kind: NotFoundKind,
  id?: string
): { title: string; note: string; back: { to: string; label: string } } {
  return {
    title: id
      ? `No ${kind} ${id} in this Studio`
      : `No ${kind} here in this Studio`,
    note:
      kind === "page"
        ? "Studio's pages are in the sidebar. A run link looks like runs/<id>."
        : `This Studio's database holds no ${kind} by that id: it was deleted, swept by retention, or recorded into another database.`,
    back: BACK[kind],
  }
}
