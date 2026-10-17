// The provider wire pair (ADR 0028 §12, plan A6): the Studio step
// card's Wire tab's reading of a requests row's raw — the panel's
// Request tab only links to it (it never draws a body). Pure values,
// no React, no DOM.
import type { RawRecord, RawSide } from "./api"
import { plural } from "./format"
import { kib } from "./honesty"

// hasWire — whether a row carries its exchange, the one test both
// clients make — is lib/requests.ts's (the panel bundles that module,
// not this one).

/** The cap the recorder cuts each wire body at (obsdb's 256 KiB). */
/** The Wire tab's caption (UX-E-58): the pair is the loop's request
 * (the request tab, before the model middleware) against the bytes
 * the adapter put on the wire (after all of it) — never "before /
 * after middleware": the layers between are not recorded one by one. */
export const WIRE_PAIR_LABEL = "loop request → wire bytes"
export const WIRE_PAIR_HINT =
  "the request tab is what the loop handed the model chain; this is what the adapter sent and received, after every WrapModel layer and the SDK — the layers between are not recorded one by one"

export const WIRE_CAP = 256 * 1024

/** A body the 256 KiB cap cut: the truncated badge's words. */
export function wireCutReason(n: number): string {
  return `the recorder keeps ${kib(WIRE_CAP)} of each wire body: ${kib(n)} of this one were not recorded`
}

/** A raw record that did not parse (content derived, zero sides). */
export const WIRE_DERIVED_REASON =
  "the raw record did not parse: its request and response were not recoverable"

/** A response body not read to its end (complete: false). */
export const WIRE_INCOMPLETE = "incomplete: the body was not read to the end"
export const WIRE_INCOMPLETE_REASON =
  "the response body was closed unread — a transport retry, a timeout or a stopped reader: the body shown is what was read, never the whole"

/** The headers as rows, names sorted, each value on its own row. */
export function headerRows(h: RawSide["headers"]): [string, string][] {
  const out: [string, string][] = []
  for (const k of Object.keys(h ?? {}).sort((a, b) => a.localeCompare(b)))
    for (const v of h?.[k] ?? []) out.push([k, v])
  return out
}

/** isJSONMedia: the media type is JSON (application/json, +json). */
export function isJSONMedia(mt: string | undefined): boolean {
  const t = (mt ?? "").split(";")[0].trim().toLowerCase()
  return t === "application/json" || t.endsWith("+json")
}

/** withheld is the stripping said: "2 headers withheld: a, b". */
export function withheld(
  names: string[] | null | undefined,
  what: "header" | "query parameter"
): string | null {
  const n = names?.length ?? 0
  if (!n) return null
  return `${plural(n, what)} withheld: ${names!.join(", ")}`
}

/** The pair's download name: run, step, attempt. */
export function wireFileName(
  runId: string,
  raw: Pick<RawRecord, "step" | "attempt">
): string {
  return `${runId.replace(/[^\w.-]+/g, "_")}.step${raw.step}.attempt${raw.attempt}.wire.json`
}
