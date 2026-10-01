# weft/thread — independent production-readiness review, 2026-10-01

> Scope: `thread` at HEAD of `main` (tag `thread/v0.8.0`; `thread/sqlite/v0.2.0`), the whole
> train v0.1 → v0.8 ("built until 0.7.0" plus the 0.8.0 identity step). Method: gates re-run,
> then seven adversarial reviews (session core, turn/steer/interrupt, approvals/signing,
> compaction/branching, jsonl/sqlite backends + threadtest, thread/pool, API/docs/principles)
> each verifying claims with throwaway tests.
>
> Verdict: **not production-ready.** The 2026-09-30 audit's "no product defect" does not hold.
> Twelve P1s (two signing, two compaction, two turn lifecycle, two pool, four backends), ~45 P2s, and the
> plan's v0.8 freeze (format spec, `Migrate`, empty allow file) was never done — the v0.8.0
> tag was spent on ADR 0024 S5 instead.

## 0. Gates (re-run at HEAD)

build / vet / golangci-lint / `go test -race` on thread, jsonl, pool, examples, sqlite: all green.
Not a signal of correctness: every bug below passes the suite. One CI gate is red at HEAD:
`.github/workflows/ci.yml:90-91` still runs apidiff for the deleted `store` module
(`scripts/apidiff.sh:35` exits 2). `thread/sqlite` has no apidiff gate at all.

## 1. P1 — fix before anyone runs this

| # | Area | Finding | Where |
|---|---|---|---|
| 1 | signing | A signed decision is not bound to the occurrence it was minted for: `RunID` is not in the MAC and the nonce is never recorded at issue time. A stale signature for `call_1` of turn 1 approves `call_1` re-issued in turn 2. | `signing.go:103-126`, `188-206`, `approval.go:849-873` |
| 2 | signing | Empty nonce skips the replay scan; expiry is read from the client-signed value, never from the request. Blank nonce + zero expiry → the same bytes verify twice after expiry. | `signing.go:247`, `:256` |
| 3 | compaction | `AfterCompact` runs with `s.mu` held; a hook touching the session (`Context()`, `Usage()`) deadlocks. Every sibling hook is lock-free. | `compaction.go:200-201`, `:255` |
| 4 | compaction | A trim record between two summary compactions is taken as "the previous compaction" → `PrevSummary=""`, the first summary vanishes from the model's context. Breaks ADR 0020 §1 "iterative". | `compaction.go:361-374` |
| 5 | turn | A mixed tool batch (one call runs, one parks) never closes the boundary after resume: the core's in-place rebuilt tool message is appended as a *new* entry, `danglingCallsLocked` only reads the message after the last assistant, so the parked call reads dangling forever. AutoResume on: resume loops (a model call each) until the provider errors. AutoResume off: the next Send queues forever, silently. Also makes `inputLen` off by one (latent slice panic). | `perstep.go:56-80`, `approval.go:806-825`, `turn.go:412-427`, `886-892` |
| 6 | turn | One failed per-step Append silently drops a message and duplicates another: `persistStep` ignores the failure and the turn-end skip is positional, not content-compared. The model is told the tool call "was interrupted". | `perstep.go:71-74`, `122-125` |
| 7 | pool | Depth guard does not cover fan-out × depth: `pool.New(3)`, one step calling a wrapped agent 3×, each sync-delegating once → all slots held, every child blocks on the semaphore forever; the parent run fails on its deadline, async submits hang until `Close`. ADR 0022 "deeper than max is refused" is false for any fan-out ≥ 2. | `pool/wrap.go:97`, `pool.go:360` |
| 9 | jsonl | A torn tail (crash mid-append) poisons the next Append on every backend: `Open` warns and continues, the next prompt is glued to the torn line, and from the third process on `Load` fails `ErrCorrupt`; with `Salvage` the whole post-crash turn vanishes. The crash matrix never produces a torn tail. | `jsonl/jsonl.go:195-222`, `438-461`, `session.go:331` |
| 10 | sqlite | `Watch` holds the pool's only connection across `yield`; any write from the consumer (a pool parent tailing a child) deadlocks in `BeginTx`. | `sqlite/sqlite.go:117`, `sqlite/watch.go:73-94` |
| 11 | sqlite | Pid reuse: a restarted process with the same hostname and pid (the container shape, PID 1) reads itself as a live foreign holder → permanent `ErrLocked` on the session it crashed on. | `sqlite/sqlite.go:610-634` |
| 12 | jsonl | `Watch` panics (`slice bounds [1:0]`) on a session file with no complete line (empty, or a torn first line). | `jsonl/watch.go:52`, `:122` |
| 8 | pool | A child that parks twice can never be resumed: the pump replays decisions for every mirror ever written, the child rejects the already-decided one with `ErrNotPending`, receipt stays `running` forever. Tests pass only because `wefttest.Script` reuses `call_1`. | `pool/bridge.go:150`, `305-323` |

## 2. P2 — wrong behaviour (confirmed by repro)

Turn / steer / interrupt
- Run-id collision across reopen: `Open` recovers `turnSeq` from the turn-entry count, but the live counter moves on mint and on overflow remint; after an overflow re-run or a lost turn end the next Send reuses an id. (`session.go:303-308`, `turn.go:272`, `698`)
- Interrupt on a `RequireSigned` session (or any `Decide` failure) queues the follow-up then only warns when the denial fails; `Wait` hangs forever. Unlock/relock under a deferred unlock lets another Send slip ahead. (`interrupt.go:34-35`, `76-84`)
- Prompt flush failure: the prompt is adopted as leaf before Flush fails; Send errors; a retry appends a second identical user message. (`turn.go:304-311`)
- The runner's settled-boundary pickup never sets `inFlight`, so `Branch` and a Reject Send pass mid-run and start a second runner on one session. (`turn.go:419-426`)
- Queue-policy sends are not durable on acceptance (`Turn.ID` names an entry that does not exist yet) though ADR 0011 §4 says accepted input is durable; Steer's acceptance is.
- `Turn.Wait`/`Events` cannot be abandoned by ctx; post-turn auto-compaction runs under `WithoutCancel` before the turn is decided.
- Steer outcome overloaded: delivered, deferred and dropped-by-ClearQueue all finish `(nil, nil)`.

Pool
- The hidden wrapper call accepts a direct `Decide` → the delegation re-executes, a second child session is created, the side effect runs twice. ADR 0022 §7 says never offered. (`approval.go:251` vs `:292`)
- A `RequireSigned` parent never receives its child's answer (`resolveWrapper` uses unsigned `Decide`); a later Resume tells the model the delegation was denied. (`pool/bridge.go:391-397`)
- A resumed child runs outside the semaphore and ignores the caller's ctx (`WithoutCancel`). (`pool/bridge.go:325-340`)
- Mirror-append failure leaves the delegate parked, receipt `running`, wrapper offered as a normal request; a crash before the mirror orphans the child forever (no restart re-mirror). (`pool.go:398-402`)
- Nested approvals cannot expire: children get no `RequestExpiry`, mirrors copy zero expiry, the `lapsed` branch is unreachable and would not resume anyway. (`pool.go:283-292`, `bridge.go:159`)

Session core
- Open/Load accept empty ids, duplicate ids, dangling parents and multiple roots silently; a dangling parent silently truncates the model's context. Reachable via `Salvage()` skipping a mid-file line. (`session.go:293-329`, `455-467`, `memory.go:73-98`)
- `Entries()`/`Path()` godoc promises isolation; `cloneEntry` shares `Message.Content` slices and `ReceiptEntry.Msg`; mutating a snapshot corrupts the in-memory tree. (`session.go:415-427`, `1002-1044`)
- `PublicID` can be rotated by `SetInfo` (runs read `Meta()`, `List` matches the header) — exactly the divergence CHANGELOG 0.8.0 says cannot happen. (`session.go:118-121`, `764-781`, `turn.go:829-835`)
- `Fork` drops `WithMeta`/`PublicID`/`WithLineage`, skips `compaction.resolve` (no window → no auto compaction), skips measurement recovery and steer resurrection; its godoc on title/meta is wrong. (`branch.go:142-175`)
- `Open` can start a model run in the background (steer resurrection) under `WithoutCancel`; undocumented. (`session.go:339`, `steer.go:229-249`)
- No in-process one-writer enforcement: two `Open`s on one Memory/jsonl instance both append; `ErrLocked` never returned by Memory; no threadtest row. (`session.go:137-139`, `jsonl.go:431-437`)
- No `Session.Close`/wait: the runner is a detached goroutine; nothing quiesces before exit or `Delete`.

Approvals
- Expiry only enforced by explicit `Resume`; `Decide` on an expired request is recorded and auto-resumed as approved. `Decide` godoc is false on the default path. (`approval.go:354-359`, `392-420`, `turn.go:412-426`)
- `MaxUses` not enforced within one turn (chain audit entries buffered until turn end). (`grants.go:234-262`, `approval.go:539-563`)
- `RequireSigned` is a cfg flag, not session state: dropped by `Fork`/`Open`; it also wedges `Interrupt` and pool delegations, which use the exported unsigned `Decide`. (`signing.go:78-88`, `interrupt.go:72-83`, `pool/bridge.go:115,325,392`)
- Under `Quorum(n≥2)` a single chain approval parks the call with no request entry: no expiry, no `OnRequest`, immortal. (`approval.go:563,593`, `turn.go:776-800`)
- Duplicate call ids in one `Decide` batch accepted; `ApproveAlways`+`Deny` mints a grant for a denied verdict. (`approval.go:299-306`)
- Quorum identity is a free-text `Who`; one key with two names satisfies `Quorum(2)`.

Compaction / branching
- Custom `Trimmer` never changes the model context (only re-derived for the built-in); trims pile up, trigger keeps firing. (`compaction.go:938-954`, `session.go:579-583`)
- Built-in trim replay depends on runtime `keepLast`, not the file — model-visible transcript changes with options (rule 5). (`session.go:583,603`)
- `TokensBefore`/`Preparation.Context` count the raw path, not the compacted view. (`compaction.go:399-422`)
- `max_tokens`-truncated summary accepted silently (rule 8). (`compaction.go:637-658`)
- `BeforeCompact` gets `*Preparation` but only `FirstKept` edits are honoured; a redaction hook does nothing. (`compaction.go:469-494`)
- `SummarizeLeft` feeds the summarizer the raw pre-boundary range plus the summary — the overflow ADR 0020 §1 forbids. (`compaction.go:1119-1135`)

Backends / conformance
- `Watch` stalls forever after Delete+Create of the same id inside one poll (both backends); the promised `ErrNotFound` ending never comes. (`jsonl/watch.go:88`, `sqlite/watch.go:62`)
- `Query.Before` skips sessions on `Created` ties; the doc describes an id cursor the type lacks. (`storage.go:72-80`)
- jsonl `List` allocates 1 MiB per session file per call (2k sessions → 2 GiB/op, 228 ms per page); no jsonl List budget row. (`jsonl/jsonl.go:569`)
- jsonl keeps one fd plus flock per session touched for the process lifetime with no release: a server hits `EMFILE`, and no other process can write or delete idle sessions. Locks are lifetimes, not leases; `Storage` has no Release. (`jsonl/jsonl.go:177`, `457`)
- jsonl `Watch`'s malformed-line error is not `ErrCorrupt`; sqlite's is. (`jsonl/watch.go:83`)
- `List` is a fleet scan in both durable backends (the `sessions_created` index is dead); the SQL-side paging 0.7.0 promised for 0.8 did not ship and the 0.8.0 entry does not say so. (`sqlite/sqlite.go:439-482`)
- jsonl `Watch` re-reads the whole file every 200 ms (2% of a core and 50 MB/s allocation per idle watcher on a 10 MiB session).
- threadtest has no row for `ErrLocked`, concurrent same-session Append, header-level `ErrNewerFormat`, Limit normalisation, Created ties, `Flusher`, `ValidID` on non-Create methods, Watch corruption/replacement/writing consumer; `RunWatch` is not called from `Run`, so a backend can implement `Watcher` badly and still pass.

## 3. P3 — polish, but real

Backends: Windows liveness treats access-denied as dead (takeover of a live holder); jsonl on non-unix silently has no lock; the title rule differs between the backends and `Session.Title()` for an empty-title info entry; sqlite `:memory:` loses the DB if database/sql replaces its connection; the DSN is built by concatenation (`?`/`#` in a path misparse); `migrations` is an init-time var that panics on a bad embed; jsonl Create holds the instance mutex across two fsyncs; "all or none become visible" is overclaimed for cross-process readers and power loss; three copies of `limitOf`/`metaMatch`/`titleMatches`/`splitLines` across backends; `ErrLocked` leaks the absolute path; `open.go` still says `Watcher` is "unimplemented".


Turn/pool: `weft.ErrInvalidSteer` named in the string but not wrapped; `rejectTranscriptOptions` returns bare errors; overflow re-run drops attempt 0's usage from the ledger; `Canceled` set only for `context.Canceled`, not deadlines; `Turn.Wait` doc wrong for non-run failures and the interrupted partial is mutated in place; `Close` does not cover the fallback (non-session) wrap path; `pool.IDs` doc false (mints entry ids too); depth refusal reports `SUBAGENT_CYCLE` for a non-cycle and the pool bypasses the core's real cycle guard; cancel text unpinned; `sessionAgents` never pruned; duplicate `Wrap` names silently overwrite; double settle double-counts `Delegated`; `ErrNotRunning` carries four meanings; `Delete(parent)` orphans children, `Fork` copies the original's mirrors so a fork can decide the original's children.

Stale trigger re-fires on the Send after a compaction (extra summarizer calls); two manual compactions at the same `FirstKept` both apply; `Pin` accepts non-message entries then drops them; tiny windows (`Reserve ≥ Window`) silently never compact; `AppendApprovalRequests` skips the id guards every other write path has; `Branch` to a decision entry re-spends a signed approval; nil-args `ApproveAlways` mints a grant that never matches; JSON-pointer index parsing is lenient (`/01`); `ArgGlob` `?` is byte-wise; numeric equality is float64; unsigned `Decide` can claim `Via:"signed"`; failed `DecideSigned` attempts leave no audit trace; `CorruptError.Unwrap` hides the cause from `errors.Is`; `thread.Proceed`/`Cancel` are mutable package vars; `errNothingToCompact`/`errCompactCanceled` unexported though callers must branch on them; no clock injection anywhere (tests sleep).

## 4. Misalignment with weft principles

- **Nothing silent** — the dominant failure class: items in §2 (tree validation, trimmer no-op, truncated summary, ignored Preparation edits, dropped Fork options, create-only options accepted by Open/Fork, native-compaction fallback swallowing its error).
- **Explicit concurrency semantics** — true in code, scattered across four godoc comments; hook lock discipline unstated and inconsistent (§1 #3); no Close; Open may start a run.
- **stdlib-shaped / small surface** — 282 exported identifiers (thread 247); 48 option constructors in three dialects (`With*`, bare, `Require*/On*`); 24 of 38 session options are compaction knobs; `OpenConfig`/`ResolveOpen` are an exported config struct on the user package; `AppendApprovalRequests`/`AppendPoolReceipt` are pool plumbing on `Session`; `thread.Instructions` collides with `weft.Instructions`; `int` vs `int64` token types mixed; dead public fields (`SplitPrefix`, `Summary.Reason`, `FilesModified`, `TrimReport`).
- **Compatibility gate** — exists for thread; allow file not emptied (its own comment says v0.8 does); sqlite ungated; CI apidiff job red.
- **Docs are the product** — 14 exported names without godoc, 189 without any example; ~18 godoc sites narrate "step 1.x / v0.1 / later"; `doc.go` still says Send "follows in the next steps of v0.1"; STATUS.md header at root v0.3.4 with no thread section; ADR 0011 §5 `Load` signature wrong; PLAYBOOK still names `runtime`/`store`.
- **Imports only root + stdlib** — non-test imports still clean; `go.mod` now carries three OTel modules for tests.

## 5. Plan §10/§11 bar — status

| Item | Status |
|---|---|
| v0.7 fuzz, crash matrix, budgets, security review | done (security review missed §1 #1-2) |
| v0.7 "API still fits one screen" | false, admitted in CHANGELOG |
| v0.8 format spec (ADR 0011 amended, v2-v4 kinds, fields) | **not done** |
| v0.8 `thread.Migrate` exercised on every golden | **not done; symbol does not exist** |
| v0.8 `.apidiff-allow` emptied; deprecations removed | **not done** (`ErrNotImplemented` kept for a v0.1 never tagged) |
| v0.8 SQL-side List paging | not done (fleet scan per page) |
| §11 example per feature | false: Interrupt, Rollback, Resume/Revoke, most compaction layers, pool Forward/Cancel, all signing options |
| §11 `-count=10` race soak in CI | not in CI |
| §11 every ADR decided | ADR 0023 still "proposed" though abandoned |

## 6. Change list (ordered; breaking changes are fine pre-1.0)

**Security / correctness first**
1. Signing: add `RunID` + request-entry id to the challenge and `SignedDecision`; reject empty nonce; verify `sd.Expiry == req.Expiry`; persist issued challenges (or derive nonce as HMAC over request id). Pin stale-occurrence, empty-nonce and client-expiry tests.
2. Sweep expiry on every arming path (`Decide`, `DecideSigned`, runner pickup, Send-arms), not only `Resume`.
3. `MaxUses` counts uses minted in the current chain.
4. Split an internal `decideLocked` (Interrupt, pool, expiry) from the exported guarded `Decide`; persist `RequireSigned` in the header so `Open`/`Fork` inherit it; fail `RequireSigned` without a keyring at construction.
5. Quorum: write the request entry and fire `OnRequest` when the chain decides but the boundary stays open; reject duplicate call ids per batch; force `Via="user"`; bind approver identity to `KeyID` or rename `Who` as declarative (ADR 0021 §5 amendment).
6. `ApplyCompaction` calls `AfterCompact` outside the lock; document "hooks run lock-free and may call the session" and make it true everywhere.
7. `computeCompaction` skips trim records when resolving `prev`; reject a second compaction at the same `FirstKept`.
8. Trim contract: record `keepLast`/stubbed call ids in the entry and replay from the file for built-in and custom trimmers, or delete `WithTrimmer`.
9. `TokensBefore`/`Preparation.Context`/`SummarizeLeft` built from the compacted view; honour `Preparation` edits or pass it by value; treat `StopMaxTokens` on a summary as a failed summary.
10. `Fork` goes through `Open`'s constructor path (options, resolve, measurement, resurrection); decide and document what a fork inherits (pending approvals, grants, steer queue).
11. `Open` validates the tree (unique non-empty ids, every parent held and earlier, one root) → `ErrCorrupt`; `Salvage` reports orphaned ranges; validate `Reserve < Window`, `KeepRecent < Window-Reserve`.
12. `cloneEntry` deep-copies `Content` and `ReceiptEntry.Msg` (and covers every kind); `runMetadata` reads `weft.public_id` from the header; `SetInfo` rejects `weft.*` keys.
13. In-process one-writer registry returning `ErrLocked` (Memory included) + threadtest row; add `Session.Close(ctx)` that drains the runner; defer steer resurrection to the first `Send` or document it at `Open`.
14. Export `ErrNothingToCompact`, `ErrCompactCanceled`, `ErrNoEntry`; make `CorruptError.Unwrap` return `[]error{ErrCorrupt, Err}`; `Proceed()`/`Cancel()` as funcs; a clock option.
15. Grants: strict RFC 6901 indexes, rune-wise `?`, nil args → `{}`, `GrantID` on audit entries instead of parsing `Detail`; record failed `DecideSigned` and a resume-completed audit step; document `Audit` is an index, not evidence.

**Turn lifecycle**
15a. Resume join: write the rebuilt tool message as a replacement (branch back to the assistant, then append) or make `danglingCallsLocked` merge consecutive tool messages; add a mixed-batch approval test on every backend with AutoResume on and off.
15b. Per-step skip becomes content-aware on the success path (or a failed step fails the turn loudly); test with the Nth Append failing.
15c. Recover `turnSeq` from the max `-t<n>` suffix across turn entries, not the count; reopen tests after overflow re-run and lost turn end.
15d. Interrupt: if the denial cannot be recorded, dequeue the follow-up and fail the Send; `RequireSigned` returns `ErrSignatureRequired` up front. Prompt flush failure branches back to the pre-prompt leaf. Set `inFlight` in the settled-boundary pickup.
15e. Queued sends durable on acceptance (reuse the steer receipt shape) or documented as not; `Turn.Wait(ctx)`/`Done()`; decide the turn before the between-turn compaction; a distinct outcome for dropped steers; `RunOptions` godoc no longer claims to carry Approve/Deny.

**Pool**
15f. Fix the fan-out deadlock: release the delegating call's slot while it blocks on a sync child, or refuse sync delegation from depth ≥ 1; rewrite ADR 0022 Consequences honestly; fan-out × depth test with a stateless model.
15g. Replay only decisions whose call is in `child.Pending()`; refuse `Decide` on a mirror's wrapper call; pool-internal resolve that bypasses `requireSigned`; acquire a slot and write a `running` receipt around resumed runs and honour ctx; on mirror failure settle `failed` and return a tool error; restart recovery that re-mirrors from `running` receipts with pending children; propagate `RequestExpiry` into children or delete the dead `lapsed` branch.
15h. `acquire` checks `closed`/joins `wg`; prune `sessionAgents`; refuse duplicate wrap names; guard double settle; split `ErrNotRunning`; export `Receipt.Settled()`; `Decide` returns all turns and a joined error; say "per pool" not "process-wide"; qualify FIFO; define Delete/Fork behaviour for children; carve the rule-13 usage exception into AGENTS.md.

**Backends**
15i. jsonl: truncate a torn tail under the writer's lock on first write and report it; add a crash row that dies *inside* a write, then reopen, Append, reopen, clean Load.
15j. sqlite Watch drains rows before yielding; lock row carries process start time (migration 0003) compared in `acquire`, special-casing `pid == own pid`; Windows access-denied → alive.
15k. jsonl Watch guards the empty-lines case; both Watches end with `ErrNotFound` when the session is replaced; jsonl Watch wraps `CorruptError` and stat-and-seeks instead of `ReadFile` per tick.
15l. `Query.BeforeID` keyset cursor pinned on ties; jsonl `readHeader` via bounded `bufio` plus a jsonl List-10k budget row; then SQL-side `WHERE created < ? ORDER BY created DESC, id DESC LIMIT ?` with `COUNT` for Total.
15m. A `Releaser` capability (`Release(ctx, id)`) that `Session.Close` calls (jsonl closes the fd, sqlite drops its lock row); jsonl on non-unix uses `LockFileEx` or refuses to Open without an explicit no-lock option.
15n. threadtest: rows for ErrLocked, snapshot Load under Delete, concurrent same-session Append, header ErrNewerFormat, Limit normalisation, ValidID on every method, Flusher, Watch corruption/replacement/writing consumer; `Run` calls `RunWatch` when the backend implements `Watcher`; one shared internal package for the title/limit/split rules; `jsonl/example_test.go`.

**The real v0.8 freeze (do before v0.9)**
16. One option dialect (drop `With*`), rename `thread.Instructions` → `SummaryInstructions` (or fold into `SummaryFocus`), `Disabled` → `NoAutoCompact`, uniform `int64` tokens; move compaction options to `thread/compact` or collapse the four hooks into one; move `OpenConfig`/`ResolveOpen`/`Flusher`/`Append*` plumbing to an internal/backend package; delete `ErrNotImplemented`, `SplitPrefix`, `Summary.Reason`, `FilesModified`, `TrimReport`; `WithApprover(a, timeout)`; `(*Keyring).Sign`.
17. ADR 0011 §2/§6 amendment = the written format spec (every kind v1-v4, fields, envelope, `v` rule, promise); ADR 0020 amendment (summary message shape shared with branch summaries, pinned placement, trim replay rule, unimplemented native seam); ADR 0023 marked abandoned.
18. `thread.Migrate` (no-op for v1) exercised on every golden through `UnmarshalEntry` and both durable backends; empty `.apidiff-allow`; add `thread/sqlite` to `apidiff.sh`/Makefile/CI; delete the red `store` CI step; nightly `-race -count=10`.
19. Docs: rewrite `doc.go` to the shipped surface with the concurrency rules in one paragraph; scrub step-number godoc; godoc for the 14 bare names; examples for Interrupt, Rollback, Resume/Revoke, signing flow, quorum, each compaction layer, pool Forward/Cancel; `docs/thread-operations.md` (layout, backup incl. sqlite WAL, locks and takeover, limits incl. List fleet scan, error classes, retention); STATUS.md thread section; AGENTS.md block 8 cut to ≤15 lines; commit or remove `examples/refund-plan` (untracked, untested, skews local vs CI apidiff); `examples/session` fails on second run (fixed id in TempDir).

All seven areas reviewed; scratch probes live only in the session scratchpad, the repo tree is unmodified apart from this file.

## Resolution (2026-10-01/02)

The findings above were fixed as a train of seven lanes on branch
`thread-prod-fixes` — session core, compaction, backends, approvals,
the writer lease, the pool, the turn machinery — followed by a final
code pass and this docs pass. Commits are named by their lane sha
(each is an ancestor of the branch head); tests are the ones that fail
on the old code. Test paths are under `thread/` unless a package is
named. The CHANGELOG's fix-train entry is the user-facing record; the
ADR amendments of 2026-10-01 (0011, 0019, 0020, 0021, 0022) are the
decisions.

### P1 — all twelve fixed

| # | Finding | Lane | Fix | Pinned by |
|---|---|---|---|---|
| 1 | signed decision not bound to its occurrence | approvals | `b73f4aa` | `TestSignedDecisionBoundToItsOccurrence`, `TestRepeatedCallIDOccurrencesStaySeparate` |
| 2 | empty nonce skips the replay scan; client-chosen expiry | approvals | `b73f4aa` | `TestSignedDecisionNeedsAnIssuedNonce`, `TestSignedDecisionCannotChooseItsExpiry` |
| 3 | `AfterCompact` under the lock deadlocks | compaction | `5fc1c09` | `TestAfterCompactMayCallTheSession`, `TestEveryCompactionHookMayCallTheSession` |
| 4 | a trim cuts the iterative summary chain | compaction | `406c685`, `5fc1c09` | `TestCompactTrimCompactKeepsTheChain` |
| 5 | mixed tool batch never closes its boundary | turn | `dec5959` | `TestMixedBatchAutoResume`, `TestMixedBatchManualResume`, `TestMixedBatchAcrossReopen`; `threadtest.RunTurns` / `MixedBatchResume` |
| 6 | one failed per-step append drops and duplicates | turn | `dec5959` | `TestPerStepOneFailedAppendLosesAndDuplicatesNothing`, `TestMixedBatchResumeSurvivesAFailedAppend` |
| 7 | pool deadlocks under fan-out × depth | pool | `1b9070f` | `pool`: `TestHandoffNoDeadlock`, `TestSlotHandOff`, `TestDepthLimit` |
| 8 | a child that parks twice cannot resume | pool | `e8e0aa2` | `pool`: `TestChildParksTwice` |
| 9 | a torn tail poisons the next append | backends | `55e8bd3` (Memory), `4cd9d41` (jsonl), `e4e58e9` + `6ec3590` (sqlite) | `jsonl`: `TestCrashMidAppendThenNextWriterAppends`, `TestTornTailRepairedBeforeAppend`; `sqlite`: `TestAppendAfterTornRepairs`; `threadtest.Run` / `AppendAfterTornTail` |
| 10 | sqlite `Watch` deadlocks a writing consumer | backends | `e4e58e9` | `threadtest.RunWatch` / `ConsumerWritesInsideLoop` |
| 11 | sqlite lock reads a reused pid as a live holder | backends | `e4e58e9` | `sqlite`: `TestLockTakeoverRules`, `TestProcessTokenIsPerProcess`, `TestProcStart` |
| 12 | jsonl `Watch` panics on a file with no complete line | backends | `4cd9d41` | `jsonl`: `TestWatchWithoutHeaderIsCorrupt` |

### P2 — by group

| Group | Finding | Lane | Fix | Pinned by |
|---|---|---|---|---|
| Turn | run-id collision across reopen | turn | `80883b4` | `TestRunIDRecoversAfterAnOverflowReRun`, `TestRunIDRecoversAfterALostTurnEnd`, `TestRunIDRecoversPastIDsThatLeftNoTurnEntry` |
| Turn | interrupt under `RequireSigned` / a failed denial hangs `Wait` | approvals, turn | `d0bba20`, `80883b4` | `TestInterruptDeniesUnderRequireSigned`, `TestInterruptDenialFailureFailsTheSend`, `TestInterruptRefusedLeavesNothingQueued` |
| Turn | prompt flush failure duplicates the prompt | turn | `80883b4` | `TestPromptFlushFailureLeavesNoOrphanPrompt` |
| Turn | settled-boundary pickup not in flight | turn | `80883b4` | `TestSettledBoundaryPickupIsInFlight` |
| Turn | queued sends not durable at acceptance | turn | `80883b4` | `TestQueuedSendSurvivesARestart`, `TestRestoredQueuedSendRunsBeforeTheNextSend`; `threadtest.RunTurns` / `QueuedSendRestored` |
| Turn | `Wait` cannot be abandoned; compaction before the turn is decided | turn | `80883b4` | `TestTurnDoneAndWaitContext`, `TestWaitReturnsBeforeTheBetweenTurnCompaction` |
| Turn | steer outcome overloaded | turn | `80883b4` (ADR 0019 amendment, `5c6c2b0`) | `TestTurnOutcomes`, `TestClearQueue` |
| Pool | the wrapper call accepts a direct `Decide` | pool | `e8e0aa2` | `TestDecideRefusesDelegatingCall`; `pool`: `TestWrapperNotDecidable` |
| Pool | a `RequireSigned` parent never gets its child's answer | approvals, pool | `d0bba20`, `e8e0aa2` | `TestPoolDelegationCompletesUnderRequireSigned`; `pool`: `TestNestedSignedDecision` |
| Pool | a resumed child runs outside the semaphore | pool | `e8e0aa2` | `pool`: `TestResumeHoldsSlot` |
| Pool | mirror-append failure; a crash before the mirror | pool | `e8e0aa2` | `pool`: `TestMirrorFailureFailsDelegation`, `TestRecoverParkedUnmirrored` |
| Pool | nested approvals cannot expire | pool | `e8e0aa2` | `pool`: `TestNestedExpiry`, `TestMirrorExpiresWrapperDoesNot` |
| Core | Open/Load accept a malformed tree | core | `11c7cd6` | `TestOpenRejectsMalformedTree`, `TestOpenSalvageReportsOrphans`, `TestPathFailsOnABrokenLink` |
| Core | snapshots share memory with the tree | core | `11c7cd6` | `TestSnapshotsShareNothingWithTheSession` |
| Core | `PublicID` rotated by `SetInfo` | core | `11c7cd6` | `TestSetInfoRules`, `TestMetaReservedKeyFirstWriteWins` |
| Core | `Fork` drops options and skips Open's derivations | core | `11c7cd6` | `TestForkHonoursHeaderOptions`, `TestForkDerivesStateLikeOpen`, `TestForkNeutralisesPoolState` |
| Core | `Open` can start a run | core | `11c7cd6` | `TestOpenStartsNoRun` |
| Core | no in-process one-writer rule | writer | `43cf085`, `6560bb5` | `threadtest.RunLeaser`, `threadtest.RunOneWriter` (`TestSecondOpenIsLocked`) |
| Core | no `Session.Close` | core | `11c7cd6` | `TestCloseDrainsRunningTurnAndQueue`, `TestCloseContextEndCancelsTheTurn`, `TestCloseConcurrent` |
| Approvals | expiry enforced only by `Resume` | approvals | `b73f4aa` | `TestDecideOnExpiredRequestIsRefused`, `TestSendOverExpiredBoundaryResumes`, `TestSweepWritesOneDenialPerExpiredRequest` |
| Approvals | `MaxUses` not enforced within one turn | approvals | `b73f4aa` | `TestGrantMaxUsesWithinOneTurn` |
| Approvals | `RequireSigned` is a process flag | approvals | `b73f4aa`, `d0bba20` | `TestRequireSignedIsDurable`, `TestForkInheritsRequireSigned`, `TestRequireSignedHeaderGolden` |
| Approvals | quorum-open chain approval parks with no request | approvals | `b73f4aa` | `TestQuorumChainApprovalStillParksARequest` |
| Approvals | duplicate call ids; `ApproveAlways` beside a deny | approvals | `b73f4aa` | `TestDecideRejectsInvalidBatches`, `TestApproveAlwaysGrantsOnlyAnEffectiveApproval`, `TestDenyAlwaysMintsNoGrant` |
| Approvals | quorum identity is a free-text `Who` | approvals | `b73f4aa` | `TestSignedQuorumCountsKeys` (unsigned `Who` stays a declaration, documented on `Quorum`) |
| Compaction | custom `Trimmer` never changes the context | compaction | `5fc1c09` | `TestCustomTrimmerChangesTheContext`, `TestUnrepresentableTrimIsLoud` |
| Compaction | trim replay depends on runtime options | compaction | `406c685`, `5fc1c09` | `TestTrimReplayIgnoresTheCurrentOptions`, `TestTrimRecordGolden` |
| Compaction | `TokensBefore` counts the raw path | compaction | `5fc1c09` | `TestTokensBeforeCountsTheCompactedView` |
| Compaction | truncated summary accepted | compaction | `5fc1c09` | `TestTruncatedSummaryIsNeverStored` |
| Compaction | `BeforeCompact` edits ignored | compaction | `5fc1c09` | `TestBeforeCompactEditsAreHonoured` |
| Compaction | `SummarizeLeft` feeds the raw range | compaction | `5fc1c09` | `TestSummarizeLeftKeepsTheBranchCompaction` |
| Backends | `Watch` stalls after Delete+Create | backends | `4cd9d41`, `e4e58e9` | `threadtest.RunWatch` / `ReplacedSessionEndsTheTail` |
| Backends | `Query.Before` skips ties | backends | `55e8bd3` | `threadtest.Run` / `ListPagesThroughTies` |
| Backends | jsonl `List` allocates 1 MiB per file | backends | `4cd9d41` | `jsonl`: `TestBudgetList10k`, `TestListBoundedSkipsOversized` |
| Backends | jsonl holds an fd and a lock per session for ever | backends, writer | `55e8bd3`, `4cd9d41` | `threadtest.Run` / `Releaser`; `jsonl`: `TestReleaseDropsTheFile` |
| Backends | jsonl `Watch` malformed line is not `ErrCorrupt` | backends | `4cd9d41` | `threadtest.RunWatch` / `CorruptLineIsErrCorrupt` |
| Backends | `List` is a fleet scan (sqlite) | backends | `e4e58e9` | `sqlite`: `TestListPageUsesTheIndex`, `TestBudgetList10k`. jsonl still reads every header per call — bounded and budgeted, documented |
| Backends | jsonl `Watch` re-reads the whole file per tick | backends | `4cd9d41` | `jsonl`: `TestWatchPollReadsOnlyNewBytes` |
| Backends | threadtest has no rows for the rules above | backends, writer, turn | `17f8d06`, `43cf085`, `7199e14` | the rows themselves; `Run` calls `RunWatch` |

### P3

Fixed with their lanes, each with a test: the Windows liveness reading
of access-denied (vet-only, see below), the non-unix jsonl lock
(`TestLockSupportGate`), one title rule on every backend
(`TestTitleRules`, migration 0003), sqlite `:memory:` across a replaced
connection, paths with URI characters, migrations returning errors,
jsonl `Create` off the instance mutex
(`TestSlowSetupBlocksOnlyItsOwnSession`), the shared backend rules in
`thread/internal/rules`, `ErrLocked` naming the session; the wrapped
`weft.ErrInvalidSteer` and `weft.ErrInvalidRunOption`, the overflow
attempt's usage, `Canceled` on a deadline, the interrupted partial
completed on a copy, `Close` over the bare wrap path,
`SUBAGENT_DEPTH` and a real cycle guard, the pinned cancel text,
pruned registrations, duplicate wrap names, idempotent settlement, the
split `ErrNotRunning`, `Children`/`Descendants` for a deleted parent,
a fork that leaves the origin's mirrors out; the trigger's stand-down,
the same-boundary refusal, `Pin`'s refusal, the validated window
knobs, id guards on `AppendApprovalRequests`, spent decisions, the
grant predicate fixes, `Via` always `user` on `Decide`, audited signed
refusals, `CorruptError.Unwrap`, `Proceed()`/`Cancel()`, the exported
compaction sentinels, and `thread.Clock`.

The "all or none become visible" claim is restated rather than made
true: `Append` is atomic against the writer's death, not isolated from
a concurrent cross-process reader (`Storage.Append`'s godoc, ADR 0011
amendment §B). sqlite keeps its own copies of the list rules — it is
its own module and cannot import `thread/internal`; the conformance
table holds the copies to one answer.

### Principles and docs (§4, §6 item 19)

- Godoc: no exported name in `thread`, `pool`, `jsonl`, `backend`,
  `threadtest` or `sqlite` is without a doc comment (checked with an
  AST walk on this branch); the step-number narration is gone
  (`e90aeff`); `doc.go` describes the shipped surface with the
  concurrency contract in one place.
- Examples: added across sessions, turns, steering, compaction,
  approvals and signing, the pool and the backends (`2aaa84a`,
  `3ec4a73`, `1a8495a`, `298d6aa`, `32bbff8`, `fe2fb23`).
  `examples/session` is re-runnable (`e78792b`).
- `docs/thread-operations.md` exists; AGENTS block 8 is 25 lines with
  the thread rules beside it; README's snippets compile
  (`TestDocsSessionsBlocksCompile`); ADR 0011 carries the amendment
  and a format reference; ADR 0023 is marked abandoned.
- `OpenConfig`/`ResolveOpen` moved to `thread/backend`;
  `thread.Instructions`, `Disabled`, the mixed `int`/`int64` token
  types and the dead public fields are gone.

### Not fixed / deferred

By decision (maintainer, 2026-10-01) — the freeze is deferred until
the API has proven stable in real use; production readiness does not
wait for it:

- the format spec as a **compatibility promise** (ADR 0011's appendix
  documents the current format and says it is not one);
- `thread.Migrate` (the symbol still does not exist);
- emptying `thread/.apidiff-allow` (it now lists this train's
  deliberate breaks against `thread/v0.8.1` under a dated block);
- one option dialect — `With*`, bare and `Require*`/`On*` names still
  coexist; moving the compaction options to their own package or
  collapsing the hooks. Renames wait for the freeze.

Still open, known, documented:

- The pool's plumbing is still exported on `Session`
  (`thread/poolplumbing.go`: `ReplayDecisions`, `CancelDelegated`,
  `MirroredRequests`, `DenyMirrored`, `ResolveDelegation`,
  `AppendApprovalRequests`, `AppendPoolReceipt`). The exported surface
  grew in this train; "small surface" is not met.
- The Windows code paths (jsonl's `LockFileEx`, sqlite's liveness and
  start time) compile and vet; they have never been executed.
- Stale-writer detection is a count plus the header's creation time,
  not a content comparison of the entries.
- sqlite's lock is never taken over across hostnames on its own; the
  operator calls `sqlite.BreakLock` (`docs/thread-operations.md` §4).
- `Audit()` is an index, not evidence; quorum over unsigned decisions
  counts declared names.
- The steer queue is unbounded.
- jsonl `List` reads every header per call.
- `Recover` reads a finished child's answer as its last assistant
  text, cannot rebuild the ancestry above a rebuilt child, and assumes
  one pool owns a storage's delegations.
- `examples/refund-plan` (untracked in the maintainer's checkout) is
  neither committed nor removed.
- `Session.Request` without a keyring fails with a plain error (no
  sentinel) before any other check.

Closed by the final code pass (lane `tfix/final`):

| Item | Commit |
|---|---|
| The decision chain never decides a delegating call | `699de6a` |
| A parent-recorded decision (interrupt, expiry, direct Decide) pumps the child | `f150c60` |
| A fork settles a copied queued send as dropped | `5229b1c` |
| `Continue` runs restored queued sends under its own context | `3658cdf` |
| `threadtest.RunTurns` on jsonl and sqlite | `cf806ab` |
| `CustomMessage` refused with `ErrBusy` while a turn runs | `bac2cb6` |
| Delete + re-Create under an open Session is stale | `d1e2ed0` |
| jsonl: a failed append leaves no prefix of its batch | `baba0c9` |
| Crash matrix: eight more points | `7347555` |
| CI: sqlite apidiff gate, nightly soak, self-test fixed, allow file lists the breaks | `25d5d33` |
| Fuzz seeds and the golden walk cover every format directory | `88047b5` |
| `examples/studio-local` keeps one Session | `5f12a06` |
| sqlite fsync policy is the synchronous level; Flush checkpoints | `72f52a9` |
| Godoc corrected: `Turn.Next`, `Clock`, `Fork`, `FormatVersion`, `Outcome` | `603d0f1` |
| `sqlite.BreakLock` | `effcb31` |
| Internal path reads no longer deep-copy the transcript | `6cf8e43` |
| Comments describe behaviour, not plan steps | `36108b1` |
| `TestSentinelsAreMatchable` (thread: 31 sentinels; pool: 7), and a closed session refuses `Compact` before any work | the closing commit |

Before a release: maintainer review, merge to `main`, the two-phase
tags (root first), and a re-run of the `-race -count=10` soak and the
full fuzz pass.
