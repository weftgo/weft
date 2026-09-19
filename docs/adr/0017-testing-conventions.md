# ADR 0017 — Testing conventions: replay, wefttest growth, the fuzz gate

- Status: decided (2026-09-19, TODO §9.2/§9.3/§9.4; 9.1 closed 2026-09-10)
- Implementation plan: `docs/phase2-testing-plan.md`; every open decision
  it marked **Guess** is recorded here (the T-register below), plus the
  corrections the implementation forced (the C-register).
- THE-END-GOAL: "testing/replay story from day one" is principle 8;
  "`wefttest`: httptest for agents" is the day-one bar; principle 5
  (no dependencies) and principle 7 (additive evolution) shape every
  choice.
- Amends: ADR 0013 (one sentence — application-level replay is this
  ADR's; wire fixtures stay the adapter proof).

## Context

"Record/replay" answers two different questions, and the research
answers each from a different framework:

1. **Does the adapter parse the vendor's wire format?** The input is
   HTTP bytes; the recording must be bytes, because an adapter bug is
   exactly a bug *between* the bytes and the events. Crush does this
   with go-vcr and notes the cassettes rot as vendor formats drift
   (`docs/frameworks/crush.md` §9.1). Weft already answers it with
   curated `.sse` fixtures behind `conformance.FixtureServer` (ADR
   0013) — hand-recorded, committed, diff-reviewed. Its Done line
   ("adapters' conformance suites run offline on fixtures") was already
   true before §9.
2. **Does my *application* behave correctly given what the model
   actually said?** The code under test is the agent's tools, prompts,
   loop configuration, middleware — the adapter is incidental. A
   recording at the `weft.Model` seam is exactly right: vendor-neutral
   (the same fixture replays whichever adapter recorded it), small,
   HTTP-free.

pi deliberately ships **no** VCR at all (hermetic scripted provider +
compat-matrix adapter tests + live evals; `pi.md` §9) — a caution about
(1), where cassette rot is real. Deer-flow's §9.1 contributes the
keying discipline for (2): deterministic keys, no timestamps, the
recording is the review.

## Decision

**Both layers, one answer each: adapters keep wire-level `.sse`
fixtures (ADR 0013, unchanged); application tests get model-event
replay at the `weft.Model` seam — `wefttest.Record` / `wefttest.Replay`
— small and loud.** No HTTP cassettes, no go-vcr, no auto-matching of
vendor requests, no dependency (`encoding/json` + `hash/fnv` only);
pi's stance is honoured for the layer it warned about, Crush's need
(offline CI against real transcripts) is met at the cheaper layer.

### The replay seam

`Record(t, dir, inner)` and `Replay(t, dir)` are ordinary `weft.Model`s
placed where the adapter would be — the same seam `mw.Retry` and
`mw.Fallback` sit on — so middleware, `PrepareStep`, subagents, and
approvals run unchanged above them. Nothing in the loop knows either
exists. The concrete types are unexported; the returned models also
satisfy `interface{ Requests() []weft.ModelRequest }` and
`interface{ Info() weft.ModelInfo }`.

### The key

`FNV-64a` over the canonical JSON of:

| Field | Keyed? | Notes |
|---|---|---|
| `req.Messages` | **yes, verbatim** | weft's own message JSON, call ids included |
| tool names | **yes, sorted** | the catalogue changes the answer; descriptions and schemas are what a prompt tweak changes |
| `req.Thinking` | **yes** | level and budget; omitted when zero |
| `req.SequentialTools` | **yes** | omitted when false |
| `req.System` | **no** | recorded in the file for the reviewer, not keyed — a prompt-wording tweak must not invalidate every fixture |

**No volatile-substring normalisation** (the TODO's sketch asked for
it): a regex over timestamps and UUIDs would silently merge requests
that should differ and still miss the volatile thing a prompt actually
carries (a date in words, a random seed). The discipline is the
golden-file one — a test that wants replay keeps its prompts
deterministic, and the miss error tells it which request drifted.
Orbit: a `ReplayOption` with `Normalize(func(string) string)` if real
suites prove the need.

**The test name comes from `t`, not ctx.** A `context.Context` does not
carry the test; the TODO's "via ctx" is not implementable without a
core hook the core should not grow (the plan's own rule: "if a helper
seems to need a core hook, the helper is wrong"). Both take
`testing.TB`, like `Golden` and every conformance server; subtests get
their own directories (`TestX/sub` → `dir/TestX/sub/`).

### The file

`dir/<t.Name()>/<seq:03d>-<hash:016x>.json`, `seq` 1-based in
conversation order (so a listing reads in order and identical requests
never collide); pretty-printed JSON (`MarshalIndent` + trailing
newline), `0o644`, directories `0o755`, like `Golden`. Shape:

```json
{
  "model":   {"provider": "openai", "name": "gpt-5"},
  "request": {"system": "…", "messages": […], "tools": ["lookup"], "thinking": {"Level": 2}},
  "events":  [{"type": "tool_call", "id": "call_abc", "name": "lookup", "args": {"order": 42}}, …],
  "error":   ""
}
```

- The event envelope is **wefttest's own** (`type` discriminator,
  snake_case keys — `weft.Event`'s convention, ADR 0004): five event
  types, ~40 lines. The core has no JSON codec for `ModelEvent` and §9
  does not add one; if the store (TODO §11, ADR 0010) ever wants the
  model-side stream persisted, the envelope moves to the core with an
  ADR 0010 amendment. `tool_call.args` is embedded as a JSON value so
  the diff shows the arguments.
- `error` is the stream error's text. A non-empty `error` replays as
  `errors.New(text)` — the *type* is lost — except weft's own
  sentinels, re-wrapped by prefix match so `errors.Is` holds for the
  one realistic case (a denial recorded under the kill switch). A test
  asserting on a *vendor* error type is testing the adapter, which is
  layer (1)'s job.
- `Record` **replaces** the test's directory on its first stream,
  never merges — merging is how stale entries survive a prompt change.
- A caller that breaks out of a recorded stream still leaves a file:
  the events it saw, `"error": "wefttest: stream abandoned by the
  caller"`.
- `Replay` reads the directory once at construction (files in name =
  sequence order, grouped by key); a missing directory is not a
  construction failure — the first request misses loudly. A file that
  exists but does not decode fails construction: that is fixture
  corruption, not a miss.
- `Replay.Info()` is the first fixture's model (a mixed-model
  recording — `mw.Fallback` mid-run — reports the first).

### Misses are loud, doubles are honest

A request no fixture answers fails the stream with `ErrNoFixture` — a
distinct diagnosis from `ErrScriptExhausted` — naming the fixture
directory, the key, and the first user message's opening words.
Repeated identical requests (a retry, a re-ask) replay in recorded
order; the N+1th is a miss, never a stale repeat. `Replay` ignores
`WEFT_MODEL_REQUESTS` like every wefttest model (the switch governs
egress, and doubles make no request); `Record` does not check it
either — `inner` does, so a recording made under deny stores the
denial's text and the diff shows it. Both honour the Model contract:
ctx error first, exactly one `ModelFinish`, `Info` forwarded;
`conformance.Run` green against a `Replay` of recorded transcripts is
the proof (`TestSuiteGreenAgainstReplay` — the four transport cases,
whose behaviour a recording cannot carry, keep their purpose-built
models).

**No `-record` flag and no env var is read by wefttest** (T8):
`Golden`'s `-update` rewrites a comparison; recording makes paid
requests, and the decision belongs to the suite that holds the key.
The godoc shows the pattern with `WEFT_RECORD` as the *suggested*
switch name so suites converge on one spelling.

### The scripting helpers (§9.3)

| Addition | Replaces / serves |
|---|---|
| `Args(v any) string` | the TODO's `Call{Args: any}` — a public field's type change is incompatible under apidiff; a helper is additive and reads as well |
| `Raw(events ...weft.ModelEvent) Turn` | the TODO's `Malformed(...)` family — every contract violation is one `Raw` away; also the way to script signed reasoning blocks (making `Think`'s "signed blocks are scripted with raw model events" doc literally true) |
| `SayThenFail(text, err) Turn` | mid-stream failure after output — `mw.Retry`'s hard case; pins that the loop discards the partial turn |
| `(Turn).WithUsage(u)` | lifts a turn off the fixed 10/5 so budget tests state their arithmetic; no-op without a finish |
| `Request` + `HasTool`/`ToolNames`/`LastText`, `Model.LastRequest` | the three assertions agent tests make most; every field stays reachable through the embedded `ModelRequest` — matchers, not a DSL |

`LastText` is precisely *the last text part of the request's last
message*: the user's prompt on the first call, a follow-up later, `""`
when the last message is a tool message (C3 — the plan's parenthetical
"the tool result" overstated; `Messages` stays right there).

`Model.Stream` restructure this forced (C1): a turn's error now yields
**after** its events — one check, not the plan's "keep the
before-check too", which would have yielded `SayThenFail`'s error
before its text. `Fail` (no events) behaves identically.

### The fuzz gate (§9.4)

Four targets exist (`FuzzToolInvokeArgs`, `FuzzRepair`,
`FuzzUnmarshalEvent`, `FuzzMessageUnmarshal` — the TODO's names
reconciled to the code's, plus `FuzzRepair`, which has already earned
a committed crasher-seed). New: `make fuzz` runs every `Fuzz*` target
of the root module — discovered by `go test -list 'Fuzz.*'`, so a new
target needs no Makefile edit — one invocation each (Go fuzzes one
target at a time), `FUZZTIME ?= 10s` apiece. A dedicated CI job
(`fuzz`, stable Go, root module only, not under `-race` — the seeds
already run under `-race` in `test`) fails on any crasher and uploads
`testdata/fuzz` as an artifact on failure; the fix PR commits the
input as a regression seed. Standing rule (AGENTS.md): a new message
part or event type adds a fuzz seed.

## The registers

The plan's guesses, as shipped (T1–T13), and the implementation's
corrections (C1–C6):

| # | Decision |
|---|---|
| T1 | Two replay questions, two answers: adapters keep wire `.sse` fixtures (ADR 0013); application tests get model-event replay at the `Model` seam |
| T2 | `Replay(t, dir)` / `Record(t, dir, inner)` take `testing.TB`; the directory is `t.Name()`, not ctx |
| T3 | Key = FNV-64a over messages (verbatim), sorted tool names, thinking, sequential; not the system prompt; no volatile-substring normalisation |
| T4 | File = `dir/<test>/<seq>-<hash>.json`, pretty JSON, wefttest's own five-type envelope; `Record` replaces, never merges |
| T5 | Miss → `ErrNoFixture` naming dir, key, first user words |
| T6 | `Replay.Info()` is the recording's model; `Replay` ignores the kill switch; `Record` defers to `inner` |
| T7 | Recorded stream errors replay as text; weft sentinels re-wrapped by prefix |
| T8 | No `-record` flag in wefttest; `WEFT_RECORD` is the suite's own switch, documented as the suggested spelling |
| T9 | `Args(v)` helper, not `Call{Args: any}` |
| T10 | `Raw(events...)`, not a `Malformed` family; `Think(text, then)` stands |
| T11 | `Request` with three matchers, no DSL |
| T12 | Fuzz gate: `make fuzz` over `go test -list`, 10s per target, own CI job, crashers uploaded then committed as seeds; not under `-race` |
| T13 | `ModelEvent` JSON stays in wefttest until the store wants it (ADR 0010 amendment then) |
| C1 | `Model.Stream` yields a turn's error after its events (one check); `Fail` unchanged |
| C2 | `ExampleReplay` is a doc-pattern example — examples cannot construct a `testing.TB`, so the pattern lives in the godoc of `Record`/`Replay` |
| C3 | `LastText` is the last message's last text part; `""` on a tool message |
| C4 | A fixture write failure surfaces as the stream's terminal error (loud on both channels), keeping `FailNow` off run goroutines |
| C5 | `seq` is 1-based (`001`…), matching the plan's `<seq:03d>` reading order |
| C6 | The miss error names the test's fixture directory (`dir/<test>`), key, and first user words — the plan's `%s/%s` folded into the joined path |

## Consequences

- `wefttest` gains twelve exported names (`Record`, `Replay`,
  `ErrNoFixture`, `Args`, `Raw`, `SayThenFail`, `Turn.WithUsage`,
  `Request` + three methods, `Model.LastRequest`); `make apidiff`
  reports additions only. No change under `weft/` proper; no adapter,
  conformance case, event, message, or tool type touched.
- Application suites get the "test against what the model really said"
  capability offline, deterministically, with no dependency and no key
  in CI; the committed recordings (`wefttest/testdata/replay/`,
  `wefttest/conformance/testdata/replay/`) were recorded from `Script`,
  so the tree replays from a fresh checkout with no network.
- Recording is a deliberate, local act: `Record` never runs in CI, and
  the suite that holds the key owns the switch (T8).
- Out of scope, recorded: HTTP-level recording for adapters (`make
  fixtures`) stays deferred per ADR 0013; `ReplayOption.Normalize` is
  an orbit item; evals against live models are post-v1; adapter-module
  fuzz targets are the SDKs' surface.

## Tests

`wefttest/replay_test.go` (R1–R10 plus the abandonment, ctx-error,
sentinel-replay, and determinism cases); `TestReplayPlaysRecordedEvents`
replays the committed recording under `WEFT_MODEL_REQUESTS=deny`;
`wefttest/conformance/replay_test.go` runs the full suite against
`Replay` with every cap except Live; `wefttest/model_test.go` covers
each helper, and the root package consumes each one (`otel_test.go`'s
ad-hoc contract doubles replaced by `Raw`, `WithUsage` in the
`UsageLimit` overshoot case, `Args` in the end-to-end tool test,
`LastRequest` in the `PrepareStep` tests). `make fuzz` is the gate's
own test: its exit code is the verdict.
