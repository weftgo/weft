// The ⌘K palette's finding rules (U26; UX-A-13), pure: what a typed
// text asks the server for, which rows are its exact matches, and how
// every other item matches it.
//
//   - An id is never fuzzy-matched: a pasted id prefix is looked up on
//     the server (GET /api/runs?id_prefix=…&all=1 — children included —
//     and, UX-A-13, a session's and a trace's: /api/sessions?id_prefix=,
//     /api/traces?id_prefix=) and its rows rank first as exact matches; any such word is also
//     looked up as a public id (GET /api/runs?public_id=…: an app picks
//     its own, "pub_…" or "arena:run-1:rw.deep-delegation" alike); a
//     tag (key=value) is GET /api/runs?tag.key=value. Each answer is
//     kept only where it
//     says what was asked (the id starts with the text, the public id
//     or the pair is the text), so a server that ignores a filter can
//     never put a stranger first.
//   - Every other item matches by contiguous substrings: each word of
//     the text inside the item's value or keywords, ignoring case —
//     never a subsequence, so "1c06797f" no longer finds a trace whose
//     id merely holds those characters in order, and Enter opens
//     nothing when nothing matches.
//   - A run's metadata pairs (k=v) are keywords: a scenario tag's value
//     finds the loaded runs it labels. The server matches a tag only by
//     its key (tag.<k>=<v>), so a bare value finds no older run: the
//     palette says so (TAG_VALUE_HINT) while nothing exact answers.
//   - Enter opens the exact match. With none, cmdk's own pick of the
//     first item is not taken (acceptSelection): Enter does nothing
//     until the user moves the selection (arrows, the pointer).

/** The shortest text looked up as an id prefix. */
export const ID_PREFIX_MIN = 4

/** What the text asks the server for, each when it reads as one. */
export interface Lookups {
  idPrefix?: string
  publicId?: string
  tag?: { key: string; value: string }
}

/** lookupsOf reads a settled palette text: one word with no space is
 * an id prefix and a public id once it is ID_PREFIX_MIN long (pub_… at
 * any length is a public id); key=value is a tag (and nothing else). */
export function lookupsOf(term: string): Lookups {
  const t = term.trim()
  if (!t || /\s/.test(t)) return {}
  const eq = t.indexOf("=")
  if (eq > 0 && eq < t.length - 1)
    return { tag: { key: t.slice(0, eq), value: t.slice(eq + 1) } }
  const out: Lookups = {}
  if (eq >= 0) return out
  if (t.length >= ID_PREFIX_MIN) out.idPrefix = t
  if (t.length >= ID_PREFIX_MIN || t.startsWith("pub_")) out.publicId = t
  return out
}

interface Row {
  id: string
  public_id?: string
  meta?: Record<string, string>
}

/** exactRows is the exact matches, in the order found and once each:
 * the id-prefix rows (the exact id first), then the public id's, then
 * the tag's — each kept only when it is what was asked. */
export function exactRows<TRow extends Row>(
  l: Lookups,
  found: { idPrefix?: TRow[]; publicId?: TRow[]; tag?: TRow[] }
): TRow[] {
  const out: TRow[] = []
  const seen = new Set<string>()
  const add = (r: TRow) => {
    if (seen.has(r.id)) return
    seen.add(r.id)
    out.push(r)
  }
  if (l.idPrefix) {
    const p = l.idPrefix
    const rows = (found.idPrefix ?? []).filter((r) => r.id.startsWith(p))
    rows.filter((r) => r.id === p).forEach(add)
    rows.forEach(add)
  }
  if (l.publicId)
    (found.publicId ?? [])
      .filter((r) => r.public_id === l.publicId)
      .forEach(add)
  if (l.tag) {
    const { key, value } = l.tag
    ;(found.tag ?? []).filter((r) => r.meta?.[key] === value).forEach(add)
  }
  return out
}

/** exactSessions is the sessions an id prefix or a public id names
 * (UX-A-13; GET /api/sessions?id_prefix= and ?public_id=), each kept
 * only when its id starts with the prefix or its public id is the
 * text, once each, the exact id first. */
export function exactSessions<TRow extends { id: string; public_id?: string }>(
  l: Lookups,
  rows: TRow[] | undefined
): TRow[] {
  const p = l.idPrefix
  const pub = l.publicId
  if (!p && !pub) return []
  const seen = new Set<string>()
  const hit = (rows ?? []).filter((r) => {
    if (seen.has(r.id)) return false
    seen.add(r.id)
    return (!!p && r.id.startsWith(p)) || (!!pub && r.public_id === pub)
  })
  return [...hit.filter((r) => r.id === p), ...hit.filter((r) => r.id !== p)]
}

/** exactTraces is the traces an id prefix names (UX-A-13: GET
 * /api/traces?id_prefix=), each kept only when its id starts with the
 * text. */
export function exactTraces<TRow extends { trace_id: string }>(
  l: Lookups,
  rows: TRow[] | undefined
): TRow[] {
  const p = l.idPrefix
  if (!p) return []
  return (rows ?? []).filter((r) => r.trace_id.startsWith(p))
}

/** The value prefix of an exact match's item: it ranks first. */
export const EXACT_VALUE = "exact:"

/** metaKeywords is a run's metadata as k=v keywords. */
export function metaKeywords(meta: Record<string, string> | undefined) {
  return Object.entries(meta ?? {}).map(([k, v]) => `${k}=${v}`)
}

/** paletteFilter is cmdk's filter: 0 unless every word of the search is
 * a contiguous substring of the value or a keyword (case ignored); an
 * exact match scores 1, an item whose value starts with the search
 * 0.9, any other match lower the later its first word sits. */
export function paletteFilter(
  value: string,
  search: string,
  keywords?: string[]
): number {
  if (value.startsWith(EXACT_VALUE)) return 1
  const words = search.toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return 1
  const hay = [value, ...(keywords ?? [])].join(" ").toLowerCase()
  for (const w of words) if (!hay.includes(w)) return 0
  const v = value.toLowerCase()
  // An item's value is "<kind>:<id> …": the id after the kind counts as
  // the start.
  const body = v.slice(v.indexOf(":") + 1)
  if (body.startsWith(words[0]) || v.startsWith(words[0])) return 0.9
  const at = hay.indexOf(words[0])
  return 0.1 + 0.7 / (1 + at / 32)
}

/** What the palette says under a bare word nothing exact answers: the
 * server matches a tag by its key only. */
export const TAG_VALUE_HINT =
  "a tag value? the server matches tags as key=value (e.g. scenario=rw.deep-delegation)"

/** tagHintFor is TAG_VALUE_HINT when the settled text is one bare word
 * (an id prefix's shape, no "=") and no exact match answers it. */
export function tagHintFor(
  l: Lookups,
  exactCount: number,
  settled: boolean
): string | undefined {
  if (!settled || exactCount > 0 || !l.idPrefix) return undefined
  return TAG_VALUE_HINT
}

/** acceptSelection is whether the palette takes a selection cmdk
 * offers: always while the box is empty, once the user has moved it
 * (arrows, the pointer), or when an exact match answers the text —
 * otherwise cmdk's automatic pick of the first item is dropped, so
 * Enter on a substring hit opens nothing (UX-A-13). */
export function acceptSelection(o: {
  text: string
  exactCount: number
  moved: boolean
}): boolean {
  return !o.text.trim() || o.moved || o.exactCount > 0
}

/** The keys that move cmdk's selection: the palette counts them as the
 * user's choice. */
export function movesSelection(e: { key: string; ctrlKey?: boolean }): boolean {
  if (
    ["ArrowDown", "ArrowUp", "Home", "End", "PageUp", "PageDown"].includes(
      e.key
    )
  )
    return true
  return !!e.ctrlKey && ["n", "p", "j", "k"].includes(e.key)
}
