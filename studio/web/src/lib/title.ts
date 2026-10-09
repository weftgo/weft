// Page titles (plan G2): one shape for every Studio page, so a tab
// strip, the browser's history menu and a bookmark say which run,
// trace or page each one is — `run s_…-t3 · succeeded · weft studio`.
// Plain strings, no DOM: hooks/use-document-title.ts writes them.

/** The suffix every title ends with (the root route's own title). */
export const STUDIO = "weft studio"

/** What a page is, for its title. */
export type TitlePlace =
  | { page: "run"; id: string; status?: string }
  | { page: "trace"; id: string }
  | { page: "session"; id: string }
  | { page: "compare"; a?: string; b?: readonly string[] }
  | { page: "playground"; experiment?: string }
  | { page: "runs" | "sessions" | "live" | "agents" }

/**
 * shortId elides a long id for a title, keeping what tells ids apart:
 * a session turn's id keeps its prefix and its turn (`s_…-t3`), any
 * other long id its head and tail. An id of 16 characters or fewer is
 * kept whole.
 */
export function shortId(id: string): string {
  if (id.length <= 16) return id
  const turn = /^([A-Za-z]+_).+(-t\d+)$/.exec(id)
  if (turn) return `${turn[1]}…${turn[2]}`
  return `${id.slice(0, 8)}…${id.slice(-4)}`
}

/** pageTitle is a page's document title. */
export function pageTitle(place: TitlePlace): string {
  const parts: string[] = []
  switch (place.page) {
    case "run":
      parts.push(`run ${shortId(place.id)}`)
      if (place.status) parts.push(place.status)
      break
    case "trace":
      parts.push(`trace ${shortId(place.id)}`)
      break
    case "session":
      parts.push(`session ${shortId(place.id)}`)
      break
    case "compare": {
      const ids = [place.a, ...(place.b ?? [])].filter((x): x is string => !!x)
      parts.push(ids.length ? `compare ${ids.map(shortId).join(" ↔ ")}` : "compare")
      break
    }
    case "playground":
      parts.push(place.experiment ? `playground ${shortId(place.experiment)}` : "playground")
      break
    default:
      parts.push(place.page)
  }
  parts.push(STUDIO)
  return parts.join(" · ")
}
