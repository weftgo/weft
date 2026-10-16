// The run tree's descendant half (U9.2, split out in P8-fix-a): the
// rules lib/waterfall.ts's time scale applies to a descendant run's
// spans, as GET /api/runs/{id}/spans?tree=1 serves them beside the
// run's own. Studio's run page reads that tree and builds its rows
// through treeRows; the devtools panel reads a run's own spans only
// (no ?tree=1), where none of this has anything to do — so panel.js
// does not carry it, and the rows it draws for its input are the same
// (parity.test.ts holds both surfaces to them).
import type { Span as TimedSpan } from "./api"
import type {
  Descend,
  Placed,
  Scale,
  WaterfallInput,
  WaterfallModel,
} from "./waterfall"
import { attrsOf, spanRef, str, waterfallRows } from "./waterfall"

/**
 * descend: a child run hangs under the span its invoke_agent span names
 * as parent — call ids repeat per step (core numbers call_1 each step),
 * so the span, never the call id, says which tool. Only when that span
 * is absent: by (parent run, parent call) — and since a call id repeats
 * per step and a child names no parent step (nor is its id parsed,
 * docs/record-contract.md §2), a tool takes the child under its (run,
 * call) whose run starts inside it — one candidate or several. A
 * collapsed child's spans (and its descendants', `<run>/…`) stay inside
 * its one row (claim).
 */
export const descend: Descend = (placed, invoke, root, runOf, byRun) => {
  const present = new Set(placed.map((p) => spanRef(p.span)))
  const under = new Map<string, string>()
  const byCall = new Map<string, Placed[]>()
  for (const [run, p] of invoke) {
    if (run === root) continue
    const ps = spanRef(p.span, p.span.parent_span_id)
    const a = attrsOf(p.span)
    if (p.span.parent_span_id && present.has(ps)) under.set(ps, run)
    else if (str(a["weft.parent.call.id"])) {
      const k = `${a["weft.parent.run.id"]}\n${a["weft.parent.call.id"]}`
      const list = byCall.get(k)
      if (list) list.push(p)
      else byCall.set(k, [p])
    }
  }
  return {
    under,
    fallback: (run, call, t) => {
      const list = call ? byCall.get(`${run}\n${call}`) : undefined
      // The one candidate too must start inside the tool: step 0's tool
      // never takes step 1's child because step 0's own is absent.
      const pick = list?.find(
        (c) => c.start >= t.start && c.start <= t.start + t.took
      )
      return pick && runOf(pick)
    },
    claim: (run, claimed: Set<TimedSpan>, bySpan) => {
      for (const [r, list] of byRun)
        if (r === run || r.startsWith(`${run}/`))
          for (const p of list) {
            claimed.add(p.span)
            if (!bySpan.has(spanRef(p.span))) bySpan.set(spanRef(p.span), null)
          }
    },
  }
}

/** treeRows is waterfallRows with the descendant rules: the run tree
 * of a run and its descendants' spans (Studio's run page). */
export function treeRows(input: WaterfallInput, scale?: Scale): WaterfallModel {
  return waterfallRows({ ...input, descend }, scale)
}
