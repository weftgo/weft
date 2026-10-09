# ADR input notes — resume semantics and the at-least-once tool contract

> Working input, not a decision record. Provenance: the maintainer's Q&A
> session of 2026-09-29 (`../WEFT-THREAD-QA-2026-09-29.md` in the parent
> repo), which compared weft/thread with Temporal, LangGraph and pi on
> durability, replay and resume. This file organizes that discussion into
> ADR-shaped material: claims, field evidence with citations, candidate
> decisions audited against the house rules, verification obligations, and
> the open questions the maintainer must answer. An ADR generated from this
> would most naturally be **ADR 0024 — "Resume semantics and the
> at-least-once tool contract"**, with small amendment notes into ADR 0003
> (tool contract), ADR 0006 (the OnMessages seam) and ADR 0011 (§7).
>
> State of the world when written: thread v0.1–v0.3 released; v0.4 merged
> with **ADR 0011 §7 (per-step turns) shipped via `weft.OnMessages` and
> ratified by the maintainer on 2026-09-29** — "a crash mid-turn loses
> nothing emitted; the turn's end then appends only its bookkeeping … never
> a message twice." Next release: v0.5 (pool, ADR 0022 proposed).
>
> Companion: `../../WEFT-FLOW-TIER.md` (parent repo) — where the
> orchestration tier these decisions feed *lives*: the settled layering
> (core/thread/app/flow-tier), the enforcement ladder, and the gated
> path to a `weft/flow` module. The refund-chat worked example for D3
> below is implemented for real in
> `thread/examples/refund-plan/main.go`.

---

## 0. The claim, and the three schools

**Claim.** weft/thread never re-executes anything to resume. The agent
loop's entire state is the message list; the session file is that list,
durable and complete (per-step since ADR 0011 §7). Resume = `Open` +
walk `leaf → root` + continue the loop from the last durable step. No
replay engine, ever.

The field has three answers to "how does durable state resume?":

| School | State lives in | Resume means | Tax |
|---|---|---|---|
| Replay (Temporal) | running code's locals — **not serializable** | re-execute the function from the top, substitute recorded effects at each operation, continue past the crash point | determinism + worker versioning |
| Checkpoint (LangGraph) | explicit data channels, serialized per superstep | restore state, **but a node re-executes from the top** on resume | logic must be graph-shaped; side effects before an interrupt re-execute |
| Transcript (weft, pi) | the message list itself — serialized by definition | rebuild the list, continue | none for the loop; two residues below |

Why replay exists at all — the minimal example. A Temporal workflow
accumulating `total` from two activity results (3 and 4) crashes after the
second. History holds the *effects* (3, 4) but not `total = 7`, the branch
position, or the loop counter — arbitrary code's local state cannot be
serialized. Re-running the function with substituted results is how the
state gets rebuilt. LangGraph avoids replay across supersteps by forcing
state into channels, but pays it back at node granularity (evidence in §1).

weft needs neither trick because the conversation **is** its own
serialization. The loop's control logic is fixed ("model → tools → append →
repeat"); there is no user code inside the turn whose position must be
restored. The one sentence to keep, for the ADR's context section:

> Temporal replays because its journal remembers what happened but not what
> it meant; weft doesn't need to replay because for a conversation, what
> happened **is** what it meant.

## 1. Field evidence (verbatim, cited)

**pi — the strongest precedent, and the study set's own recommendation.**
The pi harness deep-dive (`docs/frameworks/pi.md`, parent repo) records
harness.md:52: *"after every durable transition, the harness replaces
`operationState(operationId)` with the **complete, total** current state —
never depending on a previous state. After task loss, recovery reads it and
starts at the responsible procedure, **never replaying a journal or
inferring position from what is missing**."* And on the exact residue
below: *"Crash mid-tool with `replay: "never"` → the tool is **not**
re-run; the harness synthesizes an interrupted-result entry from the last
durable checkpoint (§0.5)."* The deep-dive's "For weft" verdict names this
document's central proposal almost verbatim: *"`replay: "never"|"safe"` as
a **per-tool declaration** is exactly the annotation weft's tool options
(TODO §4.3) need for checkpoint/restart semantics."* pi also carries the
warning labels: its crash repair is real code (session-manager.ts:549-556)
and its data-loss bugs (#9482 destructive compaction destroying ~400k
tokens; #9413 new session lost on early interrupt) were closed "Not
planned" — durability failures are where users get hurt.

**LangGraph — the checkpoint school's own double-effect trap.**
`interrupt()` is resume-by-index; on resume *"the node re-executes from the
top"* and *"side effects before an `interrupt()` re-execute on resume (the
classic double-side-effect trap)"* (langgraph.md:304, :318). Interrupts
require a checkpointer (JS throws without one). And ADR 0010's evidence
appendix: LangGraph burned **four on-disk checkpoint formats in 26 months**.

**Temporal — at-least-once, honestly.** Activities execute at least once;
idempotency is the activity author's contract. Replay rebuilds
unserializable state; worker versioning exists because old histories must
replay on new code. The field is adopting it as the *scheduler* layer, not
the conversation layer: OpenAI Agents SDK's Temporal integration GA'd
(Python, March 2026); Pydantic AI ships durable exec as Temporal/DBOS/
Prefect adapters (WEFT-VS-THE-FIELD.md).

**Crush — single-process exactly-once is real engineering.** crush.md:206:
agent.go carries "RunComplete-exactly-once, accept-sequences, cancel
high-water marks" with ~600 lines of turn-lifecycle invariants. Exactly-once
at the turn layer costs real code even without distribution.

**Mastra — the cautionary tale.** 185 open/closed issues with "suspend" in
the title; suspend/resume across their storage layer is the buggiest area of
the framework (mastra.md, WEFT-VS-THE-FIELD.md:155). Resume semantics grown
inside a storage layer, without a format contract, rot.

## 2. The two residues, precisely

**R1 — the effect-without-result window.** A tool executed in the world but
its result not yet journaled (crash between effect and record; per-step
durability narrows this to one step's boundary, it cannot eliminate it).
The journal shows `call_5` with no result. Something must decide: re-run
the tool (risk: double side effect — a second refund) or synthesize an
interrupted result and let the model re-decide. **No journal can close
this**: the side effect happened in the world, not in the file. Temporal
has the identical window at the activity boundary; LangGraph has it before
every interrupt. The only real fix is a per-tool declaration of whether
re-running is safe.

**R2 — logic above the turn.** The loop resumes perfectly, but driving
code *between* turns may hold state ("step 2 of 5 done; VIP branch
chosen"). That state lives in the process, not the file — the Temporal
problem, miniaturized. The weft-shaped fix is not a replay engine; it is
journaling the plan into the session (`custom` entries), making resume
"read state from the file, continue" again — event-sourcing the
orchestration into the same durable record.

## 3. Candidate decisions

### D1 — Journal-resume is the stance; no replay engine, ever

Resume of a crashed turn = `Open` + context walk + continue from the last
durable step. Already true since ADR 0011 §7 (`weft.OnMessages` appends
each step's messages the moment they join the transcript; the turn-end
batch carries only bookkeeping). Nothing new to build; the ADR ratifies
the *stance* so it is never accidentally re-litigated.

Precedent: pi harness.md:52 ("never replaying a journal"); LangGraph's
four burned checkpoint formats as the cost of getting the state boundary
wrong; Mastra's 185 suspend issues as the cost of growing resume inside a
storage layer without a format contract.

Audit: append-only ✓ (resume reads, never rewrites); loud-on-unknown ✓
(an unknown/dangling state is repaired visibly, never guessed); no new
model-visible bytes ✓; no code beyond what §7 shipped ✓.

### D2 — The at-least-once tool contract, with a per-tool replay declaration

The split rule, stated once, in one place:

- **A model call with no recorded output may be freely re-executed.** No
  world-state changed; the dead call's output simply never existed. (Cost
  was incurred; that is honest and visible in the usage ledger.)
- **A tool call with no recorded result is never blindly re-run.** The
  default is repair-as-interrupted — the synthesized result the model
  sees, golden text that already exists (v0.3's interruption text) — and
  the **model re-decides**: it can call the tool again, with intent, as a
  new call. This converts an unsafe automatic retry into a safe,
  model-visible decision, which is the loop-shaped version of idempotency.
- **A tool whose author declares re-runs safe may be re-executed by a
  resuming driver.** This is pi's `replay: "never" | "safe"` as a
  `ToolOption` — the annotation the pi deep-dive explicitly recommends for
  weft's TODO §4.3 tool-options seam, which already exists (`Timeout`,
  `MaxResultBytes`, `StrictInput`, `Sequential`, `PromptSnippet`,
  `WrapTools`; additive variadic, manifest-recorded).

Candidate shape (naming is an open question, Q1): a tool option in the
root — `weft.RerunSafe()` / `RerunUnsafe()` (default) — recorded in the
manifest beside the policy options. The *default must be the safe one*
(never re-run): loud on the unknown, fail toward no-side-effects. Note
`wefttest.Replay` (the recorded-model replayer) already owns the word
"replay" in the testing surface; the tool option should not reuse it.

This extends TODO §3.7's recorded stance ("retry transport, not logic",
after LangGraph) from *retry* to *resume*: **resume re-derives; tools are
at-least-once; idempotency is the tool author's declared contract.**

Precedent: pi §0.5 + harness.md:52; Temporal's at-least-once activities
with the contract on the author; LangGraph's documented double-side-effect
trap as what happens when the declaration is missing.

Audit: functional options ✓ (ToolOption seam exists); manifest-recorded ✓;
no model-visible bytes change — the interrupted-result golden already
exists and is reused ✓; root change (the option lives in the root's tool
seam), so two-phase release per ADR 0005 ✓; residuals pattern — the
*option* is justified because the pi deep-dive already named the consumer
(the resuming driver); a driver that *acts* on it is the supervisor
pattern (D4), example-first.

### D3 — The orchestration ledger: plan state as `custom` entries

Convention, not API: when driving logic between turns has state, journal
it — `s.Custom(ctx, "plan", data)` — and resume reads it from the file.

Shape guidance the ADR should record:

- The `kind` is the app's namespace (`"plan"`, `"experiment"`, …); thread
  stays opaque to it.
- The **data carries its own schema version** (`{"v":1,…}`), and the app's
  reader is loud on a version above what it knows — the entry format's "v"
  rule, re-expressed at the app layer. A silent guess here is the
  miniaturized Temporal-versioning tax; refusing loudly keeps resume
  honest.
- Fold rule: last plan entry wins (or app-defined fold); plan entries are
  `custom`, **never** `custom_message` — the plan is not for the model,
  and `custom` already carries the survival guarantee (ADR 0020 §4:
  custom entries survive every compaction), which is exactly the property
  an orchestration ledger needs.
- Pin nothing; ledger entries are re-derivable by definition (the
  transcript holds the facts; the ledger holds the position).

Precedent: pi's `operationState` — "the *complete, total* current state"
after every durable transition, "never depending on a previous state" —
is precisely this rule, and the strongest formulation in the study set:
write the whole plan each time, so recovery never replays or infers.

**Worked example (refund-support chat).** The app's policy between turns
has steps: ask what happened → look up the order → if ≥ $100 ask for the
receipt → if not VIP wait for a manager → refund. The conversation lives
in the session; the *position* ("awaiting_manager, not VIP, receipt OK")
lives in a process variable — until the process dies Friday 17:03. The
transcript reopens perfectly Monday, but the resumed app must not (a)
re-ask the customer for facts visible in the transcript, (b) infer the
step from the assistant's phrasing (pi's "inferring position from what is
missing" — the next prompt tweak silently breaks resume), or (c) re-run
the policy (the VIP list changed over the weekend; behavior and transcript
now disagree). With the ledger, every policy transition appends
`{"kind":"refund_plan","data":{"v":1,"step":"awaiting_manager","order":
"1234","amount":240,"vip":false,"receipt_ok":true}}`, and Monday's resume
is: reopen → take the last `refund_plan` entry → `switch plan.Step` →
continue. No re-asking, no inference, no re-deciding. Why not a side DB
table: then conversation and position live in two stores that a crash can
leave disagreeing — the same atomic-append file cannot. Why not
`custom_message`: the plan is for the app, not the model, and `custom`'s
compaction survival (ADR 0020 §4) is exactly the required guarantee.
Versioning, concretely: `data` carries `"v"`; when a future build drops
support for v1, an old session's v1 ledger must be refused loudly ("plan
v1 predates the migration"), never guessed — the entry format's "v" rule
re-expressed at the app layer.

Audit: no thread code, no API ✓; pattern + example (`thread/examples/`
candidate) ✓; compaction survival already guaranteed ✓.

### D4 — The supervisor pattern: provided-for, not provided

The control plane from the discussion is scan → claim → `Open` → continue.
What thread/root already supply, and the ADR should enumerate as the
supported surface:

| Supervisor need | weft mechanism | State |
|---|---|---|
| Death detection | advisory lock released by the OS on process death | shipped (jsonl v0.1) |
| Claim / fencing | `ErrLocked` for a second writer | shipped (v0.1) |
| "What needs resuming" | `Pending()`, approval entries, `expiry`; scan headers + tails | shipped (v0.2) |
| "How far did it get" | per-step message entries | shipped (ADR 0011 §7, v0.4) |
| Live watch | `Watcher` tail from another process | shipped (v0.4) |
| Unique resume identity | `<session>-t<n>` run ids unique across reopen | shipped (v0.1) |
| Plan state | custom-entry ledger (D3) | convention |

What deliberately does **not** land in weft, each with its reason and its
seam: cross-machine leases and split-brain protocol (replicated-cluster
engineering — Temporal's decade); task queues, priorities, backpressure
(fleet machinery); a timers daemon (firing belongs to the owner process;
expiry is data, evaluated on touch); a graph DSL (ADR 0014: "loop +
patterns, not a graph"); a replay engine (D1). The escalation path is one
sentence in the docs: **when the supervisor itself must survive its
machine, that is Temporal's job — one turn, one activity, the session file
referenced, never embedded.**

Audit: scope discipline ✓ (harness not framework — the pattern is an
example, not a package); no package-level state ✓; single-writer rule
strengthened, not weakened ✓.

### D5 — Timers stay data

Expiry-on-entries with lazy evaluation is the stance (v0.2 shipped it for
approvals). A derived wake-up (e.g. a storage-level "earliest un-lapsed
deadline" query) is the natural extension if a consumer asks; the owner
process owns firing. No daemon in the library, no background goroutine,
per the code rules. Residuals pattern: nothing to build until the first
supervisor consumer exists.

## 4. Principles audit (house rules × candidate decisions)

| Rule (PLAYBOOK / code rules) | D1 | D2 | D3 | D4 | D5 |
|---|---|---|---|---|---|
| Append-only; nothing deleted | ✓ read-only resume | ✓ repair appends | ✓ ledger appends | ✓ | ✓ |
| Loud on the unknown, never skip | ✓ dangling → visible repair | ✓ default never-re-run | ✓ app must refuse unknown ledger `v` | ✓ | ✓ expiry states a reason |
| thread imports root + stdlib only | ✓ | option lands in **root** (tool seam) | ✓ | ✓ | ✓ |
| Model-visible bytes change only with ADR + golden | ✓ none changed | ✓ reuses the v0.3 interruption golden | ✓ ledger never model-visible | ✓ | ✓ |
| No package state / init / registry | ✓ | ✓ per-tool option value | ✓ | ✓ example, not package | ✓ |
| Functional options, small interfaces | ✓ | ✓ ToolOption seam | ✓ | ✓ | ✓ |
| Residuals: one good default, revisit when asked | ✓ stance only | option justified by the named consumer (pi verdict); **driver behavior stays unwritten until a consumer exists** | convention + example | example-first | nothing until asked |
| Retry transport, not logic (TODO §3.7) | ✓ | ✓ extended, not contradicted | ✓ | ✓ | ✓ |
| Two-phase release for root changes (ADR 0005) | n/a | ✓ required | n/a | n/a | n/a |
| "replay: never" default | ✓ | ✓ the default is the safe side | n/a | ✓ | n/a |

## 5. Interactions with existing decisions

- **ADR 0002** (`RunError.Result`, the partial-transcript rule): D1/D2
  extend its spirit to resume; no change to the rule itself.
- **ADR 0003** (tool contract): needs an amendment *note* recording the
  at-least-once contract and the replay-declaration option (D2) — the
  contract text, not new semantics.
- **ADR 0006**: `OnMessages` (its 2026-09-29 note) is the seam D1 stands
  on; cite, don't change.
- **ADR 0007 / 0014**: pending-approval resume and `SUBAGENT_PENDING` are
  unaffected; the supervisor rescans `Pending()` — already first-class.
- **ADR 0011 §7**: the foundation; D1 ratifies its consequence (resume
  semantics) as a stance rather than new mechanics.
- **ADR 0020 §4**: custom-survives-compaction is why the ledger (D3) is
  sound; cite.
- **ADR 0021 §5**: expiry's lazy evaluation is D5's precedent; cite.
- **ADR 0022 (pool, proposed)**: a pool child that dies is the same
  residue inside one process; D2's declaration governs whether a pool
  driver may re-run a child's tool. Worth one line in ADR 0022 when
  decided.

## 6. Verification obligations (for whatever the ADR decides)

1. **Tests first**, per the PLAYBOOK. D2's option: manifest records it;
   a driver honors `never` by construction (it does nothing), so the
   first real test target is the example supervisor honoring `safe`.
2. **Crash matrix extension**: v0.7's planned `kill -9` matrix (turn end,
   compaction, approval, receipt) gains the per-step write points §7
   introduced — kill between OnMessages batches, verify reopen shows the
   partial steps and at most a dangling call.
3. **Goldens unchanged**: no new model-visible bytes; the interruption
   text is the existing golden.
4. **Example pinned**: `thread/examples/supervisor` (if accepted, Q3) —
   scan, claim, reopen, resume, offline through `wefttest.Script`, output
   pinned — the pattern's documentation.
5. **apidiff**: D2 is additive to the root's tool seam; gate must stay
   clean.

## 7. Open questions for the maintainer

1. **D2 now or later?** Ship the `ToolOption` declaration (root, additive,
   manifest-recorded) in the v0.5 lockstep, or record the contract in the
   ADR and ship the option with the first supervisor example?
   *Recommendation: ADR now, option with the example — the residuals
   pattern; the option without a reader is unpinned surface.*
2. **Naming**: `RerunSafe()`/`RerunUnsafe()` vs pi's `Replay("safe")` vs
   `Idempotent()`. Must not collide with `wefttest.Replay`.
   *Recommendation: `Idempotent()` reads truest to the contract and
   avoids "replay" entirely.*
3. **Ledger kind convention**: free-form app namespace (recommendation) or
   a reserved `"plan"` shape documented in the README's Sessions section?
   *Recommendation: free-form; document the versioned-data and
   loud-on-unknown rules, not the kind name.*
4. **Supervisor example placement**: with v0.5 (pool era — its children
   need the same rules) or v0.7 (hardening)? *Recommendation: v0.7, where
   the crash matrix that proves it already lives.*
5. **ADR shape**: one new ADR 0024 with amendment notes into 0003/0006/
   0011 (recommendation), or amendments distributed only?

## 8. Distillation

| | Temporal | LangGraph | weft/thread (as decided here) |
|---|---|---|---|
| Journal holds | effects | channels per superstep | the transcript, per step |
| Resume | re-execute + substitute | restore + re-execute the node | walk + continue |
| Side-effect window | activity, at-least-once | before every interrupt | one step; default never-re-run, per-tool opt-in to safe |
| Logic state | rebuilt by replay | the channels | custom-entry ledger, app-versioned, loud |
| Clocks | durable timers | n/a (scheduler-external) | expiry as data, owner fires |
| Fleet | the cluster | external | the supervisor pattern; Temporal at the seam |
