// The Scope value (plan §13.3): the unit every discovery rung, deep
// link and API call carries — one conversation's public id, and
// optionally the session, flow and run inside it. Its one string form
// is the data-weft-scope DOM marker the framework helpers emit:
//
//   pub_…;session=s_…;flow=f_…;run=r_…
//
// the public id first and bare, the rest optional, in that order,
// each value percent-encoded (an id never needs it; a ";" or "=" in
// one cannot break the marker). Both clients share this module; C3.1
// extends it and adds the Go side, which must read and write the same
// form.

/** Scope names what the devtools follow. publicId is required ("" is
 * the dev list: no conversation); the rest narrow it. */
export interface Scope {
  publicId: string
  session?: string
  flow?: string
  run?: string
}

/** The optional fields, in their serialised order. */
const KEYS = ["session", "flow", "run"] as const

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
 * session=, flow=, run= for each one set (empty counts as unset). */
export function serializeScope(s: Scope): string {
  const parts = [enc(s.publicId)]
  for (const k of KEYS) {
    const v = s[k]
    if (v) parts.push(`${k}=${enc(v)}`)
  }
  return parts.join(";")
}

/** parseScope reads the marker back. The first segment is the public
 * id; a later segment is key=value, and a key it does not know, a
 * repeated key or a segment without "=" is ignored — a newer writer's
 * field is not this reader's to reject. Never throws. */
export function parseScope(str: string): Scope {
  const [head = "", ...rest] = String(str).split(";")
  const out: Scope = { publicId: dec(head.trim()) }
  for (const seg of rest) {
    const at = seg.indexOf("=")
    if (at < 0) continue
    const k = seg.slice(0, at).trim()
    if (!(KEYS as readonly string[]).includes(k)) continue
    const key = k as (typeof KEYS)[number]
    const v = dec(seg.slice(at + 1).trim())
    if (v && out[key] === undefined) out[key] = v
  }
  return out
}
