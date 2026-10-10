// The Settings page's logic (plan H4), no React: the pricing table's
// per-browser edits (plan A5: "editable per session, exported as the
// override file") and what the stored bearer is.
//
// Pricing edits live in this browser's localStorage, keyed by the
// served table's source and dated — a new table served (another file,
// a newer weft) starts clean. They are never sent to the server:
// Studio never writes files. The download is the one way out — a
// pricing.json in exactly the shape studio.LoadPricing accepts
// ({dated, note?, models: [{provider, model_glob, input, output,
// cached_input?, cache_write?, reasoning?}]}, unknown fields refused,
// input/output required, rates non-negative, dated YYYY-MM-DD, globs
// path.Match patterns), which WEFT_PRICING / --pricing then load.

import type { Meta, ModelPrice, PricingSettings } from "./api"
import { tokenScopeOf } from "./replay"

/** The rate fields of a row, in the file's key order. */
export const RATE_FIELDS = ["input", "output", "cached_input", "cache_write", "reasoning"] as const
export type RateField = (typeof RATE_FIELDS)[number]

/** Every field of a row, in the file's key order. */
export const ROW_FIELDS = ["provider", "model_glob", ...RATE_FIELDS] as const
export type RowField = (typeof ROW_FIELDS)[number]

/** A row as the editor holds it: every field as typed (a half-typed
 * "1." stays as it is); "" is an absent optional rate. */
export type DraftRow = Record<RowField, string>

/** The editor's table: the dated stamp and the rows. */
export interface PricingDraft {
  dated: string
  rows: DraftRow[]
}

/** draftOf is the served table as the editor holds it. */
export function draftOf(s: PricingSettings): PricingDraft {
  return {
    dated: s.dated,
    rows: s.models.map((m) => ({
      provider: m.provider,
      model_glob: m.model_glob,
      input: String(m.input),
      output: String(m.output),
      cached_input: m.cached_input === undefined ? "" : String(m.cached_input),
      cache_write: m.cache_write === undefined ? "" : String(m.cache_write),
      reasoning: m.reasoning === undefined ? "" : String(m.reasoning),
    })),
  }
}

/** An empty row (the editor's "add row"). */
export function emptyRow(): DraftRow {
  return { provider: "", model_glob: "", input: "", output: "", cached_input: "", cache_write: "", reasoning: "" }
}

/** The localStorage key one served table's edits live under. */
export function editsKey(s: Pick<PricingSettings, "source" | "dated">): string {
  return `studio.pricing.edits:${s.source}:${s.dated}`
}

function isDraft(v: unknown): v is PricingDraft {
  if (!v || typeof v !== "object") return false
  const d = v as { dated?: unknown; rows?: unknown }
  return (
    typeof d.dated === "string" &&
    Array.isArray(d.rows) &&
    d.rows.every(
      (r) =>
        !!r && typeof r === "object" && ROW_FIELDS.every((f) => typeof (r as Record<string, unknown>)[f] === "string")
    )
  )
}

/** loadEdits is this browser's edits of a table; null when none (or
 * storage is unreadable, or holds something else). */
export function loadEdits(key: string): PricingDraft | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const v: unknown = JSON.parse(raw)
    return isDraft(v) ? v : null
  } catch {
    return null
  }
}

/** saveEdits stores the edits; false when storage refuses (they then
 * last this page only). */
export function saveEdits(key: string, d: PricingDraft): boolean {
  try {
    localStorage.setItem(key, JSON.stringify(d))
    return true
  } catch {
    return false
  }
}

/** clearEdits drops the edits: the server's table again. */
export function clearEdits(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {
    // unwritable storage: nothing was stored
  }
}

/** sameDraft reports whether two drafts hold the same table. */
export function sameDraft(a: PricingDraft, b: PricingDraft): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/** One field the loader would refuse: the row (-1 for dated), the
 * field, and why — the loader's own words where it has them. */
export interface DraftProblem {
  row: number
  field: RowField | "dated"
  message: string
}

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/

/** isDate is Go's time.DateOnly parse: YYYY-MM-DD, a real day. */
export function isDate(s: string): boolean {
  const m = DATE_RE.exec(s)
  if (!m) return false
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const t = new Date(Date.UTC(y, mo - 1, d))
  return t.getUTCFullYear() === y && t.getUTCMonth() === mo - 1 && t.getUTCDate() === d
}

/**
 * globError is path.Match's ErrBadPattern check on a pattern (Go's
 * path.Match validates the whole pattern): an unclosed or empty
 * character class, a class range out of order, a '-' or ']' where a
 * class character belongs, a trailing backslash. "" when the pattern
 * is good.
 */
export function globError(p: string): string {
  let i = 0
  const esc = (): string | null => {
    if (i >= p.length) return null
    let c = p[i]
    if (c === "-" || c === "]") return null
    if (c === "\\") {
      i++
      if (i >= p.length) return null
      c = p[i]
    }
    i++
    return c
  }
  while (i < p.length) {
    const c = p[i]
    if (c === "\\") {
      if (i + 1 >= p.length) return "a trailing backslash"
      i += 2
      continue
    }
    if (c !== "[") {
      i++
      continue
    }
    i++
    if (p[i] === "^") i++
    let n = 0
    for (;;) {
      if (i < p.length && p[i] === "]" && n > 0) {
        i++
        break
      }
      const lo = esc()
      if (lo === null) return "an unclosed or malformed character class"
      if (p[i] === "-") {
        i++
        const hi = esc()
        if (hi === null) return "a malformed character range"
        if (hi < lo) return "a character range out of order"
      }
      n++
    }
  }
  return ""
}

/** A rate as the loader reads it: a finite, non-negative number. */
function rate(s: string): number | null {
  const t = s.trim()
  if (t === "") return null
  const n = Number(t)
  return Number.isFinite(n) && n >= 0 ? n : NaN
}

/** problems is every field of the draft the loader would refuse. */
export function problems(d: PricingDraft): DraftProblem[] {
  const out: DraftProblem[] = []
  if (!d.dated.trim()) out.push({ row: -1, field: "dated", message: "missing (the day the rates were read, YYYY-MM-DD)" })
  else if (!isDate(d.dated.trim())) out.push({ row: -1, field: "dated", message: `"${d.dated}" is not a YYYY-MM-DD date` })
  d.rows.forEach((r, i) => {
    if (!r.provider.trim()) out.push({ row: i, field: "provider", message: "missing" })
    if (!r.model_glob.trim()) out.push({ row: i, field: "model_glob", message: "missing" })
    if (r.provider.trim() && r.model_glob.trim()) {
      const bad = globError(`${r.provider.trim()}/${r.model_glob.trim()}`)
      if (bad) out.push({ row: i, field: "model_glob", message: `not a path.Match pattern: ${bad}` })
    }
    for (const f of RATE_FIELDS) {
      const v = rate(r[f])
      if (v === null && (f === "input" || f === "output"))
        out.push({ row: i, field: f, message: "missing (USD per 1M tokens)" })
      else if (v !== null && Number.isNaN(v)) out.push({ row: i, field: f, message: `${r[f].trim()} is not a non-negative rate` })
    }
  })
  return out
}

/** The override file: pricing.json's shape, nothing else. */
export interface PricingFile {
  dated: string
  note?: string
  models: ModelPrice[]
}

/**
 * pricingFile is the draft as the file studio.LoadPricing accepts, in
 * the file's key order; null while any field would be refused
 * (problems says which). The note is the served table's, kept.
 */
export function pricingFile(d: PricingDraft, note?: string): PricingFile | null {
  if (problems(d).length > 0) return null
  const models = d.rows.map((r) => {
    const m: ModelPrice = {
      provider: r.provider.trim(),
      model_glob: r.model_glob.trim(),
      input: Number(r.input.trim()),
      output: Number(r.output.trim()),
    }
    for (const f of ["cached_input", "cache_write", "reasoning"] as const) {
      if (r[f].trim() !== "") m[f] = Number(r[f].trim())
    }
    return m
  })
  return { dated: d.dated.trim(), ...(note ? { note } : {}), models }
}

/** pricingFileText is the file's bytes: indented JSON, a final newline. */
export function pricingFileText(f: PricingFile): string {
  return `${JSON.stringify(f, null, 2)}\n`
}

/** What the stored bearer is, as far as the page can tell. */
export interface TokenIdentity {
  kind: "none" | "unused" | "server" | "panel-read" | "panel-playground"
  /** One line for the reader. */
  words: string
  /** A panel token's claims, when readable: its public id and expiry. */
  publicId?: string
  exp?: string
}

function panelClaims(token: string): { public_id?: string; exp?: string } {
  try {
    const body = token.slice("weft_pt.".length).split(".")[0]
    const b64 = body.replace(/-/g, "+").replace(/_/g, "/")
    const c = JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4))) as Record<string, unknown>
    return {
      public_id: typeof c.public_id === "string" ? c.public_id : undefined,
      exp: typeof c.exp === "string" ? c.exp : undefined,
    }
  } catch {
    return {}
  }
}

/**
 * tokenIdentity says what the stored bearer is: a panel token names its
 * scope in its claims (weft_pt.<claims>.<sig>; Studio verifies it, the
 * page only reads it); any other bearer accepted by a token-walled
 * Studio is the server (dev) token — confirmed when api/meta served
 * what only the server token or a loopback request without a Token
 * reads (live_all, pid, db.path). A token stored for a Studio with no
 * Token configured is sent and not needed.
 */
export function tokenIdentity(token: string, meta: Pick<Meta, "auth_required" | "live_all" | "pid" | "db">): TokenIdentity {
  const full = meta.live_all === true || meta.pid !== undefined || meta.db.path !== undefined
  if (!token) {
    return {
      kind: "none",
      words: meta.auth_required
        ? "no token stored"
        : `no token needed: this Studio has no Token configured${full ? " and serves this loopback request everything" : ""}`,
    }
  }
  const scope = tokenScopeOf(token)
  if (scope) {
    const c = panelClaims(token)
    return {
      kind: scope === "playground" ? "panel-playground" : "panel-read",
      words: `a ${scope}-scoped panel token${c.public_id ? ` for ${c.public_id}` : ""}: it reads one public id's runs${
        scope === "read" ? " and no prompts or catalogs" : " and may run experiments in it"
      }`,
      publicId: c.public_id,
      exp: c.exp,
    }
  }
  if (!meta.auth_required)
    return { kind: "unused", words: "a stored token, not needed: this Studio has no Token configured (it is sent and ignored)" }
  return {
    kind: "server",
    words: full
      ? "the server (dev) token: it reads everything, the database's path included"
      : "the server (dev) token, by its shape (this Studio served nothing only it reads)",
  }
}

/** maskToken is the token as the page shows it hidden: dots, nothing
 * of the token itself. */
export function maskToken(token: string): string {
  return token ? "••••••••" : ""
}
