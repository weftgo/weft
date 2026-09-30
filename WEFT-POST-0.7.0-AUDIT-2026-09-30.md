# Post-0.7.0 full audit — 2026-09-30

> Triggered by the maintainer's standing instruction: after `thread/v0.7.0`
> ships, go through all of it — clean, fix, test, verify security,
> production-readiness and reliability, and check it against Weft's principles
> and goals. Runbook: `../WEFT-POST-0.7.0-AUDIT-PLAN.md` (parent repo). Branch:
> `post0.7-audit`.

## Verdict

**Production-ready as released — one test-harness fix landed post-audit.**
The release's own claims all held under independent verification: no production
file changed, every decoder is fuzzed, the crash matrix's site enumeration is
accurate, the budgets are enforced (as failing Tests, not decorative
benchmarks), and the security posture checks out at the code level. The audit
found no product defect. It found one real bug in the new crash-matrix harness
(a timing-dependent invariant that any loaded `-count=10` soak can trip —
fixed here, test-side), one inaccurate CHANGELOG bullet (fixed), one piece of
build cruft (deleted), and two nuances recorded for v0.8. No patch release is
warranted — nothing released behaves wrongly; the harness fix rides to `main`.

## Phase A — the release itself

- Tag `thread/v0.7.0` (lightweight, at `8887cde`) is tree-identical to the
  merge commit `7fe45b7` on `main` (empty diff). Local `main` == `origin/main`.
- Scratch-module verify from the public proxy, outside the repo:
  `github.com/weftgo/weft/thread@v0.7.0` and `github.com/weftgo/weft/thread/sqlite@v0.1.0`
  both resolve, build, and run (thread's `FormatVersion`/`ValidID` exercised;
  sqlite pulls modernc driver + `weft v0.5.0` indirect, as expected).
- Release notes and CHANGELOG match the shipped code, with one bullet-level
  exception fixed in Phase D (below).

## Phase B — gates

| Gate | Result |
|---|---|
| `make fmt` | no-op (tree stayed clean) |
| `make vet` / `make build` | green, all 10 modules |
| `make lint` | 0 issues × 10 modules |
| `make offline` (`WEFT_MODEL_REQUESTS=deny`) | green, all suites |
| `make apidiff apidiff-store apidiff-thread` | green; thread vs `thread/v0.7.0`: no incompatible changes (one locally-added untracked example package reported — see leftovers) |
| `make apidiff-selftest` | all six failure modes behave |
| `make test` (race, count=1, all modules) | green |
| `go test -race -count=10 ./...` on `thread` | green (66s thread, 82s jsonl; re-run after the harness fix below: green) |
| same on `thread/sqlite` | first run: default 10m package timeout under load (invocation artifact); then two findings below; final post-fix soak ×10: **green (477s, full capture)** |
| `GOOS=windows go vet ./...` × all modules | green |
| Fuzz ≥10 min per target (10 targets: 5 root, 4 thread, 1 jsonl) | **all 10 PASS, zero failures** — GrantMatches 57.3M execs, DecideSigned 60.2M, DecodeEntry 40.4M, DecodeHeader 29.2M, jsonl FuzzLoad 19.5M, plus the root's five (PartialJSON, ToolInvokeArgs, Repair, UnmarshalEvent 69.5M, MessageUnmarshal) |
| Benchmarks | run and within budgets: jsonl Open100k ≈ 1.3–1.4 s (budget "generous" over ~3 s measured), Append ≈ 6–9 µs, context-after-50-compactions ≈ µs-scale |
| `make studio-check` | correctly skipped — `studio/` unchanged since `studio/v0.2.1` |

Sqlite soak note: the first `-race -count=10` run of `thread/sqlite` hit go
test's default 10-minute package timeout (`FAIL … 600.281s`) while two
10-minute-per-target fuzz jobs loaded every core — CI never runs count=10
(it runs `-race` count=1, green everywhere, including sqlite); the ×10 soak
is a local gate. Re-runs with `-timeout=40m` and full output capture then
produced the two findings below.

## Phase C — fresh-eyes review

The whole release diff (`thread/v0.5.0..thread/v0.7.0`, 17 files, +1785/−20)
was read file by file. Every file is test-side, harness, CI, Makefile, docs or
a committed fuzz seed — the "not one production file changes" claim is exact.

**Claims vs. code, independently verified:**

- *Fourteen `storage.Append` sites, all covered.* I enumerated the call sites
  myself: exactly 14 in the session layer (turn.go:302/962, perstep.go:71,
  approval.go:345/411, signing.go:306, session.go:856/940, branch.go:108/192,
  steer.go:78/208, compaction.go:242/1070). Twelve sit behind dedicated crash
  points; two are covered by documented shape-equivalence — the expiry sweep
  (approval.go:411, inside `Resume`) writes the same decision+audit batch as
  the `decision` point's site, and the auto trim (compaction.go:1070,
  `writeTrim` on the auto-compaction path) writes the same `CompactionEntry`
  as the `compaction` point's site. `persistStep` is actually killed twice —
  mid-run by the `steer` point and in-flight by the `resume_arm` point.
- *The matrix harness itself* (`threadtest/crashmatrix.go`): sound re-exec
  protocol (env-gated child, marker line, parent SIGKILL for the mid-run
  points, self-kill via `syscall.Kill` for the rest), per-point reopen
  invariants including "the session still continues", and per-backend wiring
  that proves sqlite's dead-holder lock takeover at every point.
- *Fuzz targets* do what the notes claim: the header fixpoint rule (decode →
  encode → decode must hold, first pass may normalize), the signed-decision
  catalogue (only `ErrUnknownKey/ErrBadSignature/ErrExpired/ErrReplay/
  ErrNotPending/ErrArgsChanged`, nothing recorded without the MAC), grant
  determinism (double-evaluation equality), and entry seeds spanning every
  format golden (format1–4 all present in `thread/testdata/`).
- *Security spot-checks at the code level:* `hmac.Equal` (constant-time) at
  signing.go:238; `thread.ValidID` enforced at every jsonl/sqlite entry point
  (Create/Load/Delete/List/watch — an id is one path component, so no
  traversal); files chmod'ed 0600 umask-exact and created dirs 0700 on both
  backends; no string-built SQL anywhere in `thread/sqlite`.
- *Budgets are real enforcement:* `TestBudget*` tests fail over the constants
  in the normal suite (CI's test job runs them); the Benchmarks carry the
  numbers. The List fleet-scan finding is honestly recorded as a v0.8 proposal.

**Findings:**

1. **P2, test harness (fixed):** the crash matrix's `steer` and `clear_queue`
   points asserted a single tree shape, but the steer walk has two legitimate
   durable orderings. A steer accepted while the second model call is already
   in flight stays queued (receipt only); a steer accepted in the
   between-steps window is drained by `steerSource` into the next call and
   the per-step observer persists its message *after* the receipt (ADR 0019's
   drain point + ADR 0011 §7's persist). Under a loaded box the window widens
   and the strict assertion trips — reproduced as
   `TestCrashMatrix/steer: steer kinds = "message,message,message,receipt,message"`
   in a `-race -count=10` soak. The product behaved correctly in both
   orderings; the harness was wrong. Fixed in `threadtest/crashmatrix.go`:
   both points accept both shapes, and the `clear_queue` child no longer
   dies (`n != 1`) when the queue was already drained (`n == 0`). Both
   backends' matrices green ×5 under `-race` after the fix, plus the full
   final soaks: thread module ×10 green, sqlite ×10 green (477s).
2. **P3, open watch-item:** one `TestWatch` failure (1.12s, error-path
   timing) in a single soak iteration under full load; the message was lost
   to output truncation, and it did not reproduce in the next full soak or
   in 30 isolated `-race` iterations under load. Watch's budgets (5s
   awaits) make a plain timeout unlikely; the candidate is a late non-fatal
   assertion (the deleted-session tail's ending error, or a stream error).
   Recorded for the next session: if it recurs, capture with `-v` before
   anything else.
3. **P3, docs (fixed):** the CHANGELOG's crash-matrix bullet enumerated only
   the first walk's points (7 + pool trio) and read as exhaustive, while the
   shipped matrix has twelve points and the review commit (e444a4a)
   documents the fourteen-site enumeration. Corrected on this branch to name
   all twelve points and the two shape-covered sites.
4. **P3, hygiene (fixed):** a 7.5 MB compiled `refund-plan` binary (ELF, Go
   build) sat untracked at the repo root — deleted.
5. **P3, recorded for v0.8 — matrix completeness:** the expiry sweep and the
   auto trim have no dedicated crash point; coverage is by shape-equivalence
   (documented in e444a4a and now in the CHANGELOG). Cheap v0.8 hardening:
   an expiry-armed child and a trim-triggered child. Same class:
   `thread/pool` has five receipt-writing features (acceptance, settlement,
   mirror — crashed — plus the `canceled`/`capped` writes at pool.go:309/420,
   covered by the same `PoolReceiptEntry` shape).
6. **P3, reviewed semantic (no action):** a pool parent's mirror of a child's
   parked boundary lands asynchronously; a handle reopened in that window
   sees the boundary only after the mirror write. The release pass settled
   this test-side (wait on the live session, then reopen — 8887cde) and ADR
   0022's async-answer design covers it; noted here so the semantic is on
   record.

**Leftovers (left in place deliberately — next-version work, not cruft):**

- `docs/adr-input-resume-and-idempotency.md` — ADR-0024 input notes (resume
  semantics, at-least-once tool contract), explicitly "working input".
- `thread/examples/refund-plan/main.go` — the orchestration-ledger example
  those notes reference; compiles and vets clean but has no ADR/CHANGELOG
  story yet.
- Effect worth knowing: because the example sits inside the module tree,
  local gates and CI gates see different trees (local `apidiff-thread`
  reports the package as added; CI's fresh checkout does not). Recommend the
  v0.8 session either commits it with its ADR or moves it out of tree until
  then. Not the audit's call to finish or delete in-flight work.

## Phase D — fixes

- `thread/threadtest/crashmatrix.go`: the `steer` and `clear_queue` points
  accept both legitimate durable orderings (the between-steps drain window);
  the `clear_queue` child tolerates the already-drained queue. The failing
  input was the soak itself — the fix's justification is the ordering
  analysis above (steer.go's `steerSendLocked` receipt-then-drain sequence
  and the per-step observer), in the house style of the v0.5 flake fixes.
- `CHANGELOG.md`: matrix bullet corrected (docs-only; no failing test
  applies).
- `rm refund-plan` (untracked build artifact).
- No behavior fix → **no patch release**; nothing re-tagged. All fixes are
  test-side/docs; they ride to `main` and ship with v0.8's train.

## Phase E — principles conformance

| Principle | Check |
|---|---|
| stdlib-shaped API | unchanged by 0.7 (no production change); v0.5 reviews stand |
| no vendor gravity | unchanged; adapters still wrap official SDKs |
| MCP-native both ways | unchanged |
| explicit concurrency semantics | unchanged; the pool reopen-vs-mirror window is documented above |
| compatibility gate | apidiff green ×3 + selftest; the gate is real (selftest breaks it deliberately) |
| thread imports only root + stdlib | verified by direct-import listing: zero imports outside stdlib + `github.com/weftgo/weft`; `thread/sqlite` adds only `modernc.org/sqlite` |
| no package-level mutable state / `init()` | grep: none in thread/ |
| sealed kinds, loud on unknown | `ErrNewerFormat`/unknown-kind loudness pinned by fuzz + goldens |
| sentinel errors via `%w` | unchanged (0.7 touched no production file) |
| doc comments + runnable examples | no exported API added by 0.7; existing examples run in the suites |
| one-screen API | honestly re-reported by the release itself: AGENTS.md block ~175 lines; restructure is the standing v0.8 item |
| docs are the product | CHANGELOG corrected; README/STATUS accurate post-release |

## Phase F — disposition

- Commit on `post0.7-audit` (CHANGELOG fix + this report), PR to `main`; no
  tag. The audit report and the CHANGELOG fix ride to `main` and ship in the
  v0.8 docs.
- TODO ledger (untracked, per house rules) updated with the audit outcome.
