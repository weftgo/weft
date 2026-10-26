// The glossary's lines the panel draws (U21.3; UX-D-22, UX-F-10): the
// eight core one-liners of its ? overlay and its scope chips' tooltips —
// apart from lib/glossary.ts so the panel bundles these lines and no
// glossary entry (their long text, docs and retired words are Studio's).
// glossary.ts re-exports both and builds each of these terms' short from
// SHORT, so the two surfaces cannot drift.
import { STATUS } from "./status"

const said = (k: keyof typeof STATUS) => `${STATUS[k].word} — ${STATUS[k].hint}`

/** The core terms' one-liners and the scope chips' (U21.3): each is its
 * glossary entry's short (lib/glossary.ts reads them from here). */
export const SHORT = {
  run: "run — one execution of an agent: a prompt in, an answer out, its steps, tokens and cost",
  step: "step N — one model call and the tools it asked for; steps count from 0",
  attempt:
    "attempt — one try of this step's model call; retries and fallbacks add attempts",
  turn: "turn N — one exchange of a session: a run with the session's history",
  session:
    "session — the history your app keeps (weft/thread): its turns, in order",
  scope: "scope — which conversation the panel follows on this page",
  flow: "flow — the run's weft.flow: the scope's flow=, stamped on each run; it filters lists",
  replay:
    "replay — run the agent again from this step in your app; earlier steps are kept",
  parked: said("parked"),
} as const

/** The panel's ? overlay's core one-liners (U21.3), in order. */
export const CORE_TERMS: readonly (keyof typeof SHORT)[] = [
  "run",
  "step",
  "attempt",
  "turn",
  "session",
  "scope",
  "replay",
  "parked",
]
