// The playground's drafts (plan F5): the form, kept in this browser's
// IndexedDB under a draft id the page's URL carries (?exp=<id>), so a
// reload — or the link, opened again in this browser — reopens it. Per
// browser by nature: nothing here reaches Studio, another browser or
// another reader (the share link, #state=, is what travels). A matrix
// also keeps the form it ran under its experiment id (kind
// "experiment"): the experiment page clones from it when it is there,
// exactly, and from the saved definition and its runs when it is not.
//
// IndexedDB can be missing or refuse (a private window, blocked site
// data, jsdom): the drafts then live in memory for the page's life —
// the playground still works, a reload just forgets them.
import type { FormState } from "./form-state"
import { formRefusalOf, validateForm } from "./form-state"

export type DraftKind = "draft" | "experiment"

export interface DraftRecord {
  id: string
  kind: DraftKind
  /** When it was last written (RFC 3339). */
  saved: string
  /** What the drafts list shows beside the id. */
  label: string
  form: FormState
}

const DB_NAME = "weft-studio"
const STORE = "drafts"

/** The drafts kept when IndexedDB is not there. */
const memory = new Map<string, DraftRecord>()

let opening: Promise<IDBDatabase | null> | null = null

function factory(): IDBFactory | null {
  try {
    return typeof indexedDB === "undefined" ? null : indexedDB
  } catch {
    return null
  }
}

function open(): Promise<IDBDatabase | null> {
  if (opening) return opening
  const idb = factory()
  if (!idb) return (opening = Promise.resolve(null))
  opening = new Promise((resolve) => {
    try {
      const req = idb.open(DB_NAME, 1)
      req.onupgradeneeded = () => {
        if (!req.result.objectStoreNames.contains(STORE))
          req.result.createObjectStore(STORE, { keyPath: "id" })
      }
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => resolve(null)
      req.onblocked = () => resolve(null)
    } catch {
      resolve(null)
    }
  })
  return opening
}

/** run one request against the store; undefined when IndexedDB is not
 * there or refused (the caller falls back to memory). */
async function run<T>(
  mode: IDBTransactionMode,
  fn: (s: IDBObjectStore) => IDBRequest<T>
): Promise<{ ok: true; value: T } | { ok: false }> {
  const db = await open()
  if (!db) return { ok: false }
  return new Promise((resolve) => {
    try {
      const req = fn(db.transaction(STORE, mode).objectStore(STORE))
      req.onsuccess = () => resolve({ ok: true, value: req.result })
      req.onerror = () => resolve({ ok: false })
    } catch {
      resolve({ ok: false })
    }
  })
}

/** A record read back: a damaged or newer one is dropped, never drawn. */
function valid(r: unknown): DraftRecord | null {
  if (!r || typeof r !== "object") return null
  const o = r as Record<string, unknown>
  const form = validateForm(o.form)
  if (typeof o.id !== "string" || !form) return null
  return {
    id: o.id,
    kind: o.kind === "experiment" ? "experiment" : "draft",
    saved: typeof o.saved === "string" ? o.saved : "",
    label: typeof o.label === "string" ? o.label : "",
    form,
  }
}

/** newDraftID mints a draft id (the ?exp= value). */
export function newDraftID(): string {
  const b = new Uint8Array(5)
  crypto.getRandomValues(b)
  return `d_${[...b].map((x) => x.toString(16).padStart(2, "0")).join("")}`
}

/** The record id a matrix's form is kept under. */
export const experimentDraftID = (experimentID: string) => `exp:${experimentID}`

/** saveDraft writes (or replaces) one record. */
export async function saveDraft(
  id: string,
  form: FormState,
  opts: { kind?: DraftKind; label?: string } = {}
): Promise<void> {
  const rec: DraftRecord = {
    id,
    kind: opts.kind ?? "draft",
    saved: new Date().toISOString(),
    label: opts.label ?? "",
    form,
  }
  const out = await run("readwrite", (s) => s.put(rec))
  if (!out.ok) memory.set(id, rec)
}

/** loadDraft reads one record (null: none in this browser). */
export async function loadDraft(id: string): Promise<DraftRecord | null> {
  const out = await loadDraftOutcome(id)
  return "rec" in out ? out.rec : null
}

/** loadDraftOutcome is loadDraft saying why it read nothing: a record
 * kept in an older form version (older), else none here (missing). */
export async function loadDraftOutcome(
  id: string
): Promise<{ rec: DraftRecord } | { refused: "older" | "missing" }> {
  const out = await run<unknown>("readonly", (s) => s.get(id))
  const raw = out.ok ? out.value : memory.get(id)
  const rec = valid(raw)
  if (rec) return { rec }
  const form =
    raw && typeof raw === "object"
      ? (raw as Record<string, unknown>).form
      : undefined
  return { refused: formRefusalOf(form) === "older" ? "older" : "missing" }
}

/** listDrafts lists the records of a kind, newest first. */
export async function listDrafts(
  kind: DraftKind = "draft"
): Promise<DraftRecord[]> {
  const out = await run<unknown[]>("readonly", (s) => s.getAll())
  const all = (out.ok ? out.value : [...memory.values()])
    .map(valid)
    .filter((r): r is DraftRecord => !!r)
  return all
    .filter((r) => r.kind === kind)
    .sort((a, b) => b.saved.localeCompare(a.saved))
}

/** deleteDraft removes one record. */
export async function deleteDraft(id: string): Promise<void> {
  const out = await run("readwrite", (s) => s.delete(id))
  if (!out.ok) memory.delete(id)
}

/** resetDrafts forgets the in-memory records and the open handle
 * (tests: one page per test). */
export function resetDrafts(): void {
  memory.clear()
  opening = null
}
