// The Scope value (plan §13.3): the unit every discovery rung, deep
// link and API call carries — one conversation's public id, and
// optionally the session, flow and run inside it. Its one string form
// is the data-weft-scope DOM marker the framework helpers emit:
//
//   pub_…;session=s_…;flow=f_…;run=r_…;agent=…;tag.<k>=<v>
//
// the public id first and bare, the rest optional, in that order,
// each value percent-encoded (an id never needs it; a ";" or "=" in
// one cannot break the marker). Both clients share this module; C3.1
// extends it and adds the Go side, which must read and write the same
// form.

/** Scope names what the devtools follow. publicId is required ("" is
 * the dev list: no conversation); session, flow and run narrow it.
 * agent and tags name a page of runs that share no conversation (a
 * test harness, a batch: U22) — the runs of that agent whose metadata
 * carries every tag pair; they may stand without a public id. */
export interface Scope {
  publicId: string
  session?: string
  flow?: string
  run?: string
  agent?: string
  /** Run metadata pairs (core.WithMetadata's keys), each written
   * tag.<k>=<v>, in key order. */
  tags?: Record<string, string>
}

/** The optional fields, in their serialised order (the tags after). */
const KEYS = ["session", "flow", "run", "agent"] as const

/** enc percent-encodes a value; a string encodeURIComponent refuses
 * (a lone surrogate) has only the marker's own delimiters and the
 * control characters (below U+0020, and U+007F) encoded — the marker
 * is set inside a host's ref or action, where a throw would be the
 * host's, and a raw CR/LF must never reach a Weft-Scope header (the
 * Go side's fallback, scope.String, does the same). */
const enc = (v: string) => {
  try {
    return encodeURIComponent(v)
  } catch {
    return v.replace(
      // eslint-disable-next-line no-control-regex
      /[%;=\x00-\x1f\x7f]/g,
      (c) => `%${c.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`
    )
  }
}
const dec = (v: string) => {
  try {
    return decodeURIComponent(v)
  } catch {
    return v
  }
}

/** serializeScope is the marker's string form: publicId first, then
 * session=, flow=, run=, agent= for each one set and tag.<k>=<v> per
 * tag in key order (empty counts as unset). */
export function serializeScope(s: Scope): string {
  const parts = [enc(s.publicId)]
  for (const k of KEYS) {
    const v = s[k]
    if (v) parts.push(`${k}=${enc(v)}`)
  }
  for (const [k, v] of Object.entries(s.tags ?? {}).sort(([a], [b]) =>
    a < b ? -1 : a > b ? 1 : 0
  ))
    if (k && v) parts.push(`tag.${enc(k)}=${enc(v)}`)
  return parts.join(";")
}

/** parseScope reads the marker back. The first segment is the public
 * id — unless it is a known key=value (session, flow, run, agent or
 * tag.<k>: what a page writes as data-scope="tag.arena.batch=…"),
 * read as a later one and the public id "". A later segment is
 * key=value, and a key it does not know, a repeated key, an empty
 * value or a segment without "=" is ignored — a newer writer's field
 * is not this reader's to reject. Never throws. */
export function parseScope(str: string): Scope {
  let segs = String(str).split(";")
  const known = (k: string) =>
    (KEYS as readonly string[]).includes(k) || /^tag\../.test(k)
  if (known(segs[0].split("=")[0].trim()) && segs[0].includes("="))
    segs = ["", ...segs]
  const [head = "", ...rest] = segs
  const out: Scope = { publicId: dec(head.trim()) }
  for (const seg of rest) {
    const at = seg.indexOf("=")
    if (at < 0) continue
    const k = seg.slice(0, at).trim()
    const v = dec(seg.slice(at + 1).trim())
    if (!v || !known(k)) continue
    if (k.startsWith("tag.")) {
      const name = dec(k.slice(4))
      if (name && out.tags?.[name] === undefined) (out.tags ??= {})[name] = v
    } else {
      const key = k as (typeof KEYS)[number]
      if (out[key] === undefined) out[key] = v
    }
  }
  return out
}
