# Changelog

Notable changes to weft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.0.0/); the project
is pre-1.0; since 0.9.0 a release is one tag for the framework plus
`core/vX.Y.Z` for the loop module (ADR 0027; before it, one tag per
module, ADR 0005).

## Unreleased

### Fixed

- Studio, `weft export --wefttest` (P020.2): save as test on a run that offered no tools writes a test that compiles — it declared a tool-result handler it never used (`declared and not used: answer`); now it declares and imports only what the test uses.
- Studio (P020.2): the runs list fits its frame at 1280 to 1800 px — a run that delegated shows its subagents' tokens on a second line under its own instead of beside them, a token pair breaks only at its dot ("20 in ·" over "10 out", never "10" apart from "out"), the agent column holds a longer name ("procurement-desk", it read "procurement…"), the run column is 14 rem wide, 16 rem from 1360 px and 20 rem from 1680 px (14 rem again from 1536 px, where the session and model columns join), and the refresh button stays at the end of the first row instead of wrapping onto a row of its own.
- Studio (P020.2): a derived run's fix says what to set without a repository path ("(docs/record-contract.md §9)" is gone), and its waterfall starts under the view's controls — the layout toggle ends that row instead of standing alone above the waterfall.
- Studio (P020.2): the run page's waterfall keeps a duration on one line ("21.3 ms", it wrapped "21.3" over "ms"), shows a span's whole name when its row has room ("execute_tool lookup_order"), and an attempt's name beside its error chip ("attempt 1 · script" with `stream_idle`, it read "attempt 1 · s…").
- Studio (P020.2): the sessions list breaks "awaiting a decision · 1 call" and "1 exchange · 2 turns" only at their dot, and its find box shows "find title, prompt, id" whole; the Agents page's numbers end every bar at one place, whatever the width of its figure.
- devtools panel (P020.2): the header shows each of its figures — the tokens, the cost, the unpriced count — whole or not at all, never cut mid-figure ("≥1.0M in …", "2… $.."), the title keeping its room (a page of runs' title keeps the space before its count, "170907 · 928 runs"); the narrow dock's run picker puts the status, steps and duration before the title or a replay's lineage, so a cut title no longer takes them ("· 3 st"); a running turn's duration reads "…" instead of "—"; a parked call's arguments read on one line, as in the step's call list, instead of a pretty-printed block hanging off the tool's name; the decision field's placeholder is "result or deny reason", shown whole (0.20.1 said "result to resolve with" was shown whole; the placeholder was longer and cut).

## 0.20.1 — 2026-10-27

### Fixed

- Studio (P020.1): the run page's waterfall draws only the time-axis ticks its track's width holds — a short run's or a narrow track's labels no longer print over each other; a derived run's root row counts its steps as its header does ("2 steps", it read "0 steps"); an attempt's error chip (`model_error`, `stream_idle`) stays whole beside its name instead of under its bar's marker.
- Studio (P020.1): the runs list and the sessions list fit a 1280 px window with the sidebar open — the runs list's model column shows from 2xl (1536 px) and its status, tokens and cost wrap, so the compare pick no longer pushes tokens off the edge; the sessions list's status and turns wrap and its agent truncates, so tokens, cost and last seen stay in the frame.
- Studio (P020.1): `/agents?agent=<name>` keeps the named card in view while the numbers above it load and lay out (with many agents it ended far down the page); the Agents page says a registration time as Studio says times (relative, the stamp on hover — not the server's raw RFC 3339 with nanoseconds), points at keeping a proposed prompt in user words (it named an internal design doc), and pads the selected card inside its ring.
- Studio, devtools panel (P020.1): a plain Go error a tool layer hid (`mw.MapErrors` over an `errors.New` value) reads `INTERNAL ← plain error`, not `INTERNAL ← *errors.errorString` (an `errors.Join` or two-`%w` error, `joined errors`); the recorded `weft.tool.error_masked` is unchanged and stays on the hover.
- Studio (P020.1): the call graph sizes its nodes and column gaps to their words — an edge label no longer prints over a node, and an MCP server node's failure line ("1 unreachable · 1 refused · 1 tool error") is no longer cut; a node grows to 420 px at most, a longer name cut with an ellipsis and whole on hover.
- devtools panel (P020.1): a page of runs' header reads "batch <id> · N runs" at every dock width — the id is cut before the count, and the token, cost and unpriced lines give way before the title; the Timeline axis names its two ends instead of five overprinted ticks; "has error" is a normal checkbox on its label's line; the replay drawer's engine options no longer wrap into two overlapping columns in a 640 px dock; a parked call's "result to resolve with" field shows its whole placeholder.

## 0.20.0 — 2026-10-27

### Added

- core, mw, otel, obsdb (U23.1; UX-S2-01, UX-E-08, UX-E-09, UX-E-10, UX-E-15, UX-A-43, UX-S2-04): the middleware onion recorded. The manifest lists each agent's `model_middleware` and `tool_middleware` outer to inner (a tool's own `middleware`) and its `hooks` (`prepare_step`, `tap`, `on_run_end`, `tool_source`), each `{name, params?, source?, opaque?}`: a model layer describes itself with the optional `Describe() core.MiddlewareInfo` (`mw.Retry` its budget and delays, `mw.Fallback` its backups, `mw.Log`, `mw.RepairJSON`), others by type — a layer without `Unwrap` ends the walk `opaque` — and functions by name, with their source when not weft's own. A manifest `source` (a tool's, a hook's, a middleware's) is one form on every host, never an absolute path: the defining package's import path and the file's base name (`github.com/x/app/tools.go:12`; a module-cache file by its module path, the linker's `%2e` unescaped). The spans say what a layer decided: `core.ToolReportFromContext` (`ToolReporter.Denied`, `Masked`) puts `weft.tool.denied_by` (`mw.Allow`: `allow`) and `weft.tool.error_masked` (`mw.MapErrors`: the hidden error's kind — a standard sentinel through `errors.Is`, an optional `ErrorKind() string` — never its text; a mapping that only wraps reports nothing) on `execute_tool`; `core.Reporter.Repair` (`mw.RepairJSON`) a `weft.repair` event on `chat` and `weft.tool.args_repaired` on the call; `core.AttemptLayerRetry` marks `mw.Retry`'s retried requests; `core.DenyBy` names a deny grant on a resume's denied span (`weft.tool.denied_by` `grant`, `weft.grant.id`; else `approval`), which `weft/thread` passes. `otel.DevDetail()` (dev-gated like `RawBodies`; `LoggerProvider.DevDetail`) adds one `layer <name>` span per layer per call (`weft.layer.name` / `kind` / `index`; a panic through a layer ends it `Error` / `panic`), the repair's original arguments and a contained tool or middleware panic's stack (a `weft.panic` event on `execute_tool`, 16 KiB), for the Local and Studio destinations with content on only; never by default. The request record gains `wire {server_address, extra_body_keys, extra_headers, dropped}` (UX-S2-09): the adapters' endpoint `host[:port]`, `ExtraBody` keys and `ExtraHeaders` names — names only, bounded — through the optional `WireFacts()` method (`openai`, `anthropic`, `google`; the primary's behind an `mw.Fallback`). obsdb derives the `repaired` counter. Record contract §3 (the `layer` span, the `weft.repair` event, the `execute_tool` rows), §6, the manifest's onion, the `onion` conformance fixture.
- mcp, core, obsdb (U23.2, O8; UX-S2-08, UX-A-43, UX-S2-04): MCP identity. `mcp.Server(name)` names the server an import comes from — every imported tool's `Origin` reads `mcp:<name>` — and `mcp.Transport(kind)` how it is reached; `core.OriginDetail(core.SourceDetail{Server, Transport, Prefix, RemoteName, Skipped})` carries the parts. The tools record gains `source_detail` per tool and `skipped[] {name, reason, source}` (the tools the listing could not import — a server none of whose tools imported included, through `core.SkippedImports` and `mcp.ImportError.Option()` — once per source, at most 64, `skipped_more` the rest, names cut at 128 bytes; neither in the catalog hash); `execute_tool` carries `weft.tool.origin` (the tool's Origin when not local: `subagent`, `mcp:<server>`). An MCP call's failure is one of three `error.type`s, contract rows: `mcp_transport` (no answer: a closed session, a stdio stream at EOF, the go-sdk's refused, dropped or TLS-failed streamable-HTTP connection or a 5xx / 429 without a JSON-RPC body — `ErrRejected` −32005, −32004, −32003), `mcp_rpc_error` (the server's own JSON-RPC error answer: its handler failed, the tool is not listed) and `mcp_tool_error` (an `isError` result). obsdb reads it all (`ToolsRecord.Skipped`, `ToolEntry.SourceDetail`); `/api/runs/{id}/tools` serves it. Record contract §3, §4, §10 (`mcp_mesh`); ADR 0015 and 0028 amendments.
- Studio, devtools panel (U23.2; UX-S2-08, UX-A-43, UX-S2-04): the onion shown. The drawer's "what wrapped this call" — a model or tool call's chain outer to inner from the run's stored manifest (its `Timeout` outermost), each layer's decision from its spans (Retry's attempts, waits, HTTP status and give-up, Fallback's served model, RepairJSON's "repaired 1 arg", Allow's "✋ denied by policy", MapErrors' "INTERNAL ← io.EOF"), a decision shown once per layer kind (a second `mw.Retry` says the record cannot tell which), an idle layer greyed, a click opening its attributes; without a stored manifest only the layers that reported, said so. The Agents card draws each agent's model and tool chains; the story's step chips (`↻ retry ×2`, `⇄ fallback`, `✎ repaired 1 arg`) and tool-row chips (`✋ allow`, `⇣ io.EOF`, `✎ repaired`), on the panel too, with its MCP server; a denied call reads "denied by policy", an MCP failure "MCP unreachable", "MCP refused the call" or "MCP tool error" on both renderers; the call graph draws `mcp:<server>` from the span's origin — a child run's calls included — with its calls, slowest call and failures by kind ("1 unreachable · 1 refused · 1 tool error"), and a caller-linked run under its caller; the Request pane's catalog names each tool's origin in parts and lists the skipped tools greyed (the panel's Request tab too). `lib/onion.ts` serves both renderers. Glossary: `middleware`, `MCP server`.
- mcp, core, obsdb (U33.1; UX-S2-08's remainder): a served agent's run is linked to its MCP caller. Under the new `mcp.CallerMeta()` option (off by default — the weft run and call ids and the trace state name your runs to the server, so send them to servers you run, not to third parties) `mcp.Tools`' imported tools send their caller in `params._meta`: the `execute_tool` span's W3C `traceparent` (and `tracestate`) and `mcp.MetaCaller` (`"dev.weftgo/caller"`: `{run_id, call_id}`). `mcp.Serve` (and `AddTools`' tools) read it back: the served run's `invoke_agent` span starts its own trace with an OpenTelemetry link to the caller's span — never the caller's child, so a client's "not sampled" flag never decides whether the server records the run — and records `weft.caller.run.id` / `weft.caller.call.id` with `weft.caller.remote` `"true"`: a link, never `weft.parent.*` (O11; record contract §2, the `remote_caller` fixture). A remote link is the caller's own statement, so readers never place a run by it: obsdb leaves `caller_run_id` / `caller_call_id` empty for it on both backends, the run stays top-level, and Studio says "started over MCP by run … in another process (not linked)". A missing or malformed `_meta` links nothing and refuses nothing. New `core.WithCaller(ctx, core.Caller{RunID, CallID, Span})` (and `weft.WithCaller`, `weft.Caller`): every run started directly on `ctx` (not inside a tool call) records that caller; no run started inside it inherits it. The mcp package now imports the OpenTelemetry trace API (no SDK).
- core, mw, obsdb, Studio (U30.3; UX-S2-02's remainder): `core.AttemptInfo.GiveUp` (`AttemptGiveUpBudget` / `AttemptGiveUpNotRetryable` / `AttemptGiveUpRetryAfter`, re-exported by `weft`) becomes the attempt span's `weft.attempt.giveup_reason`: `mw.Retry` says why it stopped there — its retries spent, a failure its classifier does not retry, a retry-after past `MaxWait` (behaviour unchanged). It is that layer's stop: a `Retry` under a `Fallback` marks where it stopped and the call may go on to the backup; a `Retry` over a self-reporting model gives no reason. The step route's `attempts[]` carry `giveup_reason`. Studio's attempts list words each cause ("rate limited (429)") and ends a failed call with "gave up: retries spent (2 attempts)" ("gave up after N attempts" on an older record; never when the last attempt ended on the run's cancellation or deadline); the run tree draws an attempt's `weft.attempt.wait_ms` as a hatch before its bar.
- core, obsdb, Studio (U30.3; UX-S2-07's, UX-S1-06's remainders): the request record's body carries `parallelism`, the step's `Parallelism` dial (the agent's, a run's lower one, 1 under `Sequential()`), drawn "parallelism N" beside a step's tool lanes, a `Sequential` tool's row "▮ sequential". An `Output` agent's run names its typed answer's type on `invoke_agent` — `weft.output.type`, package-qualified (`main.Invoice`), at most 128 bytes cut at a rune boundary — and the manifest's `submit_output` tool carries it as `output_type`; Studio's OUTPUT card shows it and the Agents card marks the tool "typed output · main.Invoice". The manifest carries an agent's configured sampling knobs as `params` (its `weft.Params`, by the request record's names; absent when it sets none).
- obsdb, Studio, devtools panel (I6; UX-H-01, UX-H-02, UX-H-03, UX-H-04, UX-S4-02, UX-S4-03): derived runs from the producer's content (docs/record-contract.md §9). A top-level `invoke_agent` span without `gen_ai.conversation.id` (no other agent span stored above it) is a derived run with no session; one nested under another agent still folds into the enclosing run, and a derived row an export folds later (an inner agent exported before its parent) is dropped — on ClickHouse reconciled against a fresh read of the trace, so the last of two racing writers leaves no stale row, and announced live as a run frame whose row is `removed` (`obsdb.RunRow.Removed`), which the runs list and the Live page drop. A derived row's `message_count` counts its chat spans carrying message content (`obsdb.StockHasMessages`). `steps/{n}` serves a derived call's `args` and `result` from `gen_ai.tool.call.arguments` / `gen_ai.tool.call.result` when the producer captured content (`obsdb.StockToolContent`; else `args` `null` and no `result`), and the run page's story draws each step's USER / ASSISTANT / tool-call rows from its chat span's `gen_ai.input.messages` / `gen_ai.output.messages` / `gen_ai.system_instructions` (the trace page's chat renderer, `components/studio/genai-chat.tsx`). The derived badge's reason and fix are said once, in the run header — the fix speaks to a Python / TypeScript developer: content capture (`OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true`), `gen_ai.conversation.id` for a session, "weft's own records need a weft SDK (Go today)" — and every block read off the spans carries the chip "derived · from span attributes" alone; a derived run's replay, save as test, open in app and compare are hidden on both surfaces, said once ("they need weft's records"). `/api/traces` and `/api/traces/{id}` name a trace's derived runs in `run_ids` (stored rows only), and `/api/traces/{id}` answers `not_run` `{reason, fix}` (`obsdb.StockNotRun`) when the trace makes no run — "model or tool calls only: no invoke_agent span opens a run", "its agent span has not arrived". `/api/traces?not_run=1` (`obsdb.TraceQuery.NotRun`) lists the GenAI traces that make no run, and the runs page says how many over the last 7 days, linking that list. Conformance: `stock_genai` with content, `stock_genai_noconv`, `stock_genai_nested` (both backends).
- Studio, studio (U33.2; UX-H-10, UX-H-07, UX-H-35, UX-S5b-04): `GET /api/runs/{id}/logs` rows carry `event_name`, the OTLP record's EventName (absent on a plain line; the logs view also reads an older SDK's `event.name` attribute), and the MCP `logs` tool answers it; a GenAI log event (`gen_ai.<role>.message`, `gen_ai.choice`) is drawn in the Logs view as that message — role, content, an assistant's tool calls, a choice's finish reason — and a derived run's Story lists those messages below its step cards ("messages the app logged"; badged when cut, never drawn twice). An unknown session or experiment figure carries `cost_partial` `{usd, priced, of}` — the sum and count of the parts whose whole bill (own + delegated) is known, absent when none is — drawn on Studio's sessions list, session page and experiments list as the panel's header draws its turns, by one rule on both surfaces (a part whose delegated figure is unknown adds nothing): "≥ $x · k of n turns priced", "≥ $0 · k of n turns priced" when the priced parts are $0, "—" when none is (Go writes `testdata/api/cost-parity.golden.json`; the web's parity test feeds it to both renderers). `/api/sessions` and `/api/sessions/{id}` rows carry `derived: true` for a session every turn of which is a derived run, drawn "derived (stock OTel)". A replayed run's header says its provenance in words beside "replay of …" — "5 edits (user, args, result, insert)" from `weft.edits` (", …" when the record's cap left kinds out), "fed the compacted view #7" from `weft.replay.view` — the raw chips behind "details"; the panel's replay chip names the edits in its title.
- weft CLI (B7; UX-S5a-01, UX-S5b-02, UX-S5a-02, UX-S5a-08, UX-S5a-05, UX-S5a-06): one resolver for every API client — `weft runs`, `open`, `export`, `doctor` and `mcp` find the Studio through `--url`, else `WEFT_STUDIO_URL`, else the discovery file (the trust rules `otel.Install` uses; `WEFT_DISCOVERY=off` skips it), else `127.0.0.1:7331`, and say so on stderr when you named none (`weft: Studio http://127.0.0.1:7347 (discovery file …)`); the token is `--token`, else `WEFT_STUDIO_TOKEN`, else the file's when it names that Studio. `weft open` takes a run id, a public id, a session id or a trace id (tried in that order) and, with no id, prints the Studio root — `weft open --with-token` is a terminal's signed-in link. `weft runs` gains DUR, COST, PUBLIC ID (when a row has one) and ERROR (kind and why, 60 runes) columns, STARTED with its zone, and names the next step on stderr. `weft doctor` gains `discovery` (this Studio, another — two on one database warned —, none, off), `data` (the newest run's age, weft version and service) and `otlp` (open, walled, off) lines; a 401 names `--token "$(cat <db>.token)"` and `weft open --with-token`; a capability off names its cause (`--no-playground`: "drop the flag"); the stale panel and manifest lines name the reinstall and the latest run's manifest hash. `weft studio`'s banner ends `studio: sign in with weft open --with-token` when it did not open the browser.
- studio (B7; UX-S5a-10): `studio.DiscoveryFile(path)` — the hosting command names the discovery file it writes; `/api/meta` serves it as `discovery_file` (db.path's rule) while the file names this process. `studio.OffReason(capability, why)` — the host's words for why a capability is off (an unknown capability panics at New), served in `capabilities_off`. `content.latest` gains `started`, `weft_version` and `service`. The token wall names `<db>.token` and `weft open --with-token`.
- weft mcp (U33.1; P10a-pre-go 9): a `stats` tool over `GET /api/stats` (`by` agent, model, tool, error_type or step; the route's filters) answering compact groups — no 24-bucket `series` (`full: true` keeps it), empty figures omitted — with a summary naming the busiest groups; `error_class` on the `runs` and `search` tools, and `search`'s summary names the `facets.error_class` classes. 22 tools.
- scope, studio (U22; UX-D-05, UX-D-08, UX-E-27, A5): a Scope names a page of unrelated runs — `Agent` and `Tags` (run metadata pairs, written `tag.<k>=<v>`, repeatable, ANDed) — and may stand without a public id (`;tag.arena.batch=b1`, or a first segment that is a known `key=value`); `Scope.Equal`; `lib/scope.ts` mirrors it, one golden pins both. `FlowKey` (`weft.flow`) and `WithFlow(ctx, flow)`: a scope's flow is run metadata `Header` stamps on every run its handler starts (record contract's `weft.flow` row, the `flow_scope` fixture). `flow=` and `tag.<k>=<v>` filter `/api/runs`, `/api/sessions`, `/api/search`, `/api/stats`, `/api/traces` (`obsdb.TraceQuery.Meta`: a trace holding a span with each pair) and `/api/live`, where a filter narrows a selector or stands alone (record and attempt frames of the runs it admitted), and `agent=` narrows `run`, `session`, `public_id` or `all`; the live grant binds each filter pair. Capability `scope_tags`.
- studio (I4; issue 004): tag-scoped panel tokens — `POST /api/panel-tokens` takes `"tags": {"arena.batch": "…"}` with or without `public_id` (one required; server token only; read-only; no `weft.*` key but `weft.flow`, at most 16 pairs, keys and values at most 256 bytes — else 400). Every read the token makes is held to its tags as a public id token's is to its public id: runs, search, stats (a delegated bill sums only descendants carrying the tags, and leaves out one under an out-of-scope run — the run tree's rule, both backends), sessions, traces (every span carries the tags: `obsdb.TraceQuery.ScopeMeta`), a run and its steps, spans, logs, export and tree (`obsdb.TreeQuery.Meta`, `TreeRun.Meta`) — a run without the tags is 403 — and the live stream and its grant; the command routes refuse a tag-only token. A request narrows, never widens: another value of a token's tag key is 403. A Subagent child inherits the tags; an untagged pool or thread child is a counted `hidden` hole, "without your token's tags". Capability `panel_token_tags`. A tag scope is only as tight as the app's own tagging: metadata is caller-written.
- devtools panel (U22, I4; UX-D-05, UX-D-07, UX-D-08, UX-D-09): a tag, agent or flow scope lists that page of runs — "batch cli-… · 196 runs" (the first tag's key by its last dotted segment; a chip for every other tag), rows saying "run" and naming their agent, the live lane on the filter; `flow` narrows every read. A panel token's `public_id` or `tags` claim is the scope when no rung names one, so `data-token` alone lists exactly that batch's runs. "how to scope" leads with the zero-code `?weft_scope=pub_…` line, each line with a copy button; the npm README carries the ladder.
- Studio, studio, obsdb (U30.2; UX-A-13, UX-A-15, UX-A-16, UX-A-17, UX-A-18, UX-A-19, UX-A-21, UX-A-33, UX-C-39): ⌘K finds a session and a trace by id prefix as it finds a run — `/api/sessions?id_prefix=` and `/api/traces?id_prefix=` (`obsdb.SessionQuery.IDPrefix`, `obsdb.TraceQuery.IDPrefix`, both backends: bytes, no wildcard, no case folding). The run page's export offers "copy as curl" per format, naming `$WEFT_STUDIO_TOKEN`, never a token. The runs list picks runs to compare ("compare N"); the compare page gains "changed only" (on by default when a step differs; a hidden linked step says "show all steps") and a usage line per run against the base. One "this run | whole trace" switch carries the selection both ways (`/traces/{id}?run=`). `/live` folds a parent's ended child runs behind "+N subagents" and draws a mini waterfall per row. Run rows carry `own_usage` / `delegated_usage` (usage less its Subagent children's, and theirs; omitted with no Subagent child, running, a failed children read, or a child a panel token cannot see), drawn "3 in · 1 out +9 in · 5 out" and as per-child token chips on the run header. At 480 px and under the waterfall keeps thin bars and the run header's verbs fold into "⋯ actions". A runtime's breakpoints say "set by <surface>": `PUT /api/runtimes/{id}/breakpoints` takes an optional `surface`, served as `breakpoints_surface` and, with `breakpoints_set_at`, kept across a Studio restart — `obsdb.BreakpointProvenanceStore` (`PutBreakpointSet` / `BreakpointSet`, both backends) beside the unchanged `CommandStore`, `RuntimeServer.SetBreakpointsFrom` beside `SetBreakpoints`.
- Studio (U30.3; UX-S5b-03's, UX-H-18's, UX-F-16's, UX-D-12's remainders): the Request pane tags each overridden param "◂ override" against the agent's from the stored manifest's `params` ("temperature 0.4 ◂ override (agent: 0.7)"), else "(source run: …)", else "override" alone, there and in an experiment's variants. A breakpoint park's card offers "clear the breakpoint on status" — to the server token only, with capability `breakpoints`, on the one connected runtime of the run's service holding it (two or more: a pointer to Runtimes), its set read again before the PUT; anyone else reads why not. One duration format on both surfaces ("11 ms", "1.2 s", "15 µs") and a "start" column on the run tree. The panel's Story step card folds to its head line (the read step and the streaming one open; "step N" is the toggle).
- Studio (U33.1, U33.2; UX-H-22, UX-H-31): `/api/runtimes` `agents[].tools[]` gains `approval: true` for a `RequireApproval` tool, and the replay drawers say "needs approval" beside its ⚠ on both surfaces. `side_effects`' `never` is documented as the replay class, not "no side effects".
- Studio and the devtools panel (U21.1, U21.2, U21.3; UX-F-01–UX-F-17, UX-F-28, UX-A-20, UX-C-22, UX-B-24, UX-H-20, UX-D-22): one vocabulary. `lib/glossary.ts` `TERMS` — each concept a tooltip, a short entry, a docs anchor and the words it retires — drawn by `<Term>` (a focusable glossary word, `aria-describedby` while shown) on the tables' headers, the run header and the replay drawer; the `?` dialogs gain a concepts tab (the panel's the eight core one-liners and "all words in Studio ⤢"), `/settings#concepts` lists every term. `lib/keys.ts`: one keymap both `?` overlays draw — `j`/`k` the list, `[`/`]` the step, `s`/`w`/`q`/`r` story · waterfall · request · raw (again: the view before it), `o` open it elsewhere. `lib/palette.ts`: each status · kind · diff · hole role one hex on both renderers per theme. `lib/run-row.ts`: "↻ replay of t3 · step 1", "⑃ fork of s_…#12" on both. Every list page's empty state says how to connect an app to this Studio by its own URL (`lib/connect.ts`), `/settings` leads with "Connect an app", `/runs` carries a dismissible first-run card. Linkable: `/sessions/{id}?turn=N`, `/live?q=&agent=`, `/experiments/{id}?pick=`, `/agents?agent=`. Studio's "open in app" carries the step and tab (`#weft_scope=…&weft_step=<n>[.<tab>]`) and the panel opens there. The Studio-only views get the panel's ⤢ (an agent's card, the live page, the runs list with the scope's filters, a turn's replays, the runtimes a refusal names). `make studio-check` runs `scripts/vocab-audit.ts`, which fails on a retired word on screen in either renderer, and `palette.test.ts`, which refuses a new status or kind colour literal.
- devtools panel (U28, U30.1; UX-D-11, UX-D-17, UX-F-03, UX-F-09, UX-D-23–UX-D-40): when Studio stops answering after boot the line under the header says so in the boot probe's words ("Studio not reachable at … · retry", or the token it wants), the turn list dimmed; the live lane's backoff and, past it, one `/api/meta` read a minute while the dock is open and the page visible bring it back (a 401/403 stops it; retry starts over). The docked turn column is `clamp(240px, 28%, 420px)` of the dock and its rows never overflow. The footer names the rung that chose the scope (`scope: explicit`, `url`, `marker`, `header from <path>`, `switcher`, `none`); an explicit public id with no run, once the list read lands, reads "Studio has no runs for pub_… yet — check the id". A scope naming a session and no public id titles the header "session <id>". The pill reads the current turn's priced whole bill (`· $0.0029`), else its tokens. The axe budget covers the fallback's "how to scope", refused verbs, the switcher and the steer box in both themes.
- weft dev (U30.1): without a browser it prints where the token is (`studio: token: <db>.token (weft open --with-token)`; a reused Studio's "the running Studio's, not <db>.token" when the file holds another), and "no runtime registered yet" adds "or the app links its own embedded Studio".

### Changed

- Devtools panel: panel.js grows from 95,558 B to 98,921 B gzip (headroom 7,575 B under the 106,496 B cap).
- Studio: the dist grows from 723 KiB to 753 KiB gzip, of the 800 KiB budget.
- Studio, devtools panel (U21.1, U21.2, U21.3): the words. "session" everywhere a sentence named a thread ("weft/thread" where the Go package is meant); "turn" only for a session's runs ("run" for a public id's sessionless runs, a page of runs and the fallback); "child run" for a subagent's run, "pool sessions" for thread/pool's children, "gaps" for holes, "tokens", "first token" (TTFT in hovers only); the event bar is the **playback** and "replay" is re-execution only; "Write to: nowhere (ephemeral) | a new session (fork)" in the replay drawers; "✎ Experiment" is "✎ Replay…"; "your token's access" (not scope) on refusals. `tokenPair` ("60 in · 30 out") and `costLine` ("$0.0012 own · $0.0040 delegated") on both surfaces; /live's "● live" in the live role (green).
- devtools panel (U21.3, U22, U30.1; UX-S5a-03, UX-D-06): a Studio that answers 401/403 is never removed — the line says what token it wants ("add data-token (the dev token) or a panel token (POST /api/panel-tokens) · retry"). A script tag's dock still removes itself silently when Studio does not answer, unless the host named a loopback endpoint (a dev Studio not running yet is said, with retry); a bare tag (no endpoint, no `data-weft`) whose guessed endpoint is on loopback says once in the console "weft devtools: Studio did not answer at <endpoint>; add data-weft or data-endpoint on the panel's <script> tag". A module-loaded panel reads its own `<script>` when it carries `data-weft`, else the page's first `data-weft` tag. The fallback (no scope) selects no run, sums no cost and groups its rows by agent. The dev list's empty line names this Studio's URL. A narrow dock hides the header's stats and cost from sight only (the title's tooltip carries them). A refused app-turn approval states its refusal once beside disabled verbs; a parked plain Generate's fix reads "resume it in your app; to decide here, run it as a session turn with thread.PublicID(...) (weft/thread)".
- devtools panel (U33.2; UX-H-07): the header's set figure follows `cost_partial`'s rule — the k turns whose whole bill is known, a turn with an unknown delegated part adding nothing (it added its own figure), "≥ $0 · k of n turns priced" when they are $0 (it read "—"); "—" only when none is priced.
- studio (U22): `GET /api/live` with a `tag.<k>=<v>` or `flow=` filter alone opens a stream (it was a 400 without a selector); the live grant's stream binds the filter.
- studio (U22; UX-D-18): `/api/panel-handoff` hands off for the server token too — the location carries it in the fragment, as `weft open --with-token` does — so the panel's ⤢ / `g s` land signed in.
- weft mcp (U33.1; R-I5b, UX-H-29, UX-H-36): a session answer's `turn_defaults` is, per key, the value most turns share (ties to the earlier turn), kept only when two turns share it. An unnamed agent reads "(unnamed agent)", counts agree with their nouns, a 404 says where ids come from, and an `events` page's summary names its positions, the next cursor and whether the run is terminal. Tool texts say "read-only panel token".
- weft CLI (U33.1; UX-S5a-11, UX-S5a-12): usage errors name their arguments in words ("export needs exactly one run id (got none)"), never a Go slice; every `-h` spells its flags `--name`. `weft vacuum` prints the main file and the WAL apart (`vacuum: weft.db 176128 → 98304 bytes (WAL 4120032 → 0 bytes)`): the SQLite `Vacuum` checkpoints the WAL after `VACUUM` (PASSIVE then TRUNCATE, under a 250 ms busy timeout of their own), so the file's size after a vacuum is its size; a WAL a live reader or writer holds is said.
- docs (J2, B7, U33.1; UX-H-05, UX-H-11, UX-S5a-04, UX-S5b-01, UX-S5b-06, UX-E-55): setup B's Python line exports over OTLP/HTTP (`OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`, GenAI content on, `opentelemetry-instrument`) and says what makes a run; `@weftgo/devtools` is not published to npm yet — its README opens with the script tag, the `panel-vX.Y.Z.js` release asset and the locally built tarball; no TS or Python runtime-link client exists (Go only; studio/README's wire section is "for future non-Go clients"); `studio/README.md`'s devtools-panel section opens with a quickstart per setup; the npm README lists every meta field `config.ts` reads, `scripts/npm-package.ts --check` failing on drift; docs/record-contract.md's span-id, caller-link and fixture claims match the code (`TestRecordContractMigrationNames`).

#### Breaking

- **Breaking:** core, mw, mcp (U23.1, U23.2, U30.3): `weft.manifest.hash` changes for an agent with a tool defined outside weft (each tool's `source` is now the import-path form, `github.com/x/app/tools.go:12`, was module-relative `tools.go:12`), with model or tool middleware or hooks (the manifest lists them; a non-weft one, a hook too, with its `source` file:line), with an `Output` (`submit_output`'s `output_type`) or with `Params` (the manifest's `params`). Regenerate weft.json (`go test ./... -update`). A run recorded before 0.20.0 keeps the hash it ran with, so it reads "stale" against the regenerated manifest (the stale title says the weft version changed); an agent's latest-run check (`weft doctor`'s `manifest`) reads current again once the agent runs on 0.20.0. Middleware and hook names in the manifest no longer depend on inlining or the Go version (P10b-inline-names): weft's own read `mw.Allow` / `mw.Audit` / `mw.MapErrors` / `pool.Wrap` (registered through the new `core.NameToolMiddleware`), a named function by its name, a closure as `func@<dir>/<file>:<line>` (was its runtime name, `pkg.Outer.func2`, which go1.26.0's inlining renamed — `caller.Allow.func9` for `mw.Allow`); go1.26.0 and later, inlined or `-gcflags=all=-l`, hash one agent alike.
- **Breaking:** mcp (U23.2, O8): a tool `mcp.Tools` imports under `mcp.Server(name)` records `source` `mcp:<name>` in the tools record (and `weft.tool.origin` on its spans) — a reader matching the literal `mcp` must match the `mcp` prefix; without `mcp.Server` the source is still `mcp`.
- **Breaking:** mcp (U23.2): a JSON-RPC error answer from a live MCP server (its handler failed, the tool is not listed) is `error.type` `mcp_rpc_error`, no longer `mcp_transport`, which keeps a call that got no answer.
- **Breaking:** core, mw, Studio (U23.1, U30.3; UX-S2-02): a failed `attempt` span's `error.type` is classified — `rate_limit` (429), `overloaded` (503, 529), `timeout` (408, 504, a transport timeout), `auth` (401, 403), `server_error` (other 5xx), `context_overflow`, weft's own sentinels as the run reads them (`stream_idle`, …), `model_error` the rest; it read `model_error` for every provider failure (the run's `error.type` keeps its table). `weft.attempt.layer` gains `retry` (`mw.Retry`'s retried requests), so a reader that took any non-empty layer for a fallback must compare with `fallback`.
- **Breaking:** the devtools panel's Timeline tab is the **waterfall** tab (U21.1; UX-F-05): its id is `waterfall` (`#weft-tab-waterfall`, the stored layout's `tab`, the `weft_step=<n>.<tab>` link), and a stored `tab: "timeline"` opens the story tab.
- **Breaking:** studio (U21.1; UX-F-15): `delegated_cost_reason` says "child runs": `1 of 3 child runs unpriced: …`, `1 of 3 child runs' cost not known yet (…)`, `N of M child runs carry another public id, or none: …`.
- **Breaking:** studio, obsdb (U21.3, U28): the `hidden` hole's reason reads "your token's access does not reach this: a read-only panel token reads no system prompt or tool catalog" and its fix "use a playground token"; the `no_public_id` reason and the app-owned 403 say "session" ("is not a turn of a session with a public id"), a parked plain Generate's fix "resume it in your app; …".
- **Breaking:** studio (U33.2; UX-H-07, UX-H-35): a session's or experiment's unknown cost reason reads "N of M turns' cost not known (…)", without "yet", when every unknown part is a derived run whose spans report no usage; `GET /api/sessions/{id}` numbers a derived session's top-level turns 1..n by start order in `runs[].turn` (was 0 for each), after the thread's highest where a session holds numbered turns too.
- **Breaking:** scope, obsdb (U22, I4): `scope.Scope` (now holding `Tags`), `obsdb.TraceQuery`, `obsdb.TreeQuery` and `obsdb.TreeRun` (now holding `Meta` maps) are no longer comparable with `==` — use `Scope.Equal`. `scope.Parse` and `lib/scope.ts` read a first segment that is one of the form's keys (`session=…`, `tag.<k>=<v>`) as that key, no longer as a public id.
- **Breaking:** obsdb (I4, issue 004 §4): `SessionQuery.Meta` scopes turns before the grouping, as `SessionQuery.PublicID` does: a session whose turns carry the tags only partly lists, counts, sums and derives its status, title and turn facts from the tagged turns alone — it no longer selects whole sessions; `/api/sessions?tag.<k>=<v>` follows.
- **Breaking:** studio (I6): a read-scoped panel token is served stock spans without their system prompt — `gen_ai.system_instructions`, system-role messages, plain `gen_ai.prompt` / `gen_ai.content.prompt` and the `gen_ai.system.message` / `gen_ai.content.prompt` events.
- **Breaking:** studio (U30.1; UX-D-27): `POST /api/panel-tokens` refuses a body field it does not know — `{"scope":"playground"}` is a 400 naming the field and `"playground": true`, never a read token minted in silence.
- **Breaking:** weft CLI (B7; UX-S5a-13): one Studio per SQLite file — `weft studio` and `weft dev` take `<db>.lock` beside the database (an exclusive flock, LockFileEx on Windows, held while serving, taken before the port policy, released by the kernel when the process ends, a crash included); a second `weft studio` on the file — at the same moment, from another directory, on any port — is refused, `studio already running at http://127.0.0.1:7347 (pid n) on …/weft.db: …`, exit 1 (it was "…, reusing", exit 0, and only on the wanted port). `weft dev` reuses the holder instead, named only from this user's 0600 lock file with a loopback-IP url and a live pid. The lock stands for the file a symlinked path names (hard links and case-insensitive spellings are not detected). A ClickHouse database takes no lock.
- **Breaking:** weft CLI (B7): the API clients' `--url` default is the resolver's (the discovery file before 7331), no longer `$WEFT_STUDIO_URL` else 7331; `weft runs`' table columns changed (scripts read `--json`, unchanged).
- **Breaking:** weft doctor (U33.1; UX-S5b-09): the manifest check's label is `manifest` (was `weft.json`), its source the first words — `weft.json: 1 agent, 1 checked against their latest runs: current`, `runtime registrations (no weft.json): 9 agents differ from their latest runs (runs from another build)`.
- **Breaking:** weft mcp (U33.1; UX-H-36): the `events` tool's body renames the route's `done` to `run_terminal` (the run ended — not the last page; follow `next_after`).
- **Breaking:** Studio (U33.1; UX-H-19): with a `runtime.Sessions` runtime connected, a cancel on the app's own turn is refused at once, **409** `cancel is not supported on an app turn: run <id> is the app's own turn`, and a steer on an app turn that runs nothing is **409** — parked: `no running turn to steer: run <id> is parked: decide its pending calls (approve/deny) instead`, finished: `no running turn to steer: run <id> already finished (<status>)` — no longer 202 and then rejected or failed on a command's row. Without such a runtime the three verbs still answer 403 "app-owned turn".

### Upgrade notes

- The SQLite file moves to schema 0026 and a ClickHouse database to 0033 on first open; a 0.19.x binary refuses the migrated database with `ErrNewerSchema`. SQLite 0026 (ClickHouse 0031) re-derives the stored stock GenAI traces the new derived-run rule changes — those holding a non-weft `invoke_agent` span without a string `gen_ai.conversation.id`, or a non-weft chat span carrying message content, or a conversation-rooted `invoke_agent` span in a trace no writer derived (every stock trace when the database predates derived runs: SQLite 0008, ClickHouse 0009 — an upgrade from there that stopped part-way converges on reopen) — so a setup B database may list runs it did not before (a top-level agent span without a conversation id), and its derived rows gain their message count.
- That re-derive is a one-time cost on the first open, and Open waits for it: an INFO line names the trace count before (`weft/obsdb/sqlite: migrating: re-deriving N stock traces (SQLite 0026); Open waits for it`, `… (ClickHouse 0031) …`) and the time after — about 1 s per 10,000 traces on either backend (ClickHouse inserts synchronously, a thousand traces per insert). It is idempotent: an interrupted run records nothing (SQLite rolls the migration's transaction back) and the next open starts it again from the beginning. On SQLite it runs inside the migration's write transaction, so an app's local sink writing to the same file waits on its busy timeout (30 s) meanwhile — open the file with `weft studio` once before pointing a running app at it if the store holds many stock traces.
- `weft studio` and `weft dev` create `<db>.lock` beside a SQLite file (0600, holding the holder's url and pid while it serves; left in place on exit — leave it, removing it races the next start). A symlink, directory or other non-regular file at that path — on unix a hard link too (more than one link) — is refused at start (`lock …/weft.db.lock: dblock: the lock file is not a regular file`) and never followed, truncated or chmodded; remove it to start.
- Breakpoints set with a surface are stored as a JSON object in the same column: a 0.19.x Studio opened on that store reads no set for that runtime (the runtime's registration re-delivers its own).

### Fixed

- mw (U23.1): a backup under its own `mw.Retry` beneath `mw.Fallback` marks every request it sends `fallback` (its retries included), so `fallbacks` counts the run it answered.
- studio, obsdb (I4): `/api/stats?by=agent`'s delegated bill under a public id scope leaves out a descendant under an out-of-scope run between it and the group's run — the run tree's rule — so it matches the run document; ClickHouse reads an out-of-scope intermediate as SQLite does.
- Studio, devtools panel (U21.1, U21.3, U33.2): a view key pressed again goes back to the view before it (Studio's went to the waterfall); an experiment result's deny sends the typed reason (it was dropped); `/compare` drops a repeated `b` and a `b` equal to `a` from the address.

## 0.19.0 — 2026-10-26

### Added

- examples/studio-local under `weft dev` (U20.2; UX-D-01, R3): setup B for the page — with `WEFT_STUDIO_URL` set the page's panel tag loads `panel.js` from that Studio, names it as `data-endpoint`, is scoped to `pub_demo` and carries a panel token the app mints per page (`POST /api/panel-tokens` with `WEFT_STUDIO_TOKEN`, `"playground": true`; the server token never reaches the page, no token rides a URL), so the panel, the runtime and weft dev's Studio are one Studio; the runtime link registers `runtime.Sessions(demo.resolve)`, so `weft dev -- go run ./examples/studio-local` approves a parked refund from the page's panel. Under weft dev the embedded `/studio/` is the app's read-only view (no playground: weft dev's Studio owns the command store in the shared `WEFT_DB`). The page is served on `GET /{$}` alone (one panel token per page view; a favicon or any other path mints none), and the app creates its threads directory when it is missing. `serve` splits into `build` (the wiring) and the listener. `TestWeftDevApprovesFromThePagePanel` (weft dev's environment, a token-walled Studio, the echo model: park → the panel token's approve → finished → the resumed turn refunded; then the app killed → the turn reads disconnected with the restart fix).
- studio (U20.2; UX-H-14, UX-D-01, A7): an app turn whose service has no connected runtime with `runtime.Sessions` but one that registered it listed disconnected — even while another process of the service, a CLI, is connected without Sessions — is refused `app-owned turn · read-only: the app's runtime <id> of service <svc> disconnected — last seen <UTC second>` (the clause after "disconnected" left out when the row has no `last_seen`) with the fix `restart the app: its runtime link registers again` — never the closed link's or "add runtime.Sessions"; while a Sessions runtime of the service is connected no disconnected app is reported (Studio's playground header included, which otherwise reads the app's own turns disconnected with that fix, not "viewer-only"); with none of the service known and this process's own link dialled another Studio, `… is connected here — the app's link dialled <url>` with `open the panel there (data-endpoint), or link in-process: runtime.Local(srv)`. `/api/meta` adds `runtime_link` `{studio_url?, in_process}` (this process's runtime link: `in_process` for `runtime.Local(srv)` and for an HTTP link of this process that dialled this very Studio — it holds a registration with the link's instance, `RuntimeServer.HoldsInstance` — else `studio_url`, the Studio it dialled; omitted with none open and for a panel token; `internal/linkreg` is how weft/runtime tells weft/studio). Both surfaces say the same words (`lib/honesty.ts` `appGone`, `APP_ELSEWHERE_FIX`; parity rows). `TestStoppedAppTurnReadsDisconnected`, `TestAppTurnGoneAppWinsOverACLI`, `TestMetaNamesTheStudioTheLinkDialled`, `TestMetaRuntimeLink`, `TestAppTurnNamesTheStudioTheLinkDialled`, `testdata/api/meta-runtime-link.golden.json`.
- studio, studio/runtime (U20.2; UX-H-15, UX-H-12, R4): `GET /api/runs/{id}/commands` (capability `run_commands`, with the playground) lists the decisions, steers and cancels sent to a run with their lifecycle rows — `RuntimeServer.CommandsOn`, `RunCommand` — so the run page marks a parked call whose decision was lost (`your approve at … was lost: <why> — decide again`) after a reload or a Studio restart; the devtools panel reads the route once per parked turn and marks a lost decision after a reload by the same rule (each call's latest decide) in the same words. A Studio restarted over its command store logs one INFO line naming the runtimes it restored and the commands still open and lost. `TestCommandsOnARun`, `TestRunCommandsRoute`, `testdata/api/run-commands.golden.json`, the auth matrix.
- Studio (U20.1; §6.11, UX-C-12, UX-S4-12, UX-S5a-15, UX-E-56): a top-level `/runtimes` page (capability `runtimes`; sidebar, command palette). Every runtime that registered, grouped connected · restored after a Studio restart · gone (disconnected and not restored; "stale" is only the manifest mark's word, here and in Settings), each told apart by pid, cwd, host and id, the connected links of one process (one host, pid and cwd) drawn as one process naming the agents two links both register; per runtime what Studio may do on the runs it starts and on the app's own turns — approve, steer, cancel, breakpoints, every "no" with why (not connected, a capability off, `runtime.Sessions` not installed, no agent built with `runtime.Breakpoints()`) — and `debug_scope` in words (UX-S4-06, UX-S4-10); per agent its registered manifest hash against its latest run's, tools, models and the resolver explained; the runtime-wide breakpoints (the editor moved here from Settings: clearable, the all-tools warning, who set them); a "forget" on a gone or restored runtime; `?runtime=` scrolls to a card, `?agent=` explains an agent no connected runtime registers. `src/routes/runtimes.test.tsx` (three arena runtimes apart by pid and cwd, a restored one, a forget, one process with two links, axe), `src/lib/runtimes.test.ts`.
- Studio and the devtools panel (U20.1; UX-D-02, UX-C-11, UX-C-14): the replay drawer's target section is a "Runs in" line (`lib/runs-in.ts`, both surfaces, parity-tested): `Runs in: rt_0396… weft-arena-dev (pid 2902, seen just now) · runtimes ⤢`, the runtime a command goes to being the connected registration first, then the newest seen (`registering`, behind `pickTarget` and the panel's `pickRuntime` — never the first listed); a select of the connected runtimes registering the agent when there are two; a registration that is not connected is no target; when none does, the refusal — `✗ no connected runtime registers <agent>`, what is connected, the fix (`runtime.Install(…, runtime.Agents(<agent>))`), the Studio asked (and, in Studio, the Studio each runtime's link dialled, named by that runtime: `rt_… <service> chose <url>`). The replay drawer, the playground, the experiment page's cell re-runs and the source matrix hold Run on that refusal, with a `runtimes ⤢` link to `/runtimes?agent=` (`runtimeRefusal`, `RuntimesLinkFor`), and a 503 on Run reads the same refusal and fix; the panel's Runs in line is not drawn while no agent is picked. The playground's runtime select labels read `<service> · <env|(no env)> · pid N · N agents · rt_…` (UX-S4-05) beside a `runtimes ⤢` link.
- Studio (U20.1; UX-S4-01): `/agents` badges each card with `manifest_check` — "stale" (its title names the hash the latest run ran: "the latest run ran d2151022, not this manifest"), "current", "other version ran", "unchecked" (no stored run of the agent carries a manifest hash) (`components/studio/manifest-badge.tsx`); on a weft.json Studio a file agent, which carries no hash of its own, reads the check's verdict, "current" or "stale"; the run header reads `manifest <hash> · current` or `· stale` from `manifest_check` alone (no fetch a read-scoped token may not make); the run page's running app turn says why it offers no steer — the token, the capability, or no connected runtime of its service registered `runtime.Sessions` — with cancel's word and `debug_scope` explained, linked to `/runtimes` (UX-S4-10; `steerWhyNot`, `steerOffLine`); Settings' Runtimes section says `manifest: checked N of M · stale K (…) · differs D`, the runtimes by state, `debug_scope` in words and links to `/runtimes`. `src/routes/agents-manifest-check.test.tsx` (13 agents, 9 stale), `manifest-badge.test.tsx`.
- studio: `/api/meta`'s `manifest_check.latest` — each checked agent's latest run's `manifest_hash` (what the stale badge names; absent when nothing was checked). `GET /api/runtimes` serialises `connected_since` and `last_seen` in UTC (`TestSnapshotTimesAreUTC`); its rows add `weft_version` (the registration's) and `breakpoints_by` for a non-empty set — `studio` (this Studio's PUT; `breakpoints_set_at` says when), `runtime` (the registration carried a set an earlier Studio delivered), `store` (restored after a restart). `DELETE /api/runtimes/{id}` (capability `runtime_forget`, with the playground; `capabilities_off` names the option otherwise) forgets a runtime that is gone: 204, its row and breakpoint set removed from memory and the command store, its commands and run routes kept as history; 409 while it is connected or a queued or accepted command is addressed to it, 404 for an unknown id, 403 for any panel token. `studio/runtime`: `RuntimeServer.Forget`, `ErrConnected`, `ErrRuntimeBusy`, `RuntimeView.WeftVersion`, `RuntimeView.BreakpointsBy` and the `BreakpointsBy*` values (`ExampleRuntimeServer_Forget`, `TestBreakpointsByAndForget`, `TestForgetThenReregisterBeforeTheWriterDrains`, `TestRuntimesToldApartRestoredAndForgotten`, the auth matrix, `meta.golden.json`, `runtimes.golden.json`).
- obsdb: `Aggregator` (UX plan D7, A4; UX-E-25, UX-A-14), implemented by both backends — `Stats(StatsQuery)` groups the runs `/api/runs`' filters keep, started in a window, by agent, model, tool, error type or step: runs, failed, model or tool calls and failures, retried calls (more than one attempt; `obsdb.AttemptIndex` reads `weft.attempt.index` one way on every backend, an integer or a string of digits), denials, the calls' own usage split by model, run or call duration p50/p95 and TTFT p50/p95 (nearest-rank, exact on both backends), and a per-bucket series; by=agent adds `StatsGroup.Delegated`, `DelegatedModels` and `DelegatedRuns` — the model calls of the Subagent descendants of another agent, each once, apart from its own. A derived run counts as a run (no calls), so a group's runs match the runs list; every span-side figure is read from the window's start; `CheckStats` refuses a window outside 1678–2262. by=model keys on the model name alone (two providers' models of one name are one group, its cost still priced per provider/model part); by=step keys on the run's own step index, so a replay's calls count from its from_step as step 0 (not the source's ordinal the diff aligns on). `ErrorCounts(SearchQuery)` reads a search match's distinct error texts. SQLite answers from `span_stats` (migration 0023), a derived index written at ingest beside every stored `invoke_agent`, `chat` and `execute_tool` span (the derivation prepared once per `Write`, 54 µs/span in 450-span batches, `BenchmarkSpanStatsIngest`, `TestSpanStatsIngestCost`; read through the partial index `spans_attempt`, so a 30,000-span trace ingests in 1.4 s), backfilled on open in keyset batches (450,000 spans in 2.8 s, `TestSpanStatsBackfillTime`; `Open` logs one INFO line with the span count before and one with the duration after) and deleted with its span by trigger; a `span_stats` write that fails rolls back to its own savepoint and keeps the span (one WARN per process) — the index is rebuildable, the OTLP batch is not. Migration 0025 creates `spans_attempt` on a file that reached 0023 without it (a no-op on a 0.18.x file: 0023 creates it). ClickHouse groups `otel_traces` natively, no table. `obsdbtest`'s `Stats`, `StatsDeleted` and `ErrorCounts` cases hold both to the same answers; `TestSpanStatsFailureKeepsTheBatch`, `TestUpgradeTo0023LogsTheBackfill`.
- obsdb: `ErrorClass` normalises an error text into its class (numbers `N`, hex ids, UUIDs and mixed ids `<id>`, quoted strings kept), `ErrorClasses` folds `ErrorCounts` into classes and `ErrorClassTexts` resolves one back to its texts; `RunQuery.Errs` keeps the runs whose error is one of a set (nil any, empty none) on `Runs` and `Search`.
- studio: `GET /api/stats?by=agent|model|tool|error_type|step&since=&until=&agent=&tag.<k>=&public_id=&parent=&playground=&experiment_id=&status=` (capability `stats`; `capabilities_off` says why without an `obsdb.Aggregator`): per group runs, failed, `failure_rate`, calls, tokens, the cost block (each model's usage priced at read time; unknown, never $0, for an unpriced model; an all-scripted group the exact $0 its runs read, unknown — "no model usage recorded" — when a real model's calls that reported no usage sit beside its scripted ones), `duration_ms` and `ttft_ms` `{p50, p95, n}`, `retry_rate`, denials and a 24-bucket `series`; by=agent groups carry `delegated` `{runs, input_tokens, output_tokens, cost block}` (never summed across groups); agent and error_type groups carry `runs_query`, the `/api/search` query string whose total is the group's runs. `parent` defaults to `*` (every run, children under their own agent), unlike `/api/runs`' top-level; `playground` defaults to `false` — health numbers (rates, percentiles, call counts) never mix in scripted or playground replays — with `true` the replays alone (`/api/runs`' meaning) and `all` both, and `runs_query` carries the effective filter so a card's link lands on the same runs; with `experiment_id` and no `playground` the default is `all` — every cell of an experiment is a playground run, so its cells are counted — an explicit `playground=` winning; `experiment_id` and `status` read as `/api/runs` does; an unknown value or a window outside 1678–2262 is 400; a panel token's numbers are its public id's alone. One golden per `by` (`testdata/api/stats-*.golden.json`); `TestStatsPlaygroundDefault`, `TestStatsScriptedZeroUsage`, `TestStatsScriptedBesideIdleModel`, and `TestStatsCountsTheAppsOwnRunsEndToEnd` (one real run, two scripted replays through a real runtime: `by=agent` counts 1, `playground=all` 3).
- studio: `/api/search` answers `facets.error_class` — the match's failures grouped by `obsdb.ErrorClass`, most runs first (UX-C-37); `error_class=<class>` filters `/api/search` and `/api/runs` to that class (`TestErrorClassFacet`: the arena's usage-limit failures are one class).
- obsdb, studio: an experiment definition keeps a `pass_predicate` (UX plan D8, UX-C-38; SQLite 0024, ClickHouse 0028), stored and returned verbatim, at most 4,096 bytes; `POST /api/experiments` and the reads round-trip it. `web/src/lib/api.ts` types the new routes and fields (`fetchStats`, `SearchFacets`, `error_class`, `pass_predicate`).
- studio: `POST /api/playground/preview` says before Run whether the scripted engine holds a recorded answer: `scripted_hit` `[{step, hit, because[]}]` from `from_step` through the first miss, and `scripted_key_match` `{ok, because[], step, message}`, computed from the record and the registration with the engine's own key — a narrowed tool list, a thinking level, a tool choice, sequential tool calls or other messages miss at the step they change; a recorded call to a tool the registered agent lacks misses at the next step; a source that ended on a tool call for a reason the replay does not repeat asks the step it never answered (`unanswered`); a step's request record naming a `PrepareStep` change is read for that step. Computed whatever the engine, for every identity inside the source's public id (component names only, never content). No model is called (UX-D-03, UX-C-06). `TestPreviewScriptedCheck`, `testdata/api/playground-preview.golden.json`.
- studio/runtime: `CommandStatus.Reason` — `GET /api/playground/commands/{id}` reads a failed scripted run's error into `reason` `{kind: "scripted_miss", step, components, message}`, the message Studio's one sentence for the Studio UI and the panel, ending as the refusals do: `scripted has no recorded answer for step 2: the tool list differs from the recording — run on live` (`, or replay from an earlier step` when the source never answered it); `error` stays the runtime's text. `ReasonOf` and `ScriptedMissReason` are the mapping (`ExampleReasonOf`, `ExampleScriptedMissReason`); `TestCommandReasonNamesScriptedMiss`, `testdata/api/playground-command-reason.golden.json`, and `TestScriptedEngineEndToEnd` with a real runtime on both halves.
- runtime, studio/runtime: the finished ack carries the scripted miss typed — `reason {kind: "scripted_miss", step, components}` beside `error` (which the runtime cuts at 4 KiB) — and `CommandStatus.Reason` prefers it, its components kept to the closed vocabulary; the error text is read (`ReasonOf`) only for a runtime older than the field. The preview and the run agree on the step, the components and the sentence. The registered defaults carry `thinking_budget` (a budget with no level still sets the request's thinking, which the scripted key reads), and the preview reads it. Studio keeps `studio_url` without userinfo, query or fragment whatever a client sends (and drops a non-http(s) one). `TestFinishedAckCarriesTheTypedMiss`, `TestCommandReasonPrefersTheTypedAck`, `TestPreviewScriptedBudgetOnlyThinking`, `TestRegisterStripsStudioURLCredentials`, `TestScriptedPastTheEndAgreesEndToEnd`, `TestScriptedPastTheEndWithMissingToolAgreesEndToEnd`.
- studio/runtime: `AgentView.ModelProvider` — `GET /api/runtimes` gives each agent `model_provider`, its own model's provider as its manifest names it (`AgentRegistration.ManifestModelProvider`). Studio's replay drawer and playground say, beside the engine, when it is `wefttest`: live calls the app's test script, not a provider, and runs out of turns; a run that ended "model script exhausted" reads "the wefttest script ran out" on both surfaces (UX-C-09). Web types gain `AgentDefaults.thinking_budget` and `PlaygroundRunBody.cross_agent`.
- runtime, studio: the registration carries each agent's `manifest_hash` (sha256 of its registered manifest — the `weft.manifest.hash` its own runs carry) and the `studio_url` the link dialled (credentials, query and fragment stripped; absent in-process). `GET /api/runtimes` gives each agent `manifest_hash` (the registration's, else Studio hashes the manifest — an older runtime's too) and each runtime `studio_url` under `cwd`'s rule (the server token or setup A's loopback, never a panel token), beside `sessions`, `breakpoints_own`, `restored` and `cwd`; a recorded run's `manifest_hash` joins the registered build (UX-C-06, UX-S4-06, UX-S5a-15, UX-D-01 — the wire half; the `/runtimes` page and `/api/meta`'s `runtime_link` are later items). `testdata/api/runtimes.golden.json` pins both against a real runtime dialling a real Studio; `TestRuntimesViewCarriesManifestHash`, the auth matrix.
- studio: capability `target_check` (with the playground; `capabilities_off` names the option otherwise) — what a client gates the target check on.
- weft/otel (I2b, issue 002): `otel.DevMode()` — the code-side twin of `WEFT_ENV=dev`: it satisfies the dev gate (ADR 0028 §12) that `otel.RawBodies(true)` and a destination's `otel.Blocking()` need, without reading the environment, so an isolated `otel.NoEnv()` pipeline in a test or harness that controls its own process can record one `weft.raw` per attempt (or block on a full queue). `NoEnv` still reads nothing, `WEFT_ENV` included; DevMode unlocks those two switches and nothing else. The operator's environment wins over it: when the pipeline reads it (no `NoEnv`) and `WEFT_ENV` is set to anything but `dev`, DevMode is refused with one WARN `otel: DevMode ignored: WEFT_ENV=<value>` (hint: unset it or set `WEFT_ENV=dev`) and RawBodies and Blocking stay off — refused together as exactly one WARN; `NoEnv` + DevMode and DevMode with `WEFT_ENV` unset keep the gate open. Whenever DevMode is what opened the gate Start logs one INFO line per switch — `otel: raw bodies on by otel.DevMode()`, `otel: Blocking on by otel.DevMode()` — so a binary that ships it is visible; a Local destination's `Blocking()` (Local has no queue and ignores it) neither logs that line nor counts toward the `Blocking ignored` WARN. The fixes that named only `WEFT_ENV=dev` add `(or otel.DevMode())`: `weft doctor`'s dropped-records fix, the `gap` / `raw_off` hole texts (`obsdb` and `lib/honesty.ts`, so the API goldens move) and Settings' queue line. `TestRawBodiesNoEnvDevModePerAttempt`, `TestRawBodiesDevOnly`, `TestBlockingDevGate`, `TestBlockingDevGateLocalAndBoth`, `ExampleDevMode`.
- weft/otel (I1): `otel.NoDiscovery()` skips the discovery file and nothing else — `WEFT_STUDIO_URL`, the `OTEL_*` destinations and the `WEFT_ENV` dev gate are read as usual (`NoEnv` drops them all): the per-pipeline form of `WEFT_DISCOVERY=off`, for a harness that keeps the operator's destinations. The "joined the running Studio" INFO line's hint names it; the `NoGlobal` godoc says it does not stop discovery. `TestDiscoveryNoDiscoveryKeepsEnv`, `TestDiscoveryNoDiscoveryExporters`, `ExampleNoDiscovery`.
- `weft mcp` (I5; UX-H-27): eight read tools more, each one GET of an existing route under the same bound — `logs` (`/api/runs/{id}/logs`), `spans` (`/api/runs/{id}/spans`, `tree`; compact: name, `start_ms` / `dur_ms` from the first span, status, the model / token / tool / step / attempt / error attributes, content attributes and events left out, `full: true` as served), `trace` (`/api/traces/{id}`, compact the same way), `transcript` (`/api/runs/{id}/transcript`, `step`), `live` (`/api/runs?status=running&all=1`, a snapshot: an MCP answer is no stream), `agents` (`/api/manifest`), `experiments` (`/api/experiments`, `/api/experiments/{id}`) and `runtimes` (`/api/runtimes`). 21 tools, all GET.
- Studio (U24.2; UX-E-25, UX-A-14): the Agents page draws `GET /api/stats` — a numbers strip per agent card (runs, failed %, duration p50 · p95, TTFT p50, tokens, cost or its badge — "unpriced", never $0 — retried %, denials, sparklines over the window, and the subagents' bill apart as *delegated*, whose hover says it is never summed across agents), one chart per metric across the agents, each tool's calls · failed % · p50 · p95 on its tool card, and the agents seen in runs that no manifest registers (dimmed); a registered agent always gets its own numbers — when the page's group list is cut at the route's 200-group cap (`groups_cut`), each registered agent it left out is read by `GET /api/stats?by=agent&agent=<name>` over the page's exact window, and one whose read is pending or fails says "numbers not read" with why, never "no runs"; a 24h / 7d window rides the URL (`?window=7d`). Every number links to the runs it counts through the group's `runs_query`, read into the runs list's URL (`lib/stats.ts`'s `statsRunsSearch`: `all=1` is *with subagents*, `playground=false` *experiments off*) — never a hand-built `/runs?agent=`. One small chart component, `components/studio/spark.tsx` (an inline-SVG sparkline and a bar list in the palette's `--chart-1` / `--status-bad` tokens, no chart dependency: shadcn's recharts stays unused). The run header gains "vs typical" chips: this run's duration against its agent's p50 and its own tokens and cost against the mean per run over the last 7d, only with at least 5 runs (it says so otherwise; an unpriced side draws no cost chip).
- Studio (U24.2; UX-C-37): the runs list's error-class facet — a failed list (or one class) reads `/api/search` where the database aggregates, its `facets.error_class` drawn as picks with counts; a pick is the `error_class` chip (and `error_type` a chip too, both read from a stats link). One class picked offers "Experiment on these N →" per agent: the playground's multi-source matrix (`/playground?sources=<ids>&agent=<name>`, at most 20 runs, said when the class holds more), one row per source run continuing from its own failing step (`lib/matrix.ts`'s `failingStep`: the core's "at step N"), the agent's models as columns (each live, or the agent's own on scripted), a pass predicate, and the estimate before Run — "28 cells · ≈ 34k tokens · ≈ $0.012" with each column's figure — from the source steps' recorded usage priced at each column's model (`priceAt`, pricing.go's formula restated over `GET /api/settings/pricing`), "cost unknown: <model> has no price" for an unpriced one, never $0. Run saves one experiment and issues every cell through the grid's own cell rule, then links the grid with the count it sent (`/experiments/<id>?issued=N`): the grid reads the experiment again until that many top-level runs joined (a subagent child carrying the experiment id is no cell), for at most two minutes — then "k issued cells never started" and the summary with them missing — its empty cells "issued, not started" and the summary waiting meanwhile. A failed source is a floor ("≥"); its failing step that billed nothing back is estimated at the step before's input tokens, said in a note, never the whole matrix unknown. A failed list stays on `/api/runs` (no facet) while a `?before=` cursor pins its page — search pages by offset and would drop it. A playground or scripted run's "vs typical" chips say they compare with the app's runs. The client price (`priceAt`) follows pricing.go's order and `path.Match`'s globs (a bad pattern — `[a-]`, `[-a]`, `[]a]`, an unclosed class — matches nothing); `studio/testdata/pricing-cases.json` holds both to the same figures (`TestPricingCasesShared`, `matrix.test.ts`).
- obsdb, studio (U24.2; UX-C-37): an experiment input keeps its own `from_step` (`obsdb.ExperimentInput.FromStep`; stored in the inputs JSON, no migration) — a multi-source matrix row continues from its own failing step; absent, the experiment's `from_step`. `POST /api/experiments` refuses a negative one (400 naming `inputs[i].from_step`); Studio's cell re-run reads it (`lib/form-state.ts`'s `cellBody`). `TestUsageLimitOneFacetOneMatrix`: seven usage-limit failures at different steps are one facet of 7, the class filters `/api/search` to exactly them, and one matrix over them saves with each row's source and step (golden `testdata/api/search-usage-limit.golden.json`).
- Studio (U24.2; UX-C-38, UX-C-16): the experiment's pass predicate has a language, `lib/predicate.ts` — the one place it is read: clauses joined by `AND` — `status = | != <word>`, `answer ~ | !~ /re/flags`, `answer = | != "text"`, `steps | tokens | cost < <= = >= > <number>`; empty is `status = succeeded`. The grid judges each cell's latest run ✓ pass / ✗ fail / ? not judged with its reason (a fact not read — an answer not read, content off, stripped or unreadable, an unpriced cost — is never a pass, never an empty answer), each `answer ~` pattern tested off the page's thread (`lib/regex-run.ts`: the answer's first 8 KiB, a group holding an unbounded quantifier that is itself unboundedly quantified — at any depth, [classes] and escapes skipped, a bounded {m,n} let through — or a backreference refused when typed, every other one in a module worker bounded at 250 ms per answer, counted once the worker has loaded (the load itself bounded at 10 s) — past it "not judged", the worker replaced; a worker that cannot load says so for every job and is not respawned; a job whose query was let go is skipped — memoised per run and predicate), the answer read for every cell of the first 20 rows (the matrix's own bound, said past it); while any cell is not judged no cheapest column is claimed ("k cells not judged: no cheapest yet", with the reason) and pass rates read "n/m judged"; a bill not known reads "unpriced" only for a model with no price, else "cost unknown"; the predicate is Studio's alone — the server stores it verbatim and validates nothing in it, and no server read judges a cell, tallies each column ("5/7 · $0.0021") and names the cheapest column passing every row and the cheapest overall (never a column with rows missing); the predicate can be tried on the grid and saved on the definition.
- `make mcp-check` (I5) seeds four shapes built in-repo on wefttest scripts through the otel pipeline — the arena's `sub.failed` and `agentic.chaos`, a thread turn denied by alice and resumed, a nine-turn session (each turn a tool call and an answer, every third tool call failing, ~220-character prompts) — and asserts `explain_failure` answers the child's error, the four failure shapes (SUBAGENT_FAILED, a timeout, SUBAGENT_PENDING, a panicking tool in a child that recovered), "denied by alice", each turn's own text, and the session under 4 KB; it calls `spans`, `logs`, `transcript` and `live` too.
- core, weft: `KeptSteps(n)` — a run option saying the run's `Messages` end with the first n steps of the turn it continues (a replay from step n is fed them as its kept prefix). The kept steps count toward the turn's guards: `MaxSteps` budgets the whole turn (the run makes at most `MaxSteps` − n model calls) and `DetectLoops`' streak starts from the kept steps' tool calls. The usage limit, `MaxModelRetries` and `StopWhen` see the run's own spend and steps; the run's own steps stay indexed from 0 (`StepStart`, `StepRecord`, `RunError`).

### Changed

- Studio and the devtools panel (U20.1; UX-C-18, UX-D-02): the playground's per-variant form no longer carries "Break on (parks every run)" — it points to `/runtimes` with the count in force ("breakpoints for the whole runtime live on Runtimes · 1 tool break on every run"), as the replay drawers do (their pointer links `/runtimes`); the Breakpoints editor moved to `components/studio/breakpoints.tsx`. Settings' Runtimes card is a summary and a link to `/runtimes`. The panel's ✎ Experiment with no runtime at all opens the drawer on its Runs in refusal instead of a rejected Result card ("no runtime registers an agent" is gone). `lib/drawer-spec.ts`'s `targetLine` is gone (the Runs in line replaces it).
- studio: the dist gzip budget is 800 KiB (was 700; ADR 0018 §7 amended 2026-10-26 by the user — phase 10a's screens put the dist at 696 KiB before /runtimes and the Agents charts; the panel.js copy still counts). `make studio-check` enforces it.
- Studio and the devtools panel: the experiment drawer chooses the engine per command (U17, UX-C-01; `lib/engine.ts`'s `engineChoice` and `factsOf`, one reading of the command both drawers make, parity-tested). A plain replay opens on `scripted` — a source run, no edit, no prompt / model / input / fork / tools / thinking / tool-choice change, no parallelism 1 (SequentialTools is part of the replay key), a `from_step` the source answered, and the preview's `scripted_key_match` / `scripted_hit` not a miss (a miss disables scripted in the server's own sentence) — anything else on `live`; a model equal to the source's own is no override and is not sent, a tool choice or thinking equal to the agent's default and an input equal to the source's are no change. Two radios with one line each replace the Engine select: scripted's "0 tokens · replays the recorded answers", or why it is disabled; live's "calls <model> · ~N tok ≈ $x", the source's recorded steps from `from_step` to its last at the pricing table's rate ("≥ $x · later steps also run live" when some are not read), or "cost unknown: <why>" (never $0 for an unknown; a scripted model's exact $0 reads "$0.00"). The engine is never switched behind the user: a scripted pick that cannot answer holds Run with its reason inline. Run names the engine: "Run on scripted · 0 tokens" / "Run live on <model> · ≈ $x" (the panel's "Run experiment ▶" and Studio's "Run" are gone). "Edit this result" opens on an empty field with the recording beside it and seeds no edit; an edit equal to the recording is no edit — never sent, never a reason for live (Studio's field says "same as the recording · not an edit"); a kept result or reply typed back to its recording drops its draft. `editResultAndReplay(step, callID)` lost its third argument; a `ReplayDraft` names the field it opens on (`at`). The playground and a saved experiment's cell re-run choose the engine the same way: the radios replace the Engine select, Run reads "Run A on scripted · 0 tokens" / "Run A live on <model> · ≈ $x", and an engine the reader picks rides the URL (`engine=live` too) and the form. In 0.18.3 every replay defaulted to `live`, a real model call, with nothing saying what it would call or cost.
- Studio and the devtools panel: the experiment drawer reads in `lib/drawer-spec.ts`'s order — source, target, engine, 1 what changes, 2 will be sent (drawn once), 3 tools during the replay, then side effects, thread mode and the option lab folded under "advanced", and Run — each section marked `data-drawer-section`; the panel's engine block is a radio group. Runtime-wide breakpoints ("Break on (parks every run)") are no longer a per-replay knob: the drawers point to `/runtimes`, where Studio sets them (the panel's rung-3 checkboxes and its `PUT /api/runtimes/{id}/breakpoints` call are gone).
- Studio and the devtools panel: a model the source never ran on reads "· live call" in the model choice (UX-D-04); the replay vocabulary is one (UX-D-14) — the panel's result head "Replay 1 of t3 · scripted · finished" (was "Result · t3·x1"), its chip "replay of t3 · step 0" (was "experiment of t3"), Studio's run header "replay of <run> · step N", the panel's "replay of" line spaced; the preview folds each run of unchanged messages as "N same", the changes open (UX-D-36); Studio's drawer keeps Run in a sticky footer (UX-C-19); the side-effect modes read Studio's words in the panel too.
- Studio and the devtools panel (U18.2; UX-C-02, UX-C-03, UX-C-06, UX-D-03, UX-C-07): the target is checked before Run (`lib/target.ts`'s `targetCheck`, one rule both drawers and the playground read, parity-tested). A source run that names no agent, or one no connected runtime registers, holds Run with "the source run names no agent: pick one" / "the source run is agent orders; no runtime registers it: pick one, or start its app" and an agent picker — the agents whose tools match the recorded catalog first — and posts nothing until a pick; Studio never takes the first registered agent as a stand-in (`pickTarget` returns `unnamed` for a source that names none; the panel's drawer opens on no agent instead of refusing). A source run set but unread, or whose read failed, holds Run ("the source run could not be read: <why>"). A pick of another agent than the source's is held with both names until "replay as <agent> anyway", which sends `cross_agent: true` (on the post only, after a release, never in the preview's body). A source recorded by another build of the same agent says so in the target block — "recorded by researcher@9b5997a3 · registered now researcher@a59ce89e · tools differ: search_docs → web_search · scripted will miss at step 2" (the run's `manifest_hash` against the registration's, the preview's `was_sent` tools else the step's catalog, its `scripted_key_match`); an unnamed source reads "recorded by an unnamed agent@…"; the build line, another agent and an unregistered one read one template — and, when the tools differ, holds Run until "replay on today's build anyway". An escape holds only for what it was taken for — the source run and step, its agent, the runtime, the target and the target's manifest hash (`targetKey`; a header agent / runtime pick, a new source, step, pick or build asks again on all three surfaces). The playground's Run and Run matrix wait for the preview, hold on its refusal and on the same target check (never a stand-in agent on a mismatch), and read the preview for the active variant. A saved matrix whose agent is not its source run's is held on /experiments with the drawer's target block until "replay as <agent> anyway", and its cells then send `cross_agent: true` (`cellBody`'s `crossAgent`); a pre-U17 matrix with no saved engine keeps the engine its runs ran on — live included (`modesOf`). After Run, a scripted miss is the command's `reason.message` with a plain list of ways on: "run again on live (≈ $x)" — the estimate on the active variant's card only — a choice that puts the form on live, never a post; "replay from step 0 with the recorded agent" (the panel offers it only while that agent is registered); and, in Studio, "save the recording as a test"; the panel's "run again on live" switches only its own source's drawer, reopening it when another is open. The result head reads the run's status word (`lib/status.ts`: "failed", never "finished"; `outcomeWord`); the exhaustion words are "the wefttest script ran out". Studio's picker is a labelled group that takes focus once when it holds Run, Run is described (`aria-describedby`) by every hold that applies (or the "previewing…" line while the preview is pending) and "run again on live" never focuses a disabled Run; the panel's notes are status lines and its options a group. Studio's target block and miss line live outside the eager chunk (`components/studio/target-check.tsx`). `TestScriptedRefusalsAreActionable`, `src/lib/target.test.ts`, `src/panel/target.test.ts`, the parity test "another build of the same agent" on a fixture shaped like the seeded researcher.
- studio and runtime (U18.2, UX-C-08's server half): every scripted refusal is a sentence that says what differs and the two ways on, without a doc-section reference: `scripted cannot try a changed system prompt: the recorded answers ignore the prompt, so it would replay the old answer — run on live, or drop the prompt change` (was `scripted engine with an instructions override would silently replay the old answer (WEFT-PLAYGROUND §5.5)`), `scripted cannot try another model: it replays the answers the source's own model recorded — run on live, or drop the model override`, `scripted replays a recorded run's answers: pick a source run, or run on live` (was `… a source run is required`), `your edit changes step N's request, and scripted holds only the recorded answer to the original — run on live, or drop the edit` (was `… transcript edits need engine live`), `scripted has no recorded answer for step N: the source never answered it — run on live, or replay from an earlier step` (was `… (use engine live)`); Studio's copies and weft/runtime's word them identically. A caller matching the old sentences must match the new ones. `testdata/api/playground-scripted-refusals.golden.json`.
- runtime: the scripted engine's miss names the source step whose recorded turn was nearest the request, each differing key component and the remedy: `weft/runtime: no recorded turn for step 2 (scripted engine, key …): the tool list differs from the recording [tools]; the scripted engine answers only what the source recorded: run on live` (components `tools`, `thinking`, `tool_choice`, `sequential`, `messages`; `unanswered` when the source recorded no turn, `exhausted` when it answered the request fewer times than asked). The text changes: `no recorded turn for this request (scripted engine, key …): the input changed since the recording, or the run is not scripted-replayable` now reads `no recorded turn for step N (scripted engine, key …): <why> [<components>]; …` (`for this request` only when the source recorded no turn at all) — a caller matching the old sentence must match `no recorded turn for` instead. A source that ended on a tool call (failed or canceled mid-run) misses at the step it never answered, `unanswered`; a recorded call to a tool the agent no longer has misses at the next step, `tools`, and — when that call was in the source's last step — at the step the source never answered, `[tools, unanswered]`: the words and the step the preview's `scripted_hit` uses. `TestScriptedMissNamesStepAndComponent` (R2, UX-C-07), `TestScriptedPastTheEndWithMissingToolAgreesEndToEnd`.
- Studio and the devtools panel: the compare reads each compared run's row in one word — kept · same · changed · missing (`lib/stepdiff.ts`'s `rowState` / `ROW_WORDS`, read from the cells; cell state `kept`, `differs`) — and a marker, row link or cost read uses the side's own ordinal. An identical scripted replay from step 1 reads "kept · same" on both surfaces; a compared run absent at an ordinal of an N-way compare reads "missing" (`stepdiff.test.ts`, `step-diff.test.tsx`, `panel/compare.test.ts` over `diff-replay.golden.json`).
- Studio: the replay drawer's inline compare, read right after its command finished, retries a 404 three times a second apart ("recording the run…") — the command's finish can arrive before Studio holds the run row — then says "<run> has not reached Studio yet …" with a read-again; it never sticks on "could not be read: no run …" (UX-C-10; `diffQuery(a, b, {arriving})`, `lib/stepdiff.ts`'s `ARRIVAL`). Elsewhere a 404 is still not retried. The panel reads its compare only after the run row settled (no race there).
- Studio: `/experiments` has a *replays* tab beside the saved *matrices* (UX-C-15): every playground run (the newest 2,000 through `/api/runs?playground=true`, said when cut) grouped under the run it replays (`forked_from`), the group with the newest replay first, each row its step, what it changed (edits, prompt, model chips), its engine, its outcome, its start and a compare against its source; the page opens on replays when no matrix is saved (`lib/experiments.ts`'s `replayGroups`, `replayChanges`, `fetchReplays`). `GET /api/experiments/{id}` for an id runs carry but no definition was saved answers a synthetic document — `unsaved: true`, no variants or inputs, the agent its runs name, its runs listed — instead of a 404; an id with no runs stays a 404 (`TestExperimentWithRunsButNoDefinition`).
- Studio: an experiment grid cell reads outcome · the answer's first line · tokens · cost · engine (`scripted` or `live · <model>`, `lib/engine.ts`'s `runEngineWords`) · its wall time, the run id as its tail (`…VGKSMX`, ULIDs share their head) and a compare against its source (UX-C-16; `lib/experiments.ts`'s `answerLine`, `usageWords`, `idTail`). The grid has a source column (the run its row's cells replay and the step), and each cell a vs-source chip — "same as source" / "changed at step N" — read lazily: one `GET /api/diff` per cell, the grid's first 24 cells once settled (`VS_CHIP_CELLS`, `vsSourceWords`); a later cell links to the compare and says it was not read.
- `weft mcp` (I5; UX-H-23, UX-H-24, UX-H-25): `explain_failure` reads the whole run — every step (at most 25) and the run's subagent children (at most 12, two levels for a first error), at most 60 reads, the run row and the transcript counted — and answers `first_error` (the first error anywhere, in step order, a failed call's child run before the call's own text: `{run, agent, step, tool, error, via}`; an error nothing recovered from first, a recovered model attempt marked `(retried, attempt N ok)` when it is the only one), a `digest` (one line per step: each call and what it returned, failed model attempts, holes; one per child: its outcome, a park, its own first error; each line at most 1 KB, `+n more`; the caps — steps not walked, the children past 12 by id, the budget's stop, a child the budget left unread by id — said at its end; it follows `next` and `last_assistant_text` in the JSON so a cut takes it first), `decisions` and `resumed_by` / `resumed_from`. A child run is matched to its call by the step's `child_run_id` (else core's `<run>/<step>/<call>` id, else a call id only one child names: call ids repeat across steps). `last_assistant_text` is read from the run's own output batches only (a thread turn's input — an earlier turn's answer — never), null with `last_assistant_note` when it produced none; a parked run a resume decided reads `parked, decided: deploy denied by alice: <reason> → resumed as <run>` (the run document's `decided` block, U13.2), never "awaiting a decision", and a resume names the calls it decided. The `run` tool's line says the same of a decided park.
- docs/mcp-recipe.md (I5; UX-H-28): installs `@latest` (`weft mcp` ships since v0.15.0), no version pinned in the text; the tool list, the parked-run row (decided reads decided) and the worked examples follow I5.
- Devtools panel: panel.js grows from 91,032 B to 95,558 B gzip (+4,526 B; the rows from "U17 engine per command" to "P10a-pre-web-fix" in `studio/web/panel-budget.json`; 10,938 B under the cap); the cap is 104 KiB (106,496 B), up from 96 KiB (98,304 B) by the user's decision of 2026-10-23, so phase 10's panel features land without a trim pass — the ledger's `cap` stays the one source the build (`vite.panel.config.ts`) and `scripts/panel-asset.ts` fail on.

#### Breaking

- **Breaking:** weft/otel (I1, issue 001): an explicit `otel.Local(...)` switches the discovery file off, as an explicit `otel.Studio` does — a pipeline that names its store writes only to the stores it names (and the environment's `WEFT_STUDIO_URL` / `OTEL_*`), never into a running `weft studio` / `weft dev` it found on disk. A Local-only pipeline (setup A's own store, a test's `otel.Start(ctx, otel.Local(p), otel.NoGlobal())`) used to join that Studio too and double-write every run, with one INFO line as the only sign. An app that relied on the join names the Studio as well — `otel.Install(otel.Local(path), otel.Studio(url, token))` — or sets `WEFT_STUDIO_URL`. `otel.Install()` with no destination still joins (its default Local is added only when nothing else is found). An `otel.OTLP`, `Datadog`, `Langfuse` or `Exporters` destination does not switch the read off (add `otel.NoDiscovery()`). `runtime.Install` follows: beside an installed pipeline with a Local and no Studio (`otel.LocalDB() != nil`, `otel.StudioEndpoint()` empty) it skips the discovery file, opens no link and WARNs once (`weft/runtime: the pipeline records only into a Local store (otel.Local, or otel.Install's default) and names no Studio: no link opened`) — it used to register with the discovered Studio, which could then start runs whose spans never reached it; setup A dials its embedded server with `runtime.Local(srv)`, a running Studio takes `runtime.Studio(url, tok)` with `otel.Studio(url, tok)`. The rule reads the *installed* pipeline only: a pipeline started with `otel.NoGlobal()` is not installed, so `runtime.Install` with no `runtime.Studio` / `runtime.Local` still joins a discovered Studio there — name it with `runtime.Local(srv)` or `runtime.Studio(url, tok)` (the `runtime.Install` / `runtime.Studio` godoc and the README say so). examples/studio-local, run bare beside a `weft studio`, stays its own setup A and moves its listener to 8080 because 7331 is taken (it used to fail "address in use"). `TestDiscoveryLocalOnlyWritesOnlyLocal`, `TestDiscoveryLocalPlusStudio`, `TestDiscoveryWeftDevShape`, `TestInstallLocalPipelineSkipsDiscovery`, `TestListenBesideAStudio`.
- **Breaking:** `weft mcp` (I5; UX-H-26): the list tools — `runs`, `search`, `sessions`, `session` and the new `live` — answer compact rows, not the route's JSON as served. Omitted: every empty, zero, false or null field (a `*_known` flag is kept when false and omitted when true; `cache` is kept unless `exact`), and on a run row `trace_id`, `manifest_hash`, `instructions_hash`, `catalog_hash`, `weft_version`, `service`, `last_seen`, `meta`, `event_count`, `message_count`, `delta_count`, `events_emitted`, `messages_emitted`, `request_count` and `finished` (the `run` tool has them); `unpriced_models` / `delegated_unpriced_models` and every `*cost_reason` move into one top-level `cost_reasons` list (`{reason, rows: [row ids], more?}`) per answer. Changed: `model` reads `provider/name`, `err` is cut to 200 runes, `duration_ms` stands for `finished`, kept `*_hash` values are cut to 12 characters; a session row's `outcomes` strip becomes `outcome_counts` and its `first_prompt` is cut to 80 runes; a session's turns omit the agent, session and public id the session names, cut `prompt_preview` to 80 runes and omit `model`, `cost_badge`, `stop_reason` and `stopped_by` when equal to the answer's `turn_defaults` (the first turn's; `none` where a turn has none). `full: true` on the call answers the route's JSON as served. A nine-turn session of tool-calling turns with ~220-character prompts answers 3.2 KB, 13.6 KB in 0.18.3 (`make mcp-check` asserts under 4 KB).
- **Breaking:** studio: `POST /api/playground/runs` refuses a source run recorded by another agent than the command's target — 400 `source run <id> is agent <recorded>; this command targets agent <target>: send agent "<recorded>" to replay it as recorded, or cross_agent: true to replay it as <target> anyway` — unless the body sends `cross_agent: true` (Studio's alone; it never reaches the runtime: commands reach a runtime only through Studio, and the source the runtime resolves carries the transcript, not the agent name, so the check sits where the run's agent is known). In 0.18.3 a replay was that agent's prompt and tools over the run's transcript, silently; under the scripted engine it then failed "no recorded turn" with no reason (A6; UX-C-03, UX-C-06). A command that names no agent is refused 400 too, naming the source's agent or saying the source names none — Studio never picks one (UX-C-02's wire half); a source with no recorded agent name still runs on the target a command names. The preview keeps assembling a cross-agent replay, with its `agent` warning. `TestPlaygroundRunRefusesCrossAgent`, `testdata/api/playground-cross-agent.golden.json`.
- **Breaking:** Studio (U17, UX-C-01): a form, link or saved experiment without an engine no longer means live — it means the command's own choice (above); a saved experiment's engine (live included) is kept as a pick.
- **Breaking:** studio: `GET /api/diff` aligns runs on the step ordinals of the nearest run both sides share (A8, UX-C-04). It follows each side's `weft.forked_from` chain (`<source>#<n>`, offsets summed, at most 8 links; a panel token reads only the sides' own `forked_from`): a replay against its source — or a replay of a replay against the root — aligns its step k with the source's step n+k, the source's steps before n rows with `kept: true` (the replay side null, nothing compared, never a change; a kept row only where the other side holds the step); two replays of one run — siblings, maybe from different steps — align on it, offset by their difference, steps neither ran no row, and the rows between their from_steps (one side kept the shared run's step, the other re-ran it) are compared against the shared run's own step: `kept_by` (`"a"|"b"`) names the keeping side, whose column is the shared run's step (its own ordinal null), the re-run compared with it — a change there counts in `changed_steps` and `first_changed`, never in `summary.kept`; a shared run Studio cannot read (another public id, gone) leaves those rows not compared, every column in `unknown`. Studio's compare and the panel's read them "same · vs source" / "changed · vs source", the note "b kept the source's step; a vs source: changed" (`lib/stepdiff.ts`'s `rowNote`, parity-tested). A reader of the rows must key them as the shared run's ordinals: `step` is the aligned ordinal, the new `a_step` / `b_step` each side's own (null where a side has no step — link to a run's step by its own), `kept` marks the prefix the replay did not re-run, `aligned` (`{replay: "a"|"b"|"both", source, a_from, b_from}`, null without a lineage) names the shared run and each side's offset into it, and `summary.kept` counts the kept rows. A `forked_from` naming a step past the source side's last is no alignment and a `gap` hole names it. In 0.18.3 an identical replay from step 1 read "changed at every step" (its fresh step was compared with the source's step 0). Runs with no lineage align by their own ordinals, as before. `TestDiffAlignsAReplayOnItsSource`, `TestDiffFollowsTheForkChain`, `TestDiffFromStepPastTheSource`, `TestDiffSiblingsCompareAgainstTheSource`, `TestDiffSiblingsSourceUnreadable`, `testdata/api/diff-replay.golden.json`, `testdata/api/diff-siblings.golden.json`; `TestScriptedReplayFromStepOneIsKeptSame` with a real runtime.
- **Breaking:** studio: a usage that differs where a side billed no tokens (the scripted engine's 0→0) is no longer a change: `GET /api/diff` lists it in the row's new `differs` (`[]` otherwise), not `changes`, the numbers kept on both sides (UX-D-15). Studio and the panel draw it "differs · not counted", greyed, and the compare's line reads "no step changed of 1 · 1 kept (not re-run) · usage differs (0 tokens)" — not "identical" beside "2 of 2 steps changed".
- **Breaking:** studio: save as test on a replay composes its whole `forked_from` chain: for every step the replay kept (steps 0 to n−1 of a replay from step n > 0) the parent's own composed set (followed up the chain, at most 8 links, a loop a 409) byte for byte, then its own fixtures renumbered after them, so the generated test replays the turn from its start — a replay from step 0 of a replay still keeps its parent's. A scripted replay's own fixtures are stamped with the source's model, not `weft/runtime/scripted`; the skeleton's header says "Kept: steps 0–n−1 are <source>'s fixtures (not re-run by this replay)", the export answers `Weft-Kept-Prefix` naming the chain from the root (`<source>#<n>`, or `<root>#<k>,<replay>#<m>`) and Studio's toast "saved … (steps 0–0 from the source <run>, 1 on from the replay)" / "steps 0–1 from the sources r → pg". A source that is gone, derived, or did not record each kept step is a 409 saying so — never a partial test. `POST /api/playground/fixtures` composes the same (UX-C-30; `TestSaveAsTestPasses` runs the composed test, two chained replays included, under `go test`; `TestSaveAsTestForkCycle`). An agentless run's generated test is `run_replay_test.go` (package `run_test`), no longer `replay_replay_test.go`.
- **Breaking:** cmd/weft: `weft export <id> --wefttest <dir>` writes Studio's save-as-test tree into the package directory `<dir>` byte for byte — `<agent>_replay_test.go` beside `testdata/TestReplay_<run>/*.json`, a test that passes under `go test` — instead of the fixtures alone under `<dir>/<run id>/`; a test file or fixtures already there need `--force`, and a symlinked `testdata` or `testdata/<test>` is refused before any write. The old behaviour is `--fixtures-only` (with `--test name`, which is now refused without it) (UX-S5a-07; `TestExportTreeIsTheUIZip` compares the two trees file by file, `TestUnzipTreeGuards`).
- **Breaking:** obsdb: `CommandStore` gains `DeleteRuntime(ctx, runtimeID)` — a runtime's row and its breakpoint set removed (Studio's forget, U20.1); its commands and routes stay. Both first-party backends implement it and `obsdbtest`'s Commands case holds them to it; a third-party `CommandStore` must add it.
- **Breaking:** runtime: a replay from step n > 0 — live or scripted — is the same turn: it runs with `core.KeptSteps(n)`, so the kept steps count toward the agent's `MaxSteps` and `DetectLoops`, and an unchanged replay of a turn a guard stopped stops the same way at the same step (the compare reads kept · same, the same `max_steps` / `loop_detected` end) instead of asking a step the source never made. A substitute-mode chain's legs continue the turn too (`KeptSteps` of the kept steps and the legs before), so the chain ends `max_steps` where the source did; the chain's own "the substitute chain spent N steps, the agent's budget is N" error is gone. A decision on a parked replay still resumes with a fresh budget, as any resume does.
- **Breaking:** runtime: a replay's command error names the step in the source's ordinals, as the scripted miss and the step compare do, and says so: `weft: run failed at step 3 (the source's step ordinal; this run's step 1): …` for a replay from step 2 (it read `run failed at step 1: … no recorded turn for step 3`). The replayed run's own record keeps its own step numbering.

### Upgrade notes

- The SQLite file moves to schema 0025 and a ClickHouse database to 0028 on first open; a 0.18.x binary refuses the migrated database with `ErrNewerSchema`. Keep a copy before upgrading if you may go back. A large SQLite file's first open waits for the `span_stats` backfill (Open logs its span count and duration).
- An agent behind a model middleware that does not forward `Info` gets a new `weft.manifest.hash` after the upgrade (below), so its runs on either side of it read as two builds.

### Fixed

- cmd/weft: `weft dev`'s one line names the app's runtime after a restart over the same database, or a restart on a `.go` save: it read a stable runtime id already listed (restored or disconnected) as not new and said "no runtime registered yet" while `/api/runtimes` listed it connected; it now waits for a runtime connected that was not connected before the app started, or connected then from another pid (`TestDevRuntimeRegisteredAfterRestart`).
- core: `weft.manifest.hash` is computed after the `WrapModel` chain is built, so it is always sha256 of `weft.Manifest(agent)` — what weft/runtime registers. It was computed before: a model middleware that does not forward `Info` gave the run one hash and the registration another, and Studio read the same build as two. `TestManifestHashAfterTheModelChain`.
- weft/otel (I2b, issue 002): the dev gate's refusal names its real cause — under `otel.NoEnv` without `otel.DevMode` Start WARNs `otel: RawBodies ignored: otel.NoEnv reads no WEFT_ENV` (hint: add `otel.DevMode()`, or drop NoEnv — `otel.NoDiscovery()` isolates from the discovery file and keeps `WEFT_ENV` — and set `WEFT_ENV=dev`); it used to say `… WEFT_ENV is not dev` even when `WEFT_ENV` was `dev`. Outside NoEnv the text stays `… WEFT_ENV is not dev`. The same pair for `otel: Blocking ignored: …`.

## 0.18.3 — 2026-10-21

### Fixed

- thread/pool: a `Forward` landing between a run's running receipt and its running mark — the run holding its slot, the ledger reading `running`, the pool's phase still queued — is refused as the state the run is leaving, `accepted` before a first run and `parked` before a resume, as `Forward`'s godoc says; v0.18.2 fell through to the ledger and refused with a `*StateError` reading `running` ("receipt … is running, not running"), so a caller retrying only on `accepted` gave up before its steer landed (the arena's `pool18.restart` under full-suite load: its child then never saw the steer and its gated model looped on, `Wait` timing out — the hang was the scenario's, no delegation was left unsettled). A `Forward` while the child settles — the settled phase down, the settlement receipt not yet written — names the state it settles to instead of the ledger's `running`; one to a parked child is refused as `parked` even when a decision resumes it at that moment (the refusal was read from the ledger after the pool's lock was let go, the resume's running receipt already there). A refusal of a child this pool runs is now the state read under the pool's lock with its phase, the ledger read only for a receipt no child of this pool serves; `Forward`'s godoc says such a refusal never reads `running`. `TestForwardBetweenTheRunningReceiptAndTheMarkReadsTheStateItLeaves`, `TestForwardWhileSettlingReadsTheSettlement`, `TestForwardToAParkedChildResumingMeanwhileReadsParked`, `TestForwardWhileParkingNeverReadsRunning`, `TestForwardRetriedFromTheReceiptSteers` (the arena's shape, failing on its first iteration on v0.18.2; `POOL_FORWARD_RETRY_ITERS`); `TestNestedRandomChains` retries a `Forward` at every `Submit` receipt from its creation until it steers on one iteration in three, before and after a restart (`POOL_CHAINS_FORWARD_RETRY`).

## 0.18.2 — 2026-10-21

### Fixed

- thread/pool: `Cancel` of a child that is parking — its approval requests already mirrored on the parent's `Pending` and its receipt already reading `parked`, the pool not yet at rest with it — now keeps the parked-child promise: the settlement is recorded before `Cancel` returns. The pool took such a child for a running one: `Cancel` asked the (already ended) run to stop and returned at once, the receipt still `parked`, and the park settled it `canceled` a moment later (`TestCancelParkedChildBesideARunningSibling` on CI: "receipt when Cancel returned = {… State:parked …}, want canceled"). `Cancel` now waits for the park to finish (its ctx bounds the wait); the park reads the cancel, closes the boundary and settles `canceled`, as before. `Cancel`'s godoc and docs/thread-operations.md say so. `TestCancelWhileParkingSettlesBeforeReturning`.

## 0.18.1 — 2026-10-21

### Added

- docs: docs/testing.md — every kind of weft test in one guide (`make test`/vet/lint/fmt and `core/contract_test.go`, godoc examples, `make offline` and `WEFT_MODEL_REQUESTS=deny`, `wefttest` Script/Record/Replay and the conformance suite, the `live` tag, `make fuzz`/`fuzz-thread` and their eleven targets, `make stress` and `TestNestedRandomChains`' `POOL_CHAINS_*` knobs, the nightly `soak-thread`, ClickHouse, `studio-check`'s budgets and ledgers, `devtools-vite-check`, `mcp-check`, apidiff and the facades, the Go 1.26.0 floor, the phase gates, the arena): what each proves, the command, when it runs, measured durations, knobs, how to reproduce a failure; before-push and before-release checklists; which tests a new feature adds. Linked from AGENTS.md and README.md. AGENTS.md said `make fuzz` runs five targets; it runs core's six (and `make fuzz-thread` five more). ci.yml's studio-job comment said a 600 KiB gzip budget; the gate (Makefile `studio-check`) is 700 KiB.
- studio: a test fails when a route has no auth-matrix row. `TestAuthMatrix` now reads every route Studio registers — the package's `HandleFunc`/`Handle` pattern literals (each resolved on the matrix's server) and the run and session sub-route tables — and fails naming any route (method + pattern) with no row, and any row that reaches no registered route (rows pinning the API's 404 are marked as such). The OTLP receiver's `POST /v1/traces` and `POST /v1/logs` were the routes without rows (checked in a separate block); they are rows now: the ingest token as a bearer opens them, every API identity is 401. `serveSessionRoutes` dispatches from one table (`sessionSubRoutes`), unchanged in behaviour, so the check enumerates what it serves. docs/testing.md said this was a review duty; it says the test enforces it. `TestAuthMatrixCompletenessCheck`.

### Fixed

- core: the step's dispatch table no longer shares its backing array with the `ModelRequest.Tools` a model is handed. The loop dispatched from the very slice it passed down the `WrapModel` chain, with or without a `PrepareStep` chain, so a model or middleware reassigning `req.Tools[i]` changed what the step ran — another tool of the same name ran instead, and a `nil` element panicked `findTool` (under `Stream` outside the contained region, killing the process). The model now gets its own copy of the slice. Each message's `Content` slice is copied for the request too (one backing array per step, one allocation), so reassigning a part in `req.Messages[i].Content` no longer rewrites the run's transcript — under a `PrepareStep` chain too, where the model was handed the chain's own `Messages` (a message a function splices in and keeps across steps, a pinned instruction, was rewritten by a model writing into its request; `TestPrepareStepRetainedMessageSurvivesTheModel`). `ModelRequest`'s godoc names the copied slices; the bytes inside parts (`Args`, `Data`) stay shared and read-only.
- core: `OutputDecoder` partials only grow. The prefix closer dropped an open array whose tail could not close yet — a key cut mid-string, a literal cut mid-token inside it — back to the last top-level member, so a streamed partial could go 2 items → 0 → 2 and a UI flickered. An uncloseable tail is now cut back to where it began and no further: a number keeps its longest valid prefix (`1e` → `1`), a cut literal goes and its key closes as `null`, a cut `\u` escape goes, and otherwise only the innermost member is dropped, back to the last comma or open bracket at any depth (a first top-level member still being written still reports no partial). `FuzzPartialJSON` now also asserts monotonic partials across every truncation.
- core: a `PrepareStep` or a `WrapModel` middleware writing through `req.Params` — `*req.Params.Temperature = 0.9`, `req.Params.Stop[0] = …` — no longer reaches the agent's `Params`, a run option's, or the caller's variables behind them. The loop handed both seams the agent's own pointers, so one write changed the sampling knobs of every later step and run of the agent (concurrent runs raced on them), the request record's `prepared[]` missed `params` (before and after shared the pointer), and a retried attempt's request record carried what the model had written. PrepareStep's godoc already promised "a copy the function may mutate freely"; the chain and the model now each get their own copy of the values (one allocation for the pointer knobs, one for `Stop`, none when nothing is set), and `ModelRequest`'s godoc says so. `TestParamsNeverWrittenThrough`.
- core: an `OutputDecoder` string partial only grows by appending. A string cut right after a lone backslash closed as an escaped one (`"x\"` showed `x\`, then the escape arrived: `x⏎`), a rune cut mid-sequence showed U+FFFD before the rune, and a `\uD83D` escape showed U+FFFD before its low surrogate; each tail is now held back until it completes (`mw.RepairJSON`'s closer is unchanged). Only a `\u` escape waits for a low surrogate: a `\\`, `\n` or `\t` followed by four hex characters in D800–DBFF is text (the first 0.18.1 patch held it back too, so `C:\\DBFF\\file` streamed `C:\DBF`, then `C:`). Only JSON whitespace (space, tab, CR, LF) is trimmed around the document: a raw NBSP, NEL, U+2028/2029, U+2000–200A or U+3000 at the end of an open string is text (0.18.0 trimmed any Unicode whitespace, so `{"s":"prix  : 10"}` streamed `prix`, `prix `, `prix`, …). `FuzzPartialJSON` and the decoder's partial tests now hold strings to a prefix, bools fixed and every scalar's type stable, seeded with these cases; `TestClosedPrefixStringsGrowByPrefix`, whose generator now writes `\uXXXX` escapes (surrogate pairs as escapes, lone surrogates), non-`u` escapes followed by hex, raw Unicode whitespace, and invalid UTF-8.
- core/mw: `Retry` and `Fallback` hand each attempt its own copy of the request — fresh `Messages`, `Content` and `Tools` slices and fresh `Params` values. Every attempt was given the one request the layer received, so what a failed attempt's model or an inner middleware wrote into it (`*req.Params.Temperature`, `req.Messages[0].Content[0]`, `req.Tools[0] = nil`, all allowed by `ModelRequest`'s godoc) reached the next attempt and the next model of the chain: a middleware inside `Retry` rewriting by element assignment compounded per attempt, and a `nil` tool reached the adapter. Paid on the `Retry`/`Fallback` path only (about five allocations per attempt). `TestRetryAndFallbackAttemptsGetTheirOwnRequest`.
- core/mw: `RepairJSON` keeps Unicode whitespace in the payload. It trimmed the arguments (and a fence's payload) with `strings.TrimSpace`, so a raw NBSP, NEL, U+2028/2029, U+2000–200A or U+3000 ending a truncated open string was cut away and the tool got `{"s":"prix"}` for the model's `{"s":"prix<NBSP>` — model-visible content lost, against the godoc's byte-for-byte promise. At a cut inside an open string only JSON whitespace (space, tab, CR, LF) is trimmed now, as in the `OutputDecoder`'s closer. Outside the document nothing changes: Unicode whitespace before the arguments, after a document that closes, before a fence (an NBSP before ```` ``` ```` still opens one) and around a fence's payload is trimmed as 0.18.0 trimmed it, and ASCII whitespace exactly as before. `TestRepairJSONKeepsUnicodeWhitespace` (its outside-the-document cases pinned to v0.18.0's output), `TestRepairJSON`'s new cases.
- core: the request recorder's catalog memo keeps its own copy of the tool slice it hashed, not the slice the model was handed — a model reassigning `req.Tools[i]` to the next step's tool made that step record the previous catalog's hash and no tools record of its own. `TestRequestRecorderCatalogMemoIsItsOwn`.
- thread/pool: a two-level sync park (lead → child → grandchild, each a sync wrap) decided once on the top parent resumes the whole chain — the grandchild runs the decided call, its answer resumes the middle child, whose answer is the lead's tool result. 0.18.0 settled the middle delegation failed with "nothing to resume: the child's resume did not survive a restart" though nothing had restarted (the middle child's replay started no resume of its own: its boundary is its grandchild's delegation, whose resolution now hands its resume to the middle delegation's run). The chain also resumes after a restart that wraps the agents again; the restart words are left to a grandchild no agent can resume. A grandchild that parks again while the middle child's resume waits on it climbs the chain like the first park: the middle child parks in turn and mirrors the new request to the lead, at any depth (the first 0.18.1 patch left it in the middle — the lead's `Pending` empty, its receipt `running`, `Wait` blocked for good). The second mirror keeps the namespaced call id and names the new occurrence by its run, superseding the first (ADR 0022 §C): one request offered, undecided, the earlier decision not carried over. `Cancel` or `Close` of the middle child while its resume waits ends the grandchild first (a parked one is canceled at its boundary; `Close` leaves it parked) and waits for it to rest, at most 30 s, so the grandchild settles `canceled` in the middle's ledger before the middle's session closes and the lead reads `SUBAGENT_CANCELED` (it read the grandchild `running` for good). The pool's bound holds through the hand-off: the grandchild's slot passes straight to the middle child's resume, which used to run its first model call slotless beside the next delegation to take the freed slot (two model calls in flight on `New(1)`). `TestNestedTwoLevelApprove`, `…Deny`, `…AcrossRestart`, `…RestartWithoutGrandchildAgent`, `TestNestedTwoLevelReparkApprove`, `…ReparkDeny`, `…ReparkThreeTimes`, `TestNestedThreeLevelRepark`, `TestNestedCancelWhileAwaitingTheGrandchild`, `TestNestedResumeHoldsTheBound`.
- thread/pool: nested delegations end settled, whatever ends them. A grandchild that ends holding no slot — canceled while it queued, canceled at its boundary, panicked — queues for one on the middle child's behalf before its answer starts the middle's resume; the resume ran its first model call slotless, beside the bound (the second 0.18.1 patch lent only a slot the grandchild held). A grandchild that parks again while a sibling grandchild still runs its approved call no longer parks the middle child: the middle waits until every delegation it has left is parked (a Cancel or Close of the middle parked that early closed its session under the running sibling — its receipt `running` for good, "settlement receipt not recorded: session is closed" — and resumed the re-parking one with the cancel's denial). `Cancel` of a middle child parked on its grandchild's request ends the grandchild before it returns — canceled at its boundary, never resumed, settled `canceled` in the middle's ledger (0.18.0: the cancel's denial re-armed the grandchild, whose resume failed into the middle's closed session, and the middle's ledger read it `parked` for good); from the moment a middle child starts ending, none of its sync grandchildren is armed again or resolves its delegating call in it. A `Cancel` of a running child whose run reaches its approval boundary first settles it `canceled` there instead of leaving it parked (Cancel's godoc: "the receipt settles canceled"). A resumed sync child canceled before its parent's turn had parked the delegating call — the parent still running another call of the same step — resolves that call once it parks, waiting on the pool's context — and, once the pool has closed, for at most 30 s more, `Close`'s drain waiting for it (in 0.18.0 the resolution was tried at once, failed "delegating call no longer pending", and the parent parked on the call with nothing left to resolve it: `Pending` empty, `Next` nil; the third 0.18.1 patch still did so after `Close`, whose canceled context ended the wait at once). The 30 s bound on a middle child's wait for its grandchildren has a defined outcome: a grandchild still running past it is settled `canceled` by the pool in the middle's ledger while that session is open, its stop naming the bound, and its abandoned run records nothing when it ends (it was logged and left, its settlement failing into the closed session, `running` until a `Recover`); `Cancel`'s and `Close`'s godoc, ADR 0022 §F and docs/thread-operations.md say so. Ending a middle child no longer cancels its async children (`Submit`, an `Async` wrap): they follow D4. `TestNestedResolutionQueuesASlotWhenTheGrandchildHasNone`, `TestNestedReparkWaitsForTheRunningSibling`, `TestNestedCancelParkedMiddleEndsTheGrandchild`, `TestCancelBeforeTheWrapperParksStillResolvesIt`, `TestNestedEndBoundSettlesTheStuckGrandchild`, `TestNestedEndLeavesAsyncChildren`, `TestCloseResolvesACanceledResumeOnceTheParentParks`, and `TestNestedRandomChains` — random chains of depth 2–4 and fan-out 1–3 on pools of one and two, with `Submit` children beside them, a request expiry, `Forward`s to running receipts; decided and denied to the end, or interrupted half-way by a `Close` racing a `Decide`, a `Cancel` racing a `Decide` on the same lead, or a restart (`Close`, the leads reopened on a second pool that `Recover`s them, driven on to the end); every `Wait` returning, every receipt terminal at the end, no delegating call delegated twice, the bound held, no goroutine left (`POOL_CHAINS_ITERS`, `POOL_CHAINS_MODE`, `POOL_CHAINS_EXPIRY` for a longer run).
- thread/pool: `Cancel` of a sync child parked while its parent's turn still runs a sibling call returns at once, under its own context: the settlement is recorded before it returns, and the delegating call — not parked yet — resolves with `SUBAGENT_CANCELED` on a pool goroutine once the turn parks it (`Wait` waits for that). The third 0.18.1 patch waited for that park on `Cancel`'s caller, ignoring its context, and a `Cancel` made from inside that sibling's own handler deadlocked. Under a `RequestExpiry` a delegating call is never offered as a decidable call, never lapses as one and never runs its delegation twice: a middle child whose grandchild's mirrored request lapsed in it before it parked (its step still running another call) parked with nothing to mirror, so the lead's `Pending` offered the middle's delegating call itself (`Child` ""), the middle's later answer failed "does not delegate to child session", the lead stayed parked, and approving the offered call ran the delegation again (in 0.18.0 too). A child now parks only on a boundary that offers something — one open only on its own sync delegations is waited on, like a resumed child's — and a sync grandchild resumes only while the child above it waits on it (a lapse in a parked middle resumed the grandchild alone, its answer starting a resume of the middle nobody followed). `Close` of a middle child waiting on its grandchildren cancels a parked one at its boundary with the middle, as the middle's own end does, instead of leaving it parked under a middle that settles canceled (nothing could resume it, and `Recover` of the lead does not descend into a settled child); `Close`'s godoc now says which parked children stay parked for a later pool: those under a parent at rest. A steer `Forward` sent to a running child whose run `Cancel` or `Close` ended before draining it no longer runs its follow-up turn: that turn ran on the `Forward` caller's context after the run had given its slot up, a model call beside the bound (in 0.18.0 too). ADR 0022 §D, §F, §H, `Cancel`'s, `Close`'s, `Forward`'s and `Decide`'s godoc and docs/thread-operations.md say so. `TestCancelParkedChildBesideARunningSibling`, `TestCancelFromTheSiblingHandler`, `TestNestedExpiryNeverOffersTheDelegatingCall`, `TestCloseCancelsParkedGrandchildUnderAnEndingMiddle`, `TestNestedReparkWaitsForTheRunningSibling` (its `Close` case now wants the parked grandchild canceled), `TestForwardedSteerDoesNotOutliveACanceledRun`, `TestNestedAbandonedRunParkingLaterWritesNothing`.
- thread/pool, docs: `Recover`'s godoc and docs/thread-operations.md §10 said a restart never re-runs anything; a restart while an approved call of a parked child runs — the decision recorded and replayed, the call's result not — re-runs that call when `Recover` resumes the decided boundary (the session's own rule: an audited-but-unfinished resume stays resumable). The docs now say so; the code is unchanged. The playground's `weft.Replay` side-effect classes do not apply here: make such a tool idempotent. `TestNestedRestartMidApprovedCallRunsItAgain`.
- thread, Studio: a call `Session.Resume` carries undecided reads `no_decision` — on the resume's `weft.resume.calls`, the call's `execute_tool` span (`weft.tool.denied_kind`) and Studio's decided block — not `denied`: thread no longer synthesizes `Deny(id, "no decision")` for it, leaving it to the core's rule 12 (the model still reads `DENIED: no decision`). Record contract §3's `weft.resume.calls` row said thread resumes read `denied`; it now says they read `no_decision`. `TestResumeUndecidedCallReadsNoDecision`, `TestDecidedUndecidedCallFromARealResume`.
- runtime: a playground decision on a parked `thread: fork` turn reports `weft.tool.approved_by` `runtime`, as the ephemeral path does (AGENTS block 4a); 0.18.0 read `user`, the fork session's `Decide` door (the record contract said `policy`; it now says `runtime` in both modes). Reporting only: the decision is the session's, unchanged. `TestPlaygroundForkParkedDecision`, `TestDecideOnBehalfNamesTheApproval`.
- thread/pool: `Pool.Close` canceling a wrapped call made outside any session (a bare `Generate`) gives its model `SUBAGENT_CANCELED: agent "<name>" was canceled before it finished` — ADR 0022's table and `Wrap`'s godoc (Cancel or Close ended the child) — not the core's `SUBAGENT_FAILED … context canceled`; the call's own cancellation or timeout keeps the core's words. `TestCloseCoversEverything`.
- thread/pool: a `Forward` landing between a delegation's running mark and its child's first `Send` is refused — a `*StateError` reading `accepted` (`parked` in the same window of a resume), retryable at once — instead of becoming the child session's first turn: the task's prompt queued behind the steer, the steer's turn parked on an approval no one mirrored, and the receipt read `running` for good, `Wait` and `Close` never returning (since v0.5). `Forward` now needs the run's turn in flight, as its godoc says. `TestForwardInTheStartWindowIsRefused`; `TestNestedRandomChains` forwards into start windows. The first cut marked the turn sent only once the start's caller ran again after its `Send` returned, so a `Forward` to a child whose model was already running could still be refused as `accepted` (`TestForwardRequiresRunning` and `TestForwardedSteerDoesNotOutliveACanceledRun` under `make stress`); once the start's `Send` is under way, `Forward` waits for it to return (its ctx bounds the wait) and steers the turn it sent. `TestForwardAfterTheStartReturnedSteers`. A restarted pool's first resume of a child it rebuilt (`Register` or `Recover`, then `Decide`) read `accepted` in that window too — the refusal was picked by the delegate's park count, which a rebuilt delegate starts at zero; it reads the run's kind now and refuses as `parked`. `TestForwardInARestartedResumesStartWindowReadsParked`; `TestNestedRandomChains` asserts the exact state (`accepted` before a first run, `parked` before a resume) instead of either.
- thread: `Session.Request` on a lapsed request sweeps it, as every other path does — the expiry audit and the deny, a mirrored request's denial reaching its child through the pool, the boundary resumed when that completes it — before it returns `ErrExpired`. Under `RequireSigned` (no unsigned `Decide` to sweep it) `Pending` kept offering a request no challenge could be minted for. `TestRequestSweepsALapsedRequest`, `TestRequestSweepsALapsedMirroredRequest`.
- thread/pool: two pools delegating from one parent session no longer trip on each other: each pool watches the parent, and every decision recorded there made the pool that did not make the child return `ErrNoAgent` from its `Decide` and log an ERROR from its watch. A pool now skips the receipts whose child another pool watching the parent holds (a live delegation, a `Register`, the wrap it was made under), in its pumps and its `Recover`, silently; `Pool`'s godoc and docs/thread-operations.md §10 say how to recover several pools after a restart. `Session.MirrorWatchers` (new, pool plumbing) lists the watching pools. `TestTwoPoolsOneParent`, `TestTwoPoolsOneParentRecover`. Two windows stayed open, both a closed pool answering for nothing: a pump that read another pool's child unsettled, then found it settled and its pool closed by the time it asked, and a pump reaching the child of a pool whose `Close` had canceled it but not yet recorded its settlement, each reported `ErrNoAgent` and logged the watch's ERROR ("a decision recorded on the parent did not reach its children") — `TestTwoPoolsOneParent` under `make stress`, in 6 of 13 loaded package runs. The lookup now reads the ledger again before it answers `ErrNoAgent`, and a closed pool still holds the children its `Close` is ending (never one it left parked). `TestForeignChildSettledAndItsPoolClosedDuringTheLookup`, `TestForeignChildOfAClosingPool`.
- thread/pool: a `Decide` made while a child is parking (its receipt reads parked, its phase not yet), then `Close`, no longer leaves the child parked on a decided boundary that nothing arms. The decision's pump leaves such a child to its park's own re-read, and `Decide` returns as if the resume were queued; a `Close` landing before the park finished made the park keep the child parked — its delegating call parked in the parent until a `Recover` — and `Close` returned at once, where a `Decide` that found the child parked had its resume queued and `Close` canceled it (SUBAGENT_CANCELED). The park now reads the parent's verdicts once it sees `Close`, and a boundary they complete is canceled at the boundary as a queued resume is; `Close`'s godoc says so. `TestCloseWhileParkingOnADecidedBoundary`; `TestCloseResolvesACanceledResumeOnceTheParentParks` waited only for the parked receipt (written before the park is done) and now waits for the child at rest, its own premise (it failed under `make stress`, GOMAXPROCS=1).
- Studio (docs): `POST /api/runs/{id}/approvals` with a `call_id` the run does not hold pending is 400 before any command — now said in studio/README.md; a proposal for an agent no manifest names stays 404 (409 only when a served manifest file does not name it), as documented.
- make: `make test` passes `-timeout 20m` like CI (`TEST_TIMEOUT ?= 20m`, overridable per invocation). It ran Go's default 10-minute per-package timeout, which `studio` (about 520 s under `-race`) came close to on a loaded machine; docs/testing.md described the workaround and now says it is fixed.

## 0.18.0 — 2026-10-20

### Added

- core, runtime, thread: `core.ApproveBy(callID, by, grantID)` — `Approve` naming its provenance, reporting only: the call's `execute_tool` span carries `weft.tool.approved_by` and `weft.grant.id` (span attributes, never `meta`). `weft/thread` passes it on every resume (`user`, `grant` with the grant entry's id, `approver`, `quorum`, `policy`), and a playground approval of a runtime-started run passes `runtime`. `Agent.TracerProvider()` (resolved at `New`) and `Agent.ContentCapture()` (whether `Content` was given, and its value): what a satellite reporting beside the runs reads; `weft/thread` uses both (UX-B-14, UX-S3-11).
- thread, otel, obsdb: the session's state log as OTel records (U12.1; O14; ADR 0021 amended 2026-10-19: an index, not evidence). `weft/thread` reports every state fact it writes as a log record of kind `thread` (event `weft.thread.<kind>`, scope `github.com/weftgo/weft/thread`) through the agent's `LoggerProvider`: `approval.request`, `approval.decision` (`outcome`, `via` — user, signed, grant, approver, expiry, interrupt, child, parent, quorum — `key_id`, `request_entry`, `always`, `grant_id`), `approval.refused` (`bad_signature`, `replayed`, `expired`, `args_changed`, `not_pending`), `grant.mint` / `use` / `revoke` / `expire`, `receipt` (`queued` with the turn number it took, `accepted`, `delivered`, `deferred`, `dropped`), `interrupt`, `rewind`, `branch`, `uncompact`, `label`, `pin`, `fork` and `thread/pool`'s `pool.delegate` / `pool.retire`. Each is filed under the run it concerns (the entry's run, else the turn in flight, else the last run on the leaf's line), held until that run reports, and what is still held at `Close` goes out `held: true` under no run, keyed by `weft.session.id`. Decided under the session lock, emitted by one goroutine per Session (never under it; `Close` waits). Ids, enums and counts always ride; who, reasons, resolved content and texts only under the body's `content` object — stripped content-off, redacted content-on under the new `otel.ContentThread` kind and capped by `MaxBytes`; a label's name is metadata. obsdb stores one as a plain record under its run (`obsdb.RecordThread`), never as an app log line. A panicking or refusing logger changes nothing the session does.
- thread, obsdb, Studio: the summarizer's cost (O15; UX-B-13). Every summary model call of a compaction or a `SummarizeLeft` branch is a `chat` span with `weft.chat.role` `summarizer`, its model and usage, under the turn in flight or the last run — never in that run's usage, provider, model, `meta`, `started` or `last_seen`: the span is stored and summed apart, but feeds no column of the run row, so a summary made between turns never restarts a finished run's `Retain` clock, never reads an interrupted run running again, and never recreates a run `DeleteRun` removed (`obsdb.AttrChatRole`, `obsdb.ChatRoleSummarizer`; SQLite skips the row at ingest, ClickHouse 0024 restates the traces run view without the span; `obsdbtest.SummarizerSpanIsNotRunFact` holds a backend to it on one database, as its upgrade test runs it over an older schema); one landing after `DeleteRun` stays stored under the gone id until SQLite's `Sweep` takes the spans older than its cutoff that no run row names, in batches of `obsdb.SweepBatch` as its runless session events (ClickHouse: the spans' TTL); a failed call's span carries `error.type` `summarizer`, and one whose run never reported is dropped with a Debug line. The compaction marker's body gains `summarizer_model` / `summarizer_usage`. `obsdb.SessionRow.SummarizerUsage` sums a session's summarizer spans apart from `Usage`, `SummarizerModels` splits it by model (`obsdb.ModelUsage`, `obsdb.SumModelUsage`); Studio serves `summarizer_usage` on session rows, prices it per model on the timeline and each turn's run document (`summarizer`; a call with no recorded model an estimate at its turn's model, `cost_badge` `estimate`, drawn "≈"), drawn "+ summarizer $x" beside the turn's, the session's and the panel's open turn's cost.
- thread: `thread.WithRunMetadata(kv)` — a session's run metadata on every turn's run, written into the header at `Create` / `Fork` (`Header.RunMeta`; a fork takes its origin's), held over it at `Open`, read back with `Session.RunMeta()`; identity keys win, a `Send`'s own `core.Metadata` wins per key (UX-B-26). A turn names its branch: `weft.turn.parent` (the turn whose line it continues, on the first turn after a `Branch`, `Uncompact` or rollback) and `weft.session.branch` (the label on the entry branched to, else its id); a failed send's leaf move back names nothing (`thread.LeafEntry.Abandoned`) (UX-E-20).
- obsdb, Studio: the session timeline index (U12.2; D6). Every `thread` record and `compaction` marker carrying a `weft.session.id` is indexed by session at ingest (SQLite `session_events`, migration 0022; ClickHouse `weft_session_events`, 0025, a materialized view under the content TTL; both backfilled), runless ones included. New optional `obsdb.SessionEventReader` — `SessionEvents(ctx, obsdb.SessionEventQuery{SessionID, PublicID, RunIDs, Kinds, Offset, Limit})` → `obsdb.SessionEventPage` of `obsdb.SessionEvent`, ordered by time, run, kind, then body (`obsdb.MaxSessionEventLimit`, `obsdb.IsSessionEventKind`, `obsdb.SessionEventName`, `obsdb.SessionEventKind`) — implemented by both backends (`(*sqlite.DB).SessionEvents`, `(*clickhouse.DB).SessionEvents`); `DeleteRun`, `DeleteSession` and `Sweep` remove the events with their runs. Studio: `GET /api/sessions/{id}/timeline` (turns in order, marks in record-time order, at most 2,000, each `at` / `kind` / `event` / `lane` / `run_id` / `turn` / `entry` / `fields` / `content`, a decision's `actor` — the `weft.approval.actor` of the turn that resumed the run it is filed under (never that run's own, which names who decided the run before it; absent while no stored turn resumed it, or when the turn that did carries none, the timeline's drawer and approvals view then saying the decider is not recorded yet, or not recorded by that resuming turn (named, with the fix: decide with a who, or resume with `weft.approval.actor` set) — or hidden, to a read token), the call's own decider when the resume names several and the record says which (`key_id`, else to a content reader its `who`; else the list, `actor_of: "resume"`), metadata every token reads, as the run page's decided block does; lanes `approvals`, `grants`, `compaction`, `steer_queue`, `branch_fork`, `pool`, `other`; `forks`; holes `not_recorded` when a turn parked or resumed and no mark is stored, `stripped`, `hidden`, `truncated` with `cause: "response_cap"`) and `GET /api/sessions/{id}/entries?offset=&limit=&run=` (≤ 1,000, `next_offset`; `run` one run's marks alone, its holes and hidden count that run's too, `obsdb.SessionEventQuery.RunIDs`), capability `timeline`; `GET /api/sessions?tag.<k>=<v>` (`obsdb.SessionQuery.Meta`; ClickHouse narrows it, and `RunQuery.Meta`, on migration 0026's `idx_meta` ngram index over `Meta`).
- Studio: the session page (U15.1, U15.2; UX-B-01, UX-S3-01, UX-B-07, UX-B-08, UX-B-12). `/sessions/$id` opens on the timeline: one row group per exchange (a turn and the resumes that decided its parked calls) on a shared time axis, each turn's marks under it in the state log's words, a skipped turn number said with the receipt that took it ("t2 · consumed by a steer · delivered into t1"), unexplained gaps one `not_recorded` hole, a drawer per mark (`?mark=`) linking the parking step and the resuming turn, the lane filter (`?lanes=`, dims, never hides), live on `/api/live?session=` ("● live · a turn is running"). Views: `?view=tree` — the lines the turns continue (`weft.turn.parent`, `weft.session.branch`), abandoned turns "✗ abandoned · off path"; `?view=approvals` — every decision (call, tool, outcome, who / via / key, grant, parked → resumed turn, each a link to the call's row, refused signed decisions, calls awaiting one) and every grant (rule, args globs, scope, uses n of max with their calls, revoked / expired); past the 2,000-mark cap every view draws the `truncated` hole and the header's counts read as floors ("≥ 12 approvals"), and a parked call's request expiry is read from its own run's marks; `?view=table` — the turns, newest first (`?order=oldest`), each with its start to the millisecond, the wait before it ("+0.5s waited for a decision"), duration and own cost. Forks are named on the origin (each fork's stored turns and status, "no turn recorded yet" for one that ran none; `obsdb.SessionQuery.IDs`, `obsdb.SessionForkedFromKey`) and on the fork ("⑃ fork of … at entry e9 (“…”)"). Compaction: a context lane per turn (input tokens, reason, tokens before → after), "started on a compacted context" on the turn that did, and on the run header a chip per compaction ("⌗ compacted at step 3 (4 → 1)", "⌗ session compacted after this run"). A busy policy's end reads neutral everywhere — "✗ interrupted by t7", "↶ rolled back by t9 · off path" (`weft.cancel.cause` `thread.interrupted`, `obsdb.BusyCancelCause`). A turn's run page names its session (breadcrumb "sessions › s_… › t4", a stepper "‹ t3 · turn 4 of 9 · t5 ›" with `{` / `}`, "t6 · 4 of 7 recorded turns" where the turn numbers skip), read in O(1) rows: `GET /api/runs` gains `turn_below` / `turn_above` and `oldest` (`obsdb.RunQuery` / `SearchQuery` `TurnBelow`, `TurnAbove`, `Oldest`, both backends).
- Studio: session actions (U31.2; UX-B-18, UX-B-19, UX-B-20, UX-B-29, UX-B-40, UX-B-41, UX-S3-16, UX-S3-21). `GET /api/sessions/{id}/export?format=jsonl` downloads a conversation (`<session>.session.jsonl`, `weft.session.export/1`, streamed one turn at a time: a `session` line, every turn's run export in turn order — at most 200 —, `session_mark` lines — at most 2,000 —, `session_hole` lines, and a closing `session_end` line `{turns_exported, marks_exported, bytes}`; past 64 MiB of lines before the holes — the session line, the turns', the marks' — a `truncated` hole, cause `response_cap`, and the timeline's holes when the turns ran to the end — when they stopped, its `hidden` (a panel token's marks) and `not_recorded` alone, the scope read's; studio/README.md has the shape). The session header gains copy public id, export jsonl, open in app (the newest turn's `weft.page_url`), delete session… (the app's thread store untouched) and a storage line (the app's `storage` turn metadata, else `not_recorded`). A parked call shows its request expiry from the `approval.request` record — "expires 09:57:56 (in 40.0s)", then "⏱ expired … — denied at the next touch (lazy expiry)"; the panel's approvals block shows the same expiry (read with the turn from `/entries?run=` — an answer missing an awaiting call's `approval.request` record (one with no expiry is final), or a failed one, read again with the next reload and once 2 s on, three reads at most, a failed re-read keeping the expiries read before; the clock redrawing when the words next change and stopping at the lapse, which both surfaces put past the stamp, as thread does; an unparsable stamp dropped; a `truncated` badge past that read's cap and a `not_recorded` one — on the run page too — when it fails with nothing read before).
- obsdb, Studio, panel: sessions list states and the conversation page (U16; UX-B-15, UX-B-16, UX-B-28, UX-B-30, UX-F-02). A session row carries its turn facts, counted in SQL on both backends (`obsdb.SessionTurnFacts`; `obsdb.SessionRow` `Pending`, `FailedTurns`, `EndedTurns`, `CancelledTurns`, `Resumes`, `Outcomes` — `obsdb.TurnOutcome`, `obsdb.OutcomeOf`, `OutcomeSucceeded`, `OutcomeFailed`, `OutcomeRunning`, `OutcomeParked`, `OutcomeResumed`, `OutcomeInterrupted`, `OutcomeEnded`, `OutcomeCancelled`, `obsdb.MaxOutcomes` — and `ForkedFrom`; the API's `pending`, `failed_turns`, `ended_turns`, `cancelled_turns`, `resumes`, `outcomes`, `forked_from_session`): the list says "⏸ awaiting a decision · 1 call", draws the newest 40 turns' outcome strip, counts failed ("1✗ failed", red), busy-ended ("2✗ ended by a newer message") and cancelled ("1⊘ cancelled") turns apart, says "3 exchanges · 5 turns", and filters by chips — `GET /api/sessions?awaiting=1&failed=1&compacted=1&lineage=child|fork&status=&since=` (`obsdb.SessionQuery` `Awaiting`, `Failed`, `Compacted`, `Lineage` — `obsdb.LineageChild` / `LineageFork` —, `Status` — the newest turn's outcome as the session page words it: a cancelled or busy-ended turn is not `failed`, a parked one not `succeeded` —, `Since`; a malformed value a 400; the list's state word is that outcome; `obsdbtest.CheckTurnFactFilters` holds both backends to Go's rule). The session header offers "decide…" on the parked turn and links its public id to `/conversations/$id`: every run of a public id with experiments nested under their source turn, its thread sessions, own and delegated cost apart, newest first (`?order=oldest`), each gap measured within its own session (a run of no session, within the public id's other sessionless runs), live on `/api/live?public_id=`. Panel: `GET /api/runs` narrowed by `public_id` or `session` carries each turn's `session_compactions`, `delegated_sessions` and `ended_by` / `ended_by_turn` / `ended_kind` (read with `obsdb.SessionEventQuery.Kinds`, past 50 sessions a page the `turn_marks_capped` hole), drawn as turn chips "⌗", "↳ 1", "✗ interrupted by t8" / "↶ rolled back by t9 · off path"; a header hand-off "timeline ⤢" or "conversation ⤢"; a scope naming a session lists its own turns with the public id's experiments nested.
- obsdb, Studio, panel: delegated sessions and caller-linked runs (U14; D3; UX-B-05, UX-B-06, UX-B-35). A run's caller link is a row column (`caller_run_id` / `caller_call_id`, SQLite 0021, ClickHouse 0023; `obsdb.RunRow` / `RunDetail` / `Weft` `CallerRunID`, `CallerCallID`; `RunQuery.CallerRunID`, `SearchQuery.CallerRunID`). A session row carries its lineage (`parent_session`, `parent_turn`, `parent_call`, `parent_run`; `obsdb.SessionRow.ParentSession` / `ParentCall` / `ParentRun`) and `obsdb.SessionQuery.Parents` reads one level of a delegation tree. `GET /api/sessions/{id}` adds `delegated` (each delegated session with its cost), `delegated_more` / `_unknown`, and the whole tree's `delegated_usage`, `delegated_sessions_cost_usd` / `_known` / `_reason` and `delegated_sessions_cache`, beside — never folded into — its own; a panel token not shown some of them, at any depth, reads one `delegated_holes` hidden hole with their count (a hidden session's own delegated sessions, at every depth, counted too — their ids read, never shown; past 500 hidden sessions, past two pages of 500 read for one level's hidden ids, or below a walk stopped early, `count_floor: true` and "at least N"; a session counted once, however forged its lineage) and the delegated cost unknown, never $0 (the runs read per level in batches of 50 sessions, `obsdb.RunQuery.SessionIDs` / `SearchQuery.SessionIDs` — a batch past 10,000 runs, by its first page's count, halved, so each session prices up to 10,000 runs of its own, exactly 10,000 included, a session past that unknown; a level reads at most 20 pages per 50 sessions and 20 more, the sessions it leaves unread unknown with the reason; past 500 sessions unknown). `GET /api/runs/{id}` adds `caller_linked` (the first 100 in start order; `caller_linked_more`, `_hidden` for a panel token, `_unknown`, drawn as truncated / hidden / not_recorded). Both surfaces word a pool child "delegated session", never "subagent", with its lineage as links ("↳ delegated by s_… · turn 1 · call d1"); the panel's call badge opens the delegated session.
- Studio, panel: approval decisions visible (U13.2; UX-B-03, UX-H-17, UX-S3-04, UX-H-20). `GET /api/runs/{id}` serves `decided` on a parked run its resume names and `resumes` on that resume: each call's `outcome` (approved, denied, resolved, expired, no_decision), `via` / `key_id` / `grant_id` / `at`, `who`, a denial's `reason`, a resolve's `result`, the resume's `actor` (`weft.approval.actor`, metadata on every surface) — from `weft.resume.calls`, else the pre-step results, else the step-0 `execute_tool` spans (`derived`), else a `not_recorded` hole; thread's `approval.decision` records read by `obsdb.SessionEventQuery.RunIDs`, a met quorum naming every approver, a split verdict its denier apart ("✋ denied by bob · approved by alice"); read-scoped tokens get `who_hidden` / `reason_badge` / `result_badge`; `notes` name what could not be read. The parked card reads its outcome per call on the run page and in the panel ("✋ denied · agent-7 · 'need a photo of the damage' → t2"), the resume opens with "resumed from t1 · 1 decided call" (with what each call left: "→ refund of 249.90 issued"), step 0 no longer draws the decided calls as its own, the story's tools line counts "1 decided" among the parked calls alone (never a negative "awaiting"), and a run page's approved call says "✓ approved · by grant g_…". `obsdb.AttrTrue` reads `weft.tool.denied` / `weft.tool.pending` as a bool or `"true"`.
- Studio, panel: the loop graph (U34; WEFT-LOOP-GRAPH.md; UX-S3-14). The run tree's graph scale opens on the selected run's loop — prompt → model ⇄ one node per tool (calls in call order, failures, RETRYs as ↻, denials, parks with a dashed edge; MCP tools framed by server), the end in the stopped-by words, offered tools never called drawn faint — from the `?tree=1` spans or, without spans, the run's fold; an insight strip (a budget end, the output limit, a repeated identical call, a tool that always fails, never called), subagents expanded in place with a breadcrumb and their own cost, a step scrubber (`?step=`, J / K moving the selection with it, ▶ play; Esc clears the selection until the next pick), the shared `?sel=` and drawer with `‹ call i of n ›`; `?level=agents` is the agents' call graph (a run that recorded no spans but has subagent rows draws it from the run rows, a `not recorded` badge naming what that leaves out: MCP servers, tokens); both graphs fit their pane down to 0.9 of their size (their text never under 9 px), then scroll sideways in their own focusable region, never the page; read failures said in words. The panel's Story tab draws the loop in one line, each tool chip selecting its first call, `graph ⤢` handing off.
- obsdb: `obsdb.TreeCutParent`, the parent a backend maps a run to in the parents it hands `TreeMembers` (and `TreeReadIDs`, `CutRunTree`, `CapTree`) when its read found the run but may not show it — a run past ClickHouse's retention cut. No run has it, so the run is no member, nor is a run whose stored parent it is, and core's id rule stops at it: an id-shaped descendant of a cut run stays out with it instead of joining the nearest member above. A backend that cuts nothing (SQLite) never uses it (U31.1).
- Record contract §2 (`weft.caller.*`, `WithRunMetadata`, `weft.chat.role`), §3 (the summarizer's span), §4 (kind `thread` and every kind's fields; the tool-event order rule), §6 (sessions over own turns, the timeline, decisions); conformance cases `pool_child`, `thread_approvals`, `thread_grants`, `thread_busy`, `thread_compaction`, `thread_summarizer`, `thread_held`, `thread_branches`, `thread_pool`, `thread_content_off`, read back through `obsdb.SessionEventReader` on both backends; `docs/thread-operations.md` §13.

### Changed

- Studio, panel: one decision vocabulary (`lib/decisions.ts`): the verbs read approve · deny · resolve… on the run page, the playground's result cards, the panel's approvals, result pane and parked toast (they read continue · skip · resolve); the state is "awaiting a decision" (was "pending approval" / "awaiting approval"), a finished decision its outcome, not "decided" (U13.2).
- Studio, panel: a busy-policy end (`thread.interrupted`) reads neutral — the run header's "ended · interrupted by t7", the flow strip, the run tree, the story's "■ interrupted by t7 at step 0", the drawer — and a cancel with any other cause keeps the failure's red (U15.2).
- Studio: contrast. `--faint` (eyebrows and every `text-faint` label) is text, ≥ 4.5:1 on every surface in both themes (`#716a5c` light, `#948f82` dark), decoration on its own `--faint-deco`; JSON literals and error results in `--code-lit`, every `code-*` token ≥ 4.5:1 on `--code-bg`; `--status-int`, `--ev-error` and the panel's `--weft-warn` are `#875c04` (were `#9a6a06`, 4.38:1 on a subagent block's wash); the sidebar is a navigation landmark; axe zero on the run page, both themes, with the target-size rule on too (24 × 24: the timeline's marks, the panel's filter checkbox, call links, editable args and results (each result, message and insert on its own line), hand-off links, the export's "with child runs" checkbox, the session tree's mark links, a delegated session's lineage links, the run tree's expand / collapse, the drawer's verbs and tab links, the loop graph's steps, play and insights, a step's call stepper and the subagent block's rows, a call row's toggle, the story's step scrubber, a step's request / wire tabs and its catalog and system-prompt toggles, the raw view's tabs, filter chips and event rows, the JSON tree's expand / collapse take a minimum hit area; a JSON tree on its code surface wraps long strings and scrolls inside it, so a deep request message never widens the page at 360 px); the loop and call graphs' nodes carry their tooltip as their description (a `<title>` on a wrapper and a line under the graph while one holds focus), so a node's accessible name is its drawn words (axe `label-content-name-mismatch`); `palette.test.ts` checks text on each wash (U34, U31.2).
- devtools panel: 90,778 B gzipped (90,091 B at 0.17.0; cap 96 KiB) — the decisions, the loop line, the session chips and the delegated turns, paid for by property mangling over the internal names `scripts/panel-mangle.ts` proves (a build fails on a value widened to an opaque type — a standard container's opaque type argument included — or read by a quoted name) (the host API, wire, DOM and storage names stay verbatim) and folded duplicates; `studio/web/panel-budget.json` has the ledger.

#### Breaking

- **Breaking (record):** only a `core.Subagent` child carries `weft.parent.run.id` / `weft.parent.call.id`; any other run started on a tool call's context — a `thread/pool` child session's turn, another agent's `Generate` in a handler, a run under a Subagent child's own tool call — carries `weft.caller.run.id` / `weft.caller.call.id` on its `invoke_agent` span and `run_start` record (a link, not parenthood), and a run started inside a run's machinery (its model chain, `PrepareStep`, a tap, `OnRunEnd`: a judge) carries neither — under a Subagent child it claimed the child's `weft.parent.*` (U14; O11; record contract §2). A reader that keyed pool children or nested `Generate`s on `weft.parent.*` reads `weft.caller.*` instead; Studio reads both — records written before 0.18.0 keep their `weft.parent.*` link.
- **Breaking (API):** a caller-linked run has no `parent_run_id`: it is no longer in a run's `children`, `children_count`, `?tree=1` spans or `tree=1` export — read it from the row's `caller_run_id` / `caller_call_id` and the run document's `caller_linked` instead. `GET /api/sessions` groups a session over its own turns (`<session>-t<turn>`, or a run linked to no run), so a pool child session is listed and counted in `total`, and `GET /api/sessions/{id}` answers it instead of 404; a client after top-level sessions only skips rows with a `parent_session` (U14; D3). A session row's and document's `status` is its newest turn's outcome (`obsdb.OutcomeOf`): for the four run words — `succeeded`, `failed`, `running`, `interrupted` — it agrees with `?status=`; the other four — `cancelled`, `ended`, `parked`, `resumed` — are not filterable (`?status=` with one is a 400; `awaiting=1` is the open park). Before 0.18.0 it read `failed` for a cancelled or busy-ended turn and `succeeded` for a parked one: read the outcome word instead, or the newest run's own `status` from `/api/runs?session=` (U16).
- **Breaking (Go API):** `obsdb.SessionQuery` (`Parents`, `IDs`, `Meta`) and `obsdb.SessionRow` (`SummarizerModels`, `Outcomes`) are no longer comparable: `==` on them, or their use as a map key, no longer compiles — compare the fields you need instead, or key the map on `ID` (U14, U15.1, U16).
- **Breaking (record):** a trace holds `chat` spans for thread's summarizer calls; a reader that sums `gen_ai.usage.*` over a run's `chat` spans as its model use skips those with `weft.chat.role` `summarizer` instead of counting every one, as obsdb and Studio do (O15).
- **Breaking (record):** `weft.approval.actor` on a `Quorum(n > 1)` resume names every key (or `Who`) that counted toward the quorum — the first n distinct approvers —, sorted, deduped and comma-joined — it was the last signer alone; split it on `,` instead of reading one key (U31.1).
- **Breaking (CLI):** `weft mcp`'s run summary and `explain_failure` say "N calls awaiting a decision" (were "pending approval" / "awaiting approval"): a client matching the text matches the new words instead, or reads `pending` (U13.2).
- **Breaking (API):** `GET /api/diff`'s side keeps its `hidden` hole's `count` (child runs a panel token's scope left out) beside a withheld prompt — at v0.17.0 a read token's hidden hole carried no count. A client that took a count-less `hidden` hole for "the system prompt is hidden" reads the side's `system_hidden` instead, not a count-less hidden hole (U31.1).
- **Breaking (UI links):** `/sessions/$id` opens on the timeline; a link meant for the turns table adds `?view=table` instead, whose rows are newest first — `&order=oldest` for the old order (U15.1, U31.2).
- **Breaking (UI links):** `?scale=graph` on `/runs/$id` opens the loop graph; a link meant for the agents' call graph adds `&level=agents` instead (U34).

### Upgrade notes

- Migrations run at `Open`: SQLite 20 → 22 (0021 the caller columns and the session-lineage indexes, 0022 `session_events`, backfilled from `records`), ClickHouse 22 → 27 (0023 the caller and `SessionParent` columns, backfilled by a background mutation; 0024 the run views; 0025 `weft_session_events`, backfilled from `otel_logs`; 0026 the `idx_meta` index, materialized by a background mutation — until it finishes the older parts are read in full, as before; 0027 0024's traces view restated, for a development database that applied 0024's first text — a summarizer span then still fed the run row — and the same view again on any other). Timed on v0.17.0 databases through 0025: SQLite 1.7 ms, a 25,011-run 358 MB SQLite file 151 ms, ClickHouse 246 ms, every count unchanged; ClickHouse 22 → 27 378 ms (900 runs), development databases at 0025 and 0026 holding 0024's first view 158 ms and 81 ms, every count unchanged, no mutation left running. ClickHouse 25.6 or later (obsdb/clickhouse/README.md).
- Sessions recorded before 0.18.0 show only compaction markers on their timeline: the thread records start with 0.18.0 (a parked or resumed turn there reads the `not_recorded` hole).
- obsdb/clickhouse: every read that lists or opens runs cuts at `weft_runs`' TTL — `Runs` (every `RunQuery` filter, the turn neighbours included) and `Search`, `Sessions` (its rows, `Total`, the turn facts, the tag and `Failed` filters, the summarizer usage), `Session` (its row and its runs), `Run` and its children, `ChildRuns`, `RunTreeSpans` (its rows and the spans of the runs it keeps; a run under a cut parent is out with it, by its stored parent or by core's id rule alike — `obsdb.TreeCutParent` marks a run a backend found but cut —, and a cut run, a derived one included, is `ErrNotFound` as in `Run`) and `ResolvePublicID` — so a run past its window is no longer listed, counted or opened, and no read keeps a turn or a child another cut, whether or not a merge has dropped its rows yet: Studio's `children[]`, `children_count`, `?tree=1` and the cost's subtraction drop a cut child together. The boundary is one instant per call on the server's clock (the host's now plus the server's offset, read with the window, minus the window), so the statements of one read never disagree across it and the cut follows the TTL merges' clock. A failed read of the window keeps the last one known for a minute, logged at Debug — a read the caller cancelled holds nothing —; with none known yet, or a window past about 100 years, nothing is cut. A cut resume is no resumer, on the same instant as the rest of the call: its parked run reads parked again — no `resumed_by` (so Studio's run document carries no `decided` block), in `RunQuery.Status` / `?status=parked` (`Runs` and `Search`) and `SessionQuery.Awaiting` / `?awaiting=1`, the session's `pending`, `resumes` and `outcomes` (`Sessions`, `Session`) and its `?status=` agreeing; SQLite, which cuts nothing, reads a parked run whose resume `DeleteRun` removed the same way. A run's own records and spans read by id until the merge drops them (U31.1).
- `WEFT_DISCOVERY=off` now also stops `weft studio` / `weft dev` writing the discovery file; the start says so in one line (U31.1).

### Fixed

- core: a TracerProvider or LoggerProvider that panics no longer crashes the run (P9). Every call core makes into its providers — `Tracer` / `Logger` at `New`, `Start` and every span call of the `invoke_agent`, `chat`, `execute_tool` and `attempt` spans, the records' `Enabled` and `Emit` — is recovered: the report is dropped, counted in `Agent.TapPanics` and `weft.tap.panics`, and named in one Warn line per run per provider kind; a span whose attribute calls panic is still ended; the run's result and events equal the no-provider run's. User code keeps its own rules. Studio's problem strip reads "tap panics: N (observers or providers, contained)".
- core: a tool error whose own methods panic (`Error`, `Is`, `As`, `ErrorType`) no longer crashes the process — before, a panicking `ErrorType` did under a recording tracer (P9; AGENTS rule 1). The loop reads a failed call's facts once, inside the tool chain's panic containment, with or without a tracer: a panic there is the call's `tool "X" panicked: <value>` result (`error.type` `panic`, `weft.panic.layer` `handler` or `middleware`), siblings keep running. Under an expired `Timeout` a deadline error is the `timed out` result first. A model or run error's methods keep their behaviour.
- core: the tool-event order rule is stated (AGENTS rule 3, record contract §4) and pinned by `TestToolEventOrderRule` — `ToolStart`s in call order, each `ToolFinish` after its own `ToolStart`, finishes in completion order, a batch not fenced; nothing changed in the loop, and two tests that assumed more no longer flake.
- thread: a compaction marker still waiting for a run at `Close` goes out `held: true` under no run, keyed by the session's metadata, instead of being dropped with a Debug line; both backends index it by session (U31.1).
- obsdb/clickhouse, Studio: the READMEs named ClickHouse 23.3 as enough; the schema needs 25.6 or later — migration 0001's TTLs are on `DateTime64(9)` columns, which 25.5 and older refuse ("TTL expression result column should have DateTime or Date type") — now stated in obsdb/clickhouse/README.md and named by `TestServerAtTheFloor` (23.3 is lightweight `DELETE`'s own floor) (P9).
- obsdb/sqlite: a run tree's row cap counts members, not every id under the run — app-shaped ids (`<run>/job/N`) no longer push a real descendant past the cap silently (U31.1).
- Studio: the diff's step-cap `truncated` hole carries `cause: response_cap`, as documented; a diff side says `system_hidden: true` when its prompt is withheld from the token (U31.1).
- Studio: the session page no longer says "the session document is capped": the document carries every turn (U31.2; UX-B-23).
- Studio: a tool result's tone applies — an error result in `--code-lit`, a non-JSON result in `--code-str` — where the code window's default ink used to win (P9).
- Studio: a page whose read answered 403 offers no retry — the same token reads the same 403 — and says so instead (P9).
- Studio: under a panel token an unknown session id answers 403, as another public id's session does, on `sessions/{id}` and its `timeline`, `entries` and `export` (was 404: the difference named which ids exist); a session whose stored marks are all session-level (no turn stored) answers `sessions/{id}` as its `timeline` does, 200, where its own token read 403 (P9).
- Studio: the armed `g` chord disarms on a key typed into a field or held with ctrl / meta / alt, which keeps its key (U31.2).
- devtools panel: two panels on one page say the same "(chained)" whichever installed its fetch wrapper first (U31.2).

## 0.17.0 — 2026-10-17

### Added

- otel, obsdb, Studio: the slog bridge (U11.1; UX-E-07). `otel.Slog()`, an Install option, wraps `slog.Default` (restored at shutdown, with the log package's writer and flags; a program on slog's built-in handler keeps its lines on the log package's writer through a `slog.TextHandler` at the level `slog.SetLogLoggerLevel` set when `Install` ran — read once — each line after `log.Prefix()`, in its key=value format: the built-in handler cannot be wrapped; one Slog pipeline at a time, and an out-of-order shutdown of two restores what the first found, never a closed bridge's handler); `otel.SlogHandler(next)` bridges any handler and follows the installed pipeline; `Pipeline.SlogHandler(next)` binds one pipeline. Every line logged on a span-carrying context — `weft.Logger`'s Debug run lines, `mw.Log`, `mw.Audit`, the app's own — becomes an app log record (scope `github.com/weftgo/weft/otel/slog`, the context's trace and span ids, severity level + 9, the slog level's name, attributes flattened with groups joined by `.`, every app key starting `weft.` renamed `slog.weft.…`, past 120 attributes the rest counted in `weft.log.attrs_dropped`); a line with no span is not bridged, nor the pipeline's own WARNs. New per-destination `otel.AppLogs(on)`: on by default for Local and Studio with content on, off for Datadog, Langfuse, OTLP and Exporters unless opted in. It admits, never bypasses the destination's content policy: an app log line is content — a content-on destination applies `Redact` (new kind `otel.ContentLog`) and `MaxBytes` (default 32 KiB, `weft.content.truncated_bytes`) to the message and each string attribute and drops the line when `Redact` panics; a content-off one that opted in gets it stripped (message and string values emptied, `weft.content=stripped`, time, severity and span kept). An agent's `core.Content(false)` reaches its own lines first: `core.Logger`'s `tool call` line names an error result's size (`err_bytes`) instead of its text, and `mw.Audit`'s `err_bytes` and the `*core.ToolError`'s `code` instead of `err` and `cause` (a child run without `Content(false)` logs them again). Bridged lines are shed with the deltas under a full queue. While a bridge is live — a `Pipeline.SlogHandler`, `Slog`'s wrap while it is still `slog.Default`'s handler (a line logged through it does not keep the pipeline marked once the program replaced the default), or any other handler that has teed a line into the pipeline (building a package-level `SlogHandler` marks nothing) — heartbeats and each run's `invoke_agent` span carry `weft.logs.bridged` (the lines bridged so far), on the destinations that take the lines only (a content-off sink, an `AppLogs(false)` one or a third party not opted in stores the root unmarked, so its empty page carries the hint); it is not metadata — `obsdb.MetaOf` skips it, ClickHouse migration 0021 restates both run views. `obsdb.LogPage.NoBridge`: a run whose root is stored without the mark and has no attributed line, no gap and no cut; Studio's `GET /api/runs/{id}/logs` then holds a `not_recorded` hint — "no app logs: no weft slog bridge was recorded for this run … if the app bridges slog itself, it logged nothing under this run", the fix naming `otel.Slog()` — instead of an empty page that reads "logged nothing". `examples/studio-local` installs it and wraps its model with `mw.Log(nil)`: its lines arrive under each step's chat span (`TestModelLogLinesArriveUnderTheStep`). Settings' app-log line names `otel.Slog()`. Record contract §3, §4 (app log records), §5, §7; contract fixtures `slog_bridged`, `bridge_overrides` (a redacted line, `weft.log.attrs_dropped`) and `bridge_stripped` (a content-off destination's stripped line).

- Studio, panel: a run's app logs on both surfaces (U11.2; UX-E-06, UX-A-07, UX-S2-03). The run page gains a **logs** view (`?view=logs`, key `l`; `RunView` gains `"logs"`): every line `GET /api/runs/{id}/logs` attributes to the run, a severity filter (`?lsev=info|warn|error`, `RunSearch.lsev`, the route's `severity=`), each line's offset from the run's start, severity, body and attributes, and **jump to span** — `?sel=t:<span id>&tab=logs`, the run-tree row that span is behind, or (`resolveRowSel` / `expandFor`, an optional span-parents map) its nearest drawn ancestor's. A tree row a WARN / ERROR line landed on carries a tick ("E1 W2", read at `severity=warn` only when the run document's `log_warn` / `log_error` count such lines; "≥E1 W2" and "at least …" over a cut read); its click opens the row's drawer Logs tab. The drawer's Logs tab and the view read every page (`src/lib/logs.ts`'s `fetchAllLogs` / `allLogsQuery`, 1,000 a page up to 10,000 lines; `lib/api.ts`'s single-page `logsQuery` is gone) and badge a longer list `truncated` ("the first 10,000 lines are shown … nothing was lost"); while the run runs they are read on from the line after the last held, every 5 s, show the page's `partial_reason`, and stop once the read is cut; a finished run's read is whole, once; a row with no line yet says "this row's spans are not reported yet", never "no app log line". The panel's Story step heads carry "📄 ⤢" under capability `logs` for no token or the dev token (the logs route refuses every panel token): Studio's run page on that step's row with its Logs tab open (`lib/links.ts`'s `logsLink`; `runLink` gains `tab`); the panel reads no line.
- Studio, `weft studio`, `weft dev`, `weft doctor`: a server log of refused ingests and 5xx (U11.2; UX-E-37, audit row D10). Every response carries `X-Request-Id` (the client's well-formed one echoed, else generated; in `Access-Control-Expose-Headers`); a 5xx error body carries `error.request_id`. `slog.Default` gets one WARN line per refused ingest batch (`studio: ingest batch refused`: request id, status, path, remote, code, reason, bytes), at most one a minute per (remote, route class — `/v1/traces`, `/v1/logs`, else `/v1/*` — code, status), the next one counting the rest as `suppressed`; a full limiter (1,024 keys) drops expired windows first (their counts on the next line, `suppressed_expired`) and otherwise logs no new key until a window turns (`suppressed_overflow`) — behind a reverse proxy every client shares the proxy's remote, documented. One ERROR line per 5xx (`studio: request failed`, same fields; a panel token's masked body still logs the real error as `reason`) and per handler panic (`studio: handler panicked`, then the panic goes on; `http.ErrAbortHandler` is not logged). The request wrapper is an `http.Flusher` only when the connection's writer is. `weft studio` / `weft dev` `--log-level debug|info|warn|error` (`WEFT_LOG_LEVEL`) and `--log-format text|json` (`WEFT_LOG_FORMAT`) set the server log's handler on stderr (a level alone installs a text handler; neither leaves Go's default). `/api/meta`'s `ingest` gains `accepted` / `refused` (OTLP POSTs answered 2xx / 4xx–5xx since start, in memory) and `last_error` / `last_at` (the last refusal, omitted before one; a refused `Content-Encoding` is echoed at most 64 bytes, in the body and the log line alike); `weft doctor`'s ingest line prints them — "N OTLP batches accepted, M refused" — and the last refusal with its time and a fix, a warning while that refusal is within the hour.

- obsdb, Studio: the run tree (U9.1; UX-A-01, UX-E-22, UX-E-34, UX-E-24). `GET /api/runs/{id}/spans?tree=1` (capability `spans_tree`) answers the run's spans and every descendant run's — the runs whose parent chain reaches it (a run row's `parent_run_id`, which a span's `weft.parent.run.id` fills, so a running child's finished spans count; a stored parent always wins) and, for a run no row or span names a parent of, every run core's exact child id shape names a descendant (`<id>/<digits>/<call>` or `<id>/resume/<call>`, `obsdb.TreeChildID`: a child whose `run_start` was lost is still placed, under its nearest ancestor that is a member of the tree — an app's own `job/1` is no member, so `job/1/0/c` under `job` hangs under `job`, alike on both backends; `job/1` and `job/1/retry/2` are not each other's tree) — found from the run rows first and read by run id in any trace (a child stored under another trace is in it), never by reading the run's whole trace, in start order, each keyed by `run_id` + `span_id` with its run's `parent_run_id`, at most 10,000 spans (`obsdb.TreeSpanCap`) of at most 10,000 runs (`obsdb.TreeRowCap`); a sibling run of the same app trace and a run-less span are not in it. A derived run's tree is cut from its trace before the span cap, the trace read itself capped at 50,000 spans (`obsdb.TreeTraceCap`, `RunTree.TraceTruncated`). Its `holes` (always a list): `truncated`, cause `response_cap`, past any cap; `not_recorded`, cause `no_spans`, naming (at most 100 (`obsdb.TreeMissingCap`), with a `count`) the finished descendant runs with a row and no span — a running child with no span yet is not missing (`TreeRun.Status`); `hidden`, with a `count` (`RunTree.HiddenCount`, subtrees included), when a panel token's scope left runs out — a panel token reads the run's own spans and its public id's descendants, every other descendant (a nested `Generate` or thread turn inside a tool handler, a child whose metadata cleared `weft.public_id`) left out with its whole subtree and unnamed before the cap (`obsdb.TreeQuery.PublicID`; hidden runs are not read, never take a visible span's place), never a 403 for the tree. Every `/spans` answer carries `run_id` (the run's); the TS mirror is `Span.run_id?` / `parent_run_id?`. New optional `obsdb.TreeReader` (`RunTreeSpans(ctx, obsdb.TreeQuery)` → `obsdb.RunTree{Spans, Truncated, TraceTruncated, Missing, MissingCount, Hidden, HiddenCount}`; `ChildRuns`, at most `obsdb.ChildRunsCap` (10,000) rows a call, past it `obsdb.ErrChildRunsCapped`), `obsdb.TreeQuery`, `obsdb.TreeSpan`, `obsdb.TreeRun`, `obsdb.TreeIDRule`, `obsdb.TreeChildID`, `obsdb.TreeMembers`, `obsdb.TreeMemberIDs`, `obsdb.TreeReadIDs`, `obsdb.CutRunTree`, `obsdb.CapTree`, implemented by both backends: SQLite finds the rows in one recursive statement (`runs_started`, runs' primary key for the id rule, its rows that name no parent kept only in core's child shape) and reads the spans on `spans_run`; migration 0020 adds `spans_trace_start` (a trace in start order, no sort: `Trace` and a derived run's tree); ClickHouse finds the rows on weft_runs' primary key and migration 0020's `idx_parent_run` bloom filter on `weft_runs.ParentRunID` (materialized in the background), and reads the spans on `idx_weft_run_id`. A database without a TreeReader leaves `spans_tree` off (`capabilities_off` says why) and answers `?tree=1` with 501. Record contract §6 "A run tree"; the `subagent` fixture pins the parent's `tree`, `studio/testdata/api/spans-tree.golden.json` the answer and its TS mirror.

- Studio: the child subtree in place, the graph scale and the trace link (U9.2; UX-A-01, UX-D-13, UX-A-17, UX-S3-14). On `/runs/$id` a child run row is one collapsed row with "open run ↗", and its chevron (→/←) expands its steps, model calls and tools in place from the same `?tree=1` spans, grandchildren again one collapsed row each, drawn on expand (an expanded child's own `?tree=1` is read only when the tree's `truncated` hole says it was cut). An expanded child's document and stream are read again only while it or the run runs, its stream followed from the position past the last one held by `next_after` (`childStreamQuery`, up to 20,000 events, then a `truncated` badge on its row; from the start again only when that read held gaps) and its gaps a `gap` badge. A running child row carries no loss badge (spans export when they end); a finished one's `not_recorded` badge reads the server's reason. A `?sel=` naming a row inside a child — its `rowKeys` run-qualified key, or `<child run>:<span id>` — expands the children above it and selects it (a row no child draws selects the child's row); folding a child that holds the selection selects the child's row (`lib/run-tree.ts`: `expandFor`, `ALL_RUNS`, `qualifyKey`; the `implied` expansion rebuilt when the selection, the scale or the known runs change, never per event batch). The tree's holes (`SpansDoc.holes`, the TS mirror of `treeSpansDoc`: `TreeHole` with `cause`, `runs` and `count`) are badges above it, `not_recorded` / `no_spans` also on the child rows it names. "whole trace ↗" links `/traces/<trace id>` with the selection kept, the run's own keys run-qualified. `?scale=graph` (`RunScale` gains `"graph"`; `src/lib/graph.ts` `callGraph`, `components/studio/call-graph.tsx`, inline SVG) draws agents and MCP servers as nodes in call-depth columns, edges with their call count and slowest call (not a p50), a self-call as a loop over its node, a resumed call's child under its parent (`<parent>/resume/<call>`), "MCP calls of N child runs not shown" when the page did not read their catalogs, each node with a focus ring and its accessible name starting with its visible words, the edge hint on the edge group's `<title>`, the selection shared with the tree; pool children (separate sessions) are not drawn, and nodes carry tokens when the spans do, no cost. Studio only: the panel's Timeline draws one level (U8.3).

- obsdb, Studio: the live lane's `attempt` frame (H9, audit row O17; UX-E-30). `GET /api/live?kinds=…,attempt` streams `event: attempt` `{run_id, session_id, public_id, step, attempt, layer?, status ok|error, error_type?, provider?, model?, start, end, wait_ms?, retry_after_ms?, trace_id, span_id}` — one try inside a model call, read from its `attempt` span the moment the span lands (OTLP ingest, and a self-publishing database's write; deduped on run, step and attempt), so a step that is retrying or falling back is seen before its `chat` span ends; `wait_ms` is the wait before the try (`weft.attempt.wait_ms`, the step route's `attempts[].wait_ms`), `retry_after_ms` the provider's retry-after ask, each absent when not recorded. Live only: never backfilled, nothing new is stored; each attempt frame is held to a panel token's public id. New `obsdb.FrameAttempt`, `obsdb.Frame.Span`, `obsdb.AttemptFrame`; `lib/live.ts`'s `openLive` gains a `frames` option (readers of other SSE event names) and `src/lib/live-attempts.ts` reads the attempt frames. The Live page asks for them: a failed try reads "↻ attempt 1 failed" under its step while the run runs (the error type and the provider's ask in its title); an earlier step's note is dropped.
- Studio: the runs list's delegation shape (H9; UX-A-19). Every priced run row carries `children_count` / `children_failed` (the runs whose parent it is, one level deep, and the failed ones — counted from the children read the cost already makes, one grouped read per level of a page, never a read per row; omitted when 0) and `children_unknown` when that read failed or passed its bound (10,000 child rows a request; the cost under it reads not known too), drawn "⑂ ?"; `/api/sessions/{id}`'s turn rows count every parent-linked run. The row draws "⑂ n" (the failed count in red), a disclosure that reads `?parent=<id>` and draws the child runs under it, indented, each with its own chip (grandchildren read when opened; past the 100 read, "+N more · open the list"); "with child runs" draws the loaded rows as trees, each child under its parent (`src/lib/delegation.ts`'s `nest`). `/compare` opens with a run picker (`src/lib/compare-picker.ts`: the newest runs as a base, then the base's replay source, the base's conversation (same public id) and its agent's runs, with a find box — id prefix or agent, past the newest 50) and `b=r1,r2` reads like the array form; `?b=` keeps a JSON array's elements whole.
- Studio, panel: step and tool cards tell the whole truth (U25.2, U25.3; UX-S1-03, UX-S1-06, UX-S2-02, UX-S2-06, UX-S2-09, UX-S3-12, UX-S3-13, UX-S3-15, UX-H-18, UX-S5b-03, UX-S1-13, UX-E-14). One shared module, `src/lib/cards.ts`, words what both surfaces draw (the run page's story and the run tree's drawer read the same `RunCards`): a max_tokens step's call the loop never ran (rule 11) is placed on its step from the transcript (`lib/events.ts` `applyTranscript`, `FoldedToolCall.unrun`) with its args as sent (`{"msg":"never`) and the "call never executed" badge, drawn "not executed" in the run tree and the panel's Timeline (no span by design, never a `not_recorded` hole) and counted "1 not executed" in the answer's tools line; an **OUTPUT** card on the run header (the last valid `submit_output` call's JSON, "typed output (submit_output)", "repaired after N invalid submissions"; "no output submitted" only when the root's `weft.output.submitted` is false — a submission whose arguments the record lacks, content off or a read token, reads "typed output (submit_output)" with the `stripped` badge) and the panel's typed-output chip; the Request pane's catalog marks the output tool; attempt rows name their HTTP status, the provider's cause and the wait before them ("503 · Service Unavailable: overloaded · waited 1 ms" — `GET /api/runs/{id}/steps/{n}` `attempts[]` gains `http_status`, `wait_ms`, `error`), the panel's attempts picker too (`attemptTag`); a tool card's execution line ("12 ms / 50 ms limit · queued 3 ms", its origin when not local, `callFactsOf` over the `execute_tool` spans); a parked call reads "awaiting a decision" (or "· ParkOn", "· ParkAllExcept", "· side effects", "· middleware"), a `ParkSource` park "paused at breakpoint" on a playground run or when the run page knows its service's runtime registered `breakpoints_own`, else "paused by ParkSource" (ADR 0030) — never "never completed", its "finished at" fact the same word, and the answer's tools line counts it "N awaiting a decision"; "overridden by experiment" on the first step's Request pane for any `weft.override.*` knob, its hover naming them (`lib/cards.ts` `paramsOverride` reads every stored `weft.override.params` shape); a "PrepareStep: system, tool_choice, params" chip from the request record's `prepared`; the thinking row reads "low → reasoning_effort" (`thinking.wire`); the prompt's file parts as attachments ("file image/png · url", the request's message lines too); the header's own / delegated token split ("60 in = 20 own + 40 via 1 child"; the panel's turn line), counting the core's `<id>/…` Subagent children only (a pool child session or a nested `Generate` is not delegated); a Subagent call's typed outcome ("child failed", "child awaiting a decision · refund_order", "cycle refused", "child timed out · 2.0 s limit"), kept in the drawer's call rows with the tool limit. Proven on the named seeded run shapes recorded through the real pipeline (`studio/cards_test.go` → `studio/testdata/api/cards.golden.json`; `routes/run-cards.test.tsx`, `panel/cards.test.ts`, `lib/cards.test.ts`, parity).
- core, mw, adapters, runtime, obsdb: the run cards' facts on the record (U25.1; O6, O9, O16; UX-S2-02, UX-S2-06, UX-S2-09, UX-S3-15, UX-H-18, UX-S1-06, UX-E-14). `core.AttemptInfo` gains `Wait` and `HTTPStatus`: the attempt span carries `weft.attempt.wait_ms` (the backoff slept before the try, rounded up, never 0 — `mw.Retry` reports it; through a `Fallback` beneath it on the primary's next request only, and under `Retry(Fallback(Retry(x)))` the inner Retry's first try keeps the outer backoff; a Fallback's backups carry none) and `http.response.status_code` (`mw.Retry` and `mw.Fallback` read it off the error with `mw.HTTPStatus`). The `execute_tool` span carries `weft.tool.timeout_ms` (the deadline in force, rounded up: a `Timeout(500µs)` reads 1), `weft.tool.queue_ms` (a call that actually waited, behind a `Sequential` barrier or for a `Parallelism` slot), `weft.tool.pending.source` beside `weft.tool.pending` (`approval` | `park_on` | `park_source` | `park_all_except` | `middleware` — a middleware or the handler returning `ErrApprovalRequired`: an agent `ParkSource` — `runtime.Breakpoints()` — is no longer indistinguishable from `ParkOn`) and, on `SUBAGENT_PENDING`, `weft.subagent.pending` (the parked child's calls by tool name). An `Output` agent's `invoke_agent` span carries `weft.output.submitted` (`false` is the run `GenerateAs` answers `ErrNoOutput`), and the tools record marks `submit_output` `output: true`. The request record gains `prepared` (O9: the parts the `PrepareStep` chain changed — `system`, `messages`, `tools`, `tool_choice`, `params`, `thinking`, `sequential_tools`; names only) and `thinking.wire` (O16: how the adapter sends the level — a model declares it with the optional `ThinkingWire(core.ThinkingConfig) string` method, found along the `Unwrap` chain; `openai` `reasoning_effort` | `thinking` | `none` by dialect, `anthropic` `thinking.adaptive` | `thinking.budget_tokens` | `thinking.disabled`, `google` `thinking_level` | `thinking_budget`), both derived inside the request kind's gate: a run whose destinations want no request record compares no request and asks no `ThinkingWire` (ADR 0016; `TestRequestFactsOffComputeNothing`). `weft/runtime` names a playground run's breakpoints in its `weft.playground.breakpoints` metadata (record contract §2). The new span keys are contract, not metadata: `obsdb.MetaOf` drops them and ClickHouse migration 0022 restates both run views. ADR 0013 and ADR 0028 amended (the optional `ThinkingWire` method; `prepared[]`, `thinking.wire`, the tools record's `output`); `core.ThinkingConfig`'s godoc names the convention. Record contract §2, §3, §4, §10 (new conformance cases `run_cards`, `queue_breakpoints` — `weft.tool.queue_ms` and `weft.playground.breakpoints` — and `bridge_overrides` — `weft.override.params`' null shape and `weft.override.tool_choice`; `parked`, `approval_denied`, `resumed` gain `pending_source`, `compacted` its request's `prepared`, `retry_fallback` its attempts' `wait_ms`).

- Studio: the API's run verbs on the run page (U26; UX-A-15, UX-S1-04). The header offers **export json · jsonl · otlp** beside save as test (capability `export`, downloaded with the bearer; `GET /api/runs/{id}/export` serves json, jsonl, otlp and wefttest only — `xml` is a 400 and is not offered; a read-scoped token gets json and jsonl) and **delete run…** (capability `delete`): an in-page confirm saying the run goes with every child run under it at any depth ("N known", "N+ known" past the children read) and with the derived runs read from its tools' spans, and that the rows leave Studio's database only, then `DELETE /api/runs/{id}` and the runs list (deleting a child reads its parent's document again); where the server would refuse it (a panel token, a non-loopback Host without the server token — meta `live_all` — or the capability off) one faint line says why instead. The Agents card shows `stop_when` and `usage_limit` chips and titles every policy chip with what it does and the option that set it (`src/lib/policy.ts`; the cap reads "cap 64 KiB").
- obsdb, Studio: `obsdb.RunQuery.IDPrefix` / `obsdb.SearchQuery.IDPrefix` and `GET /api/runs?id_prefix=` (U26; UX-A-13): the runs whose id starts with the text, byte for byte (no wildcard, no case folding; a prefix ending inside a UTF-8 sequence matches), on both backends — SQLite as a closed primary-key range. The ⌘K palette looks a pasted id up with it (`all=1`, children included; a panel token finds its own public id's runs only), a single word also as a public id (an app-chosen one like `arena:run-1:rw.deep-delegation`, not only `pub_…`), a `key=value` text by `tag.key=` (a bare word says tags are matched as `key=value`), and ranks those runs first under "Exact match": Enter opens the first, and nothing when no exact match answers until the selection is moved.

- Studio: the polish batch U29.2. `GET /api/runs/{id}/export?format=json|jsonl|otlp&tree=1` exports a delegation whole (UX-A-31): every subagent run under the run, breadth first, at most 200, each its own export — json a `subagents` list beside `subagent_holes` (`truncated`/`response_cap` past the cap, `hidden` with a `count` for the runs a panel token's public id does not reach, left out with their subtrees), jsonl each run's lines after its own `run` record then `subagent_hole` lines, otlp every run in the same two requests (re-ingests as the tree); the file is `<run>.tree.<ext>`; `wefttest` with `tree` is a 400 (its replay substitutes the subagents' recorded answers); the run header offers "with child runs" beside the formats on a run with children. `/api/traces` rows carry `root_run` {id, agent, prompt_preview, status} (read by its id, however many runs the trace holds) and `failed_runs` (`failed_runs_capped` when that count stopped at 500: a floor) (UX-A-39): the trace list titles a row "agent · prompt" and its runs cell reads "3 runs · 1 failed", one link to the top run, never the hex id list. `/api/runtimes` carries `breakpoints_set_at` (in memory: when this Studio delivered the set) (UX-A-33): Settings says when the set was set, or that it came from before this Studio started, warns when every tool is a breakpoint, and "clear all" PUTs an empty set. Settings has a section index — sticky beside the sections on a wide window, a row above them on a phone (UX-A-32; `lib/settings.ts`'s `SETTINGS_SECTIONS`). Every page's first tab stop is "Skip to content", landing past the runs list's filter bar (UX-F-25). Under `?sort=cost` the runs list says how many rows have no known cost and that they are listed last, newest first, not sorted (UX-A-35; `lib/cost.ts`'s `costSortNote`). One not-found state (UX-F-31; `lib/not-found.ts`, `components/studio/not-found.tsx`): a run, session, experiment or trace 404 and an unknown page read "No <kind> <id> in this Studio" with the way back, a run's with "search for it" (the ⌘K palette with the id typed in), and no retry.
- Studio: the polish batch U29.3 (the playground, the replay drawer, the experiment form, toasts). A bare `/playground` over several registered agents asks instead of taking the first one (UX-C-35): "Pick an agent (N registered on M runtimes) or paste a run id in Source run", each agent one click, the five latest failed runs as one-click sources. A rejected or lost replay command offers "send again" (a new command; held by the form's own rules) (UX-C-31). A kept tool result's edit field is a textarea labelled by its call — `lookup_order(order=42) · c1`, so parallel calls read apart — with "start from recorded", "reset to recorded" and a size line (UX-C-25), and "edit this result" focuses that step's field even when call ids repeat across steps (UX-C-24). The replay drawer holds back its command's finish toast while it shows the outcome (UX-C-32; `lib/notify.ts`'s `quietCommand`). The playground card's "save as fixture" (one download per fixture file) is the run page's "save as test" (one wefttest zip), the panel's result pane hands off to it too (UX-C-29). The preview's `prepare_step` warning is said only when a request record of the source names what a `PrepareStep` changed (the parts listed), or none was recorded; new warnings `catalog` (the registered tools differ from the recorded step's) and `agent` (the replay's agent is not the source run's) (UX-C-26). A parked app turn's refused verbs carry the refusal's head as their tooltip and point at the one line under the calls, no longer repeating it per verb (UX-A-36). At phone width the page under the open replay drawer does not scroll sideways, and the shell's place word truncates instead of pushing Search past the edge (UX-C-33).

### Changed

- core: `weft.override.params` is the run's `Params` replacement by the request record's lower-case names — **breaking for readers of the attribute**: it was the Go struct's JSON (`{"Temperature":0.4,…,"MaxTokens":null}`). A `Params` run option replaces the agent's struct whole, so the attribute holds each knob the replacement sets and `null` for each knob the agent set and the replacement leaves unset (`{"temperature":0.4,"top_p":null}` over an agent with `TopP`); `weft.override.hash` takes the set knobs alone (equal overrides hash equal), and a params override's hash differs from 0.16.0's. A named tool choice with no name reads `weft.override.tool_choice` `tool`, never `tool:` (U25.1; UX-S5b-03). Contract fixture `bridge_overrides`.
- obsdb, Studio, `weft`: one status filter for `/api/runs` and `/api/search` (I3, issue 003) — **breaking for `/api/runs` callers that relied on an unknown word**: `?status=` takes running, succeeded, failed, interrupted and the read-time `parked` (an open park: succeeded, pending calls, no run whose `resumed_from` names it) and `cancelled` (failed, `error_type` cancelled), mapped onto stored columns on both backends, so `total`, `limit` and the cursor describe the filtered set; any other word is a 400 naming them on both routes (in 0.16.0 `/api/runs?status=parked` was an empty 200, `/api/search` a 400). New `obsdb.StatusParked`, `obsdb.StatusCancelled`, `obsdb.StatusWords`, `obsdb.ParseStatus`, `obsdb.ErrInvalidStatus`, `obsdb.Status.Stored`; obsdb/clickhouse's README states the `parked` filter's cost (a column scan of `ResumedFrom`). `weft runs --status <word>` (beside `--failed`, with which `--status cancelled` is accepted; a word outside them exits 2) and `weft mcp`'s `runs` / `search` tools document the six words. The runs list's status filter is the server's and reads `lib/status.ts`'s words (`statusPick`, `ANY_STATUS`): the parked pick's count is the API's total, and cancelled is offered; a row listed under `status=parked` that the page reads decided through a later turn (no stored resume link: pre-U13.1 data, or a resume without `weft.ResumeOf`) says "decided by a later turn the record does not link". Revises U32.2's "Not changed, accepted".
- Studio: one step for the playground page (U26; UX-C-21) — **breaking for share links and drafts**: the matrix's own "from step" input is gone; the source block's step picker sets the step Run and every matrix cell continue from, and the page's transcript edits go with each cell (and are saved with the experiment) past step 0. The form format is v2 (`FORM_VERSION`; `matrix.from_step` removed, a cloned experiment's step is the form's `step`): a v1 share link or draft is refused, not read with one of its two steps dropped, and says it is from an older Studio with the way out — re-create it from the experiment ("clone into the form").
- Studio: the ⌘K palette matches by contiguous substrings, never a subsequence (U26; UX-A-13): "1c06797f" does not list a trace whose id merely holds those characters in order, and Enter on text nothing matches opens nothing (`src/lib/finder.ts`'s `paletteFilter`); a run's metadata pairs are keywords.
- Studio: `/compare` links write `?b=` as one comma list (`b=r_2,r_3`), not a JSON array (UX-F-30; `lib/links.ts`'s `runsParam`: the array kept only when an id holds a comma); the old array links still read. The live page's "ingest open" chip reads "accepting OTLP from loopback without a token" and links to Settings, whose ingest fact says loopback too (UX-A-34). The token wall has a heading, "Sign in to this Studio", and the sidebar keeps Traces, Agents, Playground and Experiments listed, disabled with "needs the token", until it is entered (UX-F-26).
- Studio: runs list, compare and live polish (H9; UX-A-09, UX-A-10, UX-A-16, UX-A-18, UX-A-21). No page scrolls sideways at 1440 or 360 px (`scripts/ux-gate/h9.js` measures it; the run page's stacked tree and drawer sit in one `minmax(0,1fr)` column): the shell's main column is `min-w-0`, a wide table scrolls inside its own frame (positioned, so an `sr-only` cell word does not widen the page), the runs table folds its quieter columns on a narrow window, a run header's long subagent id ellipsizes. The runs toolbar is one line — search, agent, status, "+ filter", the top-level toggle, a "? keys" chip in place of the key hint; the other twelve filters are folded behind "+ filter" (its button counts the set ones, each a chip as before). `/live` draws a subagent under its parent, indented; a run that ended stays a minute (was 30 s), dimmed, and the first load lists the runs that ended in the last minute, so a reload keeps them; the session cell is the session's short id with the public id under it.
- Studio: the run page's default view is the run tree (U8.2; UX-A-02, UX-A-04). `/runs/$id` draws `lib/waterfall.ts`'s rows (`src/lib/run-tree.ts` projects them into the waterfall's): run › step › model call (› attempt) / tool lane › child run, each row's label by the label rule ("model · script", the tool's name — no OTel span names, no per-row service), its gist ("1 tool: ops_manager", "→ 1 call", a reply's or result's first 60 characters; content-off, a tool row's result size from `weft.tool.result_bytes`), its badge (finish reason, error kind, ok), its duration under "ms" with the offset on hover, a step's lanes when it ran calls side by side, and the critical path as an accent under the bars with a legend. A step or call the events have and no span covers is drawn, never left out: "spans not reported yet" while the run goes on, else badged `not_recorded` in its own words (`truncated` when the tree was cut) — `WaterfallRow.untimed`, `UNTIMED_HOLE`. The time scale is linear in the run's size (a chat span's attempts grouped once by parent span, the fold's steps and resumed calls indexed once; `run-long.test.tsx` pins attribute reads per span flat). The views are renamed **waterfall · story · raw** (`?view=` and the `w`/`s`/`r` keys; `e` is gone, `RunView` is `"waterfall" | "story" | "raw"`); "trace" names the `/traces` page only. The switch is `scale: time | order | graph`, written as `?scale=` (the order value round-trips, UX-A-02; `RunSearch.axis` / `RunLinkOptions.axis` are now `scale`; a pre-U8.2 `?axis=time|events` link reads as time|order; the `t` key steps the scale); the dim filters are chips, "dim all but: errors · slow · tools", round-tripping `?dim=errors,slow,tools` (`RunSearch.dim`). With the `spans_tree` capability the page reads `/api/runs/{id}/spans?tree=1` once at load (`spansQuery(id, tree)`, on the waterfall only) and a delegation's child run is one collapsed row under its tool call with "open run ↗"; the run's own spans alone feed the header and the Request pane. A row's detail reads the fold's step, call or run (the story's bodies) where the fold has one, a child row its tool call's, a model call or any other span its span's facts; a time-scale row offers no "replay here" (its extent is milliseconds). The tree is accessible: the axis row (the head, the fold-all buttons, the ms axis) sits outside its `role=tree`, a call row's toggle is a button beside its verbs, the drawer's subagent button's name starts with its visible words (jsdom axe tests over the waterfall tree, the logs view, the compare page and a status-filtered runs list). Old `?sel=` forms resolve: `run`, the root's run-qualified keys, a pre-U8.2 `t:<span id>` (the row that span is behind), a bare `c:<call id>`, a denied call's `c:<step>:<call>` (as `c:resume:<call>`). 999.6 ms reads "1.0s" (`spanMs`).
- Studio, panel: one run-tree row model for both renderers (U8.1; UX-F-18, UX-A-04, UX-A-22, UX-A-23, UX-S2-07). `src/lib/waterfall.ts`'s `waterfallRows()` builds run › step › model call (› attempt) / tool (lane) › child run on a `time` scale (from `/spans`, sub-millisecond digits kept) or an `order` scale (stream positions, for a run without spans, from the same row constructors), each row with its selection key, offset and duration, a gist from the folded events and transcript ("→ 1 call", the first 60 characters of a reply or tool result), its status, tool lanes per step, the critical path and the label rule "model · script"; `dimRows` dims by errors / slow / tools. A child run hangs under the tool span its `invoke_agent` span names as parent (core numbers `call_1` every step, so (parent run, call id) alone is ambiguous); without that span the only child under the tool's (run, call) that starts inside it, never overwritten when two steps repeat a call id. A live delegation (the child's spans before its `invoke_agent` and the parent's tool end) is a running child row under the fold's open call, else under the root, and a running run's in-flight steps and calls are open rows; a nested Generate's run (no `weft.parent.*`, no fold link) is a collapsed child row. String-typed attributes (ClickHouse's fallback map) read: `"true"` for `weft.tool.approved` / `denied` / `pending`, a finish list stored as a JSON string. Span maps are keyed by trace and span id; a child run's `t:` keys, a step's second model call (`s<step>:m2`) and its attempts (`s<step>:m2:a<n>`) are run-qualified and stable; a step's model calls are ordered by start, the step's status and finish from the last. One selection-key scheme (`rowKeys`): the trace page's keys are the run tree's — a chat span `s<step>:m` (was `s<step>`), a denied call `c:resume:<call>` (was `c:<step>:<call>`), the trace's root run bare — and the old `?sel=` forms still resolve. The descendant-span rules (a child's tool by span or by (run, call), a collapsed child's claim) live in `lib/waterfall-tree.ts` (`treeRows`, what Studio's run page builds with); the panel reads a run's own spans and bundles none of it — the same rows for that input. The panel's `spanWindow` / `waterfall` (`src/panel/render.ts`) and its Timeline's inline seq builder are gone: the Timeline draws the model's rows (on a spanless run a step precedes its calls, instead of the order they closed in); `lib/trace.ts`'s `spansFromTimed` / `timeDomain` walk the model's span tree. Studio's waterfall axis reads "ms" (was "MSS") and "events", its time ticks and its right column humanised (7.7s, a duration per row, the offset on hover; the first tick "0", `took` picking its unit on the rounded value — 99.96ms reads "100ms") instead of raw millisecond ranges.
- Panel: the Timeline draws the run tree (U8.3; UX-D-34, UX-F-18). The rows are Studio's run page's, one level deep: the same keys, labels by the label rule (the gist as the label's title; a child run row's label one rule on both scales and surfaces — its agent, from the child's `invoke_agent` span, else its fold, else the run document's `children[].agent` (`waterfallRows`' `agents` input) — else its tool's name), kinds and glyphs (`lib/waterfall.ts`'s `ROW_KIND`, `KIND_ICON`) and tones (`rowTone`, moved from `lib/run-tree.ts`; coloured through `lib/palette.ts`'s `TONE` / `TONE_STUDIO` and a `--weft-result` token equal to Studio's `--ev-result`), indented by depth, the critical path under the bar, the extent (`took`) with the offset as its title, untimed rows in `UNTIMED_PENDING` / `UNTIMED_HOLE`'s words, a step's and a never-finished row's bar at Studio's 60% (`color-mix`). A child run is one row with ⤢ to Studio's run page at that row (`runLink`'s `sel` option: `?sel=r:<child>`); the step rows are buttons on one roving tab stop (`↑`/`↓`; a click, `Enter` or `Space` selects the step in Story); the run row is not clickable. Studio's waterfall rows carry `data-kind` / `data-tone`. `renderWaterfall` is gone.
- Studio: the answer-first drawer on the run page (U10.1; UX-A-03, UX-A-05, UX-A-42, UX-C-23). The detail pane beside the run tree is one drawer for both scales: a kind label (a model call reads MODEL, never TOOL; ATTEMPT, RUN, STEP, CHILD RUN), the verbs drawn with their words — replay from here ▸, edit…, compare… (landing on the step) — not hover-only, and where they are not offered one faint line says why (`lib/replay.ts`'s `replayOffWhy`, `REPLAY_OFF`: "replay needs the playground — <capabilities_off's reason>", "a read token cannot replay", "replay after the run ends", a derived run's stock_spans words); for a step (or its model call, attempt or tool) the answer from `src/lib/summarize.ts` — verdict (the stop and its detail where the run failed at this step, then finish, tokens, latency, first token), sent, replied, tools ran, then; and the lazy tabs Request · Response · Tool I/O · Logs · Raw OTel (`?tab=`, the tabs pattern: `aria-controls` / `aria-labelledby`, roving tabindex, ←/→ wrapping, Home/End; the attrs dump moved to Raw OTel; Logs reads `runs/{id}/logs` on open, filtered to the row's spans; a pre-U10.1 `?d=` reads as Raw OTel). The landing selection is chosen on the rows drawn: the first failed row, else a tool that never finished, else the step a failed run failed at, else step 0 — a model error or a cancel during step N lands on step N (core appends a step's record only after its model call and tools, so `weft.run.steps` N names the open step), a max_steps run and a cancel before a step began on the run's last drawn step (`failedAtStep`, `landStep`), `opt.usage-limit` on step 2 with its verdict; the verdict, the then line, the end card and the header's stopped-by chip and its link name the same step; "— the loop refused step N+1" is said for a guard's stop only (usage_limit, loop_detected, model_retries_exceeded, max_steps). A derived run lands by the same rule and its step's answer reads its spans — finish and tokens from the chat span's gen_ai attributes, its execute_tool spans as the calls (`lib/run-tree.ts`'s `derivedStep`) — what no span carries reading "— a derived run has no event record". "past the playhead" is said only while replaying. "read in the story" opens the story on the selection's step, and a `?sel=` highlights its card there. The A3 hole words are shorter on both trees (`obsdb.HoleNote` / `HoleNoteFor`, `lib/honesty.ts`, `holes.golden.json` and the API goldens; tail_lost's fix is the queue fix).
- Panel: the Story's step reads answer first (U10.2; UX-D-12, UX-D-13's panel half). The selected step — else Studio's landing step: the failed one, else step 0, so a succeeded or running turn reads its first step's answer too — opens with the run page's drawer at panel width: `lib/summarize.ts`'s verdict (`[data-verdict]`, no measured span Studio's drawer does not add) and sent / replied / tools ran / then lines (`[data-answer-line]`), the same words as Studio's drawer (`parity.test.ts`: succeeded, settling, max_steps, a model error, a cancel during and before a step, opt.usage-limit's step 2; a derived turn draws no answer — it hands the steps off), and its Logs and Raw OTel hand off — the step head's `📄 ⤢` (U11.2's `logsLink`) and the answer's `Raw OTel ⤢` open the run page at the step's row (`?sel=s<n>&tab=logs|raw`; no token in the URL). Every `[data-weft-handoff]` link in the turn view opens through the panel token's hand-off (G3) when there is one. A step's request is one line by default (the attempts, the changed marks, the badges) and opens to the prompt, the catalog and the params; params every one the adapter's default are not listed; the "insert a message" lines show on hover or focus. Subagents expand inline at any depth, each read only once opened (a grandchild's row from its parent's document; an expanded child's stream read again while its last page was not terminal); past two levels the indent stops and the row says "depth N" (`lib/waterfall.ts`'s `depthWord`); a child's final words that equal its call's result are drawn once, on the call.
- Studio, panel: phase 8's polish on the run page, run tree and header (U29.1; UX-A-24, UX-A-26, UX-A-27, UX-A-28, UX-A-29, UX-A-30, UX-A-37, UX-A-38, UX-F-15, UX-F-35). The replay bar says "live" only while the run runs; a finished run's reads "end · all N events" and its way back "back to the end" (`lib/summarize.ts` `replayEnd`). A nameless agent is "unnamed agent" (muted, "set weft.Name(…)" on hover) on the run header, the runs list, the run tree, a subagent row and the panel's Timeline (`lib/format.ts` `UNNAMED_AGENT`). The runs list's j/k selection carries `aria-current`. A disabled "open in app" is `aria-disabled` and focusable, its reason read through `aria-describedby`. A 404 is U29.2's one not-found state (no retry); any other failed run or trace read keeps retry and gains the way back to its list (`LoadFailed`, `lib/not-found.ts` `notFoundBack`). A failed tool row on the run tree reads the story's taxonomy ("panic · handler", "timeout", "retry 1/3", a ToolError code — `lib/run-tree.ts` `treeBadge`). Focus starts at the run page's title — the skip link's target there — and the tree's row chevrons leave the tab order (the tree is one stop). The story has "collapse all steps" and "only problems" (dims the steps that went smoothly; `stepHasProblem`). Delegation words: the header lists "child runs", the delete confirm counts "child runs", the runs list's toggle reads "with child runs" (`lib/delegation.ts` `RUNS_TREE`) and so does the export's tree toggle, a pool child parks as "a pool session's turn … started by turn" — "delegated" stays cost and usage's word. Under 480 px the run tree's expand / collapse are two icons and the axis two ticks.
- Studio, panel: the polish batch U29.4 (UX-C-36, UX-C-39, UX-C-42, UX-C-44, UX-C-46, UX-C-47, UX-F-13, UX-F-19, UX-F-20). The experiments list says how each experiment went and what it ran: passed ("3 / 4 passed", the top-level runs that succeeded), engine(s), source run and step, last run — `GET /api/experiments` and `/api/experiments/{id}` rows gain `succeeded` and `last_run` (omitted past the summary's walk cap) — and a filter by agent or source run (`lib/experiments.ts`). A matrix's shape is pluralised, "1 variant × 1 input" (`matrixSize`), on the playground's Matrix header and the experiment page. A scripted run carries the panel's chip, "scripted · 0 tokens" (plain "scripted" when its usage is not zero), on the run header and the experiment grid too (`lib/cost.ts` `scriptedChip`; the panel's turn chip reads the same words, was "scripted (0 tokens)"). With the replay drawer open the story rings the step it replays from ("replaying from here", scrolled to) and marks the steps before it "kept", drawn faint (`lib/replay.ts` `replayMarkOf`). The playground's transcript-edit fields show the recorded value as a muted "recorded: …" line, never as the placeholder, and an "edited" chip on a field holding a draft. A read token's replay line names the token that may act ("… it needs the dev token or a playground panel token"). The panel's header counts "runs" unless a session scopes it ("1 run", "3 turns"; was "1 turns" for any list). The panel sets prose — a step's text, the prompt, the notes — in a system sans stack (`--weft-font-prose`, a new host token; mono stays for ids, JSON and numbers); its running dot and the pill's "● step" pulse (`@keyframes weft-pulse`; `minify-css.test.ts` parses the stylesheet raw and minified and fails on a parse error or an `animation` with no `@keyframes`). Both surfaces draw a diff in one pair (`lib/palette.ts` `DIFF`): added green, removed red and struck, a changed cell mustard — the panel's prompt diff, result-pane diff and compare values were blue / mustard.
- Studio, panel: the polish batch U29.5 (UX-F-21, UX-F-22, UX-F-32, UX-F-33). A note-toned honesty badge (stripped, redacted, hidden, …) is a neutral outline on both surfaces — Studio's was the accent, the panel's blue; a loss keeps its error outline. The panel header's actions say their word beside the glyph once the dock is 480 px wide ("Studio ⤢", "layout ⇆", "theme ◐"; the glyph and its title below, a container query on the header), and its "raw" button is gone — `r` and the Raw tab remain. A turn row's accessible name is its fields in words ("turn 3, parked, <title>, 2 steps, 60 in 30 out, 3h ago"). With no turn the header's tokens read "—" (not "0→0"), no "select a turn" is drawn, and the dev list says "no runs yet — connect your app (otel.Install)" (was "no runs yet (dev)"). The raw events explorer's row toggle is a button beside the row's jump and copy verbs, not a `role=button` row around them (axe `nested-interactive`, on the Raw OTel tab and the raw view).
- Panel: 90,091 B gzipped at this release (82,608 B at 0.16.0; the cap is 96 KiB) — the shared row model, the run cards' words and the answer-first drawer are now in panel.js (`studio/web/panel-budget.json` has the per-item ledger). Studio's dist budget is 700 KiB gzipped (was 600; ADR 0018 §7 amended 2026-10-16 — phase 8's run-screen items; the panel.js copy counts here and under its own cap).

#### Breaking

- **Breaking (record):** `weft.override.params` on `invoke_agent` is the run's `Params` replacement by the request record's lower-case names, with `null` for each knob the agent set and the replacement leaves unset — it was the Go struct's JSON (`{"Temperature":0.4,…,"MaxTokens":null}`); a params override's `weft.override.hash` changes with it (U25; UX-S5b-03). Studio reads both stored shapes.
- **Breaking (API):** `GET /api/runs?status=` with a word outside running, succeeded, failed, interrupted, parked and cancelled is a 400 naming them, as `/api/search` already was — it listed nothing before (I3, issue 003).
- **Breaking (UI links):** playground share links and drafts made before 0.17.0 (form v1, with the matrix's own "from step") are not read; the page says so and how to re-create them from the experiment (U26; UX-C-21).

### Fixed

- Studio: a panel token's run document listed child runs of other public ids (P8-scope-fix) — in 0.16.0 `GET /api/runs/{id}`'s `children[]`, and so the export's run block, carried every direct child's row (its id, public id, prompt preview, holes) to a token scoped to the parent's public id, and every row's delegated cost priced them. Now one rule (`inScope`: the child row carries the token's public id — the tree scope's) cuts them at the coster every route prices with: the token reads its public id's children alone — in `children[]`, a step's `children` and its calls' `child_run_id`, every `children_count` / `children_failed` (runs list, search, a session's turns, the live lane's run frames) — and the rest as one `hidden` hole with a `count` on the run document's and the step's `holes` (drawn "hidden by your token scope · 2 child runs" on both surfaces, `StepHole.count`; the reason in the right number, "1 child run carries …"), never named; the run's own cost still takes their usage out, its delegated figure reads not known with the reason counting them; `/compare`'s system cell is hidden only by the system prompt's own hole (`systemHidden`). The server token and setup A are unchanged (`TestAuthMatrixChildScope`: read and playground tokens over a pub_a run with pub_b, cleared and pool-session children and a pub_b grandchild, across the run document, `/api/runs`, `?all=1`, `?parent=`, search, the session, step 0, the diff, the json/jsonl exports with and without `tree=1`, and a live run frame).
- Studio: the runs list is no longer N+1 (U9.1; UX-E-24): a page's subagent trees (runs list, search, an experiment's runs, a run document's and a step's children) are read for the own/delegated cost split one `ChildRuns` read per tree level instead of a `COUNT` + page per row, at most 10,000 child rows a request (past it the affected runs' cost and counts read not known, never read one by one); the amounts are unchanged, and the delegated reasons and unpriced models list a run's children oldest-first on the runs list too, as the run page did, so a row and its run document say the same words.
- obsdb/sqlite: `RunSpans` uses the partial index `spans_run` (UX-E-22): its read states the index's `run_id <> ''` beside the equality, without which every waterfall scanned the whole spans table.
- Studio: `g r` / `g a` work on every page, as the keys help said (P8-pre-web; in 0.16.0 only on the runs list): a shell-level chord (`lib/keys.ts`'s `goChord`), whose `r` does not toggle the run page's raw view.

## 0.16.0 — 2026-10-13

### Added

- core, thread, runtime, obsdb, Studio: approvals linked (U13.1; UX-E-17). `core.ResumeOf(runID)`, a new run option, names the parked run a resume continues: its `invoke_agent` span and `run_start` record carry `weft.resumed_from`, and every run fed decisions carries `weft.resume.calls` — each parked call as `<call id>=approved|denied|resolved|no_decision`, in call order. A call the resume denies (`Deny`, or no decision) gets a zero-length `execute_tool` span marked `weft.tool.denied` / `weft.tool.denied_kind` (`error.type` `DENIED`, never the reason); the root's `weft.run.denied` still counts it and the span is not counted again. The decider rides as metadata `weft.approval.actor`. The core cannot infer the link (a transcript carries no run id): `thread` passes it on every resume turn with the decisions' actor (a signed decision's key id, else `Who`; `""` on every other turn), `weft/runtime` on the playground's resume with the command's actor (`panel:<public id>`, `server`, `local`); an app resuming a plain `Generate` passes `weft.ResumeOf(res.ID)`. obsdb derives `RunRow.ResumedFrom` (SQLite migration 0018's `runs.resumed_from`, ClickHouse 0019's `weft_runs.ResumedFrom`, restating both run views with the four new contract keys) and `RunRow.ResumedBy` on the parked run — read time on both backends, so a resumer ingested before or after its parked run links it; Studio's run row and document serve `resumed_from` / `resumed_by` (omitted when none), the seam U1's status word reads. Model-visible behaviour, the transcript and the approval boundary are unchanged. Contract fixtures: `approval_denied` (now a `Deny` and an undecided call, with `ResumeOf`) and `resumed` (rw.support-desk's shape: `Approve` + `Resolve` + `ResumeOf` + the actor). Since U13.1-fix: `resumed_by` is the latest resumer by start time (a tie: the larger id), never the largest id; `weft.resume.calls` is capped at 32 calls (`,…+k` beyond); `thread` links any run that applies recorded decisions, and appends each step's messages before its own reporting, so a panicking tracer no longer delays a step's durable append to the turn's end; `weft.approval.actor` is metadata and survives content-off — put an opaque id in `Decision.Who` / `KeyID`, never an email.
- core: what stopped a run is on its record (U3.1; UX-E-11, UX-A-08, UX-S1-02). The `invoke_agent` span carries `weft.run.stopped_by` and the `run_finish` event `stopped_by` (`RunFinish.StoppedBy`): the fired `StopWhen` condition's manifest name (`has_tool_call:submit`, `step_count_is:2`, `output_submitted`, a custom condition's `String()`, else `custom`), `final_answer`, `parked`, or — on the span alone, a failed run having no `run_finish` — `max_steps`, `usage_limit`, `loop_detected`, `model_retries_exceeded`, `canceled`. ADR 0016 amendment 2026-10-12; `docs/record-contract.md` §3/§4; contract cases `stopped_by` and `canceled`.
- core: `CancelCause`, a typed cancellation cause (UX-E-41): a run canceled through `context.WithCancelCause` with a cause that has a `Kind() string` records it as `weft.cancel.cause` on its `invoke_agent` span (`deadline` for a deadline). `thread` fells a turn with `thread.interrupted` (an `Interrupt`/`Rollback` send) or `thread.closed` (a `Close` that gave up); `runtime`'s cancel command with `studio.cancel`, and its link's own shutdown (`runtime.Install`'s shutdown func) the runs still in flight with `app.shutdown`. The run's error is unchanged (`ctx.Err()`).
- obsdb, Studio, `weft runs`: what stopped a run is on its row (U3.2; UX-E-11, UX-E-41, UX-A-08, UX-S1-02). `RunRow.StoppedBy` / `CancelCause`, columns `stopped_by` / `cancel_cause` derived at ingest from the `invoke_agent` span and `run_finish` (the larger when they differ) and backfilled (SQLite migration 0010, ClickHouse 0011, which restates both run views); `RunQuery.StoppedBy` and `?stopped_by=` on `/api/runs` and `/api/search` (exact, or a prefix ending in `:` — `has_tool_call:`); the run row and run document carry `stopped_by` / `cancel_cause` (omitted when empty); `weft runs` prints a STOPPED BY column and `--json` carries both. Studio's run header shows a "stopped by …" chip on every status, linking the step where it fired, with the story's end card mirroring it; the stop reason reads "last step stopped on" only where it contradicts; "ended without run_finish" only when neither a terminal event nor a `stopped_by` exists and the run is not failed (a failed run that named no stop — a model error — ends on "■ failed · <err>", its Error root span the terminal record); the chip's detail is the error's own (the usage limit's counts, a loop's "N identical steps in a row", a retries breach's `tool "x" retried N times`; a cancel shows its cause only); the runs table's line is skipped for the default exit (a succeeded `final_answer`); the panel's turn chips carry the same words (`src/lib/stopped-by.ts`). Panel +502 B when it landed (the ledger's U3.2 row; 78,912 B after U3-fix and U2-fix). Record contract §6.

- thread, obsdb, Studio, panel, `weft runs`: titles, not ids (U6; UX-A-11, UX-S5a-09, UX-B-17, UX-D-10, UX-A-25, UX-B-25). `thread` stamps the session's title (`Session.Title`, set through `SetInfo`) on every later turn as run metadata `weft.session.title` — a label, so it survives a content-off destination. `obsdb.SessionRow.Title` (the newest turn's non-empty title) and `FirstPrompt` (the first turn's prompt preview), `obsdb.SessionTitleKey`, and `SessionQuery.Q` (every term in the title, the first turn's whole prompt, the id, public id or agent; both backends, no migration); `/api/sessions` rows and documents carry `title` / `first_prompt` and take `?q=` (the sessions page's text box is now server-side). Studio leads every run row, session turn ("t3" + prompt, "resume of t1 · 1 decided call" for a resume — the previous turn parked and this one carries no new prompt), session row, ⌘K item, run page heading ("agent · prompt") and browser tab (`director · How many units… · succeeded · weft studio`; a session `Acme support desk · 9 turns`) with its title — `src/lib/title.ts`'s `runTitle` / `sessionTitle`: the prompt, else the agent, else "(content not captured)" for a content-off run, else the id — the id a copyable mono line under it. The panel's turn row reads "t3 <prompt>", its narrow picker "● parked t3 How many units… · 2 steps", ids in the title; its filter reads each row's `prompt_preview` (the per-turn prompt memo is gone). `weft runs` prints a PROMPT column (40 runes, "…") before STOPPED BY. Record contract §2/§6; contract case `session_title`, and the fixtures' `sessions`. Panel +323 B (79,347 B).
- core, otel, obsdb, Studio, `weft doctor`: emitted counts and drop accounting (U5; UX-E-04, UX-E-05, UX-E-38, UX-A-41). The `invoke_agent` span carries `weft.run.events` and `weft.run.messages` at its end — the durable event records and growth messages records the run handed to its LoggerProvider, 0 with no logger. obsdb derives `events_emitted` / `messages_emitted` on the run row (`RunRow.EventsEmitted` / `MessagesEmitted`; SQLite migration 0012, ClickHouse 0013 restating the traces view; both backfilled) and the readers count the loss: fewer events stored than emitted is `gap`, cause `events_lost` — "N of M events were lost", split into positions missing below the last stored one and "the last K events were lost" (`obsdb.EventsLostHole`, `EventsLostReason`; through `TailLostHole`, any status); a root that emitted records none of which was stored reads the same gap ("every one"), and `not_recorded`/`no_logger` is reserved for a root that emitted none. `tail_lost` stays for roots without the count. The cause of the stress runs' silent loss, proven by `otel.TestSDKBatchQueueDropsOldestSilently`: the SDK's log BatchProcessor overwrites its oldest queued record when full (run_start first) and tells nobody. Each batch destination now admits its records ahead of the SDK queue (`otel/admission.go`): new `otel.QueueSize(n)` (Studio's default 16,384, others 2,048) and dev-only `otel.Blocking()` (refused outside `WEFT_ENV=dev` with one WARN, like `RawBodies`); a full queue sheds deltas first, then ordinary records, `run_start`/`run_finish`/the root span/heartbeats last; every refusal is counted, warned once per destination ("weft/otel: destination queue full", naming the destination and the knob) and carried as `weft.records.dropped` on that destination's heartbeats. Studio keeps the largest value per service (`/api/meta`'s `ingest.dropped`, the Settings page's "ingest health", `weft doctor`'s `ingest` line); the run row serves `events_emitted` / `messages_emitted`; every gap's fix names the knob. The Local destination writes synchronously and loses nothing (`TestLocalFloodLosesNothing`). Record contract §3/§4/§6/§7; contract fixtures carry the counts (`tracer_only`: emitted 0 → `no_logger`; `tail_lost`: 4 of 8 → `events_lost`).
- core, mcp, obsdb, Studio, panel: error kinds and problem counters (U4; UX-S1-01, UX-E-26, UX-E-12, UX-E-13, UX-S2-05, UX-A-40). The `execute_tool` span types a tool failure — `error.type` `panic` with `weft.panic.layer` `handler` | `middleware` (was `tool_error`), `mcp_transport` / `mcp_tool_error` from `weft/mcp` (through an error's `ErrorType()` string, a convention core reads after the context errors), `weft.tool.input_error` `strict` | `schema` | `syntax` beside `INVALID_INPUT` — and `weft.tool.truncated_bytes` when the result cap cut (on the `tool_finish` record too); the `invoke_agent` span counts a resume's denied calls in `weft.run.denied`. The model-visible text is unchanged. The run row gains `error_type` (the root's `error.type`, the context errors read as `cancelled`) and problem counters derived at ingest from the spans as a de-duplicated token set — `attempts_retried`, `fallbacks`, `tool_errors`, `denied`, `truncated`, `repaired` (always 0 in this release: the core does not report a repair yet, U23.1); the run document adds `log_warn` / `log_error`, counted at read (SQLite 0013, ClickHouse 0015 restating both run views, both backfilled). `?error_type=` on `/api/runs` and `/api/search`. Studio's runs table badges problems only (`↻2`, `⇄`, `✋`, `✗`, `✂`, `[max_steps]`), the run header adds the kind chip (a `cancelled` tone) and the problems beside the step count, the span drawer names a tool's failure kind and cut; the panel's turn row draws the same chips (lib/error-kind.ts, +806 B). Contract cases `tool_problems`, `retry_fallback`, `approval_denied`.

- core, obsdb, Studio, panel: phase 7 polish, batch 2 (U32.2; UX-E-64, UX-S1-07, UX-S1-08, UX-S1-09, UX-S1-10, UX-S1-12, UX-S2-14, UX-S2-15, UX-S4-04, UX-S4-14). SQLite migration 0019 adds `spans_start` (spans by `start_ns`, `trace_id`): the traces list's `since` window narrows to the traces with a span in it before grouping, instead of scanning every span (ClickHouse already prunes it by its `toDate(Timestamp)` partitions: no migration). A `loop_detected` run's repeated step cards carry "repeats step K", linking the first of the N steps `DetectLoops` counted (the error's count, ending at the step it failed at). A tool result whose text is `Repair`'s synthesised `no result recorded: the call was interrupted` is badged "synthesised by Repair" on every request message line — the one surface that can see it: Repair writes the text into a later run's input transcript only, so no `tool_finish` carries it and the story's pills and the panel's call rows never badge it (P7-fix2 dropped those two paths). Matched on the text: the record carries no other signal; recording `weft.messages.repaired` is not done. A failed model attempt reads a phrase for its `error.type` (`stream_idle` "no bytes from the provider within the adapter's idle timeout", `model_requests_denied`, `model_error`, the context errors, `model_contract`; an unknown type as itself, "no description"), the raw type in the hover. The run header's public id and metadata chips link to the runs list filtered by them (`?public_id=`, `?tag=k=v`). Settings names how app logs reach a run (the LoggerProvider; slog needs a bridge — a slog→OTel bridge (`otel.Slog`) is planned; an out-of-loop `Agent.CallTool` records nothing — also in its godoc), and the ingest-health line names the run_start and run_finish carriers. docs/record-contract.md §6 lists what the record attributes to user code today (effects only; the hooks are U23's and U25's). The `metadata_cap` reason names its three limits ("limit 64 keys, 128-byte keys, 1 KiB values"). Not changed, accepted: a server-side `parked` status filter (the client-side pick and the session's state cover it); a live `PUT /api/settings/pricing` (download + restart); ingest health persisted across a restart.

- Studio, panel: one unpriced-cost reason per page (U7; UX-A-12, UX-D-21, UX-S4-07, UX-H-33, UX-B-27, UX-C-17). A scripted model no pricing row matches — every `wefttest` model, the playground's `weft/runtime` scripted engine — is an exact `$0` by a rule in Go (`cost_known` true, `cache` `exact`, new `cost_badge` value `scripted`), drawn "$0 · scripted"; a row that matches still wins. A pricing row with no `provider` (or `"*"`) matches on its model glob alone, any transport (an OpenAI-compatible model behind the openai adapter); a file's missing provider loads as `"*"` instead of failing. A summed figure's reason (delegated, session, experiment) is one line counting its runs — "2 of 3 subagent runs unpriced: no price for openai/x" — never a clause per run; an unpriced run's reads "openai/x has no price". New `unpriced_models` / `delegated_unpriced_models` list the models behind a figure; Studio's runs list, sessions list and run header, and the panel's header, say "N runs unpriced: no row for …" once, beside a "price this model…" link to Settings (`/settings?price=`, a prefilled row). An unpriced cell is "—" with a small "unpriced" chip (the sessions list's a number or "—"); a partial sum reads "≥ $x · k of n runs priced", "—" when its known part is $0 (never "≥ $0.00", never "<" under "≥"). The live `PUT /api/settings/pricing` was not added: the table is still applied by download and restart.

- obsdb, Studio: manifests stored by hash, and indexes for the run lists (U27; UX-E-16, UX-E-23, UX-E-35, UX-E-36). New optional `obsdb.ManifestStore` (`PutManifest`, `Manifest`; `StoredManifest`, `ManifestHash`, `CheckStoredManifest`, `ErrInvalidManifest`), implemented by both backends (SQLite migration 0014, ClickHouse 0016): each agent's one-agent `core.Manifest` document under its sha256 — the `weft.manifest.hash` its runs carry — content-addressed, the first write kept, kept forever (DeleteRun, DeleteSession, Sweep and Vacuum never touch it). Studio writes it from the weft.json it serves (at `New`) and from every runtime registration, and serves `GET /api/manifests/{hash}` (capability `manifests`; `{manifest_hash, agent, source, first_seen, current_manifest_hash, manifest}`; a read-scoped panel token 403 hidden, as `/api/manifest`); the run document carries `manifest_available: true` when its hash resolves. SQLite: the public-id, session and session-list predicates state their partial index's `<> ''` so the planner uses `runs_public` / `runs_session` (each session's newest-turn, title, prompt and content-off subqueries walked every top-level run before); `runs_public` is rebuilt over the list's whole key, `runs_session_started` is new, `runs_started` and `runs_experiment` gain the `run_id` tie-break; `runs_all` serves `all=1`, `runs_last_seen` the live backfill window and the running filter, partial `runs_failed` the failed filter (runWhere adds each status's implied condition beside the CASE); migration 0015 adds partial `runs_failed_all` for the all-runs failed page (`status=failed&all=1`, which walked `runs_all` until the page filled), and the all-runs running page and the live backfill write their order `+started_ns` so `runs_last_seen` finds the few matches instead of `runs_all` walking every row (U27-fix). Cost of the upgrade: migration 0014 rebuilds `runs_public`, `runs_started` and `runs_experiment` and builds four new indexes (`runs_session_started`, `runs_all`, `runs_last_seen`, `runs_failed`; 0015 a fifth, `runs_failed_all`) — a one-time synchronous rebuild inside `Open`, seconds on a large file before Studio serves; and `runs_last_seen` is rewritten on every `last_seen` bump, which is every record batch a run writes — one more index entry per batch. ClickHouse: a run's spans are read by `TraceId = ? AND RunId = ?` when its row names the trace (falling back to `RunId` alone), and `otel_traces` gains the `idx_weft_run_id` bloom filter (materialized in the background). The run-header "config at run time" view is left to U26.

- Studio, panel: one status vocabulary on both surfaces (U1; UX-A-06, UX-B-04, UX-D-16, UX-F-07, UX-S3-03). `src/lib/status.ts` words every run row — running · succeeded · failed · interrupted · parked · cancelled, each with a hint and a colour role — and the runs list, the run header ("parked · 1 call awaiting a decision"), a session's turns and its header (its newest turn's word), the live table, ⌘K, and the panel's turn list, narrow picker and turn filter all draw it. `parked` and `cancelled` are read-time words, never stored (record contract §7): cancelled is failed with `error_type` cancelled (U4's tone folded in), parked is succeeded with pending calls while no later turn of its thread session exists — then it reads "parked → t2", a link to the resume; a plain `Generate` resume has no link yet and reads parked. The panel's "parked" filter returns open parks only and gains `interrupted` and `cancelled`; its host `run` event keeps the plain words. `src/lib/palette.ts`'s `ROLE` gives each status one colour on both surfaces: running is Studio's `--status-run` in the panel too (new `--weft-status-run`; was the interrupted mustard), interrupted the mustard (was the tool blue), parked Studio's new `--status-parked` (the panel's purple), the live dot the succeeded green (was the accent); the panel's chip gains Studio's dot. Panel +589 B (81,716 B).

- core, otel, obsdb, Studio, panel: phase 7 polish, batch 1 (U32.1; UX-S2-10, UX-S2-11, UX-S4-09, UX-S4-15, UX-E-42, UX-E-48, UX-E-50, UX-E-51, UX-E-58). A RETRY the loop counts carries `weft.tool.retry` (the tool's consecutive count with it) and `weft.tool.retry_budget` (`MaxModelRetries`) on its `execute_tool` span, and the budget's error names both (`tool "x" retried 3 times, past MaxModelRetries(2)`; the model-visible `RETRY: …` text is unchanged); Studio's story pill reads "retry 1/2", "retry 3/2 — budget spent", and "strict input" for a `StrictInput` refusal, from the call's span; `model_retries_exceeded` reads "tool retry budget spent". The `invoke_agent` span carries `weft.tap.panics` — the run's contained observer panics, beside `Agent.TapPanics` — counted as the run row's `tap_panics` problem ("⚠N tap panics"). The prompt record's body gains `segments` (`instructions`, then `tool:<name>` per snippet; content-gated, redacted per segment, dropped under a cap that cut the text), drawn under labels in Studio's Request pane (`obsdb.PromptRecord.Segments`, `obsdb.PromptSegment`; breaking for a caller comparing `PromptRecord` values with `==`: the struct is no longer comparable). `weft.metadata.dropped` becomes the run document's `truncated` hole, cause `metadata_cap` ("N metadata pairs dropped (limit 64 keys / 1 KiB values)"). New problem tokens `tap_panics:` / `metadata_dropped:` (SQLite migration 0017 backfills the second; ClickHouse 0018 restates both run views with the three new keys in the meta tuple and backfills). The kill switch's kind chip reads "denied by the kill switch (WEFT_MODEL_REQUESTS)"; Settings › Token names `weft mcp --url <this Studio>` and `docs/mcp-recipe.md`; the run header shows the tool concurrency ("parallelism: 2" / "sequential") as the run ran: its `weft.override.parallelism`, else the request record's `sequential_tools`, else the run's stored manifest; the Wire tab is labelled "loop request → wire bytes".

### Fixed

- cmd/weft, obsdb, Studio, panel, `weft doctor`, docs: the pre-release review of the Go side (R7-prep). `weft runs` and `weft doctor` print every free-text cell — the prompt preview, agent names, stop causes, ids, Studio's error text, service names — with each control character (an escape sequence, a carriage return, C1 and bidi controls) as a space (`internal/termsafe`): a prompt or an OTLP-ingested record no longer drives the operator's terminal. A content-on run that lost some of its transcript batches badges `gap`, cause `messages_lost` — "K of N transcript batches were lost" (`obsdb.MessagesLostHole`, `MessagesLostReason`, `obsdb.CauseMessagesLost`; `lib/honesty.ts`), joined to an `events_lost` gap on the one badge; a store holding no messages record is never called lost (a content-off destination stores none; record contract §7 says what that cannot tell apart); contract case `messages_lost`, and `tail_lost` gains the cause. A failed root never settles: its row reads `settling` false while its event page reads done, one answer from the first read (`DeriveSettling` is passed `rootEnded && !failed` on both backends). `weft doctor`'s ingest fix names the queue and failing exports ("queue full or exports failing — the app's WARN names which"). Record contract §10 lists `session_title`, and a test holds §10's rows to `obsdb/testdata/contract` both ways. Godoc examples for `otel.QueueSize` and `otel.Blocking` (its dev-only gate). Panel +41 B (82,373 B, measured on landing over P7-fix3).

- obsdb, Studio, panel, docs: the phase 7 gate's dry run (P7-fix3). The run header's tool-errors entry names the failed calls' kinds from the run's loaded `execute_tool` spans, in lib/error-kind.ts's words — "✗3 3 tool errors: tool error ×1, timeout ×1, pending child ×1" (`SUBAGENT_PENDING` reads "pending child") — the count alone while the spans are not loaded, and "N not in the loaded spans" for calls the counter holds and the spans lack; the panel's turn chips stay counts. The panel's in-place edit fields (plan F2: the prompt, the insert boundaries, a reply, args, a result) take their visible text as their accessible name, "edit …" as the title (axe's `label-content-name-mismatch`); an empty field keeps the aria-label. The a11y budget gains the fields at rest with the rule checked by hand (jsdom leaves it incomplete). A `records_dropped` contract case pins `weft.records.dropped` on `run_start` / `run_finish`: out of `meta`, no row field (record contract §10). The concurrency hedge reads "…read when its spans are available" (a run whose spans never load was promised a read).

- thread, obsdb, Studio, panel, docs: the review of P7-fix, U32.2 and U13.1-fix (P7-fix2). The run header's tool-concurrency chip names the manifest's value as that — "the manifest's value; the run's own setting is read once its spans load" — until the run's own `invoke_agent` span is read (a running run, or a token without the requests capability, never reads its `weft.override.parallelism`); once it is, "the run's manifest". The Repair badge is drawn on the request message lines only (see U32.2 below). A failed attempt's `model_requests_denied` reads the run chip's own words ("denied by the kill switch (WEFT_MODEL_REQUESTS)") and `unsupported` reads "the model cannot honour part of this request (ErrUnsupported)". The `mw` godoc rate-limit example pulls a past slot up to now, so idle time is never banked into a burst. A decided park whose resumer is a thread turn not in the loaded runs reads `t<n>` from its `<session>-t<n>` id instead of a cut that loses it. Comments only: the approval link's "any turn the decisions ride" path is marked defensive (today only resume turns carry decisions, ADR 0021 §1); SQLite 0018 / ClickHouse 0019 say the reader's rule (the latest resumer by start time, a tie: the larger id) — both runners track migrations by version, no content hash, and ClickHouse's skips `--` lines, so editing them changes no applied database. The approval-actor fixtures use an opaque id (`op_42`), as contract §2 asks.

- core, obsdb, Studio, panel: the combined review of U27-fix, U4-fix, U32.1 and U1-fix (P7-fix). `weft.tool.retry` is assigned after the step's tool batch, in call order, exactly as the guard counts — a RETRY in a batch that also holds a success of the same tool carries no mark (the count resets; it no longer reads "budget spent" on a run that goes on), and parallel RETRYs of one tool are numbered by call order, not completion order; the mark is on the `execute_tool` span only (its span ends after the batch, stamped with the call's own end), no longer on the `tool_finish` record, which is written before the batch is over. The run header's concurrency is the run's own (its `weft.override.parallelism`, then `sequential_tools`, then the manifest; "parallelism: 4" no longer shows for a run a `Parallelism(1)` run option made sequential). The `metadata_cap` hole's fix names all three limits ("64 keys, 128-byte keys, 1 KiB values"). ClickHouse 0018 has an upgrade test. The runs list under a loaded-rows filter (the `parked` pick, a duration) heads "N listed of M loaded", never the server's "matching" total; the parked chip says a resume that did not succeed is not looked for. SQLite's `runOrder` no longer applies under a public-id or session equality. The step route's attempts carry `layer` (`weft.attempt.layer`), and both renderers' attempts lists read "attempt 2 · fallback" (`src/lib/attempts.ts`'s `attemptLabel`).
- Studio, panel: the U1 review. "awaiting a decision" is said only where the resume was looked for — a session turn read against its session's whole turn list (the session page, the run header once its session loaded, the panel's session-scoped list); a plain `Generate`, a runs page under a filter or sort, the live table and an experiment's grid read "N calls pending · no resume recorded here" (a plain `Generate`: "a resume, if any, is not linked yet"), the word staying parked. `statusOf` reads a row's `resumed_by` first (U13.1's stored link, served on the run row). The runs list's status filter gains `parked` (succeeded asked of the API, the loaded rows kept where the word reads parked, its chip says so); the live table's filter matches status words on the key; the panel's filter gains "parked → (decided)". The experiment grid, a subagent's status, the flow strip's end mark and the live table's cancel toast and parked cell draw `statusOf`'s word and role (a parked or cancelled run no longer reads succeeded / failed). The running word is drawn in a new `--status-run-ink` (`#d13c10` light, 4.61:1 on paper; dark unchanged at 6.07:1) — the dot, pulse and pill border keep `--status-run`; the panel's theme test holds the word to 4.5:1 again. The `[cancelled]` kind chip is dropped where the status word already says cancelled. The panel's decided-park word selects its resume turn on click. Record contract §7 no longer spells a key no code writes (`TestRecordContractDocNames` passes) and says the next turn decided or dropped the calls. Panel +170 B (81,909 B).
- core, obsdb, Studio, panel, `weft runs`: the U4 review. `attempts_retried` counts only a failed attempt a later attempt of the same model call followed (an exhausted three-try `mw.Retry` reads 2, a non-retryable failure 0; the words "model attempts retried"); `fallbacks` counts the attempts `mw.Fallback` marks — `core.AttemptInfo` gains `Layer` (`core.AttemptLayerFallback`), the `attempt` span `weft.attempt.layer` — never a model-name comparison; `RETRY` (`ModelRetry`, the repair loop) is no tool error (`NO_SUCH_TOOL` still is); the denied-approval token is keyed by its `invoke_agent` span, so two roots of one run id with the same count both count. The problem set is re-derived from the stored spans (SQLite 0016, ClickHouse 0017 restating the traces view and backfilling; U4's token kinds count nowhere). A result-cap cut is forgotten at its `tool_finish` with no logger wired (it stayed until run end), and a middleware that recovers a handler's panic and later panics reads `weft.panic.layer` `middleware`. The run document's `log_warn` / `log_error` come from one severity-narrowed scan (`obsdb.DB.CountLogs`, both backends) instead of up to ten paged reads, `logs_capped` marking floors ("≥" on both surfaces). Breaking for an `obsdb.DB` implemented outside the two shipped backends: the interface gains `CountLogs` (`obsdb.CountOtherLogs` is the shared body over a severity-narrowed `LogCandidates`). Record contract §3 says which denials `weft.run.denied` counts.
- otel, obsdb, Studio, panel: the U5-fix and U7 review. A scripted parent whose subagents bill is no longer "$0 · scripted" where runs are summed (the panel's result pane and header, the experiment grid and totals, the playground): a run's whole bill must be $0 for the scripted words, as Go's set rule already required. A partial sum whose known part is under $0.0001 reads "> $0 · k of n priced" (the exact figure in its title), never a "≥ $0.0001" above the real figure; a paged sum whose loaded rows are all known at $0 reads "$0 · scripted" or "$0 · k of n priced", never "—". A session or experiment made only of the playground's scripted-engine runs (zero usage) is badged scripted, not a bare "$0.00". A session's or experiment's cost reason names no turn id ("1 of 3 turns' cost not known yet (<reason>)"; a delegated reason still names the child). The built-in $0 rule covers `wefttest/*` and `weft/runtime` + `scripted` only: `weftarena/*`, a wrapper that may wrap any model, is unpriced without a row of its own. A Go-built `studio.Pricing` row with no `Provider` is the any-provider row, as a file's missing provider loads. `unpriced_models`, `delegated_unpriced_models` and a reason's model list stop at five, a sixth and more one last element `"(+n)"`. An unpriced run's reason is the model alone, "openai/x has no price". The `?price=` prefill escapes `*`, `?`, `[` and `\` in a model name, so the row prices that name alone. The span chain's Blocking latch now sets on the first timed-out wait (a context deadline of the same length fired first and skipped the latch, so a hung exporter cost a wait per span until the timer won), and the latch's first setting logs one WARN per destination: "destination <name>: exporter stalled — records are refused until it recovers (Blocking)". ClickHouse: migration 0014 restates the logs run view with `weft.records.dropped` in its contract tuple, so stored `weft_runs.Meta` no longer keeps it (reads were already right); `obsdb.NonMetaKeys` lists MetaOf's exclusion set, and a test holds the ClickHouse copy to it whole.
- otel, Studio, `weft doctor`: the U5 review. The span batch queue is built eight places above admission's size, so a `ForceFlush` marker under a full queue no longer makes the SDK drop an admitted span (likely the run's root) silently and leak its admission place for good. `weft.records.dropped` also rides each run's `run_start` and `run_finish` records (record contract §4), not only heartbeats of runs open 10 s, and Studio's ingest health reads it from them; the key is not caller metadata. `otel.Blocking()` is bounded per stall: after one timed-out wait the chain refuses at once (counted) until an export begins, and a span's End waits under its own 5 s deadline. A detached flush ends when its chain shuts down. `/api/meta`'s `ingest` is the dev token's and setup A's loopback only (`manifest_check`'s rule), omitted for a panel token. The ingest max stays per service: the pipeline's resource names no process instance.
- panel: a thread resume across a gap in turn numbers (a queued turn's number dropped by `ClearQueue`) is titled "t5 resume of t3": the turn before is the highest loaded turn below, as Studio's session page reads it (U6 re-review c1).
- obsdb, Studio, panel: the U6 review. The run page's heading of a content-off run of a named agent says the agent once (`director · (content not captured)`, not `director · director · …`). ⌘K finds a session by its title or first prompt and a run by its prompt (cmdk keywords), the `search` capability or not. A session row carries `content_off` (`obsdb.SessionRow.ContentOff`, both backends, no migration: the first turn stored events and no `messages` record, not derived), so the sessions list titles an untitled content-off session "(content not captured)" instead of its id; an id-titled row no longer repeats the id under it. The panel's turn row and narrow picker title a thread resume with `turnTitle` as Studio's session page does ("resume of t3 · 1 decided call"); the picker adds the turn's duration and reads an experiment in its card's order (`↳ replay of t3 · scripted · ● failed · 1 steps · 2.5s`). Record contract §2 (`weft.session.title` across Fork, Branch and the compaction marker) and §6 (`content_off`); contract case `session_title`. Panel +218 B (79,565 B).
- Studio: a settling run raises no counter gap on the server (its missing positions may still land) and none on the client (`statusHoles` reads `isLive`); a settling run's story foot reads "stream in progress…", never "the stream ended without run_finish"; a failed run's last step stays `error` under the counted gap (the U2 re-review's carried items).
- Studio: the settling window reaches the UI (the U2 re-review). The run row and run document serve `settling` (omitted when false); the run page's polls and event tail, and the panel's turn, follow a settling run as a running one (`isLive` in `src/lib/honesty.ts`) and draw a `settling` note beside the status word, so a run_finish or a `tail_lost` gap landing after the root shows without a reload. A failed spans-only root's steps read `error` at once, never `running`; a settling step names no request gap in its Request and Messages-in panes; a run with a dropped mid-run batch and a lost tail says both reasons on its one `gap` badge. `stopped_by: step_count_is:` with no argument reads "stopped by a step count (StopWhen)". Record contract §6/§7.
- obsdb, Studio: the U2 review (status from the root span). A succeeded root with events stored and no `run_finish` badges `gap`, cause `tail_lost` ("the run's root span ended but its run_finish record never arrived: a destination dropped the record tail, so the last steps' records are missing") on the run and on the step whose `step_finish` was dropped (that step reads ok, not error) — no longer a clean success (`obsdb.TailLostHole`, `RunRow.FinishRecorded`; fixture `tail_lost`'s holes). A root stored before the records behind it settles for 30 s (`RunRow.Settling`, `obsdb.DeriveSettling`): no hole calls the records lost and the events route keeps `done` false while it does. `EventPage.Done` is true for a root-ended run once it stopped settling, so a poll that waits for it ends. A spans-only run's step documents and requests/tools panes read `not_recorded`, cause `no_logger`, not a step gap or an empty list (`RunRow.RequestsHoleCause`). `weft_version` comes from the `run_start` record and the root span alone (its attribute, else its resource), the larger string when they differ, on both backends — never another span's resource (ClickHouse migration 0012 restates the traces view). Derived runs all read `root_ended` (SQLite 0011, ClickHouse 0012). Record contract §6/§7.
- obsdb: `weft.run.stopped_by` and `weft.cancel.cause` are contract keys, no longer caller metadata in the run row's `meta` (the U3.1 leak; on ClickHouse `max(Meta)` also lost them, failing `TestContractClickHouse/tool_calls`). SQLite's 0010 removes them from stored rows; ClickHouse's reader drops them through `obsdb.MetaOf`.
- obsdb: a run whose `invoke_agent` span is stored is over — `succeeded` (or `failed` on an `Error` root) without a `run_finish`, never `interrupted` (UX-E-01). A tracer-only app's runs and runs whose record tail a destination dropped read the crash signal 30 s after they ended. New column `root_ended`, set at span ingest and backfilled from the stored spans (SQLite migration 0009, ClickHouse 0010; `RunRow.RootEnded`); the status filter, sessions and the sweep read the same rule. The run row's `weft_version` and `manifest_hash` now fill from the span (the version also from its resource) when no record names them (UX-E-02). Record contract §6 and §7, fixtures `tracer_only`, `tracer_only_failed`, `tail_lost`; ADR 0024 amended.
- Studio: a weft run with spans and no stored record badges `not_recorded`, cause `no_logger` ("the app exported spans only", the fix naming `weft.LoggerProvider` / `otel.Install`) instead of `derived`, which is the stock-span run's badge alone (UX-E-03; `obsdb.LostRecordsHole`, `obsdb.CauseNoLogger`, `lib/honesty.ts`). Panel +103 B (78,201 B).

### Changed

- Devtools panel: the panel's size cap is 96 KiB (98,304 B), up from 80 KiB (audit decision 1); every delta still appends a ledger row. The ledger's `cap` (`studio/web/panel-budget.json`) is now the one source the build (`vite.panel.config.ts`) and `scripts/panel-asset.ts` fail on — the build still fails over it; one row, "A5.4 cap raised to 96 KiB", records the change at the then unchanged 78,098 B (20,206 B of headroom at the cap change; the ledger's rows since carry every delta).
- Studio, panel: the pre-release review of the web side (W7-prep), UI words only. The problem badges (Studio's and the panel's turn chips) carry `role="img"` and their words as the accessible name. The problems chip draws no "parked" part for a decided park: its pending calls read "parked" only under the parked status word, "held" under any other. The resume fallback reads "next turn tN (decided or dropped)" — it no longer claims decided. The run header's concurrency chip, read from the manifest, says so in its visible text ("parallelism: 4 (manifest)"), not only in its hover. Step counts read "1 step" (one `stepCount` helper). An unpriced-runs line over a paged list adds "· the loaded rows". "(content not captured)" (`contentOff`) is gated on `isLive` and on a gap / not_recorded hole. Beside a cancelled status word the stop chip reads the cause alone. A tool failure of `context.DeadlineExceeded` reads "deadline". The kind chip reads "[retry budget spent]". The unused `statsStrip` helper is removed. The panel's subagent row and the playground's compare table take their status words through `statusOf`. Panel +176 B (82,549 B).
- Studio, panel: the pre-tag re-review (D7). Beside a cancelled status word, a stop chip with no recorded cause reads the step it fired at ("· step N"), so the run header keeps its link to that step. "(content not captured)" no longer shows for a list row whose events were lost (`event_count` below `events_emitted`). A session row's `content_off` is gated like the client's — not while the first turn is running or settling — on both backends' session list and document and in a panel token's scoped document, through one rule, `obsdb.SessionContentOff` (new; its Status and Settling derived by `DeriveStatus` / `DeriveSettling`); conformance case in `SessionTitles`. The approvals block uses the shared `AWAITING` words. A test holds `studio/README.md`'s panel figures to the ledger's last row. Panel +59 B (82,608 B).

#### Breaking

- **Breaking (record):** an `execute_tool` span's `error.type` for a contained panic is `panic` (with `weft.panic.layer`), and for `weft/mcp` failures `mcp_transport` / `mcp_tool_error` — all three were `tool_error`. A query or alert keyed on `tool_error` misses them (U4).
- **Breaking (reader):** a tracer-only app's runs, and runs whose record tail a destination dropped, read `succeeded` / `failed` from their stored `invoke_agent` span, no longer `interrupted`; `?status=interrupted` / `?status=succeeded` / `?status=failed` results change accordingly (UX-E-01).
- **Breaking (API):** a weft run with spans and no stored record badges `not_recorded` / cause `no_logger` (or `gap` / `events_lost` when its root counted records), no longer `derived` — `derived` is a stock-span run's badge alone (UX-E-03).
- **Breaking (Go):** the `obsdb.DB` interface gains `CountLogs` (U4): an implementation outside this module must add it.
- **Breaking (Go):** `obsdb.PromptRecord` carries `Segments` (a slice) and is no longer comparable: `==` on it, or its use as a map key, no longer compiles (U32.1).
- **Breaking (API):** the run row's `meta` no longer carries `weft.run.stopped_by` or `weft.cancel.cause` — they are contract keys, served as `stopped_by` / `cancel_cause` (U3.1 fix).
- **Breaking (API values):** `cost_badge` gains the value `scripted` (an exact $0 by rule for `wefttest/*` and the `weft/runtime` scripted engine); `cost_reason` texts changed (one reason per page, "openai/x has no price"); `weftarena/*` gets no $0 rule and stays unpriced without a row of its own (U7).
- **Breaking (Go):** an `INVALID_INPUT` tool error's `ToolError.Err` is now an unexported `*inputError` wrapping `ErrInvalidToolInput`, no longer the sentinel itself: `errors.Is(err, ErrInvalidToolInput)` and the model-visible text are unchanged, but `te.Err == ErrInvalidToolInput` is false (U4).
- **Breaking (CLI):** the `weft runs` table gains the PROMPT and STOPPED BY columns after STEPS: a script that splits rows by position must move to `weft runs --json` (U3.2, U6).
- **Changed:** the `ErrModelRetriesExceeded` run error's text adds the budget — `tool "x" retried 3 times, past MaxModelRetries(2)` (was `tool "x" retried 3 times`); `errors.Is` on the sentinel is unchanged (U32.1).

## 0.15.1 — 2026-10-12

### Fixed

- studio: a panel token opens every session its list shows — the
  session view (`GET /api/sessions/{id}`) and the live stream's
  `session=` selector admit it when any of the session's turns carries
  its public id (the list's rule), not only when it is the session's
  greatest; the view serves the token's turns alone. A session whose
  turns carry two public ids (hand-set session metadata, a colliding
  app) was listed for both and opened only for one
  (`TestPanelSessionTwoPublicIDsListedAndOpened`).

- obsdb: an identity value sent empty is no value on both backends —
  `MetaOf` drops an empty string (a `weft.session.parent = ""` is not in
  the row's `meta`), and `ResolvePublicID("")` is `ErrNotFound` (it
  resolved the newest session whose turns carried no public id).

- studio (web): the shell's `GET /api/runtimes` poll costs what the page needs (0.15.1 debt): every 5 s only while a surface needs the runtimes now — the playground, the open replay drawer, an experiment's run controls, a parked or running app turn's block (`useRuntimesNeeded`, one shared signal; their own observers still carry no interval) — and otherwise a quiet poll doubling per read that changed nothing, 5 s up to 60 s, back to 5 s on a change in the list, a key or pointer press, or a surface letting go of the runtimes; never while the tab is hidden. "Changed" is the runtimes' state, not the JSON: a connected runtime's `last_seen`, advanced by its stream's 15 s keep-alive, is no change. A quiet page reads it 4 times in two minutes, not 24, whether or not a runtime is connected; a runtime's toast on a quiet page comes up to a minute late.
- studio (web): the run page's parked block no longer flickers `checking` under a failed meta read: the failure is held in the query cache until meta answers (`lib/query.ts`'s `heldError`), so a block mounted again, or a refetch with its second of retry backoff, reads the failure throughout; `checking` only before the first answer. The capability pages' failed-meta line (`useCapabilities`) reads the same held failure.
- studio (web): the touched sources are `prettier`-clean (`src/components/studio`, `src/hooks`, `src/lib`, `src/panel`, `src/routes`, `src/test`; the captured `src/test/derived/*.json` bodies left byte for byte); the panel badge scan in `badges.test.ts` follows a badge call wrapped over three lines. The panel bundle is byte-identical.

### Changed

- core: a metadata pair with an empty value now means "none"
  (`Metadata`, `WithMetadata`; no API change, but a behaviour change):
  it removes the key the run would otherwise inherit from its parent run
  or a `WithMetadata` context, is never stored, never on a span or
  record, and is not counted in `weft.metadata.dropped`. A caller who
  passed `Metadata(map[string]string{"k": ""})` deliberately got an
  empty `k` attribute on every span and record before; now there is no
  `k` at all (and an inherited `k` is removed) — set a non-empty value
  to keep one. Pairs without an empty value inherit exactly as before;
  reporting only, nothing model-visible
  (`TestMetadataEmptyValueRemovesInherited`). At the 64-key cap a run's
  removals apply before its additions, so a slot an empty value frees is
  there for a new key whatever their order
  (`TestMetadataEmptyValueFreesSlotAtCap`).
- thread: a session's run carries its own identity, never an
  enclosing run's: every key a session owns — `weft.public_id`,
  `weft.session.forked_from`, `weft.session.parent`,
  `weft.session.parent_call` — is stamped with the session's value, or
  `""` (none) when it has none. A session created without
  `thread.PublicID` whose `Send` runs inside another session's tool
  handler no longer reads as that session's turn (Studio scoped it to
  the enclosing public id, the panel offered its verbs, and the
  runtime refused them: "the session resolver answered session s for
  public id pub_s, not the run's session s2"); a session's turn sent
  from inside a pool child's turn no longer reads as a pool child. A
  plain `Generate` in the same handler still inherits every pair (it is
  the enclosing turn's work). The session's keys win over a caller's
  `thread.RunOptions(weft.Metadata(...))` as before, now including the
  ones it has no value for: a thread session's public id is
  `thread.PublicID`'s alone.
- obsdb: `SessionQuery.PublicID` scopes the runs before the grouping
  (a `WHERE`, both backends — it was a `HAVING MAX(public_id)` over the
  whole session): a session is listed when one of its turns carries
  the public id, and its turns, usage, first/last seen, agent, status,
  the page's order, `Total` and the cursor are those turns' alone; a
  run of another app sharing the session id moves none of them.
- studio: `GET /api/sessions?public_id=` is that scoped query, for every
  identity, and its row is one scope throughout — turns, usage, first/last
  seen, status and cost (`cost_usd`, `cost_reason`) are that public id's
  runs alone. For the server token the cost was the whole session's
  before (a session whose turns carry two public ids priced both); its
  unfiltered list is unchanged. A panel token's list is always scoped:
  ordering, `total` and `next_before` are the token's view (a newer
  colliding derived run no longer sorts the session or becomes the
  cursor), and a page holds `limit` rows while more match. A row whose
  runs the cost walk cannot read is served with its scoped aggregates
  and an unknown cost (as the server token's is) instead of being
  skipped (`TestServerSessionsPublicIDRowCostScoped`).

## 0.15.0 — 2026-10-12

### Added

- studio (web): the devtools panel shows cost (plan A5.3, under capability `pricing`): each turn's figure beside its tokens with its subagents' as a second figure ("$0.0029 + $0.0011 delegated"), "—" with the API's reason (unpriced, running, no usage) — never $0 — the header's summed cost ("≥ $x" when a turn's is unknown), and the experiment result pane's run cost and "N replays of this turn: $x"; the words are `src/lib/cost.ts`'s, shared with the run page (parity-tested).
- studio (web): the app's own turns decided from the devtools panel and the run page (plan F4.3): while a connected runtime of the run's service registered `runtime.Sessions` (and the turn is its session's latest) a parked app turn's calls take continue / skip (with its reason) / resolve on `POST /api/runs/{id}/approvals` — the 202's command polled to its end, "decided" with the resumed turn named, a rejection said verbatim — and a running app turn takes steer; otherwise the verbs are drawn refused in the server's words — "app-owned turn · read-only" with its one-line fix, auth.go's for a read-scoped token, "decided — resumed as …" for a turn a later one resumed, a plain Generate's park with the `thread.PublicID` fix; cancel is never offered on an app turn (/live's disabled cancel now says "cancel is not supported on an app turn"). studio: the app-owned 403 tells a closed link apart — no runtime of the run's service connected answers "app-owned turn · read-only: no runtime of service <svc> is connected" with the fix "open the runtime link in the app: WEFT_ENV=dev (or runtime.Enabled(true)) …"; the Sessions fix no longer promises cancel. `thread/examples/approvals -serve addr` is the demo: the agent with `runtime.Breakpoints()`, `runtime.Install` with `runtime.Sessions` returning the app's live Session, Studio embedded with the panel on the page.
- cmd/weft: `weft mcp [--url] [--token]` (plan I4.1): a read-only stdio MCP server over the Studio read API — tools `runs`, `search`, `run`, `step`, `request`, `events`, `tools`, `diff`, `export` (json/jsonl/otlp), `sessions`, `session`, `meta` and `explain_failure` (run + step + transcript composed); every tool a GET, write verbs refused by construction; outputs the route's JSON cut at `max_bytes` (default 64 KiB) with a visible note; with no `--url`/`WEFT_STUDIO_URL` it reads the discovery file (`WEFT_DISCOVERY=off` skips it). No Studio answering at startup is one WARN, not an exit: it serves, and after a transport failure each tool call looks again (no `--url`: `WEFT_STUDIO_URL` and the discovery file re-read; `--url` retried as given) — a Studio started later, or `weft dev` on 7332–7340, is found without restarting the client; until then a call isErrors "no Studio answers /api/meta …". `explain_failure` puts `step_holes`, `last_assistant_text`, `transcript_error` and `next` before the step's bulk (`attempts`, `tool_calls`, `children`), so a cut at `max_bytes` keeps them.
- docs/mcp-recipe.md and `make mcp-check` (plan I4.2): the `weft mcp` recipe for Claude Code, Cursor and VS Code (setup, the question-to-tool table with worked examples from a real session, the read-only contract, tokens, limits, troubleshooting), and a build-tagged live check (`cmd/weft/mcp_check_test.go`, tag `livecheck`, not in `make test`) that starts a real `weft studio` seeded with the record-contract fixtures and drives `weft mcp` over stdio, ending `MCP CHECK PASS`.
- core: `ParkSource(fn func(ctx context.Context) []string) Option` and
  `(*Agent).ParkSources()` (ADR 0030, plan F4.2; additive, root facade
  `weft.ParkSource`): a park set the loop reads once per run at its start
  and merges into the run's park set exactly as `ParkOn(names...)` — the
  same boundary (ADR 0007), nothing new to the model, not an override
  (no `weft.override.*`). A nil/empty answer parks nothing; a panicking
  source is contained (one Debug line); the function must return
  promptly. A Subagent's child run reads its own agent's sources.
- weft/runtime: `Breakpoints() core.Option` (plan F4.2): pass it to
  `weft.New` and the debugger's breakpoints (`PUT
  /api/runtimes/{id}/breakpoints`) park tools on the app's own runs of
  that agent — thread turns (decided from the panel through
  `runtime.Sessions`; a thread turn parks only when the process set
  `Sessions` and the session has a public id (`thread.PublicID`) — without
  both the source answers nothing for a `weft.session.id` run, so no park
  nobody could decide holds the session) and plain `Generate` runs (ending with
  `res.Pending`; the app resumes them with `weft.Approve`). In a `Subagent`
  child the child parks and the parent's call reads `SUBAGENT_PENDING`. Bound lazily
  to the links `Install` started and not yet stopped (their union; a
  link whose stream dropped keeps its last set until it reconnects);
  process-wide — every agent built with the option parks on it, given to
  `Agents` or not; inert — byte-identical — with no Install, an Install
  not enabled or no breakpoint set (`TestBreakpointsOptionAbsentChangesNothing`).
  Playground runs (and runs started inside them: `weft.playground`
  metadata) are skipped — they keep their own filtered `ParkOn` on the
  override fingerprint, unchanged. The registration carries
  `breakpoints_own` (true when an agent given to `Agents` carries the
  option).
- studio/runtime: `Registration.BreakpointsOwn`, `RuntimeView.BreakpointsOwn`
  (`/api/runtimes` … `"breakpoints_own": bool`), `RuntimeServer.AnyOwnRuns()`.
  `meta.debug_scope` reads `"own+runtime"` while a connected runtime
  registered `runtime.Sessions` **or** `breakpoints_own`.
- studio/runtime: `WithStore(obsdb.CommandStore)` option on `New` (and
  `RuntimeServer.Close` / `Shutdown(ctx)`): commands, run routes,
  breakpoint sets and registrations are written through by one ordered
  writer goroutine — the hot path only queues (the latest write per key;
  the prune a queue item, never beside a Put), each store call bounded at
  2 s, a failure tripping a 30 s breaker that holds the queue (still
  coalescing per key, capped at 20 000 keys — past it route writes go
  first, then the oldest finished commands' (such a command may read lost
  or unknown after a restart), a WARN with the running total on the first
  drop and each doubling) and then re-applies it, the
  failed write included (one WARN, then Debug); Close ends the pause and
  drains the queue once, bounded — a store still failing when the Studio
  stops loses the writes still held (one WARN names how many); with the
  cap, the two ways a row goes absent rather than older — and loaded
  back on a Studio
  restart: a restarted Studio lists its runtimes (`restored: true`,
  `connected: false`, for at least 10 min after the load) with their
  breakpoints, answers pre-restart commands, decides `lost` from the
  stored state (a queued command past its ack window or whose runtime has
  no stored row, an accepted one past max(10 min, 3 beats) from its stored
  last beat) and never re-sends an accepted command. A store that lags or
  fails only makes rows read older (decided lost sooner, or a queued row
  re-sent to its own process, which dedupes). `studio.New` passes its DB
  when it implements the interface (both first-party backends);
  `studio.Server.Close` stops the link server's timers and drains its
  writes before the DB closes. (plan I3.2)
- `/api/runtimes` rows gain `cwd` (db.path's rule: absent to panel tokens)
  and `restored` (omitted when false).
- studio/runtime: `Registration.Cwd`, `Registration.Instance`;
  `RuntimeView.Cwd`, `RuntimeView.Restored`. A runtime-link client
  (`weft/runtime`, Go — the only one; non-Go clients are backlog I2,
  not shipped) sends `instance` (random per link) to keep at-most-once across its own
  restarts; Studio falls back to host+pid and WARNs once without it.
- obsdb: runs derived from stock OTel GenAI spans (docs/record-contract.md §9).
  An `invoke_agent` span with `gen_ai.conversation.id` and no `weft.run.id`
  — a Python or TypeScript app's, exported over OTLP — is now a run row,
  id `otel-<trace id>-<root span id>`, `RunRow.Derived` true: session the
  conversation id, steps its `chat` spans, usage the root's
  `gen_ai.usage.*` else its own chat spans' sum (own-only: a nested root is
  a child run, linked by its enclosing `execute_tool`'s call id), status
  succeeded or failed (never running/interrupted). New: `obsdb.DeriveStockRuns`,
  `StockRun`, `StockRunID`, `ParseStockRunID`, `StockTraces`, `IsStockRoot`,
  `StockCandidate`, `RunRow.Derived`, `HoleCause` `CauseStockSpans`
  ("derived from spans · no events"). `RunRow.RequestsHole` reads `derived`
  for a derived row; `ExplainMissing` answers its prompt/tools with the
  derived hole. A derived run's `RunSpans` is its subtree read from the
  trace, `weft.step.index`/`weft.tool.seq` stamped on the returned copies;
  `DeleteRun`/`Sweep` delete its subtree's spans (`DeletedSpans` seeds a
  derived id's root). Migrations: SQLite 0008 (`runs.derived`), ClickHouse
  0009 (`weft_runs.Derived`; rows inserted by Write, no view). The
  `stock_genai` contract fixture is un-pended and passes on both backends.
- obsdb: an `event` record without `weft.event.pos` is stored at position
  -1 on SQLite too (ClickHouse already did), never at 0 where it collided
  with `run_start` (I1.1 review debt). A `delta` without its position still
  counts as position 0 (both backends).
- studio: `GET /api/runs/{id}/steps/{n}` is honest for a derived run: the step is its `chat` span — usage from its `gen_ai.usage.*`, priced like any step (never a known $0), its `execute_tool` spans as calls badged `derived` — with `request`, `messages_in` and the one hole `derived` in the stock_spans words; no `gap` / `not_recorded` ("upgrade weft") holes or attempts badge.
- studio: `POST /api/playground/runs` refuses a derived source run 400 (`bad_request`, badge `derived`, stock_spans words) in `checkSource`, as the preview and fixtures routes do — no command reaches the runtime (before: accepted, then failing with the wrong cause, or spending a live model call for an input-only replay).
- studio/ingest: on a backend that publishes no run frames of its own (ClickHouse), the receiver announces derived runs on the live lane too — it re-reads the derived runs of every trace an export brought a stock GenAI span of, so a later export that grows a run's steps is announced again.
- studio: `requests` and `tools` of a derived run carry the stock_spans words and fix ("derived from spans · no events").
- obsdb: a failed derived run whose root has no status description reads `err` "the root span's status is Error (no description)" (`obsdb.StockErrNoDescription`); an empty `gen_ai.operation.name` is unset (the span name decides).
- obsdb: deleting a weft run deletes the derived row of a stock agent under its tool along with its spans (`obsdb.StockRunsOf`), on both backends; new conformance cases `DerivedMixedTrace` (weft and stock spans in one trace, no twin) and `DerivedTwoRoots` (two roots in a trace, delete one).
- obsdb: a derived run's spans (`RunSpans`, `StockRun.Spans`) carry `gen_ai.operation.name` as the derivation resolved it — from the span name when the producer set none or set it empty — beside the reader's `weft.step.index` / `weft.tool.seq`.
- studio: a derived run's step whose chat span reports only zero usage reads its cost `unknown`, as the run row does ("the stock spans report no (or zero) usage …"), never a known $0.
- studio: the playground run route answers a derived source's refusal before validating `command_id` / `experiment_id` (after the panel token's rules), as the preview does.
- studio: `steps/{n}` of a derived run follows §9's name fallback (usage, times and calls for a producer with no `gen_ai.operation.name`), and serves a call whose `execute_tool` span has no `gen_ai.tool.call.id` with `call_id: ""`, keyed by its span, badged `derived`.
- studio: a derived run (row and steps) whose stock spans report no `gen_ai.usage.*` reads its cost `unknown` ("the stock spans report no usage …"), never a known $0.
- obsdb: `DeleteRun`'s godoc names the derived row of a stock agent under the run's tool, which it deletes; `Sweep` counts those rows in its return (both backends); `DELETE /api/runs/{id}`'s note says so.
- studio: the playground's run route refuses a derived source before any other check of the command, at the preview's position, so both routes answer one body with the same derived refusal.
- studio: run rows carry `derived` (omitted when false); a derived run's
  page has the one `derived` hole (cause `stock_spans`), its `events` and
  `transcript` answer empty with that badge (`transcript?step=N` 409), its
  `spans` are the real spans, `requests`/`tools` empty with the `derived`
  badge; `/api/runs`, `/api/search` (agent, model, error text) and
  `/api/sessions` (grouped by conversation id) list it; the export carries
  the row and spans and refuses `format=wefttest` 400 with the badge (as
  `POST /api/playground/fixtures` and `/api/playground/preview` refuse a
  derived source); `/api/diff` marks every column `unknown` against a
  derived side. New goldens `run-derived`, `events-derived`,
  `transcript-derived`; `holes.golden.json` gains `derived.causes.stock_spans`.
- **weft/runtime:** `runtime.Sessions(resolver)` (plan F4.1): an opt-in Install option that makes the app's own turns targets for the debugger's write verbs. `type SessionResolver func(ctx context.Context, publicID string) (*thread.Session, func(), error)` answers the app's Session behind a public id — its own live Session (no release) or one it opens from its store (release closes it; called after each command); `(nil, nil, nil)` is refused "no session for this public id". Decisions go through the session's own `Decide` (approve | deny | resolve), which resumes the turn under thread's rules (the parked turn's `Next`); a steer is delivered only into the run it names while that run is the turn running now (`thread.Session.Steer` with the command's run id; `ErrNotRunning` otherwise — no turn running, or a newer one — the command finishes failed "no running turn to steer for run <id> (…)" naming both runs, nothing written); a delivered steer that misses its drain point (StopWhen, a park, the run ending first) becomes a follow-up turn under ADR 0019's own rule; cancel is refused "cancel is not supported on an app turn" (thread has no per-turn cancel). The runtime claims the Session's writer lease before it acks: a Session that cannot write is refused with exactly "the app is writing this session; fork instead" and nothing is written; `thread.ErrStale` releases and resolves once more. Each resolve and the claim after it are bounded at 10 s — a resolve past it is refused "session resolver timed out" (a late answer is released), a claim past it "session claim timed out". A `thread.RequireSigned` session is refused before the ack ("this session requires signed decisions: …"); a deny without a reason reads "skipped from the devtools panel", the runtime-started runs' default. Return the app's live Session: an opened one holds the writer lease through the whole resumed turn, and the resumed run's context carries none of the app's values. The registration carries `sessions` (false without the option); an app that does not set it is byte-identical (`TestSessionsOptionAbsentChangesNothing`).
- **weft/runtime ↔ Studio wire:** a new SSE frame, `event: session`, data `{command_id, verb: decide|steer|cancel, public_id, session_id, run_id, call_id?, decision?, reason?, content?, message?, actor?}` — acked like every command (accepted before the session is written, finished after; at-most-once unchanged). `studio/runtime`: `Registration.Sessions`, `RuntimeView.Sessions` (`/api/runtimes` … `"sessions": bool`), `SessionCommand`, `VerbDecide/VerbSteer/VerbCancel`, `RuntimeServer.EnqueueSession`, `SessionsRuntime(service)`, `AnySessions()`. A session command's acks never make the app's run a runtime-started one.
- **weft/thread:** `(*Session).Claim(ctx) error` — takes the session's writer lease now without writing (nil for the holder, `ErrLocked` while another writer holds it — nothing taken —, `ErrStale` when the view fell behind — the lease given back, so a fresh Session can claim it —, `ErrClosed` once sealed); `(*Session).Steer(ctx, runID, msgs...) error` — delivers into the turn running now, when it is `runID` (`""`: any), exactly as `Send(…, As(Steer))` on a busy session, the checks and the acceptance under one lock (`ErrNotRunning` naming both runs when another turn runs); a delivered steer that misses its drain point (StopWhen, a park, the run ending first) becomes a follow-up turn under ADR 0019's rule; a multi-message Steer whose later append fails has accepted the earlier messages; `ErrNotRunning` (new sentinel) when no turn is running (idle, between turns, parked), nothing written — it never starts a turn; `(*Session).RequiresSigned() bool`. Additive; what weft/runtime asks before it acks a debugger's decision.

- Docs: `docs/record-contract.md`, the record contract as one spec — the
  identity chain, every `weft.*` span attribute and log-record kind, the
  GenAI semconv mapping, the run row a reader derives and the rules for
  deriving a run from stock GenAI spans (devtools plan I1.1) — pinned by
  `obsdb/model.go` and a conformance fixture set, `obsdb/testdata/contract`
  (real pipeline output, regenerated by `go test ./obsdb -run
  'TestContract$' -update-contract` — its own flag, not the shared
  `-update` — and normalised so a regeneration with no code change
  leaves `git diff` empty; `TestContract` on SQLite,
  `TestContractClickHouse` with `WEFT_CLICKHOUSE_DSN`; the stock-GenAI
  case pending I1.2).
- `obsdb.CommandStore` (plan I3.1): an optional interface beside `obsdb.Searcher` that persists the playground link's records — commands (`StoredCommand`: state, run id, public id, fork, the wire body and last ack verbatim, `Created`/`Updated`/`AcceptedAt`/`LastBeat`), run → runtime routes (`RunRoute`), per-runtime breakpoint sets (replaced whole; a stored clear is distinct from none) and runtime registrations (`StoredRuntime`, with a `Cwd` column) — with `Commands(CommandQuery)`, `CommandByRun`, `CommandTerminal`, `CheckStoredCommand`, `ErrInvalidCommand`, the five `Command*` state constants and `PruneCommands(before, keep)` (terminal rows by age and count, never a queued or accepted one; routes and stale runtimes with them). `DeleteRun`, `DeleteSession`, `Sweep` and `Vacuum` never touch these rows. Nothing uses it yet: studio/runtime's write-through is I3.2.
- obsdb/sqlite migration 0007 (`commands`, `run_routes`, `breakpoints`, `runtimes`); obsdb/clickhouse migration 0008 (the same four as ReplacingMergeTree tables versioned by `InsertTime`, read FINAL). Both upgrade in place; the version pins read 7 and 8.
- obsdbtest: the `Commands` and `CommandsPrune` conformance cases (skipped for a backend that is not a CommandStore), and `obsdbtest.CommandsRestart` — the restart property (a second handle on the same storage reads what the first wrote), run on a sqlite file and on ClickHouse.
- obsdb: `CommandStore.PruneCommands` on ClickHouse deletes exactly the row versions each rule selected, so a `PutRuntime`/`PutRoute`/`PutCommand` landing mid-prune keeps its row (the caller still serialises the prune with its writes: it is not atomic there); on SQLite the terminal commands go in `obsdb.SweepBatch` passes, each its own transaction, ctx checked between. A stale empty breakpoint set no runtime row names is now pruned on both backends. `obsdbtest.CommandsPruneRace` is the new conformance case for the race rule.
- studio web: the derived-run badge on both surfaces (plan I1.3) — a run derived from stock OTel GenAI spans reads `derived` in the `stock_spans` words ("derived from spans · no events", the fix naming the record contract; mirrored in `lib/honesty.ts`) on its row (runs, search, session turns, live), the run header (no "0 events"), a story of per-step cards from `steps/{n}` (usage and cost as served, unknown with the API's reason), the raw view, and the panel's turn chip, notes, Request, Timeline and Raw tabs; the replay verbs, the editor and save as test are drawn disabled with those words; the compare page's cost follows its unknown usage. The panel's derived Story hands off ("2 steps · open in Studio ⤢"), and a derived run with no usage reads "—" with the API's reason on both surfaces; no "replay here", space-replay hint or "no events yet" on a derived run. The panel header's token total leaves such a turn out ("≥", or "—"); a derived run whose spans cannot be read yet shows the derived note in place of the trace. Panel ledger rows `I1.3` 74,061 B, `I1.3 fixes` 74,221 B, `I1.3 fixes 2` 74,284 B gzip (+578 B in all, 7,636 B of headroom).

### Changed

- weft/runtime: the runtime id is stable per (service, cwd, agent set) —
  `rt_` + 16 hex of sha256 — instead of a fresh ULID per process, so a
  panel link `/runtimes/<id>` survives an app restart. A registration on
  an id another process holds — a live stream on it, or a queued or
  accepted command — is answered `<id>-<pid>`, which the link adopts; a
  finished command's run route never holds it. The registration carries
  `cwd` and `instance`.
- studio/runtime: `New()` becomes `New(opts ...Option)` — source compatible
  for every call, but apidiff reports it incompatible (a func value of type
  `func() *RuntimeServer` no longer matches): the breaking-change line for
  the next minor tag of the framework module (pre-freeze).

- **studio:** `POST /api/runs/{id}/approvals`, `/steer` and `/cancel` on an app-owned run (no runtime started it) now answer alike (plan H6.1): with a connected runtime of the run's service (`service.name` = the registration's service) that registered `runtime.Sessions`, the verb is forwarded as a session command and the route answers **202** `{command_id, state: "queued", run_id, runtime_id}` — the runtime's refusal (the locked sentence above all) lands on `GET /api/playground/commands/{id}` (`state: "rejected"`, `error` the sentence); without one, **403** `{"error":{"code":"forbidden","message":"app-owned turn · read-only: …"},"fix":"set runtime.Sessions(resolver) at runtime.Install in the app (service …): …"}`. A run that is not a turn of a thread session with a public id is 403 "app-owned turn" with the fix `thread.PublicID(...)`. **Breaking for clients:** cancel on an app-owned run was 409 ("cancel arrives with F4"); it is 403 now. The panel-token rule is unchanged (another public id's run 403, read tokens 403).
- **studio:** `meta.debug_scope` is `"own+runtime"` while a connected runtime registered `runtime.Sessions`, else `"runtime-started runs"` as before (absent without the playground).

- The devtools panel ships fully minified (plan A5.3.0): `studio/dist/panel/panel.js` — served as `/studio/panel.js` and, the same bytes, the npm package's `panel.js` — goes from 281,470 B raw / 81,358 B gzip (562 B under the 80 KiB cap) to 230,920 B / 73,706 B (8,214 B of headroom), whitespace, identifiers and syntax minified (`rollupOptions.output.minify: true` — the rolldown alias in Vite 8 — overriding Vite's keep-the-whitespace rule for an es library build). No sourcemap ships, so the panel is unreadable in a host page's devtools — its source is `studio/web/src/panel`. The size ledger gains one `"minified"` row, the baseline every later row is measured against; earlier rows stay as history. The minifier reprints the version stamp as a template literal, so `normalizeStamp`, Studio's `panel_version` reader and its pin accept either quote.

### Fixed

- studio (web): phase 6 review fixes (I) — a run carrying `weft.session.parent` is a `thread/pool` child only when it has no parent run id or is its own session's `<session>-tN`; a `Generate` nested under a pool child's turn (it inherits the key, the session id and the public id through core's context metadata, `core/metadata.go`) reads the plain Generate's line again, not the pool child's (before this fix it read "a delegated session's turn", a false line). The run page's gate for a session's own `Send` inside a tool handler (`appTurn`) is now pinned by a run-page test (its verbs, its session read, resumed once the session lists its next turn). The panel is 78,098 B gzip (+4 B; 3,822 B of headroom under the 81,920 B cap).

- studio (web): Studio's own tests pin that a failed `GET /api/meta` held by `useCapabilities` clears once a later read answers, and that a meta answering without the capability shows the page's off words (`/traces`: `[data-traces-off]`), never "could not read the Studio's capabilities".
- studio (web): every test file that renders the app (`src/test/app.tsx`) now empties the shared QueryClient at its end while jsdom still stands — unmounted, in-flight reads cancelled, queries removed (their 5-minute gc timers with them), queued notifications flushed — so no TanStack notification runs after jsdom's teardown ("ReferenceError: window is not defined", an unhandled error that once made `make studio-check` exit 2).

- studio (web): the run page keeps an app turn's 403 refusal by the devtools panel's rule: until a `GET /api/runtimes` read started after it has applied and the gate says decide — then continue / skip / resolve are back, even when the runtimes read the same as when it was answered (runtime ids are stable per service, cwd and agents, so a restarted app registers under the same id, and a gate that changed and changed back no longer brings an old refusal back); a read that fails, or one still in flight, keeps it. The two surfaces asserted opposite outcomes for the same fixture before; `parity.test.ts` now holds them to the same row.
- studio (web): the run page's parked block stops re-reading an app turn's session every 5 s once the next turn is known (a parked turn another surface resumed stays `pending > 0` for ever — the resume is a new run) or the session read was refused (a 4xx, such as a 404); it read it for as long as the page stayed open. The runtimes stay on the shell's own 5 s poll of the same query (`use-notices.ts`, every page); the block's own runtimes interval, which that poll always outran, is gone.
- studio (web): a parked child run is one of three, told apart in `parkedKind`'s order (its decision table is in `lib/honesty.ts`): core's Subagent child (`<parent>/<step>/<call>`, as `pricing.go`'s `subagents()` tells them apart) reads "parked · a subagent's call — decide on the parent turn …"; a `thread/pool` child, recognised by the `weft.session.parent` thread stamps on every pool child's run (the run row's `meta`) when the run has no parent run id — `p.Submit` from app code outside a run gives the child none (core stamps `weft.parent.run.id` only from a running call's context), and then whatever its id — or is its own session's turn (`<session>-tN`, the delegating call's child), reads "parked · a delegated session's turn — decide on the parent session <session>", plus "· delegated by turn <parent>" when it ran under a delegating call (on the run page a link to `/sessions/<session>` and one to the turn; no verb, no fix: its requests are mirrored onto the parent session and decided there, ADR 0022 §7); and another agent's `Generate` inside a tool handler — which carries `weft.parent.run.id` and, because core merges a run's context metadata into every run started under it, whatever session id, public id and `weft.session.parent` the run above it carried (one nested under a pool child's turn inherits that key too), but an id of its own — never parks onto the parent and reads the plain Generate's line. A session's own `Send` inside a tool handler (a parent run id, its own `<session>-tN`, no `weft.session.parent`) is a turn of that session, gated like any other (the run page reads its session and the runtimes; a later turn of it reads resumed). Known caveat: a session created without `thread.PublicID` whose `Send` runs inside another session's tool handler inherits that session's public id through core's context metadata and reads decidable; the runtime refuses the decision in its own words ("the session resolver answered session … for public id …, not the run's session …", shown verbatim) — give such a session its own `thread.PublicID`.
- studio (web): a failed `GET /api/meta` read again (a new observer puts it back to pending with its error cleared) no longer flickers the run page's parked block to "checking" — the last error is held — and Studio's capability-gated pages (traces, live, experiments, playground, compare) say "could not read the Studio's capabilities: …" instead of claiming the capability is off ("This Studio does not list traces", "reports no live capability", no playground, no step compare). `useCapabilities` exposes the held `error`.
- studio/README.md: the panel's size sentence reads the committed bundle (242,821 B raw / 77,996 B gzip, the ledger's "phase 6 review fixes (G)" row, 3,924 B of headroom; +54 B: `parkedKind`'s pool-child kind and its words; the unbudgeted line still −0.2 KiB net).
- studio/README.md: the panel's size sentence reads the committed bundle (243,059 B raw / 78,094 B gzip, the ledger's "phase 6 review fixes (H)" row, 3,826 B of headroom; +98 B: the pool child recognised by `weft.session.parent`, its words naming the parent session and the delegating turn, a session's own turn under a parent run id gated as a turn; the unbudgeted line now −0.1 KiB net).
- studio (web): the devtools panel hands its meta to `refusalWords`, so on a Studio without the playground its app turn reads "app-owned turn · read-only: the playground is off on this Studio" with `meta.capabilities_off`'s fix, as the run page does, instead of "no runtime of service … is connected" (the parity row is a plain test now).
- studio (web): the devtools panel keeps an app turn's 403 refusal until a `GET /api/runtimes` read started after it applies — a read that fails, or one still in flight, no longer gives the verbs back on the runtimes read before the refusal.
- studio (web): a run page under a failed `GET /api/meta` draws the run's events and the parked block ("could not read the Studio's capabilities: …", no verb) instead of "no events were recorded for this run": each new observer of the failed meta read it again, putting it back to pending, and the event walk started over per read. `useCapabilities`'s `loading` is the first read only.
- `thread/examples/approvals -serve`: a serve whose cached Session another serve's write over the same `-dir` left stale (`thread.ErrStale`) closes it and opens the session again — its ask retries once, its `runtime.Sessions` resolver claims the cached Session first and hands back a fresh one — instead of answering 500 (and the runtime refusing every verb) for the process's life.
- studio/README.md: the panel's size sentence reads the committed bundle (242,553 B raw / 77,926 B gzip, the ledger's "phase 6 review fixes (E)" row, 3,994 B of headroom; the panel's first build over the run page's `lib/honesty.ts` changes, +343 B, and the meta argument, +23 B) and the unbudgeted line's net as `budget.test.ts` pins it (−0.3 KiB).

- studio: a panel token's session is its public id's turns alone. A derived run's session id is the stock app's `gen_ai.conversation.id`, so another app's stock trace colliding with a weft session's id joined the session's GROUP BY: a read panel token's `GET /api/sessions/{id}` listed the derived row (agent, model, usage, error text) and folded it into the session's turns, usage and cost, while `GET /api/runs/{otel-…}` was 403 and `/api/runs?session=` hid it. `GET /api/sessions/{id}`, `GET /api/sessions` and the session's cost now list and sum only the runs carrying the token's public id (the runs list's rule); the server token still sees every run of the session. docs/record-contract.md §9 says so.
- studio: a panel token's `GET /api/sessions` no longer answers 500 for the whole list when one session's runs cannot be read to scope it (a read error, a session past the 10,000-run walk) or none of them is the token's (a session deleted between the list and the walk): such a row is left out — never served unscoped — with one Debug line, and the other sessions answer. The list's ordering, `total` and paging cursor stay the session's, not the token's view (docs/record-contract.md §9 says so). `TestDerivedRunSessionCollision` now prices both models, pinning the session's `cost_usd` to the token's own turn on the doc and list routes.
- studio/runtime: a finished playground command's run route no longer holds the stable runtime id for the old process. An app restarted as a new process within 10 min of any playground run (`weft dev`'s restart on save; setup A, whose run routes come back from the store) was answered `<id>-<pid>` and lost Studio's breakpoint set and its `/runtimes/<id>` link; only a live stream or a queued/accepted command holds an id now. A late decision for a run parked on the old process may reach the new one, which refuses it ("no parked run …").
- obsdb: deriving runs from stock GenAI spans walks each span's parent chain once (memoised), O(n) per trace instead of O(n·depth). A 5,000-deep chain ingested in 100-span batches held SQLite's single writer up to 1.9 s per batch (31 s in all); it now takes 21 ms per batch at worst (0.5 s in all).
- AGENTS.md block 7a: cancel on an app-owned turn answers 403 "app-owned turn" (202 through a `runtime.Sessions` runtime, whose command row then reads rejected), not 409; 409 is finished or parked.
- studio/runtime: the ack handler's comment says the accepted ack is queued for the store before the 200 leaves, not written (a lag only makes the row read older); `WithStore`'s godoc and studio/README.md say one Studio per command store — two Studios over one ClickHouse database decide each other's live commands lost (nothing runs twice).

- studio (web): the run header of a derived run whose stock spans reported no `gen_ai.usage.*` reads its tokens as "—" in the API's `cost_reason`, never "0 in / 0 out" as a known figure (step cards, the runs table, session turns and /live already did).
- studio (web): an app turn's read-only line tells its causes apart (plan F4.3 review). On a Studio without the playground (`weft studio --no-playground`, setup A without `studio.Playground(true)`) the run page reads "app-owned turn · read-only: the playground is off on this Studio" with `meta.capabilities_off`'s fix, not "no runtime of service … is connected" — the approvals route is not even registered there; `lib/honesty.ts`'s `refusalWords` takes the meta facts (the devtools panel reads them once it passes its meta). A turn of a thread session created without a public id reads appturn.go's "… is not a turn of a thread session with a public id" with the `thread.PublicID` fix, not the plain Generate's line; a subagent's child run reads "parked · a subagent's call — decide on the parent turn <parent>" with no verb (its clicks were all rejected "call … is not pending on session …": only the parent's SUBAGENT_PENDING call is pending); a failed session read says "could not read the session: …" instead of checking forever. While the turn is parked or running the run page reads its session and the runtimes every 5 s, so a turn another surface resumed reads "decided" and a 403 refusal holds only until a later runtimes read has applied and says the verbs' gate holds again (the panel's rule, phase 6 review fixes (F)). A delivered steer reads "steered", not "decided", on both surfaces.
- studio (web): a run's delegated cost is badged `unknown`, not `unpriced`, when pricing.go's reason leads with "a subagent run's cost is not known yet:" — any child still running or never ended makes the whole unknown, even when the list also names an unpriced child.
- studio (web): `?save=test` (the panel's "save as test" hand-off) on a derived run is refused in the disabled button's words instead of firing the export the server answers 400.

- `thread/examples/approvals -serve`: a restart over the same `-dir` resumes the session stored under the served public id (`thread.List` by `weft.public_id`, then `thread.Open`) instead of creating a second session with the same public id — whose old parked turn the panel offered live verbs on, every decision ending "rejected: the session resolver answered session … for public id …, not the run's session …". The `runtime.Sessions` resolver opens that stored session too, so a turn parked before the restart is decided before any new question.
- studio (web): the devtools panel's app turn (and, since the (F) round, the run page's — one rule on both surfaces) gives its continue / skip / resolve back once a later `GET /api/runtimes` read says the gate holds again (the runtime reconnected, the fix applied) — a 403 answered on a stale gate no longer leaves the block read-only with the old refusal until a reload.
- studio (web): a quiet parked (or running) app turn reads `GET /api/runtimes` again on its own as the 10 s window ends — one timer, cleared on dispose — so "app-owned turn · read-only: no runtime of service … is connected" follows the link opening without a click.
- studio: `make studio-panel-asset`'s stamp check accepts only the stamp's two exact forms (`"vX"` or `` `vX` ``, matching quotes — `normalizeStamp`'s, the Go reader's rule), not any quote pair anywhere.
- studio/README.md: the panel's size sentence reads the committed bundle (241,541 B raw / 77,560 B gzip, the ledger's "phase 6 review fixes (D)" row, 4,360 B of headroom) and the unbudgeted line's net as `budget.test.ts` pins it (−0.6 KiB); the app-turn rejection list names the resolver-mismatch refusal.

- Studio's playground: an outside navigate (Back, Forward, a link followed while the page is open) adopted into an untouched form no longer counts as the reader's edit — its draft is not saved and no `?exp=` REPLACE is written back 400 ms later (G2: an outside navigate writes nothing back).
- `thread/pool`: `Pool.Wait` for a sync delegation that settles after a park
  returns only once the parent's parked call is resolved and its resume armed,
  as documented — it could return in between, with the parked turn's `Next`
  still nil (nightly soak: TestNestedSyncFullFlow, TestChildInheritsSigning).
- `thread/pool`: a `Wait` or a second `Cancel` arriving while `Cancel` settles
  a parked child now waits for it — `Wait` returns the receipt canceled with
  the parent's resume armed, the second `Cancel` reports it settled — instead
  of returning at once with the ledger still parked (or `nil`); a panic while
  recording a settlement no longer leaves every later `Wait` on that receipt
  blocked.
- otel: `Pipeline.Shutdown` is bounded by its context: the heartbeat goroutine
  stops between records instead of finishing its pass (one synchronous Local
  write per open run, each queued behind every run's), and Shutdown waits for
  it under ctx — a Local sink behind another process's write lock held `defer
  otel.Install()()` for SQLite's 30 s busy timeout per open run. A concurrent
  or later Shutdown returns the first's result, or its own ctx's error.
- `thread/pool`: a child that was still parking at an approval when its pool
  was closed has its session closed like any parked child — its writer lease
  no longer outlives the pool, and the next process's resume of it no longer
  fails "session is locked by another writer".

## 0.14.2 — 2026-10-11

A patch over 0.14.1 with the fixes CI surfaced after it: two races in
`thread/pool`'s register/settle bookkeeping, the session store's failed
sends and steers (a failed `Send` drops its receipt and moves the leaf
back in one flushed append on every path; a drained or deferred steer is
settled exactly once across a crash and reopen), and the conformance
cases that depended on their own duration. Tags `core/v0.14.2` (the
version literal only) and `v0.14.2`.

### Fixed

- `thread/pool`: two pumps racing over one restarted child — `Decide`'s own
  and the mirror watch's its decision fires — no longer report `ErrNoAgent`
  for a child the other had just resumed and settled, nor install a live
  bridge for the finished delegation (a wrapped child, whose agent no
  settlement drops); the check is per child, so unrelated settlements cost a
  lookup nothing (CI: TestRegisterResumesParkedSubmitChild,
  TestDecideJoinsErrors, "no agent for child session").
- `thread/pool`: a `Register` landing after its session's delegation settled
  (a fast Submit child ending before the call) is dropped when the pool next
  reads that settled receipt — `Wait`, `Recover`, or a pump reaching the
  session through a mirror still in force — instead of staying for the life of
  the pool (CI: TestRegistersPruned, "sessionAgents=1").
- `obsdb/obsdbtest`: the `Sweep` and `SweepBatched` conformance cases no
  longer fail when the test runs longer than `obsdb.InterruptedAfter` (a
  loaded `-race` runner writing the 1,075-run backlog): their running runs
  heartbeat just before each future-cutoff sweep, as live runs do, so only the
  sweep itself has to fit in that window.
- `thread`: a queued `Send` whose `accepted` receipt was written but not
  flushed now moves the leaf back to where it stood (a `leaf` entry after the
  `dropped` receipt), like the prompt path — a failed `Send` leaves the same
  line whether it met an idle session or a busy one, the window right after a
  turn's `Wait` included (the runner still between items, the `Send` queued).
  `Turn.Wait`'s godoc and docs/thread-operations.md §8 name that window;
  `Session.WaitIdle` waits it out.
- `thread`: a steer whose deferral at its turn's end was written but not
  flushed keeps its receipts: the follow-up runs under the turn its `accepted`
  receipt names instead of a fresh one, so a reopen before it ran restores it
  (it was lost) and one after it ran no longer runs it a second time.
- `thread`: every failed send that will not run — a queued prompt the runner
  could not flush, an `Interrupt` whose denial of the parked calls could not
  be recorded, a `Steer` at a parked boundary whose deferral could not be
  flushed — drops its receipt and moves the leaf back in one append, flushed
  (they were two unflushed appends, and the leaf stayed on the `dropped`
  receipt).
- `thread`: a steer the running turn drained is no longer restored by `Open`
  after a crash between its step (the steered message, written with the step)
  and the turn's end (its `delivered` receipt): the message on the active path
  settles the `queued` receipt, so the steer no longer runs twice and
  `ClearQueue` finds nothing to drop (the crash matrix's `clear_queue` flake).
  Read-side only; the format is unchanged (ADR 0019 amended).
- `thread`: a steer whose turn-end deferral the storage refused outright still
  runs from memory, and its follow-up's prompt append now carries the
  `deferred` receipt that settles the steer's `queued` one — a reopen after
  the follow-up ran no longer restores the steer and runs it again; one before
  it ran still does.
- `thread`: when the one append that drops a failed send's receipt and moves
  the leaf back is itself refused, the log now says what happens on that path
  (the file's leaf stays on the failed entry until the next write; only an
  undropped accepted receipt without a prompt is restored).
- `thread`: a steer the running turn drained is no longer deferred to a
  follow-up when the storage refuses the turn's end batch after the step
  carrying its message landed: the message on the line is its delivery (the
  rule `Open` applies), so it ends `TurnDelivered` with its `delivered`
  receipt written on its own (unanswered unless an assistant message follows
  it on the line) and runs once instead of reaching the line twice.

## 0.14.1 — 2026-10-11

A patch over 0.14.0 with the two fixes that followed its tag: the panel
asset script's gzip measure and `LoadPricing`'s error field under Go
1.26 (the 0.14.0 CI run on 1.26 failed one test on the wording alone;
behaviour is unchanged). Tags `core/v0.14.1` (the version literal only)
and `v0.14.1`.

### Fixed

- `make studio-panel-asset` measures the panel's gzip as the build and
  the size ledger do (the pure-JS deflate over the normalized version
  stamp) instead of node's zlib at its default level, which read ≈ 700 B
  larger and refused the 0.14.0 bundle the build had passed.
- `studio.LoadPricing` names the field of a wrong-typed rate the same
  under every Go version (`models[0].input: want a float64`): each row is
  decoded on its own, so the path no longer depends on what
  encoding/json puts in its error (Go 1.26 said `models.input`, 1.27
  `models.0.input`; CI on 1.26 failed TestPricingFile at 0.14.0).

## 0.14.0 — 2026-10-11

Phase 5 of the devtools plan (studio depth): search and filters over
runs (`GET /api/search`, FTS5 on SQLite, a derivation per search on
ClickHouse; `min_cost`, `sort=cost`), cost priced at read time from a
dated table (own and delegated, `cache` exact or not reported, a
`WEFT_PRICING` override, the Settings page), provider raw bodies under
`otel.RawBodies(true)` and the step card's Wire tab, the trace list and
a trace page whose every selection is a URL, the Live page on one
`?all=1` stream with cancel, a run page that scrolls 5,000 steps,
experiments you can come back to (`/api/experiments`, paged), save as
test (a passing replay test in the export zip) and proposed prompts,
open in app and the panel hand-off (`weft.page_url`, `core.WithMetadata`),
deletion and retention (`DELETE /api/runs/{id}`, `studio.Retain`,
`weft vacuum`), and a pre-release review of the whole phase (the Go,
panel and Studio fix lines below). Tags `core/v0.14.0`
(`WithMetadata` added) and `v0.14.0` — a minor bump: the export zip's
layout, `otel.Pipeline.LoggerProvider()`'s type, `/api/meta`'s
`pricing` shape and the five methods `obsdb.DB` gains (Changed —
breaking).

### Added

- Studio: **open in app** on the run page (plan G3) — a run carrying
  `weft.page_url` (set by `scope.Header`) opens that page with
  `#weft_scope=` so the devtools panel there scopes itself to the run's
  conversation; disabled, naming `scope.Header`, without one. The panel's
  "open in Studio" (⤢, `g s`) under a playground-scoped panel token goes
  through `GET /api/panel-handoff` and opens the hosted Studio already
  authenticated to that conversation; any refusal falls back to the plain
  link.
- Studio: `/api/runs` and `/api/search` take `experiment_id=` (a narrowing
  filter; a panel token stays inside its public id). A saved experiment keeps
  its whole command — `engine`, `side_effects`, `thread`, `from_step`,
  `source_run_id`, `transcript_edits`, and per variant its overrides plus its
  own `engine`/`side_effects`/`thread` — and `/api/experiments` rows (and the
  detail) carry `created`, `run_count` and the cost of its runs (own +
  delegated). obsdb: `ExperimentCommand` (embedded in `Experiment`),
  `ExperimentVariant.Engine/SideEffects/Thread`; migrations sqlite 0005 /
  ClickHouse 0006 add the experiments table's command column (old rows read
  back without it). An experiment's id (POST
  /api/experiments `id`, a run command's `experiment_id`) follows
  command_id's rule, `[A-Za-z0-9._:-]{1,128}`; a variant's `overrides` must
  be an object; the save's answer carries `run_count` and the cost. The
  playground mints the id (a name that fits the rule is used as is, any other
  is slugged with a nonce) and keeps the typed name as `name`.
- Studio: experiments you can come back to (plan F5). The playground keeps its
  whole form as a draft in this browser (`?exp=<draft id>`, IndexedDB) once it
  changes, and "copy share link" puts the form in the fragment (`#state=`,
  deflated where the browser can, else base64url JSON; at most 8,000
  characters, beyond which it stays a draft) — the link restores the form on
  any browser against the same Studio and starts nothing. Variants can be
  renamed, duplicated and removed; a matrix runs every cell from one
  `from_step`. New `/experiments` and `/experiments/{id}` pages: the
  variants × inputs grid with each cell's run and cost, re-run one cell or
  all under the same experiment id, clone into the form, compare any two
  cells. The runs list takes an `experiment_id` chip, and a finished matrix's
  toast carries its cost.
- Studio's saves (plan F6, web half): **save as test** in the run header
  (capability `export`) fetches the wefttest zip
  (`export?format=wefttest`) with the bearer in the `Authorization` header
  and saves it under its `Content-Disposition` name; the devtools panel's
  turn offers it as a link to the run page with `?save=test`, which
  downloads there. **Keep as prompt** on the Request pane's system prompt
  (and in the panel's result pane) copies and, under `proposals` with the
  dev token, proposes it (`PUT /api/agents/{name}/proposal`), the outcome
  toasted in the server's words (a panel token copies only). The Agents
  page shows each proposal beside the registered prompt as a diff, with
  its since and hash, copy proposed and withdraw (`DELETE`), and points to
  WEFT-PROMPT.md P3 as the durable path.
- Studio's `/live` page (plan H6) runs on one `/api/live?all=1` stream (kinds
  event,run, opened through the live grant) when `meta.live_all` allows it.
  Each row shows the run's current step, tokens so far (the sum of
  step_finish usage), TTFT (the last step_finish's `ttft_ms`) and cost — "—"
  with A5.1's reason while running, the real figure on the final run frame.
  Finished runs stay listed for 30 s. A reconnect resumes with the server's
  bounded backfill, and a `backfill_error` shows a "could not read" gap badge
  on that run. Without `live_all` (a panel token) the page polls every 5 s
  and says why. Under capability `cancel`, runtime-started runs get a cancel
  button with an inline confirm: it posts `POST /api/runs/{id}/cancel`, the
  row shows "cancelling…" until the run's row ends (any row, the filter
  aside) or 30 s pass — then "no end seen — cancel again" — the end
  toasted with its status, and a refusal in the server's own words. App-owned runs show a disabled button
  (F4), and parked runs link to their page to decide. Clicking a row opens
  its story in a resizable side pane (`?peek=<id>`, remembered as split
  `live-peek`); Escape closes it.
- Studio's trace page (plan H7): every selection is a URL — `?sel=` the
  row's step-qualified key (`r:<run>`, `s<step>`, `c:<step>:<call id>`,
  run-qualified `s:<run>:<step>` / `c:<run>:<step>:<call id>` when the
  trace holds several runs, `t:<span id>` otherwise; `traceSelectionLink`),
  each row's label a link; a pre-G1 bare `c:<call id>` lands on the first
  match with a note when the id repeats, a pre-H7 `?span=` still selects.
  A model call's chat section reads `gen_ai.input.messages` /
  `gen_ai.output.messages` (+ `gen_ai.system_instructions`) and the legacy
  `gen_ai.prompt` / `gen_ai.completion` keys (plain and `.N.role|content`
  indexed), else says `not_recorded`; an errored chat span of a playground
  run offers "replay from this step" in the run page's drawer (capability
  `playground`, a token that may act; the app's own run gets it disabled,
  saying why); the header lists the trace's root, service, start, error
  and its runs, linked.
- Studio (plan A6.2): the step card's **Wire** tab — beside the Request
  pane's request tab, each attempt's provider request and response from
  the requests row's `raw` (method, URL, headers, the body pretty-printed
  when JSON, base64 said as binary), the withheld headers and query
  parameters listed, the 256 KiB cap's cut and a body not read to its end
  badged `truncated`, a row without `raw` its `not_recorded` (`raw_off`)
  words, a read token `hidden`; addressable as `?step=N&pane=wire`
  (`runLink`'s new `pane`). The panel's Request tab links a step whose row
  carries `raw` to it ("wire ↗").
- core: `WithMetadata(ctx, kv)` — context-carried run metadata: every run
  started under ctx carries the pairs, merged under its own `weft.Metadata`
  (the option wins; same limits; subagents inherit).
- `scope.Header` stamps `weft.page_url` (plan G3) — the request's Referer,
  else Origin + "/", sanitised — on every run under the handler through
  `weft.WithMetadata`; `scope.PageURL`, `scope.PageURLFromContext`,
  `scope.PageURLKey`. Studio's run meta serves it.
- `GET /api/panel-handoff?to=<path>` (plan G3, capability `handoff`, with a
  Token only): a playground-scoped panel token → 303 `<base><to>#token=` with
  a 5-minute read-scoped token for its public id (capped at its own expiry;
  `Accept: application/json` → `{location, exp}`); the server token → 303
  with no fragment; a read token 403 (it cannot widen); `to` a path under the
  base, else 400 — never an open redirect. Logged without the token.
- Studio's run page scrolls a long run (plan H2): past 60 steps the story
  is virtualised (`@tanstack/react-virtual` 3.14.13, pinned), and past 200
  rows the waterfall and the raw events table are too — only the rows in
  view plus an overscan are mounted, the row a `?step=` / `?ev=` / selection
  names always among them; a card's open/closed state is kept per step
  ordinal. A **jump to step** scrubber heads the story (a range plus a
  number box, the `n` key; a scrub is one history entry, pushed on the drag's
  first move and written in place for the rest, and it reads out the step's
  status and model-time offset). The fold keeps its raw events in a ring of 50,000
  (`EVENT_RING_CAPACITY`): a live frame is an O(1) push, never a copy of the
  stream; past the capacity the oldest events are evicted — the fold keeps
  what they contributed, the page says "showing the last N events" (the rest
  are on `api/runs/{id}/events?after=`) and replay is off for that run.
- Studio's Settings page (plan H4): `/settings`, a sidebar entry and the
  palette's Settings item, renders `/api/meta`, `/api/settings/pricing` and
  `/api/runtimes` beside this browser's preferences — the endpoint, versions
  and pid; the stored token (masked, show, copy, clear, switch; its identity:
  server token or a read/playground panel token); the database's kind, path
  and size (badged `hidden` where `db.path`'s rule withholds them); retention;
  the pricing table, editable per browser (localStorage, keyed by the table's
  source and dated, never sent) and downloadable as a `pricing.json` in the
  exact shape `WEFT_PRICING` / `--pricing` load; the capabilities on and off,
  each off one with its `capabilities_off` reason; the runtimes with their
  breakpoints (the playground's editor); the content policy; theme and
  density; the keyboard map. Each section says why when its data is refused
  or its capability is off.
- Studio search and filters (plan H1): the Runs page's free-text box and
  filters (agent, status, tool, model, error, date range, duration, tokens,
  cost, several tags) as removable URL chips, with column sorting, read
  through `/api/search` when a text or search-only filter or a sort is set
  (`/api/runs` otherwise; duration, and without `search` the other
  search-only filters, apply to the loaded rows and say so); a sessions text
  filter over the loaded ids, public ids and agents; a `/traces` index over
  `GET /api/traces` (capability `traces`, a sidebar entry); and the ⌘K
  palette finding runs, sessions, traces and experiments and jumping to
  Playground and Settings, each group under its capability.
- `POST /api/runs/{id}/cancel` (plan H6, capability `cancel`, `Playground(true)`
  only): asks the runtime that started a playground run to stop it, through
  the runtime link's existing cancel frame; the run ends as the core ends a
  cancelled run (`*RunError`, the partial transcript kept, its command
  finished failed; a cancelled run has no run_finish — the live stream
  carries the failed run row). 202 `{run_id, command_id, runtime_id, state:
  "cancelling"}`; 404 an unknown run; 409 the app's own run (cancel arrives
  with F4) or one already finished (a parked run included); 503 its runtime
  is disconnected. Auth is steer's: the dev token or setup A's loopback, a
  playground-scoped panel token inside its public id, a read panel token
  403. `studio/runtime`: `RuntimeServer.CancelRun`, `CancelTarget`,
  `ErrUnknownRun`, `ErrRunFinished`. The Live page (H6.2) derives a running
  row from the event frames it already receives: step = the last
  step_finish's index, tokens = the sum of step_finish usage, TTFT = its
  ttft_ms; cost stays unknown while running (A5.1's rule). Cancelling a parked
  run is 409 "run X is parked: decide its pending calls (approve/deny)
  instead". Cancel is best effort: a cancel lost to a stream reconnect is
  not retried — cancel again; a fork-mode turn re-run after an overflow is
  cancelled by its original run id while it runs.
- Save as test (plan F6): `GET /api/runs/{id}/export?format=wefttest` now
  zips a generated `<agent>_replay_test.go` at the root beside the fixtures,
  which move to `testdata/TestReplay_<run>/`. Unzipped into a package
  directory it is a passing `go test` against the recorded run. It uses
  `wefttest.Replay(t, "testdata")` with stub tools that answer with the
  recorded results; step 0's thinking, tool choice and sequential flag; a
  generated `PrepareStep` for steps whose catalog or settings differ, and
  for compacted steps ("Step N replays from the compacted view", the
  fixture's `compacted_at`); and `StopWhen(StepCountIs(n))`. It compiles
  against the public `github.com/weftgo/weft` API only. `weft export
  --wefttest` reads the new layout and writes the fixtures only. The skeleton keys recorded
  results by (step, call id), so a provider that reuses call ids across
  steps (Google's filled `call_<n>`, llama.cpp-style servers) replays
  correctly; a run steered mid-turn is replayed through a generated
  `wefttest.NewSteers().At(step, …)` source, the header naming each steered
  step; a step whose transcript the recorded run changed any other way
  replays its recorded messages from that step on ("Step N replays the
  recorded messages: the recorded run rewrote them") — the limit: a
  PrepareStep that rewrites per step is replayed from the record, not
  re-derived. `weft export --wefttest` refuses a zip that mixes flat fixtures
  with `testdata/<test>/`.
- Proposed prompts (plan F6): `PUT`/`DELETE /api/agents/{name}/proposal`
  (capability `proposals`; the dev token, or loopback in setup A; every
  panel token 403) hold an agent's proposed instructions in Studio's memory
  (64 agents, forgotten on restart). `GET /api/manifest` shows them as
  `proposed: {instructions, instructions_hash, since}` beside the registered
  instructions. "Keep as prompt" stays a clipboard copy. Nothing is written
  to your files: the download is the hand-off. WEFT-PROMPT.md P3 replaces the
  clipboard with a propose command over the runtime link. A PUT for an agent the
  served manifest file does not name is 409: the proposal would not show.
- `/api/live`: a resume names a run it could not read with
  `event: backfill_error {run_id, kind}` instead of skipping it silently.
- Provider raw bodies (plan A6.1, ADR 0028 §12): `otel.RawBodies(true)` records
  each model-call attempt's wire request and response as a `raw` record
  (headers minus credentials, named in `redacted_headers`; bodies capped at
  256 KiB with the cut counted; SSE as received), for the Local and Studio
  destinations only. It takes effect only under `WEFT_ENV=dev` (never with
  `otel.NoEnv()`); otherwise one WARN and off. The anthropic, openai and
  google adapters capture it (an injected google client is not);
  `core.Reporter.WantsRaw()` and the new `RawPair` fields let third-party
  adapters report it. `obsdb.RequestRecord.Wire`; ClickHouse migration 0005;
  `GET /api/runs/{id}/requests` rows carry `raw` (or `null` with
  `raw_badge: not_recorded`)); a raw record's response carries `complete: false` when its
  body was not read to the end; under RawBodies a Studio destination's log
  batch is capped at 16 records. The gate is a marker on the pipeline's
  LoggerProvider (`RawBodies() bool`): a stock OTel SDK LoggerProvider handed
  to an agent never records wire bodies, whatever its processors answer
  Enabled.
- `GET /api/live?all=1` (plan A7, for the Live page): every run in the
  database on one stream, subagent child runs included (each frame
  names its `run_id`), with the `kinds` the request asks for; a resume
  backfills a bounded set — the runs running or last seen within the
  last hour, newest first, at most 500 (a `Last-Event-ID` older than
  that gets the same set, never an error). Setup A on a loopback Host and the server
  token only: every panel token is 403 `forbidden` on the stream and
  on `POST /api/live-grant` (`{"all":true}` or `?all=1`), as is a Host
  let in only by `AllowOrigins`. `/api/meta` gains `live_all` (true
  under `db.path`'s rule) so the UI knows whether to offer it; no new
  capability (`all` is a selector of `live`). `obsdb.Selector` gains
  `All` (matches every run's frames; an empty selector still matches
  nothing). In the auth matrix for every identity; a run grant's sig
  never opens `all`, nor an `all` grant's a run. `obsdb.RunQuery` gains
  `LastSeenAfter` (both backends, a conformance case) for the bound.
- `GET /api/search` (plan A7, capability `search`): runs found by what
  they said and did. `q=` terms — whitespace-separated, a `"quoted
  phrase"` one term — each a case-insensitive substring of the run's
  prompt (the turn's own: the input record's last user message with text — on a continued conversation the turn's question, not the conversation's opener — else the first user message with text; a message steered in later is never the prompt — with one accepted edge: on a plain Approve/Deny resume, a user message steered in before the resumed batch's first assistant reply is indexed as the prompt, being structurally the same as a resume carrying a new prompt (the transcript has no steered mark); a resume that carries a new prompt — the core records it after the rebuilt tool message, in the record after the input record — has that prompt, a plain Approve/Deny resume keeps the parked question), answer (its last assistant
  text), error or the name of a tool it called; every term required,
  each in any field; never the system prompt, a tool result or the tool
  catalog. Filters `agent`, `tool` (exact), `status`, `model` (exact),
  `error` (substring), `since`/`until` (RFC 3339), `min_tokens`
  (input + output) and `/api/runs`' own `session`, `public_id`,
  `parent`, `all`, `playground`, `tag.<k>=<v>`; `sort=started`
  (default, newest first) `| duration | tokens`; `limit`/`offset`
  paging with `next_offset`. Answers run rows only (no snippet), scoped
  like `/api/runs`: a panel token searches its own public id. In the
  auth matrix for every identity, and in `/api/meta.capabilities`;
  `capabilities_off.search` names a database that cannot search.
- `min_cost=` and `sort=cost` are reserved parameter names: 400 with
  code `unsupported`, "pricing arrives with A5", until A5 wires pricing.
- `obsdb.Searcher` (optional, implemented by `obsdb/sqlite` and
  `obsdb/clickhouse`), `obsdb.SearchQuery`, `obsdb.SearchSort`,
  `obsdb.ParseSearch`, `obsdb.CheckSearch`, `obsdb.ErrInvalidSearch`,
  and the shared text readers (`MessagesText`, `PromptText`,
  `LastAssistantText`, `PromptPreview`) and `obsdb.Weft.Input`; the obsdbtest conformance table
  gains `Search` and `PromptPreview`, so both backends answer alike.
- The run row's `prompt_preview` (`obsdb.RunRow.PromptPreview`): the
  turn's own prompt (as search reads it) cut to 200 runes, on every run row Studio
  serves; absent when the records carried no content (the existing
  `stripped`/`not_recorded` holes say why).
- SQLite migration 0004 (`run_search`, `run_tools` and the FTS5
  trigram table `runs_fts`, kept by the writer; runs stored before it
  are indexed in the migration's transaction). ClickHouse needs no
  migration: search derives the fields at read — once per search, for
  the runs the other filters leave, joined to the run rows — and tests
  every term with `positionCaseInsensitiveUTF8`. A term under three
  runes is matched with LIKE on SQLite, case-folded for ASCII only
  (`lower()` on both sides) where ClickHouse folds UTF-8 — the one
  difference between the backends; `%` and `_` are literal.
- `obsdb.DeriveRecord` reads a non-string `weft.messages.reason` as its
  text — not growth — as the SQLite transcript reader and ClickHouse
  already did: such a record is no longer counted in a SQLite run's
  `message_count`.
- `GET /api/traces` (plan A7, capability `traces`): the trace list —
  every trace the database holds, weft or not, one row each, newest
  first: `trace_id`, `root` (`{span_id, name}`, the earliest span with
  no parent; `null` with `badge: "gap"` when only child spans arrived),
  `service` (the root's, else the earliest span's), `start`, `end`,
  `duration_ms`, `span_count`, `error` (any span ended in error) and
  `run_ids` (the weft runs the trace holds — a subagent child beside
  its parent — the join key to run pages). Filters `since`/`until`
  (RFC 3339, on the trace's start: its earliest span), `service` (exact:
  traces holding a span of that service), `public_id`; paged like
  `/api/runs` (`before`/`before_id`/`limit`, default 50, max 500,
  `next_before`/`next_before_id`, `total`). Scoped like `/api/runs`: a
  panel token lists the traces every span of which carries its public
  id — the rule `/api/traces/{id}` checks, so no listed trace is refused
  and no other public id's run id is listed; a foreign `public_id` is
  403; a trace holding any span without its `weft.public_id` (an
  instrumented HTTP root, a client span under a tool) is not listed to a
  panel token: its detail would be refused. A row carries no span
  attribute, so the read scope has nothing to redact. A trace's start is
  its earliest span, so a trace whose earlier span arrives between page
  reads can be listed again on a later page (never skipped); clients
  dedupe by `trace_id`. In the auth matrix for every identity, the
  rebinding guard's table and `/api/meta.capabilities`.
- `obsdb.DB.Traces(ctx, obsdb.TraceQuery) (obsdb.TracePage, error)`
  with `obsdb.TraceRow` and `obsdb.TraceRoot`, on both backends over
  the spans table (SQLite `spans`, ClickHouse `otel_traces`; no
  migration); the obsdbtest conformance table gains `Traces`.
- `DELETE /api/runs/{id}` and `DELETE /api/sessions/{id}` (plan A7,
  capability `delete`): a run with its subagent children, or every run
  of a session (children included), removed from Studio's database —
  run rows, records, the search index, the spans (the non-weft spans
  below the runs' own too) and the app log lines under them. 200 with
  `{deleted, run_id | session_id, note}`; the session's note says it:
  the app's conversation store is untouched. Studio's tables only: a
  SQLite file that also holds `thread`'s sessions keeps them
  byte-identical (§13.13). The dev token's, or a loopback Host's in
  setup A (no AllowOrigins widening); every panel token is 403, read or
  playground, before anything is looked up; an unknown id is 404. A live
  stream following a deleted run emits nothing more unless the app keeps
  writing; a later write starts a new run row; a later read is 404. In the auth matrix for every identity and in
  `/api/meta.capabilities`; CORS preflights allow `DELETE`.
- `studio.Retain(d)`: a sweeper deletes (as above) the runs that are
  not running — succeeded, failed, interrupted — whose `last_seen` is
  older than `d`, once at `New` and then every `min(d/10, 1h)`, at least
  a minute apart; one Debug line per sweep with its count, a failed
  sweep one Warn line, never a stopped server; a closed DB stops the
  sweeper (one Debug line). Interrupted counts as not running and is
  swept, so `d` must be at least `1h` (`New` panics below it; `--retain`
  / `WEFT_RETAIN` under `1h` is exit 2): a run silent for 30 s reads
  interrupted, and a shorter retention would sweep a live run paused at
  a breakpoint. A running child of a swept parent stays, listed only
  under `all=1` / `parent=*` until swept itself. Build it with `New` and
  `Close`; under `Handler(...)` nothing stops the sweeper but a closed
  DB. Off by default (`0`):
  the local sink keeps everything. `/api/meta.retention` reports it as
  a Go duration string (`"720h0m0s"`), `null` when off. `weft studio`
  and `weft dev` take it as `--retain`, mirrored by `WEFT_RETAIN`.
- `weft vacuum [--db sqlite://path|clickhouse://…] [--retain d]`: one
  sweep when a retention is given (`--retain` / `WEFT_RETAIN`), then
  VACUUM (SQLite) or `OPTIMIZE TABLE … FINAL` on the tables deletions
  mutate (ClickHouse); `make vacuum` runs it on `$WEFT_DB`, else
  `./.weft/weft.db`.
- `obsdb.DeletedSpans`, `obsdb.SpanLink`, `obsdb.SpanKey`: the one rule
  both backends delete spans and app log lines by (a run's own spans and
  the non-weft spans below them — the rule its logs page attributes
  lines by). The obsdbtest conformance table gains `DeleteRun`,
  `DeleteSession`, `Sweep` and `Vacuum`. ClickHouse deletes with
  lightweight `DELETE FROM … WHERE` (ClickHouse 23.3 or later); its
  TTLs are unchanged, retention is additive. No migration on either
  backend.
- Cost (plan A5.1): Studio prices usage at read time — never stored,
  nothing fetched. `studio.Pricing(table)` sets the table
  (`studio.PricingTable` / `studio.ModelPrice`: `{provider, model_glob,
  input, output, cached_input, cache_write, reasoning}` in USD per 1M
  tokens, `model_glob` a `path.Match` pattern over `provider/model`, first
  match wins); the default is `studio/pricing.json` (`studio.DefaultPricing`,
  dated, generated by `go generate ./studio/...` from
  `studio/internal/pricingdata`: Anthropic, OpenAI and Gemini list prices,
  models with long-context tiers left out). `WEFT_PRICING=<path>` or
  `--pricing <path>` on `weft studio` / `weft dev` (flag wins;
  `studio.LoadPricing`) replaces the table whole; a file that does not load
  stops Studio with the path and the field. Run rows (lists, documents,
  children, search hits, live frames, experiment runs, the export) carry
  `cost_usd` (the run's own model calls: its usage minus its subagent
  children's), `cost_known`, `cache` (`exact` | `not_reported`: no cache
  split reported), `cost_badge` (`unpriced` | `unknown`) with
  `cost_reason` — null, never 0, when unpriced — and `delegated_cost_usd` /
  `delegated_cost_known` / `delegated_cost_reason` (its descendants' own
  costs, never folded into its own). Session rows carry the turns' own +
  delegated `cost_usd`; a step (`GET /api/runs/{id}/steps/{n}`) prices its
  own model call, and its `children[]` carry own and delegated. `GET
  /api/search` takes `min_cost=` (USD, the whole bill) and `sort=cost`
  (most first, unpriced last), applied in Studio over the match's first
  2,000 runs (`cost_sort_cap` when cut). `GET /api/settings/pricing`
  (capability `pricing`): the effective table in the file's shape plus
  `source` (`default` | `file` | `option`), `dated` and the file's `path`
  (the server token's, or loopback setup A's). No obsdb change.
  `cache` is `exact` | `not_reported`: `not_reported` when the adapter
  reported no cache split (no cached-input or cache-write tokens); every
  input token is then priced at the input rate, and a known figure carries
  `cost_reason` "no cache split reported; a cached prompt may be
  overstated". A running run's own cost is unknown (`cost_usd` null,
  `cost_badge` `unknown`, `cost_reason` "running: usage arrives at run
  end"), as is one whose end never arrived; its delegated figure is still
  computed. Only core Subagent children (run ids `<parent>/<step>/<call>`)
  are subtracted from a parent's own cost and summed into
  `delegated_cost_usd`; a thread/pool child session's run and another
  agent's `Generate` inside a tool handler are linked to the parent but
  never rolled up, so they keep their own figures on their own rows. A step
  is priced at the model that answered it (`gen_ai.response.model`), else
  the requested one; a run's own figure is priced at the run's model.
  `gcp.gemini` (the span's provider name) prices as `google`.

- Cost in Studio (plan A5.2): under capability `pricing` the runs table
  gains a Cost column (the run's own `cost_usd`, its subagents'
  `delegated_cost_usd` under it; sortable — through `/api/search?sort=cost`
  when the Studio also has `search`, else over the loaded rows, which the
  header says), the run header shows `own $x · delegated $y` as two
  numbers, a step card its own model call's cost once the step doc is
  read, the compare page a cost column per run (informational: an unknown
  side is "not comparable", never a change), the playground its result
  cards, compare row and matrix cells with a per-variant total summed
  client-side, and the sessions list a cost column. An unknown figure is
  "—" with `cost_reason` as its tooltip, never `$0.00`; a figure priced
  without cache splits carries a "cache not reported" hint. Without
  `pricing` no cost cell is drawn. The web mirrors learn the cost fields,
  `meta.pricing`'s object, `cost_sort_cap` and the settings route's shape.

### Changed

- `DB.Sweep` (and so `studio.Retain` / `weft studio --retain` / `weft vacuum
  --retain`) deletes in passes of `obsdb.SweepBatch` (500) root runs: one
  writer transaction each on SQLite, one round of DELETE mutations each on
  ClickHouse. A first sweep of a large database no longer blocks ingest for
  its whole length. The count it returns is the total; on an error, earlier
  passes stay deleted.
- obsdb/sqlite migration 0006 adds a partial index for an experiment's runs.
  obsdb/clickhouse migration 0007 adds a bloom_filter skip index on
  `weft_runs.ExperimentID`. Parts written before 0007 are indexed when merged;
  `ALTER TABLE weft_runs MATERIALIZE INDEX idx_experiment` does it at once.
- `GET /api/experiments` lists the 50 most recently updated experiments by
  default (`?limit=` up to 200) and adds `total`, the number saved.
- Deleting or sweeping a Subagent child moves its usage into the parent's own
  cost: own = the parent's usage minus its remaining children's.
- `scope.Header` now stamps `weft.page_url` (the request's Referer/Origin
  path) on every run under the handler, on every destination. A secret in a
  page PATH would travel: strip it in your own middleware before
  `scope.Header`, or do not use `scope.Header` on such routes.
- Studio: the experiments list follows `GET /api/experiments`' cut. It
  shows the 50 most recently updated, says "newest N of total" when more
  are saved, and loads 50 more at a time up to 200 (`?limit=`).
- Studio: Experiments is not offered under a panel token (read or
  playground) in the sidebar, the ⌘K palette or the playground's history,
  because the experiments routes refuse every panel token. Opened by URL,
  the page shows the hidden badge ("dev token only") and fetches nothing.
- Studio and the devtools panel: "keep as prompt" with no agent registered
  says "copy the edited prompt" / "copied" and no more. It no longer
  mentions a proposal, since there is no agent to propose to.

### Changed — breaking

- The wefttest export zip is no longer flat: fixtures sit under
  `testdata/TestReplay_<run>/` beside the skeleton. Unzip it into the
  package directory, not into `testdata/<TestName>/`.

- `otel.Pipeline.LoggerProvider()` returns `*otel.LoggerProvider` (embedding
  `*sdklog.LoggerProvider`, carrying the RawBodies marker); method calls are
  unchanged, a variable typed `*sdklog.LoggerProvider` must use
  `.LoggerProvider`.

- Studio's `GET /api/meta` `pricing` is an object, `{source, path?,
  dated, models}` (plan A5.1), where it was `false`; a client reading it
  as a boolean must read `pricing.source` instead.
- `obsdb.DB` gains `Traces`: a third-party `obsdb.DB` implementation
  must add the method (group its spans by trace id; the obsdbtest
  `Traces` case pins the answer).
- `obsdb.DB` gains `DeleteRun(ctx, id) error`, `DeleteSession(ctx, id)
  error`, `Sweep(ctx, before) (int, error)` and `Vacuum(ctx) error`: a
  third-party implementation must add them, touching its own tables
  only (the obsdbtest `DeleteRun`, `DeleteSession`, `Sweep` and
  `Vacuum` cases pin the answers).

### Fixed

- Studio's "save as test" skeleton puts the run id (and a compaction's hash) in
  its header as a quoted string. Run ids are not validated on ingest, and an
  id built to close the comment could inject code into the generated test.
  An agent whose name is a Go keyword gets the package stem `replay_<name>`.
- Studio's "save as test" skeleton writes a multi-line text (a prompt, a
  system prompt, the final text) as a raw string only when every byte can
  sit in one (`strconv.CanBackquote`); a text carrying a NUL or a byte-order
  mark past its start is quoted instead. Before, `go/format` rejected the
  generated file and `GET /api/runs/{id}/export?format=wefttest` answered
  500 for the whole run.
- `weft vacuum` with no database at the path refuses with "no database at
  <path> (nothing to vacuum)", exit 1, and creates nothing.
- thread/pool: an approval recorded while a nested child was still
  parking (requests mirrored, phase not yet parked) was skipped by every
  pump and the child stayed parked for good; `park` now reads the
  parent's verdicts again once parked and arms the resume itself, and a
  park generation counter keeps a stale park's replay from reaching a
  re-parked run (the TestConcurrentDecide CI flake).
- Studio: a peek (Live) or run page for a deleted run (404) kept
  `/api/live?run=` open and polled its events every 2 s for good. The
  story reader now waits for the run's document and follows nothing once
  the run answers 404. The "not found" copy stays.
- Studio: the Agents page draws a 403 as the hidden badge (as Settings
  does) instead of plain red text, and stops re-asking every 5 s. An
  experiment with no runs shows the cost cell's reason ("no run to total")
  instead of a bare dash.
- Devtools panel: after an "open in Studio" hand-off answers (opened,
  refused or failed), the note line always carries the plain Studio link,
  never the minted one. A browser that blocks the late popup still leaves
  a link to click.
- Devtools panel: a start that settles after the element was removed
  returns without touching the element.
- Studio: a panel token handed over in a link's `#token=` (the hand-off's
  5-minute read token) serves that tab alone, held in its `sessionStorage`
  (kept across a reload; a duplicated tab inherits it). It no longer
  replaces the browser's stored dev token in `localStorage`, which left
  every tab 401 five minutes later. Settings labels it "this tab only" and
  its clear drops it alone, leaving the stored token. A 401 under it drops
  it and retries with the stored token; with none stored, the token prompt
  says the hand-off token expired instead of "the stored token was
  refused". A dev token in the fragment is stored as before.
- Studio: `/experiments/A` → `/experiments/B` (the palette) is a fresh
  page. A's cell states, polls and pending "re-run all" toast no longer
  carry onto B.
- Devtools panel: a hand-off answer that lands after the element was
  removed opens no tab and leaves no note.
- The panel budget test asserts the committed `panel.js` is within the
  80 KiB cap and pins every ledger row.
- Security: Studio's setup A (no `Token`) refuses a cross-origin write. The
  loopback `Host` check proved a request reached this machine, not that this
  origin's page sent it: any site's page could `fetch(…, {method: "POST",
  mode: "no-cors"})` a text/plain body past it, with no preflight — saving an
  experiment, cancelling a run, starting a playground run, deciding an
  approval, steering. An unsafe method (anything but GET/HEAD/OPTIONS) whose
  `Origin` is neither one whose `host:port` is the request's `Host` nor an
  `AllowOrigins` entry (`null` included), or that carries no `Origin` but `Sec-Fetch-Site: cross-site`, is
  now 403 "cross-origin write refused (setup A)", naming the Origin. Same-origin
  pages, clients that send neither header (curl, the runtime link) and
  `AllowOrigins` origins are unchanged; reads are unchanged (setup A sends no
  CORS headers, so a foreign page cannot read the answer).
- Studio's cost of a set with no usage at all (a run-less experiment) is a
  known $0 with cache `exact` and no `cost_reason`; it carried the "no cache
  split reported; a cached prompt may be overstated" note.
- Studio's delegated cost carries its cause: a subagent whose figure is
  unknown (running, its end never arrived, its children unreadable) makes the
  parent's delegated figure — and a session's or experiment's summed
  `cost_badge` — `unknown`, not `unpriced`.
- The `weft.raw` record is encoded without HTML escaping: a body of `<`, `>`
  or `&` no longer grows sixfold, so a record of two full ordinary bodies
  stays ≈ 650 KiB. JSON's own escaping still makes a body of control bytes
  six times its size (≈ 3 MiB a record), so under `otel.RawBodies` a Studio
  destination now also splits each log export at 12 MiB: a burst of such
  records no longer forms a post over Studio's 16 MiB ingest limit, which
  was refused whole and lost every record in it (the Local sink writes each
  record synchronously and needs no split; ADR 0028 §12).

- studio: a cost whose badge is `unknown` (a subagent or turn still running,
  or whose end never arrived) now says so in its reason — "a subagent run's
  cost is not known yet: …", "a turn's cost is not known yet: …" — instead of
  "… is not priced: …", which stays for an `unpriced` figure (no pricing row).

## 0.13.0 — 2026-10-10

Phase 4 of the devtools plan (replay): replay from any step, call or
message on both surfaces with the ack naming what parks; the transcript
edited in place (`user`, `tool_args`, `insert` beside `tool_result` and
`reply`) with the "will be sent" preview of the exact first request —
pure assembly in Studio's process, proven equal to what the runtime
sends — and a replay across a compaction boundary reproducing the
model's input (`transcript?step=N`, `weft.replay.view`); the option lab
complete (params, tool choice, park_on, only_tools, a `ModelResolver`);
the step-aligned compare (`GET /api/diff`, N-way in Studio, 2-way in the
panel); Studio URLs that mean something; resizable panes and density;
notifications; and a pre-release review of the whole phase (the Go,
panel and Studio fix sections below — among them the version-skew
refusals, the ack window and the accepted-ack heartbeat). Tags
`core/v0.13.0` (three accessors added, `weft.override.tool_choice`
spelled `auto`) and `v0.13.0` — a minor bump, as
`studio/runtime.TranscriptEdit` is no longer comparable (Changed —
breaking).

### Added

- **Step-aligned run diff, Go half** (plan E3.1): `GET /api/diff?a=&b=`
  (capability `diff`) compares two runs step by step, aligned by step
  ordinal — each step assembled as `runs/{id}/steps/{n}` assembles it and
  reduced to its system prompt (by hash), tool calls `name(args)`, tool
  results, assistant text and usage; rows carry `changed` and `changes`
  (`missing` for a step one run lacks) and `unknown` (a column a side did
  not record — never compared, never read as the same), `summary` names
  the changed steps. obsdb gains the `response_cap` hole cause (the
  diff's step cap).
  A compaction view or a subagent call is a mark on its side, never a
  change by itself; every absent column is a hole from the one table, and
  a read-scoped panel token gets `system: null` under `hidden`.

- **Step-aligned run diff, web half** (plan E3.2): both surfaces render
  `GET /api/diff` through one module, `studio/web/src/lib/stepdiff.ts` —
  rows by step ordinal, a cell per compared field (`same`, `changed`,
  `not comparable` for the server's `unknown`, never "same"; `missing`),
  one "changed at step N" marker per changed step, each side's marks as
  chips and its holes (and the response's `truncated`/`response_cap`)
  as badges from the one table. Studio gains the N-way `/compare` page
  (N−1 diffs against one base; `compareLink` in `lib/links.ts`, whose
  `step` lands on a row), reached
  from the run header ("compare with source" via `weft.forked_from`,
  "compare with…"), the replay drawer's finished result and the
  playground's variants; the devtools panel's experiment result pane
  draws the 2-way table (the replayed run against its source). Gated on
  capability `diff` on both. Panel: +2,225 B gzip (ledger row E3).

- **The option lab, Go half** (plan F3.1): a playground command's
  `overrides` gains, beside the numeric `options` (`max_steps`,
  `parallelism`, `temperature` — unchanged), `params` `{top_p?,
  max_tokens?, stop?, seed?}`, `tool_choice` `{mode: auto|any|none|named,
  name?}`, `park_on: []` and `only_tools: []`, applied as
  `weft.Params`, `weft.ToolChoice`, `weft.ParkOn` and `weft.OnlyTools`
  (and so on the run's `weft.override.*` attributes). Narrowing only,
  validated by Studio (§10.4: 400 for a tool the agent lacks, an
  unknown mode, a named choice the command turns off or parks — or,
  with no `tool_choice` sent, an agent default naming a tool the command
  turns off — `top_p` outside 0..1, `max_tokens` ≤ 0, more than four or
  empty `stop` sequences; 403 for
  `only_tools` outside `tools_enabled`) and again by the runtime before
  its ack. An old command (none of the new fields) validates as before;
  a runtime older than the option lab (no registered `defaults`) is
  refused the four new fields (400 `runtime predates the option lab:
  upgrade weft/runtime …`), since it would run the command without them.
  **`runtime.ModelResolver(func(ctx, name) (weft.Model, error))`**: a
  model override outside `runtime.Models` is resolved by the app's code
  before the ack, bounded at 10 s ("resolver timed out"; one that ignores ctx is
  abandoned) — the run uses the returned model (named on the run
  row and `weft.override.model`), an error rejects the command with
  `model <name>: <the error's text>`; without a resolver Studio refuses
  the name (400 `unknown model …: register it with runtime.Models or
  add runtime.ModelResolver`). The registration and `GET /api/runtimes`
  carry per agent `resolver` and `defaults` `{max_steps, parallelism,
  thinking, temperature?, top_p?, max_tokens?, seed?, stop?,
  tool_choice}` (the form greys them; `testdata/api/runtimes.golden.json`
  pins the shape). Core gains the plain accessors `(*Agent).Params`,
  `(*Agent).Thinking` and `(*Agent).ToolChoice`.

- **The option lab, web half** (plan F3.2): one option form on both
  surfaces — Studio's experiment form (the playground page and the run
  page's replay drawer) and the devtools panel's experiment drawer —
  for every knob the command carries: `options` (max steps,
  parallelism, temperature), `params` (top_p, max tokens, stop — one
  sequence per line — and seed), `tool_choice` (auto, any, none, named
  and its tool), `only_tools` and `park_on`, beside the prompt, tools,
  model and thinking. Each knob shows the agent's default from
  `GET /api/runtimes` greyed (the placeholder; thinking's and
  tool_choice's in their default option); an override is drawn in
  colour with a one-click reset; an empty knob, or one equal to the
  default, sends nothing. The model is a select over the runtime's
  models plus free text when the agent's runtime holds a
  `runtime.ModelResolver` (the app resolves it; its refusal is the
  command's reason, shown as given). The form mirrors the server's
  rules by name (`lib/experiment-body.ts`'s `labOverrides`: caps lower
  only, ranges, a tool the agent lacks, only_tools inside
  tools_enabled, a named choice turned off or parked, the agent's named
  default turned off): a refusal is said on its knob in the server's
  words and holds Run; a tool turned off is greyed out of only_tools
  (a ticked box stays clearable) and the named tool picker (park_on
  lists every tool); a 0 cap is no bound; the server's 400 is still
  shown verbatim when it refuses anyway. A registration without
  defaults (an old runtime, or a Studio older than the field) shows the
  knobs with no default, greys the seven option-lab controls it cannot
  carry and says why. The ack preview follows `park_on` and `only_tools`.
  Both surfaces post byte-identical JSON for the same choices (pinned by
  a table over `testdata/api/runtimes.golden.json` and a parity test).
  Panel: +2,821 B gzip (ledger row F3, of a +3,072 B estimate).

- **The transcript as of a step** (ADR 0029): `GET
  /api/runs/{id}/transcript?step=N` adds `step`, `messages` — what step
  N's model call carried, the replay prefix for `from_step` N — and
  `compacted_at`, the run-scope compaction view that request carried
  (`{index, step, from_seq, to_seq, hash, replaced, entries}`, counts and
  hashes only) or `null`; `badge: "derived"` for a run without request
  records; `messages: null` under `badge: "hidden"` for a read-scoped
  panel token when the step carried a view; 404 past the run's last
  step; 409 with the hole (`gap`, `stripped`) when the records do not
  rebuild the step. `messages` is what step N's model call carried —
  the replay prefix for N > 0 (`from_step` 0 re-runs step 0's
  PrepareStep). Without `step` the route is unchanged.
  `obsdb.MessagesAsOf` (with `AssembleStep`, `ViewOf`, `ApplyView`,
  `StepMessages`, `StepMessagesError` and `ErrStepMessages`, and a
  `MessagesAsOf` case in the `obsdbtest` conformance suite) is the one
  assembly: the route reads through it, the fixture export and
  weft/runtime splice views through `ApplyView`.
- **ADR 0029** (the replay input is what the model saw): the prefix
  rule, the transcript route's `step`, the edit refusal inside a
  compacted range, the child-as-source rule and `weft.forked_from` as
  the link back.

- **Replay from here** (plan F1, both surfaces): on Studio's run page
  every step card, tool call, steer and subagent row carries hover and
  focus verbs — replay from this step, edit this result and replay
  (`from_step` N+1, the call's recorded result pre-filled), edit the
  prompt and replay (the step's own system prompt from its request
  record; where that is unreadable, Studio's drawer pre-fills the
  registered prompt without saying so — a fix is queued — while the
  panel says it, badged), re-run, and continue here
  with a new message (a fork; a session's top-level turn only) — that
  open a non-modal replay drawer on the run page. `from_step` and every
  edit's step are the step ordinal. Above Run, the ack preview lists per
  tool of the step's catalog whether its calls run, are substituted from
  the record (else parked) or park, and why ("replay safe · runs",
  "replay never (unannotated) · substituted from the recorded result
  when the call repeats, else parked", "breakpoint · parks", …; a
  catalog hole badged, the registered tools judged instead), the kept
  prefix ("steps 0–2 kept · the compacted prefix (what the model saw at
  step 3)"), and holds Run while side effects allow would be refused or
  a pre-filled edit has no field. A child row replays the child as its
  own run of its own agent (an unregistered agent is said, Run held).
  The verdicts, drafts and gates are one framework-free module,
  `studio/web/src/lib/replay.ts`, both surfaces import. The run header
  and the runs table say "replay of <src> from step N" from
  `weft.forked_from`, linking the source step; the playground's step
  number became a picker over the source run's steps (the number field,
  badged, when they cannot be read).
- **The panel's replay verbs** (plan F1.3): the same verbs on the
  panel's Story view (steps, calls, steers, the child row and the
  child's steps), the experiment drawer pre-filled from the verb's draft
  with the same ack preview and prefix line, focus moved into the drawer
  and back to the verb on close; the command posted is byte for byte
  Studio's for the same draft. The result pane links the new run to its
  source step (selected in the panel, ⤢ to Studio), and an experiment's
  turn row gets a "↖ t3 step 2" button beside its "experiment of" chip
  (⤢ to Studio's run page when the source is not in the list). The
  verbs are drawn only under the `playground` capability and a token
  that may act (`canReplay`, Studio's gate). panel.js grows by the F1
  row of the size ledger.

- **Edit the transcript in place, Go half** (plan F2.1, ADR 0029
  decision 8): a playground command's `transcript_edits` gains a
  discriminator `kind` and three kinds — `user` (rewrite a kept step's
  user message; step 0's is the turn's prompt, `index` picks among a
  step's several), `tool_args` (rewrite a kept call's `args`, checked
  against the tool's schema by the new `obsdb.CheckToolArgs` and refused
  in the loop's `INVALID_INPUT: tool "x": field "f": expected …, got …`
  words; the result stays unless also edited) and `insert` (a user
  message at a step boundary `0..from_step`, where a steer delivered
  there would land). An edit without `kind` reads as before (`tool_result`
  or `reply`). Validated in the runtime before the ack and mirrored in
  Studio in one wording; an edit or an insert boundary inside
  from_step's compaction view is refused. The replayed run carries
  `weft.edits` (run metadata: `<step>:<call_id>:args` is the "args
  edited" mark, beside `:result`, `:reply`, `:user`, `:insert`) in the
  source run's coordinates — its step ordinals and call ids — and a
  child run inherits it with the rest of the metadata. `args` empty or
  `null` reads as absent. In substitute mode a kept call re-issued with
  its edited arguments is answered from the kept result; one re-issued
  with the original arguments parks. `CheckToolArgs` reads a null field
  as absent (encoding/json's rule) and refuses a non-integral literal
  for an integer.
  `POST /api/playground/preview` (capability `preview`, with the
  playground; `capabilities_off` says why when off) takes the command's
  body and answers the replay's exact first request — system, messages,
  tools, model, params, thinking, tool choice, each with its source —
  beside the request step `from_step` recorded and their diff (messages
  aligned same/changed/added/removed, system, tools added/removed, the
  knobs), plus warnings (PrepareStep runs again, a compacted or derived
  prefix, the scripted engine's refusals) and the overrides no
  registration checked (`unchecked`). Pure assembly in Studio: no model,
  no tool, no runtime needed; a read-scoped panel token gets the system
  prompt and the catalog hidden (its args edits checked for shape only,
  its registration refusals generic, the messages hidden under a view).

- **Edit the transcript in place, web half** (plan F2.2): on both
  surfaces — the run page's Story and the devtools panel's Story tab —
  step 0's prompt, a steer, a call's args, a tool result and a call-free
  reply become a textarea in place (click, or Enter on its pencil), with
  revert and a red "edited" chip, and each step boundary takes an
  inserted message; the edits accumulate into the replay drawer's one
  command (opened, verb "Edit the transcript and replay", on the first
  edit), which lists them by kind, keeps `from_step` at what they imply
  (an edit of step N needs N+1, an insert at N needs N) and runs them on
  engine live (the scripted engine is refused with edits, and the drawer
  says why). `transcript_edits` always carries `kind` (`args` an object,
  each kind's own fields only) — one module, `src/lib/edits.ts`, for both
  surfaces' bodies, byte-identical. An args edit is JSON checked in the
  editor against the tool's schema from the step's catalog record —
  `checkArgs`, `obsdb.CheckToolArgs`' rules by name — and a refusal is
  said on the field in the loop's `INVALID_INPUT` words and holds Run.
  The drawer's "will be sent" preview (capability `preview`) draws
  `POST /api/playground/preview` through `src/lib/preview.ts`: the
  system's state, the messages with an op per row (a changed row shows
  was and will), tools added/removed, the knobs that changed, every
  `*_badge` from the honesty table, the warnings and the `unchecked`
  line; re-read (debounced) as the edits and overrides change, its 400
  shown verbatim and holding Run; none for a fork. A replayed run whose
  row carries `weft.edits` draws its chips ("args edited", "result
  edited", "edited", "inserted") with the edited args or result read
  from its input by call id; a child run, which inherits the mark, draws
  none. The panel no longer says "reading the step compare…" forever
  for a run that settled before meta reported `diff`. Panel: +4,822 B
  gzip (ledger row F2, estimate 4,096 B).
  Review fixes: an editor follows its command (Escape in a Story
  textarea keeps the drawer; closing it, another verb, a drop or the
  drawer's own field resets the text); a steer is its own step's user
  message, never another step's same words; an insert is offered only
  where the from_step it implies is accepted, and after the last step
  when its calls are answered; a fork with edits holds Run and says why
  on both surfaces; `checkArgs` refuses an integer past int64 and a
  number the browser would change (9007199254740993, 1e400), and an
  engine without number literals marks the integer check unchecked; the
  args schema is the run's last catalog per tool, as Studio's server
  checks it; `weft.edits` keeps a call id holding `:` and maps a reused
  call id by its step ordinal (else no value); Run waits while the
  preview of a changed command is pending; an edit inside the view
  `transcript?step=N`'s `compacted_at` names is refused before posting,
  in F1.1's words.

- **Studio URLs that mean something** (plan G2): every piece of page
  state lives in search params, through `lib/links.ts` — the trace
  page's selected span and view (`traces/<id>?span=…&view=chat`), the
  raw view's filters and open event (`runs/<id>?view=raw&q=…&hide=…&ev=…`),
  and the playground's source run, step, agent, runtime and engine
  (`playground?run=…&step=…&agent=…&runtime=…&engine=scripted`, written
  from its controls; a change from Back, Forward or a followed link is
  adopted, never written back; the page never writes a prompt into the
  query). Cursor
  moves (a selection, a filter, the open event) replace the history
  entry; view changes (tab, view mode) push one, so the back button
  walks views, not cursor moves. A **copy link** button in the run
  header and a `y` key (also in the ⌘K palette) copy the page's
  canonical link — the bare URL with its state, never a fragment, a
  token, a panel scope or prompt text (`canonical` strips `token`,
  `access_token`, `sig`, `weft_scope`, `input`, `instructions`) — and
  say "copied"; a refused clipboard falls back to `execCommand("copy")`,
  then a prompt with the link, and says "copy failed". Every page sets
  `document.title` through one helper (`lib/title.ts`):
  `run s_…-t3 · succeeded · weft studio`, `trace … · weft studio`,
  `playground · weft studio`. The ⌘K palette's items render inside
  cmdk's root (opening it threw before).

- **The replay drawer's state in the URL** (plan G2, after F2.2): the
  run page writes `?replay=<verb>&from=<n>[&of=<run>]` (`lib/links.ts`'s
  `replaySearch` / `replayFromSearch`, keys of `RunSearch`) with replace
  when a verb or the editor opens the drawer and as its step changes,
  and clears them on close; a fresh load with those keys reopens the
  drawer on that step from the verb's own draft (re-run, continue here,
  edit the prompt at `from`, else replay from `from`; the draft's edits
  are not in the link). The run page's tab is titled while the run
  loads.

- **Studio panes and density** (plan H3): the installed
  `react-resizable-panels` draws three resizable splits — the trace
  page's span tree beside the selected span, a run step's story beside
  its open Request pane (from 1024 px; one size for every card) and the
  playground's config column beside its runs column — dragged, or moved
  from the keyboard (arrow keys, `Home`/`End`, a visible focus ring from
  the theme's ring token), each pane held at a minimum size, the sizes
  remembered per device in `localStorage["studio.panes.<split>"]`
  (garbage reads as the default; never the URL), and "Reset pane
  layout" in `⌘K`. A density toggle, comfortable | compact, beside the
  theme toggle and in `⌘K`: `data-density` on `<html>` (set before paint,
  stored as `studio.density`), compact scaling the theme's spacing and
  type tokens. At phone width the splits stack and the playground has no
  horizontal scroll (its header wraps, its result cards and matrix table
  scroll in their own box). The playground's and the experiment form's
  native `<select>`s are the shared `components/ui` Select
  (`select-field.tsx`; same labels, values and options) and their raw
  Tailwind colours theme tokens; a lint rule (`controlRules`,
  `src/lib/controls.lint.test.ts`) refuses either coming back (arbitrary
  colours and Tailwind 4.3's new palettes too). A split never remounts
  its panes' contents — a step's Request pane opening, the window
  crossing the phone width — so a draft survives; a double click on a
  handle resets that split to its defaults and forgets it; the theme
  and density toggles hydrate the prerendered shell without a mismatch
  whatever is stored (the theme toggle's own hydration mismatch with a
  stored choice is fixed with it); each select's visible caption names
  and focuses it.

- **Studio notifications** (plan H5): the installed `sonner` toaster is
  mounted in the app shell (bottom-right, the Studio theme, no hotkey),
  every toast worded in `src/lib/notify.ts` and raised once per
  instance: an experiment finished (`experiment <run> finished ·
  <status>`, "open"; a matrix once, when its last cell settles), a run
  parked (`run <id> parked at <tool>`, "open", and "approve" through
  `POST /api/runs/{id}/approvals` for one parked call of a
  runtime-started run when the playground is on and the token may act;
  posted once however often it is clicked; the toast goes when the park
  ends), "finished" only from the run's settled row, never guessed,
  a runtime connected or disconnected (one toast per flip of its
  `connected` in `GET /api/runtimes`, read every 5 s by the shell —
  the live stream has no runtime kind; a runtime gone from the list is
  disconnected once, a failed read says nothing), the live stream giving up
  (`live updates stopped after 6 reconnects`, "retry" — it stopped in
  silence before; `openLive`'s handle gains `gaveUp()` / `retry()` and
  `onGaveUp`), and copy link ("link copied" / "copy failed", beside the
  header's own words). `next-themes` is no longer a dependency.

- **`GET /api/runtimes` says whether each runtime is connected**
  (`connected`: it holds a live command stream now). A runtime whose
  stream dropped stays listed until its commands resolve — 10 minutes,
  24 hours with breakpoints — and a reconnect re-registers under the
  same id, so the list alone could not say a runtime went away;
  `RuntimeView.Connected` in `weft/studio/runtime`.

- **`weft.replay.view`** (ADR 0029 §2): a playground replay from step
  N > 0 carries how its prefix knew what the model saw — `transcript`,
  `compacted:<index>`, `derived` (no request record placed N's
  messages) or `unknown` (the thread path with Studio unreachable or
  content-off records, a Studio older than `?step=`). The last two are
  the run's `derived` hole on `GET /api/runs/{id}` and in the export,
  its reason naming the mark and the source step (a subagent child of
  the replay inherits the mark but carries no hole: its input is its
  tool call's arguments), pinned by `run-replay-view.golden.json` — the
  fallback used to be a log line only. obsdb gains
  `Assembler`/`NewAssembler`; studio/runtime gains
  `AgentRegistration.ManifestToolSchemas`.
- **Accepted-ack heartbeat** (weft/runtime ↔ Studio): registration
  carries `heartbeat_ms` (one minute in `weft/runtime`); while a
  playground command is in the runtime's hands — waiting for a run slot
  or running — the runtime re-posts its accepted ack at that cadence. A
  repeated accepted ack is liveness: on an accepted row it restarts the
  finish watch, on a lost row it is a 409 (and the beating stops), on a
  finished row a no-op. Studio watches a heartbeating runtime's accepted
  command from the accepted ack on, stream open or not, and marks it
  lost after max(`FinishDeadline`, three beats) of silence with the new
  reason "accepted, but no finish and no heartbeat from the runtime in
  <d>" — so a command nobody runs any more is marked lost even while the
  stream stays open, and a run that keeps beating is never marked lost
  however long it takes. A runtime-supplied `heartbeat_ms` is taken at
  most `FinishDeadline` (the watch is at most 3 × `FinishDeadline`); a
  runtime older than the heartbeat (`heartbeat_ms` 0) is watched only
  once its stream ends, as before. The heartbeat stops — its in-flight
  POST abandoned, its goroutine gone — before the finished ack is
  posted, so an accepted ack never lands after the finished one.

### Changed

- **An old runtime's registration shows no defaults** (plan F3.1's
  fix): `GET /api/runtimes` omits an agent's `defaults` when its runtime
  registered none (one older than the option lab) instead of filling in
  its caps and tool choice `auto` — a client tells it from a current one
  and greys the option lab's knobs before Studio's 400.
  `studio/runtime.AgentView.Defaults` (new this release) is
  `*AgentDefaults`, nil for that runtime.

- **A scripted command with transcript edits is refused before the
  ack** (runtime and Studio, one sentence: `the scripted engine would
  replay the recorded turn N, which answered a different prompt:
  transcript edits need engine live`) instead of being accepted and
  failing its first step with "no recorded turn". The runtime also
  refuses edits with `from_step` 0 itself (Studio did), so a fork
  command can no longer carry edits it would drop unread; `input`
  beside `from_step` > 0 now says to edit step 0's user message instead.

- A playground command's sampling override (`options.temperature`, now
  also `params`) is laid over the agent's own `weft.Params` instead of
  replacing it: a run-level `Params` replaces the struct whole, so a
  temperature-only command used to drop the agent's `MaxTokens`, `TopP`,
  `Stop` and `Seed` to the adapter defaults. The run's
  `weft.override.params` (and so `weft.override.hash`) now carries the
  merged values: override hashes from before and after this change are
  not comparable (ADR 0029 §7). An absent field keeps the agent's own
  value; a command cannot clear the agent's `stop` or `max_tokens`.

- **The replay prefix across a compaction**: a playground command with
  `from_step` N whose source step N's request carried a `PrepareStep`
  compaction view feeds the replay that view — the model's exact input
  — instead of the original transcript, on the local obsdb, Studio and
  thread source paths alike; from a step whose request carried the plain
  transcript, the original prefix. The scripted engine keys the recorded
  turns over the compacted prefix too. The replayed agent's own
  PrepareStep runs over that prefix: the model's input is exact when it
  is idempotent over its own output (a count- or index-keyed one
  re-shapes it). A thread-stored source whose records cannot rebuild the
  step (content off) replays on the thread's messages with a warning
  instead of being refused.
- **`from_step` at the step count**: accepted on both sides when the
  source's last step ended in answered tool calls (a run that failed or
  hit its budget before answering them) — the replay's first model call
  answers them; `transcript?step=` serves that prefix (the step's
  request, else the whole transcript as `derived`). After a call-free
  reply it is still refused.
- **Edits inside a compacted range are refused** on both sides (Studio
  400, the runtime's reject) in one wording: `call "c_1" of step 0 was
  compacted away before step 3's request (messages [1, 3) replaced by
  1): the model never saw it there; edit from an earlier from_step`.
- **A subagent's child run as source** replays as its own run of the
  child's agent — registered on the runtime by that name — with
  `weft.playground` and `weft.forked_from="<child id>#<from_step>"`, no
  parent linkage; a parent replay from the step that called the child
  substitutes (or parks) the never-class `Subagent` call, and the child
  is not re-run (pinned, no behaviour change).

- **"jump to", not "replay"**: the run page's playhead buttons read
  "jump to this step/call/steer (event #N)"; "replay" now names the
  replay verbs only.
- **The panel's ⎇ Continue from step N and its "compare in Studio"
  hand-off** send the step being read by its ordinal (as every verb
  does; `stepPosition` is gone, `readStep` replaces it), and its token-scope reading is
  `lib/replay.ts`'s `tokenScopeOf` (one reading for both surfaces).
- **The panel's ↻ Re-run no longer posts at once**: it opens the drawer
  on the whole turn (the `rerun()` draft: the drawer's prompt, tools
  and model kept, its transcript edits dropped) with the ack preview; the command is posted by Run, as every
  verb's. Escape on a drawer control closes the drawer and returns focus
  to the verb that opened it, instead of collapsing the panel.

### Fixed — the playground and the replay (phase 4 review, Go)

- **Version skew, ADR 0029's half**: against a runtime older than this
  release (no registered `defaults`) Studio now refuses, on
  `POST /api/playground/runs` and the preview alike (400), a `user`,
  `tool_args` or `insert` transcript edit (`runtime predates transcript
  edit kinds: upgrade weft/runtime to use user, tool_args, insert
  edits`) and a `from_step` whose request carried a compaction view
  (`runtime predates replay across a compaction: …; upgrade
  weft/runtime`) — the old runtime's decoder dropped `kind`, `args` and
  `index` and applied a `user`/`insert` edit's content as a reply
  rewrite, acked, unmarked, and it spliced no view.
- **At-most-once across the ack window**: weft/runtime's validation —
  the source transcript fetch, the compaction view and the
  `ModelResolver` — now runs under one 25 s deadline (the resolver gets
  at most 10 s of what is left), inside Studio's 30 s ack window; it used
  to be 20 s + 10 s, so a slow fetch and a slow resolver could ack after
  Studio marked the command lost. Studio answers an `accepted` ack on a
  lost row `409` instead of reviving it, and an accepted ack Studio did
  not take runs nothing: refused (409/404/400) it posts nothing;
  unconfirmed (a transport error or a 5xx — Studio may have recorded it)
  it is settled with a finished ack, failed `not run: the accepted ack
  was not confirmed` (a held decision's adds "; decide again", a
  resume's "; decide again to resume", and the park is back) — so the
  command never shows "accepted" forever and a re-issued command can no
  longer run twice (with `side_effects: allow`, twice for real). A
  `finished` ack on a lost row still records what ran.
- **A held approval decision whose accepted ack is refused is no longer
  held.** On a run with several parked calls, a decision that does not
  complete the set and whose accepted ack Studio refuses is dropped (no
  finished ack), so deciding that call again is taken instead of refused
  "already decided"; one whose park another decision's resume took
  meanwhile (the set completed) is reported finished/succeeded — it was
  applied — instead of "decide again".
- **A `tool_args` edit is checked against the right schema**: the
  registered agent's manifest (what the runtime itself checks) when a
  runtime holds the agent, else the catalog the edited step's own
  request recorded — no longer the run's last catalog per tool name, so
  a schema that changed mid-run no longer refuses a valid edit of an
  earlier step (or passes an invalid one). Both routes.
- **A named default `tool_choice` that `park_on` parks** is refused on
  both sides, in the explicit case's sentence (`tool_choice names x,
  which park_on parks: every forced call would park`). weft/runtime's
  "turns off" sentences now read the tool unquoted, as Studio's always
  did — one sentence on both sides.
- **Read-token hygiene**: the preview no longer serves a named tool
  choice's tool or the stop sequences in `will_send`/`was_sent` to a
  read-scoped panel token, and `GET /api/runtimes` drops the registered
  defaults' `tool_choice.name` and `stop` for it — as the spans already
  hide the tool name.
- **The preview's `was_sent` at the step count**: from a `from_step`
  equal to the step count, the recorded request is now step
  `from_step − 1`'s last attempt (each query paged to its end), not the
  last of the run's first 1000 request records.
- **`weft.override.tool_choice` reads `auto`** for an auto override (the
  zero mode); it recorded `""`, indistinguishable from no override. An
  attribute, not model-visible; the override hash of such a run changes.
- **The wefttest fixture export decodes the transcript once**:
  `obsdb.NewAssembler(batches).Step(...)` answers `AssembleStep`'s
  result per step without re-decoding every messages batch (the export
  was O(steps × transcript)); output unchanged.

### Fixed — the devtools panel (phase 4 review)

- **One preview per resting draft**: typing in the drawer posted one
  `POST /api/playground/preview` per keystroke; a superseded debounce now
  posts nothing.
- **Edits are never dropped silently**: a draft holding transcript edits
  at from_step 0 is refused with a line that holds Run, on both surfaces
  (`lib/edits.ts`'s `editsProblem`, through Studio's `buildRunBody` too);
  a thread switched fork → ephemeral returns to the step its edits
  imply, and ↻ Re-run drops the edits (its title says what it keeps).
- **An editor's refusal belongs to its drawer**: the editor's state is
  keyed by run and cleared when the drawer closes, is replaced or the
  conversation changes, so a stale refusal no longer holds Run elsewhere;
  the first edit opens its editor once the drawer is there, so nothing
  typed is lost while `/api/runtimes` answers.
- **A steer's replay verb sits beside its editable note**, never inside
  it (axe's nested-interactive; the a11y budget runs the steer fixture).
- **"compare in Studio" carries the option lab and the transcript
  edits** (`lab=` and `edits=` in the fragment; Studio's playground
  adopts them), so the same experiment opens as the same command.
- **The compacted-view pre-check is re-asked after a failed
  `transcript?step` read** and forgotten on a scope change; **the step
  compare draws a thin diff** (no steps, a side without its request
  record) without a throw. Panel: +351 B gzip (ledger row "review fixes
  (session 8)").
- **A failed `transcript?step` read is not re-asked per keystroke**: the
  failure is remembered and the view asked again only after 10 s, so a
  persistent 500 costs one request per back-off, not one per key.
- **The option lab refuses a parked default `tool_choice`**: an agent
  whose default forces tool X, with `park_on` naming X, holds Run with
  the server's sentence ("tool_choice names X, which park_on parks:
  every forced call would park") on both surfaces, as Studio and the
  runtime refuse it.
- **A stale `transcript?step` failure no longer marks a newer read
  failed**: a failure replaces only its own pending marker, so one that
  lands after a scope change (or after a newer answer for the same run
  and step) neither starts a 10 s back-off nor overwrites the view.
  Panel: +8 B gzip (ledger row "web closer (session 8)").

### Fixed — Studio (phase 4 review)

- **One run page per run**: `/runs/A` → `/runs/B` is a fresh page, so a
  replay drawer open on A, its form and the transcript edits held there
  no longer carry onto B (a link to B's drawer opens it with none of A's
  edits), and B's `?ev=` is scrolled to after visiting A.
- **The playground follows runtimes under a read-scoped token**: the
  shell's 5 s `/api/runtimes` poll is off for a token that may not act,
  so the playground page polls itself there — still one poller.
- **The drawer's "open in the playground" is the same command**: its
  fragment carries the prompt, input, model, thinking, the tools left
  on, the option lab and the transcript edits, as the panel's "compare
  in Studio" does.
- **A copied link never carries `edits=` or `lab=`**: `canonical` strips
  them with the other prompt-bearing keys.
- **One failed side no longer blanks the step compare**: the runs that
  answered are drawn and the failed one is badged "not compared" with
  its error; `/compare` drops a repeated `b` and a `b` equal to `a`.
- **A matrix's finished toast is not held by a runtime that left** after
  its cells went out: the notice waits only for cells being issued.
- **The playground's hand-off survives a StrictMode double mount** (the
  controls are no longer reset to the router's query on the re-run).
- A saved pane layout outside a pane's minimum opens clamped to the
  bound (pinned by a test).
- **A hand-off's junk edits no longer crash the playground**: `edits=`
  keeps an edit only when every field has its type (a known kind, text
  where text goes, a non-negative index, args an object), at most 200;
  an edit's line draws whatever it holds.
- **The drawer's "open in the playground" and the panel's "compare in
  Studio" write the same fragment** for the same draft (pinned by a
  parity test): the input only at step 0, `step` only past 0, the
  engine, side effects and thread only when not the default.
- **Edits held at step 0 hold the playground's Run** with the line
  saying why, and their list stays on the page so they can be dropped.
- **A read-scoped token sees no Run and no Run matrix** in the
  playground (the server refuses them), with a line saying why.
- **…nor the result cards' verbs**: under a read-scoped token a card
  draws no "save as fixture", no continue/skip/resolve on a parked call
  and no steer (each POST would be refused, 403); the run link stays and
  a parked call is listed by name, read-only — the run page's
  `canReplay` rule on both pages.
- **A hand-built `edits=` / `lab=` leaves the playground's address bar**
  with the page's first write-back (the page holds them).
- **…nor the parked notice's "approve"**: a result card hands its own
  approve to the parked toast only for a bearer that may act; under a
  read-scoped token the toast offers "open" alone (the click was a 403).
- **The matrix says it runs from step 0**: when the page holds a later
  step or transcript edits, a line under the Matrix header says each
  input runs from step 0 and the step and edits stay with Run.
- **A hand-off's repeated edits are one edit per target**: `edits=`
  keeps the last edit on a target (as the editor's `putEdit` does), so
  the list draws no duplicate rows and the command sends no duplicate
  `transcript_edits`.

### Changed — breaking

- **`studio/runtime.TranscriptEdit` is no longer comparable** (plan
  F2.1): it gains `Kind`, `Index` and `Args json.RawMessage` — a slice,
  so `==` on two edits (or an edit as a map key) no longer compiles.
  Migration: compare the fields you need, or their JSON. The wire shape
  only grows: every body recorded before it decodes and validates as
  before.

## 0.12.0 — 2026-10-09

Phase 3 of the devtools plan (the panel): `@weftgo/devtools` on npm,
scope detection (explicit, response headers through `weft/scope`, DOM
markers, the page URL, the fallback), the panel's host API, layout,
theme, keyed renderer, views, honesty and the Request tab, Studio's
Request pane, and the live grant. Tags `core/v0.12.0` (the
`weft.version` literal only; core's API is unchanged since 0.10.1) and
`v0.12.0` — a minor bump, as `?token=` is refused and `mount()` may
return `null` (Changed — breaking, Changed — migration).

### Added

- **The panel's size**: at this release `studio/dist/panel/panel.js`
  ships at 62,591 B gzip (61.1 KiB, 18.9 KiB of headroom under the 80
  KiB cap then; the current size and cap are `studio/README.md`'s
  panel-size sentence, which a test keeps on the ledger's last row), the last
  row of the append-only ledger `studio/web/panel-budget.json`; every
  panel build prints the per-item table from it. The KiB figures in the
  entries below are each item's delta, or the panel's size when that
  item landed — intermediate, not the release's.
- **Review fixes (panel)**: the size table and `budget.test.ts` measure
  `panel.js` with its version stamp normalized to the fixed-length
  placeholder `"v0.00.0"` (`normalizeStamp`), so a version bump moves no
  ledger byte; the panel finds its own `<script>` tag by `data-weft`,
  else by the `src` that resolves to the bundle's `import.meta.url` —
  never by a generic `data-endpoint`/`data-token` word, so a
  third-party widget's tag ahead of it no longer becomes its
  configuration; nothing renders after a disconnect (the tree's filter
  debounce and "copied" timer are cleared, no observer on a detached
  sentinel); the turn filter's prompts, the reported parked calls and
  failures and the header rung's paths are cleared on every start and
  capped (oldest first); the server's `overflow` frame reopens a live
  lane on its own bounded backoff (1 s, doubling while overflows repeat
  within a minute, a minute at most) instead of at once; viewport
  resizes are coalesced to one redraw per animation frame; with two
  panels on a page only one (the global's, else the first connected)
  answers Alt+W; external keys are looked up as own properties only
  (`weft.content: "constructor"` is no mode, an unknown cause no
  cause); a `data-weft-scope` marker on or inside a
  `data-weft-untrusted` element is ignored, and the README says markers
  must not appear in untrusted HTML.
- **Review fixes (npm)**: `@weftgo/devtools` publishes public
  (`publishConfig.access`), with `prepublishOnly` running
  `npm-package.ts --check`, which now also fails on a missing `exports`
  or `types` target, an unguarded panel import, or a README naming
  another version (the README no longer hard-codes one); `engines`
  (node >= 18), `homepage`, `bugs`, `keywords`, and `types` for
  `./panel.js`. `mount()` waits for `<body>` (called from `<head>` it
  returns the element and appends it on `DOMContentLoaded`), is
  idempotent (a second call reuses the element it made and applies the
  new options) and returns `null` with `enabled: false` or on a server;
  the root entry is import-safe without a DOM (`ssr-guard.js` /
  `ssr-unguard.js` around the `panel.js` import; every export a no-op);
  `enabled` on `mount()` and the React/Vue/Svelte helpers; `Position`
  is the panel's whole placement union (`left-dock`, `top-dock`,
  `bottom-dock` added) and `mount()` takes `mode`, `push` and `zIndex`
  (written as `data-mode`, `data-push`, `data-z-index`). **Breaking**:
  `mount()` returns `WeftDevtoolsElement | null`.
- **Review fixes (Studio UI)**: `make devtools-vite-check` is pinned
  — `examples/devtools-vite` names vite 8.3.4 and jsdom 30.1.2 exactly
  and commits its `package-lock.json` (installed by `npm ci`; the packed
  tarball goes in after, `--offline --no-save`, and stays out of
  `package.json` and the lockfile) — and runs in CI (job
  `devtools-vite`, after `studio`); `check.ts` waits for the panel's
  `/api/meta` request with a deadline instead of a fixed 300 ms and
  gives the bundle's `import.meta` the built module's URL. The run page
  re-reads a finished run's spans for the invoke_agent span only after
  a running→finished transition it saw (a run opened cold reads them
  once). While a run runs, a request row ahead of its transcript batch
  says "bytes when the transcript is read" (`no_transcript`), not the
  `gap` badge (`messagesSent`'s new optional `running`). The result-cap
  badge shows the bytes cut (`truncated · 2.0 KiB cut`) and an unrun
  call's `max_tokens · call never executed`. A badge's reason and fix
  are its accessible description (`aria-describedby` to a hidden
  element), said once; its name is the label. The Studio suites' real
  sleeps are fake timers advanced past the 2 s poll or deadline-polled
  conditions.
- **Review fixes (verification)**: imported through `@weftgo/devtools`
  (bundled into an app's chunk) the panel no longer takes the app's
  entry `<script>` for its own tag — its `import.meta.url` is the
  chunk's — so the endpoint default stays the page's directory (it was
  `/assets/`, with a `panel-config.json` asked there and the app tag's
  `data-*` read as configuration): the package's `ssr-guard.js` marks
  the import before `panel.js` evaluates (`ssr-unguard.js` removes the
  mark) and the element class's `npmEntry` keeps it, and under it rung 4
  is `data-weft` alone; the standalone `panel.js` still matches its own
  `src`. `make devtools-vite-check` keeps the app's entry tag in a
  second variant (page `/app/`) and asserts the endpoint. The panel's
  Request tab passes `running` to `messagesSent`, so it draws the
  neutral `no_transcript` where the run page does (a parity fixture
  pins it). A badge's reason and fix are inline visually hidden text
  after the label on both surfaces (the panel's `.weft-sr`, the run
  page's `sr-only`) — part of its accessible name, replacing the
  `aria-describedby` above; `title` stays for the pointer. The overflow
  backoff measures calm from the reopen, so a sustained overflow holds
  at the minute cap. Alt+W prefers a live, page-mounted panel (the
  global's if it is one, else the first connected one; the dormant or
  self-mounted docks only when there is none), so markup a page adds
  later can be toggled. A run opened cold within 30 s of its end (the
  row's `finished`) polls its spans for the invoke_agent span inside
  that window.
- **The devtools panel's Request tab** (plan E1.2): the run page's
  Request pane in the panel, one step at a time (`J`/`K`, the tab's step
  buttons, a step card; `⤢` carries it) — the chips "changed by
  PrepareStep", "prompt changed at this step", "overridden by
  experiment" and "catalog changed at this step" from the request
  record's hashes, the system prompt with its bounded diff (vs the
  previous step; at the first step vs the registered instructions when
  the panel can read `/api/manifest`), the messages sent (count · bytes,
  the last three inline, the rest in a D4 JSON tree inside the tab), the tool
  catalog (description, policy chips, schema tree), params with "adapter
  default", tool choice, thinking, the attempts, a subagent call's child
  request and the compaction marker. Every absent block is a badge; a
  read-scoped token sees `hidden` and nothing else, and asks for no
  prompt. The panel gate checks the studio-local trim's chip and diff
  live. +5.5 KiB gzip (estimate +4 KiB). Review fixes: a derived
  prompt or catalog record (a body that did not parse) is badged
  `derived` and never diffed over, on both surfaces (`promptText` says
  undefined for it); the manifest is read per endpoint and token, asked
  again 30 s after a failure and once when it does not list the run's
  manifest hash (a redeploy); a subagent's "read its turn" says when the
  child's history is unreachable and hands off to Studio; the tab's step
  is the one ⤢ carries and J/K start from, walking the steps the record
  names too; a Studio that serves no request record is `not_recorded`
  with the new obsdb cause `not_served` (`/api/meta`'s
  `capabilities_off.requests` words first); the panes share their words
  through `lib/requests.ts` and the stripped words of the tools route
  (+0.6 KiB: the tab is +6.1 KiB, 0.1 KiB past half again its estimate).
- **The devtools panel's honesty** (plan D5): every badge the panel
  draws comes from the A3 table through one module,
  `src/panel/badges.ts` (`<span class="weft-badge" data-hole="…">`, the
  table's label, the reason and fix as its title — the run page's span;
  `badge(hole, note)` is what the Request tab calls): on the turn's
  header, step cards, tool calls (a result cap's cut is `truncated`,
  cause `result_cap`; an unrun call of a `max_tokens` step is
  `max_tokens`), compaction markers and request lines. The table's
  causes (`log_cap`, `result_cap`, `no_public_id`, `no_spans`,
  `dev_token_only`) join `lib/honesty.ts` as `CAUSES`, checked against
  the golden. The footer's content line ("content on · 2 events
  shortened (24.6 KiB cut)", "content off · weft.Content(false)" /
  "· otel.NoContent()") replaces the "content is stripped" suffix; turn
  rows carry "scripted (0 tokens)", "fork of s_…#e_…" and "experiment
  of t3" chips from the run row (its model, `weft.session.forked_from`,
  `weft.forked_from`). `parity.test.ts` proves every hole of the table
  shows on both the panel and the run page for the same run; a grep test
  keeps hole words out of every other panel source. The panel's badge
  attribute is now `data-hole` (was `data-weft-hole`), as the run page's. Review fixes: a
  finished step no request row names is `gap` on both surfaces (one
  reason, `lib/requests.ts`); the run page's result-cap badge is the
  table's (`truncated`/`result_cap`, `max_tokens` with no fix for an
  unrun call; `HoleBadge` takes a `cause`); a badge's reason and fix
  ride in its accessible text on both surfaces; the content line is said
  only on evidence, and `none` reads "captured none (weft.Content(false),
  or no destination takes content)" in the panel and in `/api/meta`'s
  note; "scripted (0 tokens)" only at zero usage; the session lookup's
  own reason/fix win; the parity test compares both surfaces' badge sets
  over a hole-free baseline.
- **The devtools panel's views** (plan D4): the open turn has tabs —
  **Story** (the step story), **Request** (the run page's Request
  pane, one step at a time — E1.2, above),
  **Timeline** (the spans waterfall at full width over a time axis in
  ms, or, on a run without spans, its steps and tool calls over the
  event sequence) and **Raw**: a hand-written JSON tree
  (`src/panel/tree.ts`, no library) of `{doc, events, transcript,
  spans}` (and `requests` when the panel holds them) with per-node
  collapse (`aria-expanded`, `→`/`←`), lazy children (only open nodes
  are built; 200 children, then "+N more"; a string over 2,048 characters shows
  "+N bytes"), a filter over keys and values that opens the path to
  each match and counts them, copy-node, copy-all (the clipboard,
  else the text selected to copy by hand) and a `<run id>.json`
  download. `/` focuses the Raw tab's filter, else the turn list's.
  ARIA tabs (`role="tablist"`, `←`/`→`/`Home`/`End`); the tab is
  remembered as `tab` in `localStorage["weft.devtools"]` (additive to
  v1; `raw` mirrors it). The turn list gets a filter box (text over
  the id, the error and the prompts the panel has read; a status;
  has error — "n of m turns") and pages older turns through the runs
  API's `before=`/`before_id=` cursor as it scrolls to its end (an
  `IntersectionObserver` on a sentinel; a button where there is none,
  and in the narrow dropdown): no 50-turn cap, "all n turns loaded".
  `r` returns to the tab before Raw (the header's `raw` is
  `aria-pressed`), the raw filter is debounced (100 ms) and a redraw
  re-walks nothing; the Story tab's node is kept across tab switches.
  +5.2 KiB gzip of its +5 KiB (with its review fixes).
- **The devtools panel renders incrementally and is read by assistive
  tech** (plan D3): a draw patches the dock in place — keyed by run id
  for turn rows and by step ordinal for step cards — so a streaming
  step re-renders only its own card, and focus, the caret, scroll and
  opened `<details>` survive a redraw because their nodes do (the
  manual restore is gone). The dock is `role="complementary"`
  (`aria-label="weft devtools"`), the turn list a `role="list"` of
  `listitem`s with a roving tabindex (`↓`/`↑` move it; `j`/`k` still
  select), the running step's text the one `aria-live="polite"`
  region, the expanders carry `aria-expanded`, every icon button an
  `aria-label`; `Tab` wraps inside an open, focused float (never when
  docked; `Esc` lets go). An axe-core run over every mode in both
  themes reports zero violations (`a11y.test.ts`; axe is a
  devDependency, never in `panel.js`), the experiment drawer and the
  `?` overlay included. The streaming text is `aria-busy` while it
  grows and a step's end is said once on a hidden `role="status"` line;
  per-call lines and dropdown options are keyed; the Studio app's
  Tailwind no longer scans the panel's sources (`@source not
  "./panel"`). +1.5 KiB gzip.
- **The devtools panel's theme** (plan D2): light and dark, following
  the host page without configuration — `data-theme="light|dark"`
  (also `weft:theme`, the script tag, `mount({theme})`) wins, then the
  user's choice from the header's `◐` button (stored as `theme` in
  `localStorage["weft.devtools"]`; auto clears it), then auto: `<html
  data-theme>`, `<html class="dark|light">`, `<html>`'s computed
  `color-scheme`, `prefers-color-scheme`, else dark. A host toggle and a
  system change are followed (passive, removed on disconnect); the
  result is `data-theme-resolved` on the element. The colours are the
  Studio app's palette (`studio/web/src/lib/palette.ts`, held in
  lockstep with `src/styles.css` by a test), contrast-checked to WCAG AA
  in both themes, and every colour, radius and font is a `--weft-*`
  custom property on the panel's `:host` — `weft-devtools { --weft-bg:
  … }` overrides it. The panel's old dark-only `--w-*` variables are
  gone.
- **The devtools panel's layout** (plan D1): the dock floats (dragged
  by its header, resized from its corner, 360×280 up to the viewport
  minus 16 px, clamped on every window resize) or docks to any side —
  `left`, `right`, `bottom`, `top` — resized along its edge; collapsed
  it is the pill (now with the current turn's tokens in→out, `—` while
  it runs), and `data-mode="hidden"` draws nothing while the API and
  events keep working. `data-position` gains `left-dock`, `top-dock` and
  `bottom-dock`; `data-mode`, `data-push="true"` (pads `<html>` on the
  docked side — the bottom sheet by its 70vh — through
  `--weft-devtools-inset`, restored exactly; off by default; a fixed or
  sticky composer uses the variable in its own CSS) and `data-z-index` / `--weft-z` are new. Under 640 px of
  panel the turn column is a dropdown; under a 480 px viewport the panel
  is a bottom sheet. The placement, the selected turn and the raw view
  are remembered per origin in `localStorage["weft.devtools"]` (`{v: 1,
  …}`; `localStorage.weft_debug=1` is migrated into it). Keys: `Alt+W`
  stays the one global; `Alt+Shift+W` (next layout), `Esc`, `j`/`k`
  (turns), `J`/`K` (steps), `g s` (open in Studio), `r`, `?` and the
  reserved `/` fire only with focus inside the panel — the panel no
  longer hears bare keys pressed on the page's body. Every panel build
  prints the per-item size table from the append-only
  `studio/web/panel-budget.json` ledger against the 80 KiB cap.
- **The devtools panel's host API** (plan C4.2): the `<weft-devtools>`
  element's `open()`, `close()`, `toggle()`, `isOpen`, `scope(s)` (a
  `Scope` or its string form; `scope(null)` hands the scope back to the
  ladder; a session-only scope resolves through
  `GET /api/sessions/{id}/public_id` for setup A and the dev token, and
  says why when it cannot), `select(runId, step?)` (a run the list does
  not show joins it when it is the conversation's, else "run r_… not in
  this conversation"), `on(event, cb)` and `studioLink(runId, step?)`
  (through `lib/links.ts`, never a token). The panel dispatches
  `weft:run` `{runId, status, publicId?, sessionId?, step?}` per status
  transition, `weft:parked` `{runId, callId, ackId, name}` once per
  parked call (`ackId` is the call id its approval takes) and
  `weft:error` `{message, runId}` once per failed run — bubbling,
  composed, asynchronous, only for the conversation followed. The
  script-tag install publishes the API as `window.weft.devtools` while a
  panel is connected (never replacing another library's `window.weft`;
  `data-global="off"` / `weft:global` opt out); `@weftgo/devtools` adds
  `select`, `isOpen` and `studioLink` and no global.
  `examples/studio-local`'s page drives the panel from its own
  "debug this" buttons and a "report this run" link, and a refund
  question parks its turn on a `weft.RequireApproval` tool. The host API
  costs +3.1 KiB gzip (ledger row "C4.2 fixes": 45,664 B). Review fixes:
  `scope()` reads `{session}` alone and `sessionId`/`runId` aliases and
  says a scope naming neither; `scope(null)` restores what `mount()`
  named; a frozen `window.weft` is said in the footer; a conversation
  returned to does not re-report its runs; an ordinal the run lacks is
  said and the last step carried; a lookup or run read Studio did not
  answer says so; a host call waits 10 s at most for a hung start;
  `error.runId` is required; the panel gate (`scripts/panel-gate.ts`) drives studio-local's
  own page script (`--page-script`); a failed deny-first in the example
  answers 409 instead of queueing.

- **`examples/devtools-vite`** (the phase 3 gate's first clause): a
  minimal Vite app that installs the packed `@weftgo/devtools` and
  shows the panel with no `<script>` tag; `make devtools-vite-check`
  builds it and proves the panel mounts (jsdom, a fake `/api/meta`)
  and that the installed `panel.js` is the served one by sha256.
- **The devtools panel's DOM-marker rung and scope switcher** (plan
  C3.3): the panel follows `data-weft-scope` markers anywhere on the
  page — the attribute the framework helpers already set — read through
  one MutationObserver (debounced) and one passive `focusin` listener,
  no global touched; with several, the marker around the focused
  element wins. A header switcher (`aria-label="conversation"`) lists
  every conversation known — explicit, markers, headers, each once, at
  most 20 — with its source and a live dot, and choosing one rescopes.
  `data-detect` accepts `markers` and `headers,markers`; the rung is on
  by default except under a read-scoped panel token off loopback, and
  the footer says `detect: markers` / `headers+markers`.
- **The devtools panel's URL rung, live fallback and activity pill**
  (plan C3.4): `?weft_scope=` (read first) or `#weft_scope=` on the host
  page — the scope's string form, URL-decoded — scopes the panel, below
  an explicit `data-scope`/`scope()` and above the marker and header
  rungs; it is re-read on `hashchange`/`popstate` (passive listeners,
  removed on disconnect), never written, and never gated (`data-detect`
  `off` turns rungs 2–3 off only; the footer says `detect: url`). With no
  scope from any rung the header reads "no conversation detected on this
  page · how to scope" (the one-line fixes behind the link) and the
  latest list follows `/api/live?agent=<the newest run's agent>` through
  a grant instead of the 10 s poll, which stays only while no stream
  covers the list (a 403 or a panel token: "streaming needs the server
  token · polling"; a second agent: said). The collapsed pill pulses
  (not under `prefers-reduced-motion`) and shows the running run's step
  count ("● 3", `aria-label` "weft devtools · running, step 3") from
  the streams the panel already holds.

- **Studio's Request pane** (plan E1.1): each run-page step card's
  request now carries the chips the request record's hashes decide —
  "changed by PrepareStep" (the step's `system_hash` moved from the
  previous step's with the tool set unchanged, or at the first step is
  not the run's `instructions_hash` plus the offered tools'
  PromptSnippets as a manifest verified for the run — by its
  `manifest_hash` — names them), the neutral "prompt changed at this
  step" when a changed tool set may explain the move, "overridden by
  experiment" (the run's invoke_agent span carries
  `weft.override.instructions`) and "catalog changed at this step";
  nothing is decided from an unverified weft.json or a ToolSource
  tool — and a bounded line diff of the system prompt against the
  previous step, or at the first step against the registered agent's
  instructions when they differ (never over a cut record), params, tool
  choice and thinking as rows ("adapter default" when nil), and the
  messages sent (count and bytes, the last three inline, the rest as a
  raw tree) from the transcript the page already holds. The story view
  reads `runs/{id}/spans` once the run ends (under the `requests`
  capability) for the override fingerprint; the time axis stays the
  trace view's. A read-scoped token still sees only the `hidden` badge.
  `examples/studio-local`'s agent gains a PrepareStep that trims a
  first-step paragraph from its system prompt from step 1 on
  (`TestPrepareStepTrimsThePrompt` pins the request record's shape).

- **`GET /api/sessions/{id}/public_id`** (plan C4.1): the reverse of
  `GET /api/public/{public_id}` — `{"session_id", "public_id"}`, the
  public id `thread.PublicID` stamped on the session's turns
  (MAX(`weft.public_id`) over its top-level turns: a session whose turns
  carry several answers the greatest, unbadged), for the devtools'
  `scope()` given a session id and no public id. A session whose turns
  carry none answers 200 with `"public_id": ""` and the `not_recorded`
  badge (cause `no_public_id`, fix `thread.Create(…,
  thread.PublicID(id))`, or set `weft.public_id` on every turn);
  an unknown session is 404 `not_found`. The dev (server) token's and
  setup A's open API's alone: every panel token, read or playground,
  is 403 with the `hidden` badge (cause `dev_token_only`) before the
  session is looked up. In the always-on read group, so `/api/meta`'s
  capabilities are unchanged. `obsdb` gains the two causes
  (`CauseNoPublicID`, `CauseDevTokenOnly`) in `HoleCauses()` and
  `studio/testdata/holes.golden.json`'s `causes`.

- **`@weftgo/devtools` on npm** (plan C1, assembled in
  `studio/web/npm`, unpublished until a release publishes it): its
  `panel.js` is `/studio/panel.js` byte for byte (sha256 equal, gated
  by `make studio-check`). `import "@weftgo/devtools"` mounts the
  panel on a page with no `<script>` tag. The typed entry exports
  `mount`, `scope`, `open`, `close`, `toggle`, `on`, `serializeScope`
  and `parseScope` (the `data-weft-scope` marker:
  `pub_…;session=…;flow=…;run=…`), and the host API's `select`,
  `isOpen` and `studioLink` (C4.2, above). `/react`, `/vue` and
  `/svelte` export marker helpers, not components. Zero runtime
  dependencies; the version is the weft version. `scope()` passes the
  whole scope to the panel (C3.2, below), and `on()` subscribes to the
  `weft:run`, `weft:parked` and `weft:error` events the panel
  dispatches (C4.2, above).
  `make devtools-npm` builds the package and dry-runs `npm pack`.

- **`weft/scope`** (plan C3.1): the Go side of the devtools' Scope.
  `scope.Scope{PublicID, SessionID, FlowID, RunID}` with `String()`,
  `IsZero()` and `scope.Parse(string) Scope`, the same string form as
  the web side's `serializeScope`/`parseScope`
  (`pub_…;session=…;flow=…;run=…`, values percent-encoded, a lenient
  parser that never fails). One golden, `studio/testdata/scope.golden.json`,
  is read by both test suites. `scope.Header(next, func(*http.Request)
  scope.Scope) http.Handler` sets the `Weft-Scope` response header on
  the app's own chat endpoint, and `scope.Set(w, s)` sets it once the
  run id is known. Both append `Weft-Scope` to
  `Access-Control-Expose-Headers`, and the header never carries a
  token. `examples/studio-local`'s `/run` answers `Weft-Scope:
  pub_demo;run=<id>`. The panel reads the header (C3.2, below). The
  package sits beside the root rather than in it: the root package is
  a generated facade over core, which never imports net/http.

- **The live grant** (plan C5): `POST /api/live-grant` (capability
  `live`, authenticated like every API route) takes one `/api/live`
  selector and its `kinds` (JSON body or query) and answers `{sig,
  exp}`; `GET /api/live?<selector>&kinds=…&sig=<sig>` opens that one
  stream as the identity that asked. The sig is HMAC-SHA256 over the
  identity, the selector, the kinds set and the expiry (60 s, never
  past a panel token's own); it is refused (401) on another selector
  or kinds set, tampered or expired. A panel token is granted only a
  stream inside its public id. Without a `Token` the key is random per
  process, so setup A takes the same path. A grant is a 60 s window,
  not a single-use ticket: any number of streams may open with its sig
  while it lasts (an `EventSource` reconnect reuses the URL), each
  running until closed, so a leaked grant URL is live for 60 s — the
  grant answer is `no-store` and Studio never logs the URL. A panel
  token's stream — opened with a grant or with the bearer — ends at
  the token's expiry with one `event: expired` frame; a server token's
  stream does not.

- **The panel follows a whole scope, and detects it from response
  headers** (plan C3.2). Detection rung 1 is the explicit forms:
  `data-scope="pub_…;session=…;flow=…;run=…"` on the element, the
  `weft:scope` meta tag or the script tag, `window.__WEFT__ = { scope }`
  (or `{ publicId }`), and `mount({scope})` / `scope()` from
  `@weftgo/devtools`. The public id selects the conversation; `run` pins
  the selected turn and its live tail once, or the panel says `run r_…
  not in this conversation`; `session` narrows the turn list to the runs
  that carry that `weft.session.id`; `flow` is a header chip and filters
  nothing yet. Rung 2 reads the `Weft-Scope` header (`weft/scope`) of the
  page's own `fetch` responses by wrapping `window.fetch`: same-origin
  responses, or cross-origin ones between two loopback origins; headers
  and URL only, never bodies, never sends, never the panel's own Studio
  requests; it chains to whatever `fetch` it found, is installed only
  once Studio has answered and is restored on disconnect. The next
  turn's header of the same conversation re-pins its run unless the
  user has clicked a turn since, and another conversation is followed
  only from the request path that set the current one. It is on by
  default only with the page and the endpoint on loopback and no token
  or a dev token, opt-in elsewhere with `data-detect="headers"` (or
  `mount({detect})`), and `data-detect="off"` turns detection off.
  Under a panel token the page's `fetch` is never touched unless asked. It is fetch-only,
  because a page cannot read an `EventSource`'s headers, and WebSocket is
  never wrapped. The footer names the choice: `detect: headers | off |
  explicit | none`. `examples/studio-local`'s tag drops `data-public-id`, and
  its page's first `/run` scopes the panel to `pub_demo`. Panel after
  C3.2: 34.5 KiB gzip (was 32.0) — an intermediate size; the release's
  is the first entry above.

### Changed

- **`data-public-id` is deprecated in favour of `data-scope`** (plan
  C3.2). It is still read, as the scope with only its public id, at
  every rung where it was read before (`weft:public-id` too), and
  `data-scope` wins at the same rung. `mount({publicId})` keeps working
  beside the new `mount({scope})`.

### Changed — breaking

- **A token in a URL is refused**: the `?token=` query parameter is
  refused on every `/api` route, in every setup (401, never echoed,
  naming the grant); a token travels in
  `Authorization: Bearer` only. Migration: an `EventSource` (or any
  client that put the token in the URL) requests a grant with `POST
  /api/live-grant` and opens `/api/live?…&sig=`. The `#token=`
  fragment of `weft open` / `weft studio --open` links is unaffected:
  it never reaches the server.
- **Both web clients request a live grant** (plan C5.2): the Studio UI
  and the devtools panel open every live stream with `POST
  /api/live-grant` (bearer in the header) and a `sig`, and put no
  token in any URL; the UI adopts a link's token from its `#token=`
  fragment only (a `?token=` is stripped, not kept) and drops the
  unused `exportUrl`.

### Changed — migration

One line per change above that asks something of a caller:

- `?token=` is refused on every `/api` route: send `Authorization:
  Bearer <token>`, and open `/api/live` with a `POST /api/live-grant`
  sig; the `#token=` fragment of `weft open` / `weft studio --open`
  links stays (it never reaches the server).
- `data-public-id` (and `weft:public-id`, `mount({publicId})`) is
  deprecated: write `data-scope="pub_…"` (`weft:scope`,
  `mount({scope})`); the old forms are still read.
- `/api/meta`'s `content` note for `none` now reads "the latest run
  captured no content: weft.Content(false) on the agent, or no
  destination takes content" (its fix names both remedies): a client
  that matched the old "the agent captured no content" text reads the
  `none` mark instead.
- The panel's badge attribute is `data-hole` (was `data-weft-hole`):
  a host selector or test reading the old name reads the new one.
- `mount()` from `@weftgo/devtools` returns `WeftDevtoolsElement |
  null` — `null` with `enabled: false` or on a server: check the
  result before calling methods on it.
- The panel no longer adopts a `<script>` tag because it carries a
  generic `data-endpoint`/`data-token`: a tag whose `src` is not the
  bundle's own URL (an inlined or re-served bundle, a loader) needs
  `data-weft` on it, or a higher rung (meta tags, the element,
  `mount()`).

## 0.11.0 — 2026-10-08

Phase 2 of the devtools plan (start and find): the `weft` command,
`weft dev`, the port policy, discovery, the stable dev token, the
Agents page from runtime registrations, `weft doctor`, and the panel's
configuration ladder. Tags `core/v0.11.0` (the `weft.version` literal
only; core's API is unchanged since 0.10.1) and `v0.11.0` — a minor
bump, as `studio/cmd` is removed (Changed — breaking).

### Added

- **Agents from runtime registrations** (plan B4): Studio remembers
  each runtime's registration manifest by (service, manifest hash), in
  memory; `/api/manifest` serves it when no `weft.json` is configured
  (the file still wins) and lists every source in `sources[]` with
  `live`; two services registering one agent name in different versions
  both appear. `/api/meta` gains `manifest_sources`, `capabilities_off`
  (why an option left a capability off) and `manifest_check.source` /
  `differs`; the Agents page, playground and debugger empty states show
  the reason.
- **The `weft` command** (plan B1, `cmd/weft`): `go install
  github.com/weftgo/weft/cmd/weft@latest` yields a `weft` binary.
  `weft studio [--addr] [--db] [--token] [--manifest] [--open]
  [--no-playground]` is setup B (what `studio/cmd` served) with the
  playground on (`studio.Playground(true)`, inert until an app's
  runtime connects; `--no-playground` turns it off), the manifest from
  `--manifest` / `WEFT_MANIFEST` else the nearest `weft.json` from the
  working directory upward (one line says which, or that none was
  found), and `--open` (default on when stdout is a terminal) opening
  the UI with the token in the URL fragment. The Studio API over a
  terminal: `weft runs [--agent] [--since] [--failed] [--limit]
  [--json]` (`GET /api/runs`; `--limit` 50 by default, and a limit
  that hid runs says so on stderr), `weft open <run id> [--open]
  [--with-token]` (prints the bare `<url>/runs/<id>`; the token goes
  only to the browser, or to stdout on `--with-token`), `weft export
  <run id> [--format json|jsonl|otlp]` to stdout and `weft export <run
  id> --wefttest <dir> [--test name] [--force]` (the wefttest fixtures
  unzipped into `<dir>/<name>/`, where `wefttest.Replay(t, dir)` reads
  them; a non-empty target needs `--force`, which replaces its `*.json`
  fixtures); `weft doctor`; `weft version`. The API clients never
  follow a redirect. The API clients take
  `--url` (`WEFT_STUDIO_URL`, default `http://127.0.0.1:7331`) and
  `--token` (`WEFT_STUDIO_TOKEN`). Exit codes: 0, 1 a failure, 2 a
  usage error.
- **`weft dev`** (plan B1.2, FEATURES D7): `weft dev [studio's flags]
  [--no-watch] [--watch dir] [-- command…]` (default `go run .`)
  starts Studio in-process as `weft studio` does (same flags and port
  policy) and runs the app with `WEFT_ENV=dev` (kept when already set
  non-empty), `WEFT_STUDIO_URL`, `WEFT_STUDIO_TOKEN` and `WEFT_DB` (the
  Studio's SQLite file) set — plain variables the app can set itself.
  The app runs in its own process group and restarts on a `.go` save
  (fsnotify, 300 ms debounce; SIGTERM, 5 s, SIGKILL); a build failure
  waits for the next save; `--no-watch` exits with the app's code
  (127 when it cannot start); a directory arriving with `.go` files
  restarts too. Ctrl-C, SIGTERM and SIGHUP stop the app, then Studio
  (Linux adds Pdeathsig for `go run`; a SIGKILL of `weft dev` can
  still orphan the app's binary). Each start prints one line:
  `studio <url>[#token=…] · app pid <n> · runtime rt_… registered`
  (the token only when generated; "no runtime registered yet" after
  5 s, then a later line). Reuse follows the port policy: without a
  fixed token a running token-walled Studio is skipped for the next
  port (plan B3's stable token changes this). The
  discovery file is plan B3's. `examples/studio-local` runs under it:
  with `WEFT_STUDIO_URL` set it listens on 8080, keeps its local sink
  (`otel.Local("")`, explicit) and registers its runtime with that
  Studio.
- **`make studio-bin`** builds `./weft` from `./cmd/weft` (ignored by
  git).
- **Studio's port policy** (plan B2, `internal/listen`, wired into
  `weft studio`): `127.0.0.1:7331` is the one default. On a busy port
  the binary asks `GET /api/meta` there (bearer: `--token` /
  `WEFT_STUDIO_TOKEN` when set): a Studio on the same database file is
  reused — `studio already running at http://127.0.0.1:7331 (pid
  1234), reusing`, exit 0, no database or listener opened — and anything else (another
  program, a Studio on another database, one whose meta this token
  cannot read) moves Studio to the next free port in 7331–7340 with
  one line naming the skipped address and why; the banner prints the
  real address. All ten busy is exit 1 naming the range. `studio.New`
  is unchanged: an embedded Studio is the app's own listener.
- **`WEFT_STUDIO_ADDR`**, the `--addr` mirror. Either pins the
  address: busy is exit 1 with the address in the error — no probe, no
  fallback.
- **`/api/meta` `pid`**: the serving process's id, under `db.path`'s
  guard (loopback with no Token, or the server token; never a panel
  token).

- **`weft doctor [--url URL] [--token TOK]`** (plan B5): checks a
  running Studio and prints one line per check — reachable, token
  accepted, the database's path and size, the content it stores,
  connected runtimes (and, when none, what this shell's `WEFT_ENV` and
  `WEFT_STUDIO_URL` say about why), the panel bundle's version, and
  whether `weft.json` is stale against the latest runs. Every line
  reads a field of `GET /api/meta` (`internal/doctor.Lines` is the
  table); the flags mirror `WEFT_STUDIO_URL` / `WEFT_STUDIO_TOKEN`. An
  unreachable Studio is the first line within a 5 s timeout and exit 1;
  a redirect is reported, never followed.
- **`weft studio --manifest path`** (default `$WEFT_MANIFEST`; the flag
  wins): the app's `weft.json`, read once at start and served as
  `studio.Manifest` — `/api/manifest` and the doctor's weft.json check
  in setup B. An unreadable file is a start error.
- **`/api/meta` explains itself**: `content` (Studio's ingest policy,
  `"as_received"`, and the latest run's content mark — `full`,
  `stripped`, `none` or `unmarked` — with a note and the fix naming
  `otel.NoContent()` or `weft.Content(false)`), `pricing` (`false`
  until a pricing table exists), `retention` (`null`: none
  configured), `runtimes` (runtimes holding a command stream now),
  `panel_version` (the embedded `/panel.js` stamp) and
  `manifest_check` (`{agents, checked, stale}`: each manifest agent's
  hash against its latest run's `weft.manifest.hash`; `null` without a
  manifest or for a panel token), and `auth_required` (whether a
  `studio.Token` is configured). `content.error` and
  `manifest_check.error` (omitted when empty) report a read that
  failed or a manifest that does not parse — logged through the
  process's `slog` default too — instead of reading as an empty
  database.
- **obsdb/sqlite**: `(*DB).Path()` — the database file's absolute
  path (`""` for `:memory:`).
- **The discovery file** (plan B3, `internal/discovery`): `weft studio`
  and `weft dev` write `studio.json` — `{url, token, db, pid, started,
  version}`, mode 0600, the bound port in it — to `./.weft/` when it
  exists, else `$XDG_RUNTIME_DIR/weft/`, else `os.UserCacheDir()/weft/`,
  and remove it on a clean exit. `otel.Install`/`otel.Start` and
  `runtime.Install` read it when `WEFT_STUDIO_URL` is unset: an app
  with `defer otel.Install()()` exports to the running Studio (and its
  runtime dials it) with no configuration, one INFO line naming the
  Studio joined. The file is trusted only when its url is loopback, it
  is fresh (pid alive, under 24 h) and, on unix, it is 0600 and the
  reading user's; anything else is ignored at Debug and removed by the
  next writer. A second Studio's exit restores the first's file; the
  writer drops `.weft/.gitignore` (`*`) when there is none. An explicit
  `otel.Studio(...)` or `WEFT_STUDIO_URL` switches the read off;
  **`WEFT_DISCOVERY=off`** turns it off; `otel.NoEnv()` ignores it with
  the rest of the environment.
- **A stable dev token per database**: `<db>.token` beside the SQLite
  file (`.weft/weft.db.token`), 32 random bytes base64url, 0600,
  created on the first start and served by every later one, so the
  token survives a restart and a second bare start's probe reuses the
  running Studio (the probe sends it only to an address the user's
  discovery file names). **`--rotate-token`** (`weft studio`, `weft dev`;
  an action, no environment mirror) writes a new one.
- **`GET <base>/panel-config.json`** (studio): `{endpoint, version,
  capabilities}` for the devtools panel, answered to a loopback (or
  `AllowOrigins`) Host and a same-origin, `AllowOrigins` or loopback
  Origin only (loopback by the Host's rule — `localhost`,
  `*.localhost`, 127.0.0.0/8, `[::1]` — even with `AllowOrigins` set) —
  a 404 otherwise; `/api/meta` lists the new `panel-config` capability.
- **The devtools panel's configuration ladder** (plan C2): each field
  (`endpoint`, `public-id`, `token`, `position`, `open`, `auto`)
  resolves on its own through `mount(opts)` → the `<weft-devtools>`
  element's attributes → `<meta name="weft:endpoint|public-id|token|
  position|open|auto">` → the panel's `<script>` tag (the running
  classic script, else the first with `data-weft`, any `src`) →
  `panel-config.json` beside the script (endpoint only, same origin
  only, never a token; asked only when nothing above named an endpoint)
  → the script's own directory. A `<weft-devtools>` element, a
  `mount(opts)` or `data-auto="false"` with no Studio answering shows
  one line, `Studio not reachable at <endpoint> · retry`; the dock the
  script mounted by itself still removes itself silently. The rung
  table is in `studio/README.md`.

### Dependencies

- `github.com/fsnotify/fsnotify` v1.9.0 (framework module only, for
  `weft dev`'s watcher; `core` is untouched).

### Changed

- `runtime/examples/local` serves on `127.0.0.1:7331` (was 7391, for
  no documented reason): one default port everywhere.
- **The dev token is stable per database and no longer printed by
  default**: `weft studio`'s banner and `weft dev`'s one line print the
  bare URL and name the token file (the stable token is the panel
  tokens' signing key, like a fixed one); `--open` still hands the
  browser the link with the token, `weft open --with-token` prints it,
  and `weft dev`'s app still gets `WEFT_STUDIO_TOKEN`. Only a database
  with no file (`:memory:`, ClickHouse) gets a per-process token,
  printed as before. `--token` / `WEFT_STUDIO_TOKEN` override as
  before and never touch the file.

### Changed — breaking

- **The panel no longer finds its script tag by file name** (the
  `panel(-vX)?.js` pattern is gone). A tag carrying none of the panel's
  `data-*` attributes — the bare `<script type="module"
  src="/studio/panel.js">` that relied on `window.__WEFT__`, or a
  renamed or proxied bundle — is no longer found: its endpoint falls to
  the page's own directory, Studio does not answer there, and the auto
  dock removes itself silently. Migration: add `data-weft` to the
  script tag (`<script type="module" src="/studio/panel.js"
  data-weft>`), or configure a higher rung (a `weft:*` meta tag, the
  element, `mount(opts)`). Tags with `data-public-id`, `data-endpoint`
  or another panel attribute keep working. A `<weft-devtools>` element
  in your markup with no Studio answering now shows the not-reachable
  line instead of an empty element.
- **`studio/cmd` is removed**: the setup-B binary is `weft studio`
  (`go install github.com/weftgo/weft/cmd/weft@latest`), every flag
  kept. `studio --version` is `weft version`; `studio doctor` is
  `weft doctor`. Scripts that ran `go run ./studio/cmd` or
  `go install github.com/weftgo/weft/studio/cmd@…` run `weft studio`.
- **`/api/meta`'s `db` is an object**: `{kind, path, size}` instead of
  the kind string. Read `db.kind` where you read `db`. `path` and
  `size` (bytes, the WAL sidecar included) are served only to a
  loopback request with no `studio.Token` and to the server token —
  omitted for panel tokens and `AllowOrigins` hosts, and for a
  database with no file.

## 0.10.1 — 2026-10-08

### Fixed

- **studio**: `make studio-panel-asset` (the devtools panel as a release
  asset) read the `studio.Version` literal that 0.10.0 removed and
  failed; it reads the one version from `version/version.go` through
  the same helper the panel build stamps from. Tags `core/v0.10.1` (the
  `weft.version` literal only) and `v0.10.1`.

## 0.10.0 — 2026-10-08

The request record (ADR 0028): every model call's system prompt, tool
catalog, parameters and attempts recorded beside the transcript, read
back by Studio and the devtools panel with a closed table of honesty
badges; app logs, the step route, the export in four formats; one
version from the module. Tags `core/v0.10.0` (compatible additions
only) and `v0.10.0` (breaking in the pre-freeze layers listed below).

### Changed — breaking

- **One version, the module's.** The new `github.com/weftgo/weft/version`
  package (standard library only) holds it: `version.Version` is the
  release tag, `version.Runtime()` reads the framework module's version
  from the build info (the main module or the `github.com/weftgo/weft`
  dependency, honouring `replace`) and falls back to `Version` under
  `(devel)`. `studio.Version` is now `version.Version` — `v0.4.1` →
  `v0.9.0` — so `/api/meta`'s `studio_version` and the devtools panel's
  embedded version (stamped from `version/version.go` at build) move
  with the module. A panel built for `v0.4.1` refuses a Studio that
  reports `v0.9.0` as newer.
- **`/api/meta`'s `weft_version` is the framework module's build-info
  version** (`version.Runtime()`), no longer the `core` dependency's;
  inside the workspace it reads the tag instead of `(devel)`.
- **`obsdb.DB` gains `TranscriptBatches`** (ADR 0028 §8): a run's
  messages records with what each stored — index, step
  (`weft.step.index`, -1 when absent), input flag. A third-party `DB`
  reads pos, step, body and the `weft.messages.input` attribute per
  `messages` record, returns them through `obsdb.DedupBatches`, and
  implements `Transcript` as `obsdb.TranscriptBodies(batches)`. A
  backend with no attribute column infers the input flag on index 0 and
  sets `TranscriptBatch.InputDerived`.
- **`/api/runs/{id}/transcript` rows carry what the record stored**: the
  stored `index`, the stored `step` (or `-1` with `"badge":
  "not_recorded"`) and the stored `input` (`"badge": "derived"` where
  the backend inferred it) — no longer derived from assistant-message
  order. Playground `transcript_edits` / `from_step` validation and the
  web client read the same stored step; only a batch without one is
  placed by inference, marked derived.
- **`obsdb.DB` gains `Requests`, `Prompt`, `Tools` and `Catalogs`**
  (ADR 0028 §10, the request record's read side). A third-party `DB`
  reads its `request`, `prompt` and `tools` records (stored under
  `(run, kind, index)`) as `obsdb.StoredRecord`s through
  `obsdb.RequestRecordOf`, `obsdb.PromptRecordOf` and
  `obsdb.ToolsRecordOf`, returns one tools record per hash (the lowest
  index) from `Catalogs`, and answers a missing hash with
  `obsdb.ExplainMissing`. `obsdb.RunRow` gains `InstructionsHash`,
  `CatalogHash` and `RequestCount`, which a backend fills with the
  larger of `run_start`'s and the `invoke_agent` span's
  `weft.instructions.hash`, request index 0's `weft.catalog.hash` and
  max `weft.request.index` + 1.
- **`obsdb/clickhouse`'s unreleased migration 0004 gained
  `weft_records.Input`, `Content`, `TruncatedBytes`, `SystemHash` and
  `CatalogHash`**: a database
  that applied 0004's earlier text (only a development build wrote one)
  lacks them and must be recreated. ClickHouse now reads the transcript
  input flag as stored; only rows written before the column read
  `TranscriptBatch.InputDerived`.
- **`obsdb.DB` gains `Compactions`** (ADR 0028 §8, plan A9): a run's
  compactions as `obsdb.Compaction` — each run-scope view in index
  order with its step, half-open range, hash and body, then thread's
  session markers in emission order. A third-party `DB` builds each through
  `obsdb.CompactionOf` (from a `messages` record whose
  `weft.messages.reason` is set, or a record of kind `compaction`) and
  orders them with `obsdb.SortCompactions`. Its `Transcript` and
  `TranscriptBatches` must skip every `messages` record whose
  `weft.messages.reason` is set, and its messages count must not count
  them (`obsdb.Weft.Reason` carries the attribute).
- **`obsdb.DB` gains `OtherLogs`** (plan A7): a run's app log records —
  the non-weft records (no `weft.run.id`) the writers keep beside
  weft's, attributed to the run through the span they were emitted
  under (its own spans and the non-weft spans below them, never another
  run's) — as an `obsdb.LogPage` of `obsdb.OtherLog`s, paged by
  `obsdb.LogQuery` (`From` inclusive, `Limit` 0 = 100 max 1000,
  `MinSeverity` filtering without renumbering), with `Partial` (the run
  is running: lines under in-flight spans appear when those spans end,
  and indexes may shift), `Gap` (lines naming a span never stored) and
  `Truncated` (more than `obsdb.MaxLogCandidates` lines in the run's
  traces, counted before attribution). A third-party `DB` implements it as
  `obsdb.ReadOtherLogs(ctx, db, runID, q, candidates)`, where
  `candidates` returns the first `limit` non-weft records of the run's
  traces within a time window, in time order. `obsdb.HoleError.Kind` gains `"logs"`: a finished run
  with no span has nothing to attribute through and answers
  `HoleNotRecorded`.

### Changed

- **The system prompt and the tool catalog now reach content-on
  destinations** (`otel.Local`, `otel.Studio`) under the content policy:
  `Redact`-able, capped with `weft.content.truncated_bytes`, dropped by
  content-off destinations. ADR 0028 reverses the old stance that no
  record carries the instructions text. An application whose prompt must
  not leave the process redacts it (`core.ContentPrompt`) or turns
  content off for that destination.

### Added

- **The compaction marker on the run page and in the panel (plan
  A9.2, ADR 0028 §8).** `GET /api/runs/{id}` gains `compactions`: each
  compaction the run's records name, counts and hash only, never a
  message body — a run-scope view `{scope: "run", index, step,
  from_seq, to_seq, hash, replaced, entries}` and thread's session
  marker `{scope: "session", hash, replaced, entries, tokens_before?,
  tokens_after?, reason}`; `[]` for a run that never compacted or was
  written before A9 (the record is optional: no badge). A read-scoped
  panel token reads it. The run page's step card whose request saw a
  view draws "2 messages rewritten into 1 by PrepareStep" ("1 message
  inserted by PrepareStep" when nothing was replaced) with the
  `compacted` badge; a session marker sits at the top of the run it is
  filed under — "12 messages compacted into 2 · 8.1k → 1.2k tokens".
  "show original" (collapsed, no fetch) expands a view's replaced range
  `[from_seq, to_seq)` from the transcript route's growth records the
  page already holds (seq = position in their concatenation); a range
  that cannot be placed is the `gap` badge. The replacement shows its
  count — and, once the step route is loaded, the request's message
  count — and says where its body is (the export's
  `compactions[].messages`). The devtools panel draws the same marker:
  session markers at the top of the turn, views on their step line.

- **A run's app logs and its delta count in Studio (plan A7).**
  `GET /api/runs/{id}/logs?from=&limit=&severity=`, under a new `logs`
  capability in `/api/meta`, pages the app's own log lines (an `slog`
  bridge or the OTel Logs API on the pipeline's `LoggerProvider`) that
  were emitted under the run's spans — a tool handler's lines are the
  run's — in time order: `{logs: [{index, time, severity,
  severity_number, body, attrs, span_id?}], next_from?}`; `severity`
  keeps a level and above (`trace`…`fatal`, or 1–24). `holes` lists
  every hole of the page (`badge`/`reason`/`fix` repeat the first):
  `not_recorded` for a run recorded without a tracer, `truncated` when
  the run's traces hold more than 10 000 log lines in its window (the
  cap applies before attribution: later lines of this run may be
  missing), `gap` for lines in the run's trace naming a span that is
  not stored (they may be another run's). A running run's page carries
  `partial: true` and a `partial_reason` (lines under in-flight spans
  appear once those spans end; indexes may shift) — not a hole.
  App logs may carry anything the app logged, prompts included, so a
  read-scoped panel token is refused them (403, `badge: "hidden"`); a
  playground-scoped token, the server token and loopback read them. The
  run row (`runs`, `runs/{id}`, sessions, the export) gains
  `delta_count`, the streamed deltas counted and never stored. The web
  client gains `fetchLogs` and the types (no UI yet).

- **Subagents on the page (plan A10).** `GET /api/runs` takes `all=1`
  (every run, subagent children included — the same as `parent=*`; a
  `parent=<run id>` beside it wins; a malformed value is a 400) beside
  the existing `parent=` filter, whose absence keeps the list top-level
  only. On the run page a step whose tool call started a child run
  shows it as a nested row — agent, status, usage, its holes, a link to
  its own page (from the run document's children, or the step route's
  `children[]` when cached) — and opening it folds the child's steps
  with the child's own request record, read by the child's id
  (`/api/runs/<child id>/requests`, under the `requests` capability:
  the child's prompt, never the parent's; a read-scoped token sees
  `hidden`). A call joins its child by the child's id, which names the
  step (`<parent>/<step>/<call id>` — call ids may repeat across steps),
  in the story and the trace view's call detail alike; a child id of
  another form falls back to the first unlinked call with its
  `parent_call_id`. The run document's `children[]` rows carry each
  child's own `holes`; a child's usage shows once it ended ("usage at
  finish" while it runs, "—" beside the interrupted badge). The runs table is "top-level only" by default with a toggle
  (`?subagents=all`) and a `?parent=` filter chip; a child row links its
  parent. The devtools panel's subagent badge opens the child inline,
  one level: its row (agent, status, usage), its steps with its request
  line, and an "open in Studio" hand-off carrying the child's id; a
  grandchild is its badge and the hand-off only. A child opened while
  it ran is read again when the parent's reload finds its status moved.

- **Studio's run export (plan A7, A9's byte-faithful fixtures).**
  `GET /api/runs/{id}/export?format=json|jsonl|otlp|wefttest`, under a
  new `export` capability in `/api/meta`, downloads the whole run
  (`Content-Disposition: attachment; filename="<run id>.<ext>"`, the
  quoted name plain ASCII — a child's slashes, quotes, backslashes,
  control and non-ASCII characters spelled `_` — plus RFC 6266's
  `filename*` for a non-ASCII id; HEAD answers the headers alone). `json` is one `weft.run.export/1`
  document — `run` (as `/api/runs/{id}`), `events` (with `gaps`),
  `transcript` (the batches as `/transcript` serves them),
  `compactions`, `requests` (the rows with `refs=1`, plus `prompts` and
  `catalogs` keyed by hash: the record, or `{hash, badge}`), `spans`
  and `holes`; every absent block carries its badge, reason and fix
  (`not_recorded` for a run written before ADR 0028, `stripped` for a
  content-off run, `gap` for records a destination dropped). `jsonl` is
  the same records one per line, `{"record": <kind>, …}`, in a stable
  order (run, badges, events by pos, messages by index, compactions,
  requests by index, prompts, catalogs, spans). `otlp` is
  `{"logs": ExportLogsServiceRequest, "traces": ExportTraceServiceRequest}`
  in OTLP/JSON (ids as hex): the records are rebuilt from what obsdb
  reads, with the run's identity and metadata on each, the events'
  `weft.content` marks, and one heartbeat at its last-seen, so
  `POST /v1/logs` and `/v1/traces` re-ingest the same run, its
  `stripped`, `truncated` and `derived` holes included (an inferred
  input flag is left unstamped, a derived prompt or tools record goes
  out with its hash and an empty body — the malformed original is not
  kept by obsdb). The log records' resource carries only
  `service.name`: obsdb keeps no record's own resource, and the spans
  keep theirs verbatim. `wefttest` is a zip of replay fixtures for
  `wefttest.Replay`. A read-scoped panel token gets `json`/`jsonl` with
  the request block `{badge: "hidden"}` (the transcript still badged
  `stripped` for a content-off run); `otlp` and `wefttest` are 403
  with the hidden badge. An unknown format is 400, an unknown run 404,
  a run with nothing to fixture 409 with its badge (`stripped`, `gap`
  — no transcript, or a step whose request record was dropped — or
  `derived`).
  `web/src/lib/api.ts` gains `exportUrl(runId, format)`.
- **Studio's replay fixtures key on the stored step and the request
  record.** `POST /api/playground/fixtures` and the wefttest export
  share one builder: each fixture is the step its records were stamped
  with (`weft.step.index`), not the Nth assistant message; its key takes
  the tool names, thinking, tool choice and sequential flag from the
  step's answering request record (the caller's `tools` only for a run
  written before the record); a step whose request carried a run-scope
  compaction view keys on the compacted messages the model saw and
  carries a `compacted_at` header (index, step, range, hash); the
  finish event carries the step's recorded reason, raw reason and
  usage; tool-call signatures are kept; and the system prompt is
  filled for the reviewer. One fixture per step, from its answering
  attempt: failed retry attempts get no fixture, so a replay answers
  the step's first call under any middleware. `POST
  /api/playground/fixtures` now refuses a read-scoped panel token (403,
  badge `hidden`: fixtures are request-derived) and answers "nothing to
  fixture" with 409 and the badge, like the export (it was 400).
- **Every hole is a badge from one closed table, on both surfaces
  (plan A3, ADR 0028 §11).** `obsdb.HoleNote(h)` holds each of the ten
  holes' one-line reason and, where one exists, its fix — Studio's
  routes read it (no second copy of the words), and its golden,
  `studio/testdata/holes.golden.json`, is what the web's
  `lib/honesty.ts` (the run page and the devtools panel's one table) is
  checked against, key by key. The events route's rows and the live
  record frames carry `attrs`, limited to the `weft.content.*` keys
  (`weft.content` when not `full` — `stripped`, or the core's own
  `none` — and `weft.content.truncated_bytes`), absent when the chain
  left the content as emitted; `obsdb.PosEvent` gains `Content` and
  `TruncatedBytes` (both backends read them; a ClickHouse row from
  before migration 0004 has neither). `GET /api/runs/{id}` gains
  `holes: [{hole, reason, fix?}]` — the run's own: `not_recorded`
  (written before the request record), `interrupted`, `derived` (no
  stored event: a row built from spans), `stripped` (a content-off
  run) and `gap` (event positions missing, once the run is over). The
  fold turns `attrs` into badges on the event, its call, its step and
  the run ("shortened by the recorder: 12.3 KiB cut", "content not
  captured by this app" with its fix); the run header, every step card,
  the raw view's event rows and the panel's turn header and step lines
  draw them; transcript words whose step has no events render as a
  `gap` row under the last step, and the replay playhead goes through
  the transcript overlay (only steps finished at the playhead take
  their final words), so holes survive scrubbing. The step route's
  holes read its events' attrs too (stripped, truncated with the bytes
  cut). The request routes' reasons are now the table's words.
  `redacted` stays reserved: weft's pipeline does not mark a redaction.
  `studio/testdata/v0.9.0.db` is a database weft v0.9.0 wrote; Studio's
  tests open it (migrated to the current schema) and every pane of its
  run says why it is empty.
- **Studio's step route (plan A7, with A4's attempts and A10's
  children).** `GET /api/runs/{id}/steps/{n}` (`n` the step ordinal)
  answers one step assembled server-side from `obsdb`: `status` (`ok`,
  `error`, `parked`, `running`), `started`/`finished`, `latency_ms`/
  `ttft_ms`, `model` (`requested`, and `answered` — the model that
  answered under `mw.Retry`/`mw.Fallback`), `request` (the requests
  route's row for attempt 1, prompt and catalog inline), `attempts`
  (each request record joined to its `attempt` span on the attempt
  number: model, provider, `outcome`, `error_type`, `retry_after_ms`,
  times, span id, request index; attempt 1 falls back to the `chat`
  span), `messages_in` (the request's `messages_ref` as a count),
  `events` (as the events route serves them), `tool_calls` (args,
  result with bytes and truncation, the `execute_tool` span, the child
  run, pending), `children` (the child runs the step's calls started:
  id, call id, agent, status, usage), `usage`, `compaction` (the
  run-scope view the step's request carried) and `holes` — every hole
  that applies, deduplicated, from ADR 0028 §11's table with a reason
  and fix. A block missing for a reason carries its badge
  (`attempts_badge: not_recorded` for a run without attempt spans or
  A4 timing, `request: {badge: "not_recorded" | "gap"}`). Scoped like
  the events route; a read-scoped panel token reads the whole step with
  `request: {badge: "hidden", reason, fix}`. A step past the run's
  last, or a running run's next, is 404. `/api/meta` lists the new
  `steps` capability; the web client gains `fetchStep` and the
  `StepDoc` types (no UI change yet). `children[].cost` is omitted
  until A5 adds costs (absent, not zero); the answering model and the
  attempts' outcomes are read from spans, so a run recorded without a
  tracer badges its attempts `not_recorded` with the fix to install
  one — `obsdb.HoleNoteFor(h, cause)` words it (`CauseNoSpans`), the
  holes golden lists such causes under their hole. Each call is badged
  `stripped` from its own events' `weft.content` too, so a content-off
  run written before the request record says so per call. With neither
  content nor a tracer, a max_tokens step's calls are not listed; the
  max_tokens hole says the step made some.
- **Compaction in the record (ADR 0028 §8, plan A9.1).** When a
  `PrepareStep` sends a request whose messages are not the run's
  transcript, the core emits one `messages` record with
  `weft.messages.reason = compacted` right before that request's
  record: `weft.messages.from_seq`/`to_seq` (the half-open range of the
  transcript it replaced, by longest common prefix and suffix over wire
  bytes), `weft.compaction.scope = run`, `weft.compaction.hash`, the
  replacement messages as its body. The request's `messages_ref` names
  it; the next request names the growth records again. Nothing is
  emitted when nothing changed or with capture off, and what the model
  receives is unchanged. `weft/thread` reports every session compaction
  (threshold, `Compact`, `ApplyCompaction`, a trim, the overflow
  re-run) as one record of kind `compaction`, emitted when it lands
  under the last run that produced the compacted context — scope
  `session`, the compaction entry's hash, the messages replaced →
  summary entries and tokens before → after; no messages. Studio's live
  lane never forwards a view, and its catch-up places messages records
  by their stored index. Both `obsdb` backends keep the plain
  transcript the growth records alone and read both through
  `DB.Compactions`.
- `(*core.Agent).LoggerProvider()` (and `weft.Agent`'s): the OTel
  logger provider the agent's runs emit records through — the option's,
  or the global one resolved at `New` — for satellites whose records
  must land beside the run's.
- `studio --version` prints `version.Runtime()` and exits.
- **Studio's request record routes (ADR 0028 §10, plan A1).** `GET
  /api/runs/{id}/requests?step=&from=&limit=&refs=1`: one row per
  model-call attempt (`index`, `step`, `attempt`, `time`, `system_hash`,
  `catalog_hash`, `content` — `""`, `stripped` or `derived` —,
  `truncated_bytes`, the parsed `body`), each row's `prompt` and `tools`
  resolved inline by hash unless `refs=1` (`{hash, badge}` when the
  record is missing), `next_from` while pages are full. `GET
  /api/runs/{id}/tools`: `{catalogs: [{hash, tools, content,
  truncated_bytes}]}` in index order. A run written before the record
  answers both with `badge: "not_recorded"` (and a reason and fix)
  beside the empty list; a content-off run's tools answer `badge:
  "stripped"`. Both are refused to a read-scoped panel token (403 with
  `badge: "hidden"`), as `/api/manifest` is.
- `GET /api/runs/{id}` (and every run row) gains `instructions_hash`,
  `catalog_hash`, `request_count` and, for a run written before ADR
  0028, `requests_badge: "not_recorded"`.
- `/api/meta` lists the `requests` capability: the UIs gate the Request
  pane on it.
- `obsdb.TranscriptBatch`, `obsdb.TranscriptBodies`, `obsdb.DedupBatches`
  (ADR 0028 §8).
- `obsdb.Hole` and its ten constants (`obsdb.Holes()`, ADR 0028 §11's
  closed badge table); `obsdb.RequestQuery` (the zero value reads every
  step from index 0; `Step *int`, `From`, `Limit`, `PageLimit()`),
  `RequestRecord`, `RequestBody` (and its parts), `PromptRecord`,
  `ToolsRecord`, `ToolEntry`, `StoredRecord`, `HoleError`,
  `RunRow.RequestsHole` (ADR 0028 §10's reading table). A request,
  prompt or tools record whose body does not parse reads
  `obsdb.HoleDerived`, its hashes from the record's attributes.
- **The request record (ADR 0028).** Three OTel log record kinds beside
  `event`, `delta` and `messages`: `request` (one per model-call attempt:
  step, attempt, system and catalog hashes, messages reference, tool
  names, tool choice, thinking, params, model), `prompt` (the composed
  system text, once per distinct hash per run) and `tools` (the offered
  catalog with its policy chips, once per distinct hash per run).
- `RunStart.InstructionsHash` (`instructions_hash` on the wire, an
  additive field): sha256 of the run's raw configured instructions,
  always set by the loop; mirrored as `weft.instructions.hash` on the
  `run_start` record and the `invoke_agent` span.
- `ContentPrompt` and `ContentStop`, the `ContentKind`s `otel`'s
  `Redact` receives for a prompt's text and a request's stop sequences.
- `weft.Origin(name)`, a tool option naming the tools record's `source`
  (`local` by default; `Subagent` sets `subagent`, `mcp.Tools` sets
  `mcp`).
- **Model-call timing and the answering model (plan A4, ADR 0016's A4
  note).** `StepFinish.LatencyMS` and `StepFinish.TTFTMS`
  (`latency_ms`, `ttft_ms` on the wire, additive, omitted when 0): the
  step's model call timed by the loop as it consumes the stream, whole
  milliseconds rounded up; `ttft_ms` is the first `TextDelta` or
  `ToolArgsDelta` and absent when neither arrived. The `chat` span gains
  `weft.stream` (`true`), `weft.ttft_ms` (when a delta arrived) and
  `gen_ai.response.model`; the `step_finish` record gains
  `weft.latency_ms`, `weft.ttft_ms` (when measured) and
  `gen_ai.response.model`; a successful `attempt` span gains
  `gen_ai.response.model`. The answering model is the last attempt the
  chain reported as a success (`mw.Retry`, `mw.Fallback`, a reporting
  adapter), else the model the call asked for.
  Studio's step card and the panel's step line show it (A4.2): "attempt
  4 of 4 · fallback to glm-b" and "1.2 s · first token 180 ms" from the
  folded `step_finish` and the request rows (no fetch while collapsed),
  the attempt list (model, outcome, `retry_after`, times) from the step
  route on expand under the `steps` capability, the same on the trace
  view's `chat` span, and `not_recorded` on a run from before A4.
- **The reporting hook (plan A8, ADR 0016).** `weft.ReportFromContext(ctx)`
  returns the model call's `Reporter`: `.Attempt(weft.AttemptInfo{…})`
  records one provider request as an `attempt` span under `chat`
  (provider, model, `weft.attempt.index`, `weft.attempt.retry_after_ms`,
  `error.type` or Ok) and, from the second attempt on, its own
  `request` record; `.Raw(weft.RawPair{…})` is accepted and dropped
  (sizes on a Debug line). The reporter numbers attempts itself; a
  layer that reports declares `ReportsAttempts() bool` and an outer
  reporting layer stays silent. `mw.Retry` and `mw.Fallback` report
  every attempt. Outside a run's model call the reporter is a no-op.
- **`obsdb` names the phase-1 readers added above**: `CompactionRun`,
  `CompactionSession`, `RecordCompaction`, `ReasonCompacted`;
  `HoleCause`, `HoleCauses`, `CauseDefault`; `LogSkew`, `ParseSeverity`,
  `SeverityText`; `RunDetail.{InstructionsHash,CatalogHash,RequestCount}`
  beside `RunRow`'s; the `RequestBody` parts `RequestMessagesRef`,
  `RequestModel`, `RequestParams`, `RequestThinking`, `RequestToolChoice`,
  `RequestTools`.

### Fixed

Found by the phase-1 review of the request record (2026-10-08); none
changes what the model sees.

- **core**: a reported attempt that raced the model call's end could
  still add a `request` record; the record is now emitted under the
  lock `end()` takes, so a late report adds none (ADR 0028 §7). A
  cancelled resume whose transcript ended at the assistant message
  (every call parked) records its rebuilt tool message like a resume
  with a held tail does, so the records still concatenate to
  `RunError.Result.Messages`. A further attempt that reports a model
  is recorded under the provider it reported, even an empty one — a
  fallback to another vendor no longer carries the primary's provider.
  The agent's instructions hash is computed once at `New` (two
  allocations fewer per run). The Debug line for a contained recorder
  panic names the panic's type, never its value.
- **otel**: `capTools` encodes the catalog once instead of once per
  dropped entry (a 400-tool catalog took 86 ms on the run goroutine).
  A `Redact` panic on `params.stop` drops the stop sequences only; the
  request keeps `messages_ref.index` on a destination that received
  the messages records.
- **obsdb**: a run row with request records but no instructions hash
  (the `run_start` batch lost or not yet landed) reads recorded, and a
  prompt or catalog its requests name but the store lacks is a `gap` —
  not `not_recorded` with "upgrade weft" (ADR 0028 §10's table gains
  the row). `weft.attempt.retry_after_ms` and the ten
  `weft.override.*` fingerprint fields join the contract keys on both
  backends (and 0004's tuples), so a non-string value no longer lands
  in ClickHouse's run meta and not SQLite's. A `messages` record
  without its index — growth or view — and an index attribute present
  but empty read position -1 on both backends and stay out of the
  transcript and the messages count (before, SQLite stored a growth
  record at 0 where the input record dropped it and ClickHouse read it
  first). A non-string `weft.messages.reason` reads as "not growth" on
  SQLite as it does on ClickHouse. Two session markers with distinct
  non-hex hashes keep distinct positions on SQLite (a 60-bit FNV
  fallback). App-log lines equal in time, span, severity and body sort
  by event name and attributes on both backends, so paging by
  `next_from` neither skips nor repeats a line.
- **thread**: a compaction in a forked session no longer files its
  marker under the origin session's run — it is held for the fork's
  first run. A mid-run overflow's compaction keeps the earlier turn's
  span context and metadata on its marker (the Session remembers the
  last four runs it saw). After a reopen the marker carries
  `weft.turn`, `weft.public_id` and the lineage pairs, not the session
  id alone. A held marker that loses a race with `Close` is dropped
  with the "compaction markers dropped at close" Debug line.
- **version**: `version.Runtime()` maps a VCS-stamped pseudo-version
  and a `+dirty` suffix to `Version`, as it does `(devel)`, so a local
  `go build` reports the same version its runs stamp.
- **studio (server)**: a read-scoped token no longer sees tool names
  through the `invoke_agent` span's `weft.override.tools`,
  `weft.override.park_on`, `weft.override.park_all_except` (and a named
  `tool_choice`) on `runs/{id}/spans`, `traces/{id}` or the json/jsonl
  export, nor a compaction view's messages in the export (`null` with
  the `hidden` badge); `/api/manifest`'s 403 carries the hidden badge
  like every other prompt-bearing refusal; the fixtures route checks
  scope before it decodes the body. The step route joins a child to
  the step its id names (a call id repeated across steps no longer
  borrows another step's child); a running step is no longer badged
  `not_recorded`/`gap` for a chat span that has not ended; a running
  run's step without a stored `step_start` reads running, not ok with
  a gap; `weft.model.tool_calls` is clamped before it sizes a response;
  a lone unnumbered request record merges into attempt 1 instead of
  counting the call twice; a `messages_ref` naming a dropped view reads
  `gap` on `messages_in`. The run document and the export share one
  rule for lost events (`gap` when requests or steps exist with no
  spans, `derived` when spans exist); one unreadable child no longer
  fails the parent's document; export children rows carry `holes`;
  the export's top-level `holes` also lists `truncated`, non-final
  `max_tokens` and per-event `stripped`. Every hole's fix comes from
  `obsdb.HoleNote`/`HoleNoteFor` (two new causes: `result_cap`,
  `log_cap`). The auth matrix gains `runs?all=1`, a child's otlp/jsonl
  export, `steps/{bad}` (403 wins), HEAD on every export format and
  the span-attribute check.
- **studio (web, panel)**: `linkView` is idempotent (a re-render no
  longer links one child to a second call with the same id); call
  rows and trace span keys are step-qualified (`c:<step>:<call>`,
  `c:resume:<call>`), so a repeated call id selects the right call and
  React sees no duplicate keys in a resumed step 0 — old `?sel=c:<id>`
  links no longer resolve; the step document is re-read while a step
  runs and once the run ends, and its running-time holes are never
  shown as final; the requests query re-reads from the first missing
  index on the terminal refetch (an out-of-order row no longer leaves a
  false `gap`); `applyTranscript` clones the steps it overlays (a late
  delta no longer appends to transcript text); the `derived` attempt
  badge says what the server means and lists an unnumbered record by
  its request index; the header and the panel share one status-hole
  rule (`statusHoles`); "show original" reports a transcript read
  error instead of loading forever; `findCall` closes the most recent
  open call; no bare "1 attempt". `logs.test.ts` reads the Go goldens
  and a drift guard checks every step and logs golden against the TS
  types.

## 0.9.0 — 2026-10-07

One module is the framework; `core` is the loop alone (ADR 0027).
`MIGRATION-0.9.md` is the migration guide, written to be applied by a
person or a coding agent.

### Changed — breaking

- **`github.com/weftgo/weft` is now the whole framework, one module at
  one version.** The adapters (`openai`, `anthropic`, `google`, `mcp`),
  `thread` (and `thread/sqlite`), `otel`, `obsdb` (and
  `obsdb/clickhouse`), `studio` (and `studio/cmd`) and `runtime` are
  packages of this module, no longer modules of their own. Their import
  paths are unchanged; their per-module tags stop at the last ones cut
  (thread/v0.9.1, thread/sqlite/v0.3.1, otel/v0.2.1, obsdb/v0.2.0,
  studio/v0.4.1, studio/cmd/v0.2.0, runtime/v0.2.0, the adapters'),
  which keep requiring weft v0.8.0. A go.mod that requires one of the
  retired module paths beside `github.com/weftgo/weft v0.9.0` fails
  with an ambiguous import: drop the line (`go mod edit -droprequire`)
  and `go mod tidy`.
- **The loop alone is `github.com/weftgo/weft/core`**, a separate
  module whose only dependency is the OpenTelemetry API, tagged
  `core/v0.9.0`. It holds what the root package held: `core.New`,
  `core.Tool`, the contracts, plus `core/wefttest` (with
  `conformance`) and `core/mw`.
- **The root package, `mw`, `wefttest` and `wefttest/conformance` are
  generated facades over their `core` counterparts**: every exported
  name is a type alias, a bound constant or variable, or a one-line
  wrapper with the original signature and doc. Code that used
  `weft.New`, `wefttest.Script`, `mw.Retry` compiles unchanged;
  `weft.Agent` and `core.Agent` are one type. `go generate ./...`
  regenerates them (`internal/cmd/genfacade`) and
  `TestFacadesAreComplete` fails when a facade and its source
  disagree.
- The layers' signatures name `core.X` where they named `weft.X`; same
  types, no call-site change.
- `mw.IsContextOverflow(err)` is exported: the overflow marker table
  the adapters' error mapping and `mw.Retry` share now lives in `mw`
  (the adapters' internal helper delegates to it).

### Fixed

- A tool defined through the facade records the caller's file and line
  as its definition site, not the facade's: `Tool`, `Output` and
  `Subagent` skip the framework's wrapper frame. Committed manifest
  golden files do not change.

### Release process

- Two tags per release, in order: `core/vX.Y.Z`, then the root tidied
  against it and tagged `vX.Y.Z`. `go.work` joins the two modules and
  the examples. The apidiff gate enforces `core` against its last
  `core/v*` tag and reports the root.

## thread/sqlite 0.3.1 — 2026-10-07

### Fixed

- A write whose context ends while its transaction is open now fails
  with an error matching the context's (`context.Canceled`,
  `context.DeadlineExceeded`), wrapping `sql.ErrTxDone` beside it.
  database/sql rolls such a transaction back on its own, and the bare
  `sql.ErrTxDone` read as a storage failure: a `thread/pool` child
  canceled while its first prompt was being written settled `failed`
  instead of `canceled` (the crash matrix's `pool_canceled` point, seen
  once in CI). The write still does not land; only the error changed.
- Requires weft v0.8.0 and thread v0.9.1.

## otel 0.2.1 — 2026-10-07

### Fixed

- The OTLP golden fixtures live in the module's own `testdata/otlp`, so
  `go test` runs from the module cache (it read `../obsdb/testdata`,
  which a module zip does not carry). Test-only; no code change.

## studio 0.4.1 — 2026-10-07

### Fixed

- Panel tokens decode strictly: `base64.RawURLEncoding` ignored the
  unused low bits of a signature's last character, so several spellings
  of one token were accepted (not a forgery — each needed the real
  signature). Now exactly one spelling is valid.
- The OTLP test fixtures live in the module's own `testdata/otlp`, so
  `go test` runs from the module cache (otel's copies too, test-only).

## 0.8.0 — 2026-10-07

The 2026-10-02 production-readiness pass and its follow-ups, tagged in
dependency order: root `v0.8.0` (additive: `ParkAllExcept`), then
`thread/v0.9.1` and `obsdb/v0.2.0`, then `obsdb/clickhouse/v0.2.0` and
`otel/v0.2.0`, then `studio/v0.4.0`, then `studio/cmd/v0.2.0` and
`runtime/v0.2.0`. Every module requires its siblings' new tags, no
replaces. `thread/sqlite` (v0.3.0) and the adapters (`openai`,
`anthropic`, `google` v0.3.8, `mcp` v0.1.9) are unchanged and not
retagged. The pre-freeze modules (obsdb/clickhouse, otel, studio,
runtime) carry breaking changes, listed first in their sections; their
tags are minor bumps.

### weft 0.8.0

- Added `ParkAllExcept(names...)` — the default-deny park rule: every tool
  call whose tool is not named parks at the approval boundary (ADR 0007),
  evaluated against each step's dispatch snapshot (so `ToolSource` tools
  are covered) and inherited by Subagent child runs; park rules only add
  up; an agent's own `Output` submission never parks under it;
  `weft.override.park_all_except` joins the fingerprint (ADR 0024
  amendment under "Per-run configuration is not a seam").
- Fixed: run records are no longer dropped when a model emits tool-call
  arguments that are not JSON — the `tool_start` event, the assistant's
  `weft.messages` batch, a parked call's `run_finish` and later runs'
  input records carry the raw bytes as a JSON string.
- Fixed: no `weft.messages` record is emitted after the run's context
  ends (S1.3's cancellation rule now covers transcript batches).
- Fixed: `Replay` — the last option wins; `Replay(ReplayNever)` after
  `Replay(ReplaySafe)` is never's.
- Fixed: `weft.override.hash` / `.tools` / `.park_on` treat tool names as
  a sorted, de-duplicated set — equal experiments hash equal.
- Fixed: `Agent.CallTool` called from a tool handler dispatches a call of
  its own — never approved, so a `RequireApproval` tool still returns
  `ErrApprovalRequired` under an approved outer call; its handler's
  `CallFromContext` reports its own id and name.
- Changed: under `Metadata`'s 64-key cap, `weft.*` keys are kept first,
  so a large caller tag set no longer drops `weft.session.id` /
  `weft.turn` / `weft.public_id`.

### thread 0.9.1

- A steer that cannot join the running turn (it meets the approval
  boundary or a StopWhen end, or arrives after the last drain point)
  now becomes a follow-up turn under the run options of the turn it was
  aimed at — the in-flight turn, or the parked turn when only a
  boundary holds the session — with the steer's own options after
  them, and the aimed turn's context values beneath the steer's
  context. A turn sent with `weft.ParkAllExcept` keeps its steer
  follow-ups parked (they ran unparked before). A steer to an idle
  session still runs under its own options. ADR 0019 amendment.
- A resume (after `Decide`, `Resume`, `Send` or `Continue`) runs on the
  arming call's context for cancellation and on the parked turn's
  context values for every key the arming context lacks, so a park
  rule or metadata that rode the parked turn's context binds the
  resumed steps whoever decides. ADR 0021 amendment.
- thread/pool: an async child's context takes the pool's cancellation
  and the delegating call's context values, so the delegating run's
  `ParkAllExcept` list and metadata bind it as they bind a sync child;
  a resumed child (sync or async) keeps the rule on every step. ADR
  0022 §M.

### obsdb 0.2.0

- Production-readiness pass (2026-10-02):
  - `weft.playground` is read in its string spelling (`"true"`) — real
    playground runs stopped being listed as session turns.
  - `FromOTLP*`: hex ids of real OTLP/JSON senders survive a protojson
    decode; nil elements at any layer are skipped; structured log bodies
    render as JSON.
  - Hub: a resume cursor from a previous process no longer mutes the
    subscription; an overflow drop releases its watcher goroutine.
  - New `DedupTranscript`: a resume's rebuilt tool message supersedes the
    partial one; every backend's `Transcript` reads through it.
  - New `MaxGaps` (1000) bounds `EventPage.Gaps`; new `MaxTies` (500)
    bounds how far a page runs past `Limit` to finish a tie; new exact
    cursor `RunQuery.BeforeID`/`RunPage.NextBeforeID` and
    `SessionQuery.BeforeID`/`SessionPage.NextBeforeID`;
    `SessionPage.Total` no longer shrinks with the cursor.
  - obsdbtest: Idempotence reads spans back; new subtests PipelineSpellings,
    PagingTies, SessionPaging, TranscriptRebuilt, NonFiniteAttrs, ZeroTimes,
    OutOfOrder, HeartbeatOnly, Strings, MetaFilter, SessionUncapped,
    ReadErrorIsNotNotFound, FilterCombinations, PagingBigTie,
    ManyDaysOneBatch, CloseRace; status fixtures run on the wall clock
    (no expiry under slow or repeated runs).
- obsdb/sqlite:
  - `Events` never reports `Done` with stored events missing; gap
    detection no longer scans the run on every page.
  - `Write`: a NaN/Inf attribute no longer fails the batch; zero
    timestamps are stored as unknown and records fall back to Observed;
    a run's `Started` is the earliest time seen of any record or span.
  - `Session` returns every turn (the 500 cap dropped the newest); paging
    never skips rows tied on the cursor time; one clock for the status
    filter and row status.
  - `Open`: paths containing `%`, `?`, `#` or starting with `//` open the
    file they name (a database previously created at the misparsed
    location is no longer the one opened for such a path).
  - Calls racing `Close` return `ErrClosed`; `Experiment` reports a failed
    read as that error, not `ErrNotFound`.

### obsdb/clickhouse 0.2.0 (breaking)

#### Breaking

- Migration 0003 changes the stored `otel_traces.SpanKind`/`StatusCode`
  strings to the pinned collector's spelling (`Internal`, `Error`, …);
  anyone querying those columns directly reads the new spelling (both
  are read back by this module). Rows stored before 0003 are not
  backfilled.

#### Changes

- Production-readiness pass (2026-10-02):
  - Runs/Sessions filters read each run's merged row: subagent runs no
    longer surface as top-level runs or session turns before a merge.
  - Migration 0003: the traces view reads the pinned collector's real
    status spelling (`Error`); spans are written as `Internal`/`Error`/…
    and both spellings read (breaking for anyone querying
    `otel_traces.SpanKind/StatusCode` strings directly). Collector-written
    failures stored before 0003 are not backfilled.
  - Paging carries tied rows (bounded by `obsdb.MaxTies`) and honours the
    exact `BeforeID` cursor; Sessions `Total` is the whole match;
    `Transcript` reads through `obsdb.DedupTranscript`.
  - Events reads the terminal flags before the events; `Gaps` capped;
    `Session` returns every turn and no longer exceeds `max_query_size`
    on huge sessions.
  - Non-finite attributes no longer cost a span its typed attributes; a
    zero record time falls back to Observed and the epoch reads back as
    the zero time; a batch may span any number of day-partitions.
  - Experiments keep nanosecond Created/Updated; a failed read is no
    longer `ErrNotFound`; rows closed on every path.
  - `ResolvePublicID` orders by (turn, started) like sqlite and reads by
    primary key; TTL windows round up to whole seconds; detail reads
    narrow `FINAL` by primary key; calls racing `Close` return `ErrClosed`.

### otel 0.2.0 (breaking)

#### Breaking

- `Pipeline` is no longer comparable (apidiff: "Pipeline: old is
  comparable, new is not"): code that compared `Pipeline` values with
  `==` or used them as map keys compares the `*Pipeline` instead.

#### Changes

- Production-readiness pass (2026-10-02):
  - Security: an https destination stays https when
    `OTEL_EXPORTER_OTLP_ENDPOINT` is an http:// URL (bearer tokens and
    content were sent in cleartext).
  - Security: redacted tool arguments that are not JSON are exported as a
    JSON string; a failed re-encode strips the record instead of exporting
    the unredacted body. A panicking `Redact` no longer unwinds the run
    (the record is exported stripped and counted) and is named in the
    WARN by type only.
  - Shutdown and `ForceFlush` flush every destination in parallel under
    the shared budget — a hung destination no longer costs a healthy one
    its last records.
  - Sampled-out runs that fail or are cancelled leave the run tracker (no
    endless heartbeats); heartbeats no longer carry `weft.event.type` /
    `weft.content`; `Heartbeat` ≤ 0 disables instead of panicking.
  - A failed `Start` releases everything it built (including exporters
    passed to `Exporters`); every `Local` DB closes at Shutdown; `Install`
    builds each destination once; `LocalDB()`/`StudioEndpoint()` return
    nil/"" after the installed pipeline shuts down.
  - Failed exports are counted per destination with the cause in the
    throttled WARN; policy filtering counts without warning; Local write
    failures no longer flood OTel's error handler.
  - `OTEL_EXPORTER_OTLP_HEADERS` values are percent-decoded; de-dup
    compares resolved export URLs; scheme-less endpoints read as https,
    non-http(s) schemes are refused, endpoint errors do not echo the URL.
  - `BatchDelay` applies to `Exporters` destinations; the Local path keeps
    bytes/slice/map attributes and renders structured bodies as JSON.
  - `ContentConfig.Redact` godoc states it sees event bodies and deltas
    only — `weft.messages` transcript records are not passed through it.
- Security: `ContentConfig.Redact` now applies to `weft.messages`
  transcript records too, part by part with the event path's kinds (text
  of every role `ContentText`, reasoning `ContentReasoning`, tool-call
  args `ContentArgs`, tool results `ContentResult`); ids, names, roles,
  signatures, file parts and record attributes are untouched, and the
  transcript is still never capped. A batch that cannot be decoded for
  redaction, or whose `Redact` panics, is dropped and counted — never
  exported unredacted. A masking redactor previously leaked the whole
  transcript (Local sink included).
- An `OTEL_EXPORTER_OTLP_ENDPOINT` written with `http://` is the
  operator's plaintext opt-in for that environment destination (the
  docker-compose/sidecar `http://otel-collector:4318` was refused as
  "needs Insecure()"). Code-configured `OTLP("http://…")` to a
  non-loopback host still needs `Insecure()`; an http:// `WEFT_STUDIO_URL`
  to a non-loopback host stays refused (bearer token).
- `Install` with no destination option falls back to the local sink, with
  one WARN, when every environment destination fails to build (it
  recorded nothing); `Start` still returns the error.

### studio 0.4.0 (breaking)

#### Breaking

- Without a `Token`, the API answers only a loopback `Host` or an
  `AllowOrigins` origin's `host:port` (the DNS-rebinding guard, below):
  an embedded Studio served on a real hostname must set
  `studio.AllowOrigins(...)` or `studio.Token`.
- studio/runtime: `RuntimeServer.Enqueue` (and the approval/steer
  paths) refuse what they used to accept — a caller's command id outside
  `ValidCommandID` (`ErrInvalidCommandID`), decisions for calls not in
  the run's pending set, out-of-range option values.

#### Changes

- Production-readiness pass (2026-10-02) — security:
  - A caller's `command_id` is validated (`[A-Za-z0-9._:-]{1,128}`) — a line
    break in it forged frames on the runtime's command stream.
  - Read-scoped panel tokens can no longer decide approvals, steer, or read
    `/api/manifest`; `/api/runtimes` omits agent instructions for them.
  - A playground-scoped token's source run must be inside its public id;
    it may not set `experiment_id`; experiments routes refuse panel tokens;
    `/api/live` checks every frame against a panel token's public id; scope
    checks fail closed on database errors; panel tokens' 500s no longer
    carry database error text; tokens compare in constant time.
  - Loopback-open ingest refuses forwarded requests and foreign browser
    Origins (a proxied Studio must set `IngestToken`).
- API and behaviour:
  - Request bodies capped at 4 MiB (413); option values validated
    (temperature 0..2); approvals validate `call_id` against the run's
    pending set; a fork-mode run is steered like an ephemeral one (an
    accepted ack naming the fork's turn maps it; no 409).
  - Transcript batches carry `input` and the real `step`; transcript edits,
    `from_step` and fixtures count the run's own steps (the input record is
    context).
  - `/api/runs` and `/api/sessions` take `before_id` and answer
    `next_before_id` (an exact cursor inside a tie); the live backfill and
    experiment detail page with it; experiment detail returns every
    top-level run, oldest first.
  - Command rows expose `status` (succeeded|failed) and the finished run's
    `error`; a panel token can poll its own approval decision and steer the
    resumed run.
  - `/api/live`: per-frame write deadline, HEAD answers headers only,
    backfill honours cancellation, bounded dedup set, `X-Accel-Buffering:
    no`; `Vary: Origin` on every CORS answer; error bodies always JSON.
- studio/runtime: `ValidCommandID`, `ErrInvalidCommandID`, `Retention`
  (24 h), `WriteTimeout`, `RuntimeView.Breakpoints`; terminal
  commands, run routes and dead runtimes are pruned; replaced or stalled
  streams end at once and their accepted commands get a finish watch;
  duplicate acks no longer cancel timers; HEAD on the stream is 405;
  breakpoints are stored only when delivered, adopted from the register
  payload and sent before the backlog on every stream open.
- Web UI: a token prompt on a walled API and `#token=`/`?token=` hand-over;
  a thread turn shows its own prompt and reply; live streams reconnect with
  backoff and lose nothing at the walk/stream seam; live renders coalesce
  on large runs; finished runs show stored transcript words; event gaps are
  surfaced; sessions list pages and nests experiments under their source
  turn; the playground targets the source run's agent, tracks every
  variant, shows held approval decisions and the run's outcome, waits for
  the run to settle, caps live cards, reads the hand-off from the URL
  fragment and handles fork mode; malformed stored data and cyclic or huge
  traces no longer crash or hang a page.
- Devtools panel: attribute changes and remounts no longer remove or
  mis-scope it, and `window.__WEFT__` scopes every connected panel; the
  keyboard never takes the host page's keys; redraws keep focus and caret
  and are throttled while streaming; nothing is thrown into the host page;
  streams and polls are bounded; verbs and typed text never carry across a
  conversation switch; the tail subscribes before reading pages; stale
  "running" rows are re-read; approval controls act on the run's pending
  set with per-call decisions; read-scoped tokens are offered no write
  verb; the hand-off rides the URL fragment; the build fails on any
  package code or over the 80 KiB gzip budget.
- Playground: `side_effects: "allow"` accepts a ReplaySafe tool that is
  not opted in (it was refused 403); the panel drawer and `/playground`
  side-effect selects say what each mode does — only `allow` runs the
  app's `AllowSideEffects` tools for real.
- Security (behaviour change): without a `Token`, the API — the whole
  `/api` tree, `/api/live` and the runtime link included — answers only a
  loopback `Host` (`localhost`, `*.localhost`, `127.0.0.0/8`, `[::1]`, any
  port) or the `host:port` of an `AllowOrigins` origin. A DNS-rebinding page
  (`http://evil.example:7331` resolving to 127.0.0.1) could start playground
  runs, approve parked calls, steer and read transcripts; it now gets a 403
  naming what to configure. An embedded Studio served on a real hostname
  lists its origin (`studio.AllowOrigins("http://myapp.internal:8080")`) or
  sets `studio.Token`. `X-Forwarded-Host`/`Forwarded` are never trusted;
  in-process callers (`runtime.Local`) pass; `/panel.js` and the UI shell
  are unchecked; nothing changes with a `Token`.

#### Release asset

- `panel-v0.4.0.js` (87779 bytes, sha256
  `c1265cf322d1bed2779ef9e1c2bef969ae604af1be6af831f424387865d15221`
  beside it) — the devtools panel for non-Go backends; serve it from
  your app and add `<script type="module"
  src="/static/panel-v0.4.0.js" data-endpoint=… data-token=…
  data-public-id=…></script>`. Staged by `make studio-panel-asset`
  (RELEASE_DIR, default `studio/web/dist-release` relative to the repo
  root); byte-identical to the committed `studio/dist/panel/panel.js`.

### studio/cmd 0.2.0

- Production-readiness pass (2026-10-02): ReadHeaderTimeout/IdleTimeout
  set; shutdown ends open streams at once; the ClickHouse handle closes on
  shutdown; a generated dev token is printed as an openable
  `http://addr/#token=…` link, a fixed `--token`/`WEFT_STUDIO_TOKEN` is
  never printed; an unopenable `--db` is an error, not a panic; `sqlite://`
  without a path is a usage error; the DSN mask handles passwords
  containing `@`.

### runtime 0.2.0 (breaking)

#### Breaking

- `AllowSideEffects` is honoured only in `side_effects: "allow"`: an
  opted-in tool no longer runs for real in `substitute` (the default) or
  `park` mode (details below).
- Side-effect parking is default-deny (`weft.ParkAllExcept`): tools a
  `ToolSource` supplies and Subagent children's tools park unless
  vouched safe or opted in.

#### Changes

- Production-readiness pass (2026-10-02) — safety:
  - Side-effect parking is default-deny (`weft.ParkAllExcept`): tools a
    `ToolSource` supplies and a Subagent child's tools park unless vouched
    safe or opted in; an Output agent's `submit_output` no longer parks;
    runs carry `weft.override.park_all_except`. Requires the root release
    that adds `ParkAllExcept`.
  - Approval decisions are checked against the parked run's pending calls;
    a parked run resumes once, when every pending call has a decision;
    concurrent decisions can no longer run an approved handler twice; a
    second decision on a call is rejected; a parked run whose resume never
    started is restored.
  - Fork mode: forks revoke the source session's approval grants; a steer
    into a fork's turn is a thread steer under the turn's run options (the
    park rule binds its follow-up — requires thread's steer follow-up fix);
    the fork's turn is acked accepted again naming its run id once in
    flight, so Studio can steer it; forking a
    source waiting on approval fails fast; a parked fork call is decided
    through the fork session, stays decidable after its park record is
    evicted, and "decide first" names the calls; naming an older fork turn
    forks from that turn; the accepted ack carries no invented run id.
  - A scripted command never runs on the live model; breakpoints are never
    substituted; the substitute chain answers repeated calls in recorded
    order, keeps recorded errors as errors, matches canonical JSON args and
    is bounded by MaxSteps.
- Correctness and robustness:
  - Source transcripts are split once into input + the run's own steps on
    every path (thread, obsdb, Studio): from_step, edits, the default
    prompt, scripted indexing and substitution count the run's own steps;
    resumed turns split the same way everywhere; the thread path refuses
    turns that ran over a summarized context; the scripted engine replays
    reasoning/tool-call signatures.
  - Budgets count failed runs and every chain leg, reserve at admission and
    release a reservation whose command never ran.
  - The runtime rejects unknown engines, modes, thinking levels, options,
    out-of-range values, bad run/command ids and from_step out of range.
  - The link: stop cancels and waits for in-flight runs; bounded
    seen/parked/forks/frames; 16 concurrent runs, 256 admitted commands;
    jittered backoff that resets; register/header timeouts; panics
    contained; the resume cursor is arrival order; oversized frames are
    acked rejected; the in-process transport honours the caller's
    context; the register payload reports `breakpoints`; finished acks
    carry `error`.
  - Fork mode under thread 0.9.0's writer lease: the runtime keeps the
    fork `Session`s it continues in place (it never reopens one, so the
    0.1.1 close-after-every-turn fix is superseded), and closes a fork's
    `Session` once nothing it holds needs it — evicted past the 64-fork
    bound with no park record or in-flight turn on it, or at `stop` —
    so another writer can continue that session (on jsonl, the file lock
    is given up). The read-side source `Session` is closed after the
    fork, and a fork whose grant revocation fails is closed. A turn
    re-run after an overflow no longer leaves its first run id in the
    steer registry.
- `AllowSideEffects` is honoured only in `side_effects: "allow"`
  (WEFT-PLAYGROUND §5.1/§6.3; behaviour change): an opted-in tool used to
  run for real in every mode. In `substitute` (the default) it is now
  answered from the source's recorded result or parked, in `park` it
  parks; ReplaySafe tools still run in every mode. `allow` no longer
  refuses a ReplaySafe tool that is not opted in.
- The in-process link (`runtime.Local`) addresses Studio as
  `http://localhost` (was `weft.studio.local`), a Host Studio's
  DNS-rebinding guard accepts like any loopback request.

### CI / repo

- The ClickHouse job (its service's invalid `ulimits:` key already
  moved into `options:` with a health check by the thread 0.9.0 train)
  fails when a gated test skips, and studio/cmd's step must report
  `TestNewServerClickhouse` passed. Actions bumped to node24 majors (the
  nightly soak workflow too); Go cache keyed on every go.sum.
- The apidiff gate covers every workspace module against its own tag
  (`make apidiff-all`, which CI runs in place of the per-module steps;
  root/thread/thread/sqlite/adapters enforced,
  obsdb/clickhouse/otel/runtime/studio reported, studio/cmd and examples
  skipped, a module without a policy refused; `APIDIFF_STRICT=1`
  requires building from published tags). A sub-module that needs an
  untagged sibling loads through the workspace with a note;
  thread/sqlite loads through go.work first, as `make apidiff-sqlite`
  always did. `make apidiff-selftest` exercises twelve failure modes
  (root, thread, thread/sqlite, a reported module, the workspace
  fallback and its strict refusal, an unknown module).
- `make studio-check` also fails on dist files the commit does not contain;
  `RELEASE_DIR` resolves from the repo root.
- anthropic/google/mcp go.sum tidied so they build `GOWORK=off`.
- examples/studio-local registers its thread store with the runtime (fork
  mode works in the demo; the panel gate covers it); `/run` takes the
  question from the body.

## thread 0.9.0 / thread/sqlite 0.3.0 — 2026-10-02 (the 2026-10-01 review fix train)

The fixes for the 2026-10-01 production-readiness review
(`WEFT-THREAD-REVIEW-2026-10-01.md`: twelve P1s, some forty-five P2s),
built as seven lanes — session core, compaction, backends, approvals,
the writer lease, the pool, the turn machinery — and a closing pass. It
is a **breaking minor for `thread` and for `thread/sqlite`** (a schema
migration, and it needs the new `thread`); `runtime` carries one fix
and ships as 0.1.1 in the entry below. Pre-1.0: breaking changes ship without
deprecation shims, and the migration checklist below names every one.

**Not a freeze.** The plan's "v0.8 freeze" — the format as a
compatibility promise, `thread.Migrate`, an emptied `.apidiff-allow` —
is deferred by maintainer decision until the API has proven stable in
real use. The API and the stored format may still change. What holds
today is the reader's rule: an unknown entry kind, a newer entry
version or a newer header envelope fails with `ErrNewerFormat`, never
a skip, and goldens pin each format version the build reads. ADR 0011
gained the amendment that records the train's decisions and a format
reference for the format as it is; `docs/thread-operations.md` is new.

### Security

- **A signed decision is bound to the occurrence it was minted for.**
  The challenge covered the call id, and call ids repeat across turns:
  a signature minted for one turn's `call_1` approved the next turn's.
  The request entry's id and the run id are now signed and checked
  against the pending request (`SignedDecision.RequestID`, `RunID`;
  `Request.ID`); a signature for another occurrence is
  `ErrNotPending`. The challenge domain moved to
  `weft/approval-challenge/v2`, so no v1 signature verifies.
- **An empty nonce is a bad signature**, and a nonce must be one the
  session's ring minted for that request. A blank nonce used to skip
  the replay scan, so the same bytes verified twice.
- **The session enforces the request's own expiry**, on every path
  that records a decision or arms a resume — `Decide`, `DecideSigned`,
  `Resume`, `Send`, the runner's pickup — not the expiry the signer
  stated, and not only in `Resume`. `Decide(Approve)` on a lapsed
  request used to run the tool.
- **`RequireSigned` is the session's, durably.** It was a process
  flag, lost on reopen and on `Fork`. `Create` now writes it into the
  header (`weft.require_signed`), every `Open` enforces it, a fork
  inherits it, pool children inherit it, and it no longer wedges the
  session's own machinery: an interrupting `Send`'s denial, an expiry
  denial and a pool delegation's resolution all record under it.
- **A decision is spent by the resume that applied it.** A `Branch`
  back to a decided boundary used to run the approved call again on
  the old approval; the calls are now pending again and need new
  decisions.
- **Quorum counts keys, not names**, for signed approvals: one key
  that signs as two `Who`s is one approver.
- **An unknown key id is indistinguishable from a bad MAC**
  (`ErrBadSignature` for both): `DecideSigned` no longer tells a
  caller which key ids exist.
- Refused signed decisions are audited (`StepSigned`), bounded: at
  most 16 per request, and nothing for a signature that fails its MAC
  and names no pending call.
- Grant predicates read arguments exactly: RFC 6901 array indexes are
  strict (`/01` is not `/1`), `?` in a glob is one code point, integers
  compare digit for digit, and a grant's `MaxUses` holds within one
  turn.

### Fixed

Session core
- `Open` validates the entry tree: an empty, invalid or duplicate
  entry id, a parent that is not an earlier entry, or a leaf entry
  navigating to an entry the file does not hold fails with a
  `*CorruptError` (`ErrCorrupt`) naming the line and the entry. A
  dangling parent used to cut the model's context short, silently.
- `Entries`, `Path` and `Audit` return deep copies of every entry
  kind; mutating a snapshot used to corrupt the in-memory tree.
- A public id can no longer be rotated: `SetInfo` rejects `weft.`
  keys, and `Session.Meta` never lets an info entry override one.
- `Fork` builds its session as `Open` does (compaction resolved,
  measurements recovered), honours the header options, rejects a leaf
  entry as its target, and removes a fork it could not write.
- `Open` never starts a run. Steers a crashed writer left queued used
  to be re-run in the background from inside `Open`.
- `examples/session` runs twice in a row.

Turns
- **A mixed tool batch — one call runs, one parks — resumes.** The
  resume's completed tool message was appended as a second tool
  message, the parked call read dangling for ever, and the session
  either looped on resume or queued every later `Send` silently. The
  completed message now replaces the partial one on the active path
  (ADR 0011 §7, amended).
- **Step writes are exactly-once.** One failed per-step append used to
  drop a message and duplicate another. A failed batch is held and
  written in order by the next step or the turn's end;
  `TurnEntry.LateSteps` counts them, and a turn whose end cannot be
  written says so (`ErrNotPersisted`).
- Run ids are unique across a reopen: the counter is recovered from
  the highest id any entry records (prompt entries now carry theirs),
  not from the number of turn entries.
- A prompt whose flush fails no longer leaves a second copy of the
  user message when the `Send` is retried.
- An interrupting `Send` whose denial cannot be recorded fails and
  leaves nothing queued; `Wait` used to hang.
- `Rollback` of a session's first turn returns to the root.
- A `Branch` or a `Reject` send can no longer slip in while the runner
  picks up a settled boundary.
- A panic in the session's turn machinery, or in the caller's `IDs` or
  `Clock` function, ends the turn with `ErrTurnPanicked` instead of
  wedging the session.
- A call left dangling by a crash mid-step is not read as an approval
  boundary.
- `TurnEntry.Canceled` is set for a deadline as well as a
  cancellation; an overflow attempt's usage is kept (its own turn
  entry).
- A mirrored child request stays decided wherever the leaf moves.

Compaction (ADR 0020, amendment 2026-10-01)
- **`AfterCompact` no longer deadlocks** a hook that touches the
  session: every compaction hook and swap-in runs without the
  session's lock and may call the session.
- **A trim no longer cuts the iterative summary chain**: the previous
  summary is the latest *summary* compaction's, so the first summary
  stays in the model's context.
- **Trims replay from the entry.** A trim records each stubbed tool
  result; the context applies exactly those stubs under any options,
  in any process. A custom `Trimmer` now changes the model's context
  (it never did), and a trim the record cannot represent fails loudly
  (`ErrInvalidCompaction`) and the summary runs instead.
- A summary cut off at `max_tokens` is never stored
  (`ErrSummaryTruncated`: one retry, then the fallback).
- `BeforeCompact`'s edits to `Messages`, `Instructions`, `Pinned` and
  `FirstKept` are honoured — a redaction hook works.
- `TokensBefore` and `Preparation.Context` count the compacted view;
  `SummarizeLeft` summarizes the branch's compacted view, not the raw
  range beside its own summary.
- The trigger stands down after a compaction until the next provider
  report; the rate limits count along the leaf's path; a second
  compaction at the same boundary is refused.
- `AfterCompact` fires for trims; `CompactFailed` fires for failed
  writes and not for dry runs.

Backends
- **A torn final line no longer poisons the session.** The next writer
  removes it before appending — jsonl truncates and fsyncs under its
  lock, sqlite deletes the torn row, Memory clips — and logs it. It
  used to glue the next entry onto the half-line; from the third
  process on the session was `ErrCorrupt`.
- **sqlite `Watch` no longer deadlocks** a consumer that calls the
  same `Storage` inside the loop.
- **sqlite's lock no longer treats a pid as an identity.** A restarted
  container (PID 1 again) was locked out of its own sessions for
  ever. Lock rows carry a process token and the process start time.
- **jsonl `Watch` no longer panics** on a file with no complete line;
  a malformed line ends it with `ErrCorrupt`; it reads only new bytes
  per poll instead of the whole file.
- Both `Watch`es end with `ErrNotFound` when the session is deleted
  and created again under them (they stalled for ever).
- `Query.Before` no longer skips sessions that share a creation time:
  `Query.BeforeID` completes the keyset cursor.
- sqlite `List` pages in SQL (keyset, `LIMIT`, `COUNT` over a new
  index); it was a fleet scan per page.
- jsonl `List` no longer allocates 1 MiB per session file per call;
  jsonl `Create` no longer holds the instance mutex across fsyncs.
- jsonl no longer keeps a file descriptor and a lock per session for
  the life of the process: `Session.Close` gives both up, and
  `Releaser.Release` does it for a `Storage` used directly.
- The listed title is the last non-empty info title on every backend;
  `ErrLocked` names the session id, not a path; sqlite `:memory:`
  survives a replaced connection; a path containing `?`, `#` or `%`
  opens the file named; sqlite migrations return errors instead of
  panicking at init; Windows liveness reads access-denied as alive.

Approvals
- Under `Quorum(n ≥ 2)` a call the chain approved once parks with a
  request entry and fires `OnRequest` (it parked with neither, and
  could never expire).
- `ApproveAlways` mints its grant only when the call's effective
  verdict is approve — never beside a denial — and works on a call
  with no arguments.

Pool (ADR 0022, amendment 2026-10-01)
- **Fan-out × depth no longer deadlocks.** A run whose sync delegation
  is running a child hands its slot back and re-acquires it through
  the queue: slots bound work, not waiting.
- **A child that parks twice resumes.** Only the calls pending in the
  child now are replayed; the decisions of an earlier park are not.
- **The delegating call cannot be decided into a second child**:
  `Decide`, `DecideSigned` and `Request` refuse it with
  `ErrDelegated`.
- A `RequireSigned` parent receives its child's answer; a resumed
  child takes a slot and its receipt records `running` again; a park
  the parent cannot record fails the delegation instead of leaving it
  parked for nobody; nested requests carry a real expiry.
- Receipts always end: settlement is idempotent (`Delegated` counts a
  child once) and bills the child's whole ledger, not its last run.
- `Close` covers wrapped calls made outside any session; the pool
  closes each child's session when its delegation settles.

`runtime`
- The playground's fork mode closes the forked `Session` when its turn
  lands. It only dropped the reference, and under the writer lease the
  next fork command's reopened `Session` could not write ("keep
  chatting" failed with `ErrLocked`).

The closing pass
- The parent's decision chain never decides a delegating call: a grant
  or a live `Approver` matching a pool wrap's tool approved the wrapper
  when it parked, and the resume re-ran the delegation in a second
  child session. Such a call now always parks and only
  `ResolveDelegation` resolves it.
- A decision the parent records on its own reaches the child: an
  interrupting Send's denial, an expiry or a direct `Session.Decide`
  on a mirrored request used to leave the child parked until the next
  `Pool.Decide`. The pool now pumps when one lands.
- A session deleted and created again under its id is stale to the old
  `Session` (`ErrStale`): `Leaser.Acquire` also reports the stored
  header's `Created`.
- jsonl: a failed or short append leaves no prefix of its batch; the
  bytes are cut off again under the session's lock.
- A fork settles a copied queued send as dropped in its own entries.
  `Continue` runs restored queued sends under its own context.
- A `CustomMessage` appended while a turn runs is refused with
  `ErrBusy`: it landed between the run's step entries and `Context`
  read a transcript no run produced.
- `examples/studio-local` keeps one `Session` for the demo's life; it
  reopened per request and the writer lease refused the second.
- Internal path reads no longer deep-copy the transcript (on a
  1000-turn session a turn costs 1.31 ms, `Pending` 0.11 ms and one
  allocation). `Context`, `Path` and `Entries` still return copies.

### Added

- **`sqlite.BreakLock(ctx, st, session)`** — the operator's way out of
  a lock row another host left (a container replaced under a new
  hostname, a restored backup). A holder that was alive after all
  fails its next write with `ErrLocked`.
- **Crash matrix**: eight more points — the expiry sweep, the automatic
  trim record, a queued send's accepted receipt, the resume join, a
  fork's settling entries, and the pool's parked, canceled and capped
  receipts. `threadtest.RunTurns` runs on jsonl and sqlite too.
- **CI**: an apidiff gate for `thread/sqlite` (`make apidiff-sqlite`),
  a nightly soak (`make soak-thread`: thread `-race -count=10`,
  thread/sqlite `-race -count=3`); the gate's self-test no longer
  exercises the deleted store module.

- **`Session.Close(ctx)`** — stop new work, drain, seal (`ErrClosed`),
  release the storage's hold.
- **The per-Session writer lease**: optional capability `thread.Leaser`
  (`Acquire`, `Yield`), implemented by Memory, jsonl and sqlite, and
  `ErrStale` for a Session whose view fell behind.
- `thread.Releaser` (end a backend's hold on a session),
  `thread.NoLock()`, `thread.OpenLogger(l)`; `thread.Memory` takes the
  open options. jsonl has a real writer lock on Windows (`LockFileEx`);
  a platform with no file lock fails `jsonl.Open` unless `NoLock`.
- Package **`thread/backend`** — `Config` and `Resolve`, for backend
  authors.
- `Session.LoadReport()` (`*OpenReport`: torn, skipped, orphaned);
  `CorruptError.Entry`, and `CorruptError` unwraps to its cause too.
- `Session.Continue(ctx)` — run what `Open` restored, now.
- **Queued sends are durable at acceptance** (an `accepted` receipt);
  `Open` restores them, `Session.Queue` lists them
  (`QueuedSteer.Policy`), `ClearQueue` drops them.
- `Turn.Done()`, `Turn.WaitContext(ctx)`, `Turn.Outcome()`
  (`TurnOutcome`: running, answered, parked, delivered, deferred,
  dropped, failed, canceled), `Session.WaitIdle(ctx)`.
- `ErrNotRun`, `ErrDropped`, `ErrTurnPanicked`, `ErrNotPersisted`,
  `ErrClosed`, `ErrCreateOnly`, `ErrReservedKey`.
- `Policy.String()`; `TurnEntry.Policy`, `ReRun`, `LateSteps`;
  `MessageEntry.RunID`; `ReceiptEntry.Unanswered`; `ReceiptAccepted`.
- `thread.Clock(func() time.Time)`.
- `Query.BeforeID`.
- Compaction: `TrimRecord`, `TrimStub`, `Compaction.Trim`,
  `CompactionEntry.Trim`; the sentinels `ErrNothingToCompact`,
  `ErrCompactCanceled`, `ErrNoEntry`, `ErrSummaryTruncated`,
  `ErrInvalidCompaction`, `ErrCompactConfig`, `ErrNotPinnable`,
  `ErrAwaitingApproval`.
- Approvals: `Keyring.Sign`, `Key.Sign`, `Request.ID`,
  `SignedDecision.RequestID`/`RunID`, `ErrInvalidDecision`,
  `ErrDelegated`, `StepSigned`; `ApprovalDecisionEntry.RequestID`,
  `Always`; `ApprovalAuditEntry.GrantID`, `GrantShared`, `KeyID`,
  `Decisions`.
- Pool: `MustWrap`, `Wait`, `Recover`, `DecideSigned`, `MaxDepth`
  (default 8), `Children`, `Descendants`, `State` with `Settled()`,
  `Receipt.Call`, `StateError`, `ErrUnknownReceipt`, `ErrNoAgent`,
  `ErrDepth`, `ErrCycle`, `ErrDuplicateWrap`, `CodeSubagentDepth`,
  `CodeSubagentCanceled`; in `thread`, `PoolParked`,
  `InheritApprovals`, `MirroredRequest` and the pool's plumbing on
  `Session` (`ReplayDecisions`, `CancelDelegated`, `MirroredRequests`,
  `DenyMirrored`, `ResolveDelegation`).
- `threadtest`: `RunTwoWriters`, `RunLeaser`, `RunOneWriter`,
  `RunTurns`, `RawHeaderInjector`; `Run` now runs the `Watch` table for
  a `Watcher`, and gained rows a careless backend cannot pass (invalid
  ids on every method, paging through ties, limit normalisation,
  concurrent appends to one session, a load under a concurrent delete,
  an append after a torn tail, a loud header, `Flusher`, `Releaser`).
- Godoc examples across the surface (sessions, turns, steering,
  compaction, approvals and signing, the pool, the backends).

### Changed (breaking) — the migration checklist

Renamed, removed, re-signed (the compiler finds these):

| Was | Write now |
|---|---|
| `thread.Instructions("…")` (a `CompactOption`) | `thread.SummaryInstructions("…")` |
| `thread.Disabled()` | `thread.NoAutoCompact()` |
| `thread.Proceed`, `thread.Cancel` (variables) | `thread.Proceed()`, `thread.Cancel()` |
| `thread.WithApprover(a)` + `thread.ApproverTimeout(d)` | `thread.WithApprover(a, d)` — `d` must be positive |
| `thread.SignDecision(secret, r, d)` with a hand-kept key map | `ring.Sign(r, d)` or `key.Sign(r, d)` (both return an error); `SignDecision` stays for a bare secret |
| `thread.OpenConfig`, `thread.ResolveOpen` | `backend.Config`, `backend.Resolve` (package `thread/backend`) |
| `Estimator.Estimate(...) int` | returns `int64` |
| `thread.SummaryMaxTokens(int)`, `SummaryInput.MaxTokens int` | `int64` |
| `Trimmer.Trim(ctx, msgs) ([]weft.Message, TrimReport)` | returns `([]weft.Message, error)`; `TrimReport` is gone — the entry's `TrimRecord` is the report |
| `Preparation.SplitPrefix`, `Summary.Reason`, `SummaryInput.SummaryModel`, `Compaction.FilesModified` | removed; nothing set or read them (`files_modified` stays readable on the wire) |
| `thread.ErrNotImplemented` | removed; nothing returned it |
| `(*CorruptError).Unwrap() error` | `Unwrap() []error` — `errors.Is`/`As` reach the class and the cause |
| `thread.Memory()` as a `func() Storage` value | it is `func(...OpenOption) Storage`; calls are unchanged |
| `p.Wrap(name, desc, agent, opts...)` returning a tool | `p.MustWrap(...)`, or `tool, err := p.Wrap(...)`; one name wraps one agent (`ErrDuplicateWrap`) |
| `p.Register(id, agent)` | returns an `error` |
| `p.Decide(ctx, parent, ds...) (*thread.Turn, error)`, blocking | `err := p.Decide(ctx, parent, ds...)` records and arms; then `p.Wait(ctx, parent, receiptID)`, or the parked turn's `Next()` |
| `p.Cancel(receiptID)` | `p.Cancel(ctx, parent, receiptID)` |
| `p.Forward(ctx, receiptID, msg)` | `p.Forward(ctx, parent, receiptID, msg)` |
| `pool.Receipt.State` as a `string` | `pool.State` (same wire strings; `r.State.Settled()`) |
| `errors.Is(err, pool.ErrNotRunning)` for a wrong id | `pool.ErrUnknownReceipt`; `ErrNotRunning` is now only "the receipt exists and is in another state" (`*pool.StateError` says which) |

Behaviour (the compiler does not find these):

- **Close before reopening.** One `Session` writes a session: it holds
  the writer lease from its first write (`Create` is one) until
  `Close`. Code that opened a session a second time without closing
  the first `Session` now gets `ErrLocked` from the second one's first
  write. Call `s.Close(ctx)`; in a test that stands in a fresh process,
  `st.(thread.Releaser).Release(ctx, id)`.
- **Never append behind a `Session`.** A `Session` whose stored session
  gained entries it did not write fails its next write with
  `ErrStale`. Open the session again.
- **`Open` runs nothing and refuses header options.** `WithMeta`,
  `PublicID` and `WithLineage` passed to `Open` fail with
  `ErrCreateOnly` (they were ignored). Steers a crashed writer left
  are restored to `s.Queue()` and wait: send, or `s.Continue(ctx)`.
- **`Open` can now refuse a file it used to open**: a malformed tree
  is `ErrCorrupt`. Open the backend with `thread.Salvage()` to read
  around damage; `s.LoadReport()` says what was skipped and orphaned.
- **`SetInfo` rejects `weft.` keys** (`ErrReservedKey`); set them at
  `Create`. A `SetInfo` with nothing to record writes no entry.
- **A dropped turn returns `ErrDropped`.** `turn.Wait()` on a message
  `ClearQueue` removed returned `nil, nil`; it now returns an error
  wrapping `ErrDropped`. Use `turn.Outcome()` to tell a delivered
  steer from a deferred one (both still `nil, nil`).
- **`Wait` returns before the between-turn compaction.** A turn is
  decided when its entries land. Code that read the session right
  after `Wait` and relied on the automatic compaction having run
  calls `s.WaitIdle(ctx)`.
- **`s.Audit()` returns approval entries only** — requests, chain
  steps, decisions, grants, revocations. Turn entries are no longer in
  it; read them from `s.Entries()`. A resume is two `StepResume`
  entries: `started`, then `completed` or `failed`.
- **`thread.RunOptions` rejects `weft.Approve`, `Deny`, `Resolve`,
  `ResolveError`** (and still `Messages`, `Prompt`, `RunID`,
  `Steering`), with an error wrapping `weft.ErrInvalidRunOption`.
  Record decisions with `s.Decide`.
- **`Query.Meta` needs the key present.** A filter `{"k": ""}` no
  longer matches sessions that lack `k`.
- **`Decide`** rejects two decisions for one call, an empty batch and
  a decision with no outcome (`ErrInvalidDecision`), a decision for an
  expired request (`ErrExpired`) and one for a delegating call
  (`ErrDelegated`); it always records `Via: "user"`.
- **`ErrExpired` reads `thread: approval request expired`** (match
  with `errors.Is`, not the text); `Session.Request` returns it for a
  lapsed request.
- **`DecideSigned`** returns `ErrBadSignature` for an unknown key id
  (was `ErrUnknownKey`, which only `Keyring.Sign` returns now) and
  `ErrNotPending` for a signature minted for an earlier occurrence.
  Signatures minted before this version do not verify: request a new
  challenge.
- **`NewKeyring` needs at least one key**; `RequireSigned` without a
  keyring that has an active key fails `Create` and `Open`;
  `SignDecision` signs an empty `Who` as the key's id.
- **Compaction is between turns**: `Compact`, `ApplyCompaction` and
  `Uncompact` return `ErrBusy` while a turn runs, and
  `ErrAwaitingApproval` while requests are pending. `Create` and `Open`
  validate the knobs (`ErrCompactConfig`: `Reserve` below the window,
  `KeepRecent` below window − `Reserve`). `Pin` refuses an entry that
  carries no message (`ErrNotPinnable`).
- **A custom `Trimmer` may only replace tool-result content**; any
  other change fails the trim and the summary runs.
- **An overflow that is re-run leaves two turn entries** (the failed
  attempt's, then the re-run's); code counting turn entries per `Send`
  reads `TurnEntry.ReRun`.
- **A non-user steer** is refused by `Send` with an error wrapping
  `weft.ErrInvalidSteer`.
- **Pool**: the bound is per `Pool` value; a delegation deeper than
  `MaxDepth` is refused (`SUBAGENT_DEPTH`), a real cycle with
  `SUBAGENT_CYCLE`; `Decide` no longer waits for the child; a resumed
  child queues for a slot; children inherit the parent's
  `RequestExpiry`, `Clock`, `Quorum`, keyring and `RequireSigned`;
  after a restart call `p.Recover(ctx, parent)`.
- **`Fork`** takes its own options (`WithMeta`, `PublicID` and
  `WithLineage` were dropped) and inherits nothing from the origin's
  header or options, with one exception: a fork of a `RequireSigned` session
  requires signed decisions too and takes the origin's keyring when
  given none. Copied queued steers are recorded as dropped, mirrored
  child requests are left out, unsettled pool receipts are settled
  canceled.
- **`jsonl.Open` fails on a platform with no file lock** unless
  `thread.NoLock()` is passed (it used to run unlocked, silently).
- **`thread/sqlite`**: migration 0003 runs on `Open` and is one-way —
  a binary from before it refuses the database
  (`sqlite.ErrNewerSchema`). The module needs the `thread` release
  that carries `thread/backend`.
- **`thread/sqlite` durability**: the fsync policy is now SQLite's
  `synchronous` level. `FsyncEveryAppend` (the default) is
  `synchronous=FULL` — an entry survives a power cut; the backend used
  to run `NORMAL` whatever was asked. `FsyncOnFlush` is `NORMAL` and
  `Flush` checkpoints the log. Expect slower appends on the default.

### Wire

Everything is additive except the trim record; no entry kind was
added and the header envelope is still `"weft": 1`.

- **`compaction` entries carrying a trim record are written with
  `"v": 5`** (`trim: {stubs: [{entry, call_id, content, is_error}]}`;
  golden `testdata/format5/compaction_trim.json`). An older build
  fails on such an entry with `ErrNewerFormat` — on purpose: it would
  replay the trim from its own options. A summary compaction is
  unchanged and carries no `"v"`. A trim entry written before this
  version (no record) is read as it was.
- `message`: `run_id` on a turn's prompt entry.
- `turn`: `policy`, `rerun`, `late_steps`; `canceled` now also set on
  a deadline.
- `receipt` (`"v": 3`): the status `accepted` with `msg`, `turn` and
  `run_id` — a queued send; `unanswered` on a delivered receipt. A
  build from before `accepted` reads the entry and does not restore
  the send.
- `pool_receipt` (`"v": 4`): the status `parked`
  (`testdata/format4/receipt_parked.json`).
- `approval_decision` (`"v": 2`): `request_id`, `always`; new `via`
  values `interrupt`, `child`, `parent`.
- `approval_audit` (`"v": 2`): `grant_id`, `grant_shared`, `key_id`,
  `decisions`; the step `signed`; the `resume` step's outcomes
  `started`, `completed`, `failed`.
- Header metadata key `weft.require_signed` (`"true"`). A build from
  before it ignores the key and does not enforce the rule.
- The signing challenge's domain string is
  `weft/approval-challenge/v2`, covering the request entry id and the
  run id. Not stored; in-flight v1 signatures do not verify.
- `thread/sqlite` migration `0003_leases_keyset`: `session_locks`
  gains `process` and `started`; `sessions` gains `gen` and `envelope`;
  the index `sessions_order(envelope, created DESC, id DESC)` replaces
  `sessions_created`; the title column is re-derived under the
  last-non-empty rule.
- Grant uses are counted from `approval_audit.grant_id`. A file whose
  uses were recorded only as prose (`detail`) is not counted: a
  `MaxUses` grant in such a file starts again.

### Model-visible changes (AGENTS rule 5)

Pool — every string is pinned by a test (ADR 0022 amendment §I):
- `SUBAGENT_DEPTH: delegation depth <n> exceeds the pool's limit <m>`
  — new; the depth refusal used to read `SUBAGENT_CYCLE`.
- `SUBAGENT_CYCLE: …` only for a real cycle.
- `SUBAGENT_CANCELED: agent "<name>" was canceled before it finished`
  — new, when `Cancel` or `Close` ends a sync child.
- `SUBAGENT_FAILED: agent "<name>" parked at an approval the pool could
  not surface: <cause>` and `SUBAGENT_FAILED: agent "<name>" failed:
  <cause>` (a failure outside a child run, or recovered from the
  ledger) — new.
- `DENIED: the delegation was canceled while awaiting approval` — what
  a canceled, parked child's model reads if its session is resumed.
- A delegating call is no longer re-executed by a decision; its result
  is the child's answer, once.

Turns
- After a mixed tool batch resumes, the model sees one complete tool
  message for the step. It used to see the partial one, with the
  parked call unanswered.
- A step whose write failed is no longer shown to the next turn as an
  interrupted call beside a duplicated message.
- A retried `Send` after a failed prompt flush shows the user message
  once.

Approvals
- No text changed. One case moved: a request that expired, or was
  interrupted, after it already held an approval is denied with the
  expiry or interrupt reason, not "conflicting decisions".

Compaction
- A recorded trim shows the same stubs under any options; a custom
  `Trimmer`'s stubs now reach the model; the first summary survives a
  trim; a truncated summary is never shown; `BeforeCompact`'s edits
  shape the summarizer's input; a branch summary made with
  `SummarizeLeft` is built from the compacted view. The marker, the
  stub text and the summary prompt are unchanged.

Session core
- A context is never built across a broken parent link (the file is
  refused); a salvaged session's context starts at the orphaned entry.

### Known limits

- **Windows is unexecuted.** jsonl's `LockFileEx` lock and sqlite's
  Windows liveness and start-time checks compile and pass
  `GOOS=windows go vet` (run by hand; CI has no Windows job). No test
  has ever run them.
- **Stale-writer detection is a count and the header's creation
  time**, not a content comparison of the entries.
- **sqlite's lock does not cross hostnames on its own.** A lock row
  left by a holder with another hostname is never taken over; a
  replacement container with a new hostname calls `sqlite.BreakLock`
  (`docs/thread-operations.md` §4).
- **`Recover`** never re-runs a child; reads a finished child's answer
  as its last assistant text (a structured `Output` answer is not
  recovered as such); cannot rebuild the agent ancestry above a
  rebuilt child (the depth limit still holds); and assumes one pool
  owns a storage's delegations.
- **`Audit()` is an index, not evidence**: entries are unsigned and
  unchained; whoever can write the storage can change them.
- **`s.Grant` is an unsigned, in-process call** under `RequireSigned`
  too.
- **The steer queue is unbounded**; an application that must cap what
  users pile onto a running turn checks `len(s.Queue())`.
- **jsonl `List` reads every header per call**; sqlite pages in SQL.
- **Session plumbing for the pool is still exported** on `Session`
  (`thread/poolplumbing.go`), and the option names still mix three
  dialects (`With*`, bare, `Require*`/`On*`); both wait for the
  freeze.

## obsdb 0.1.1 / obsdb/clickhouse 0.1.1 / otel 0.1.1 / studio 0.3.1 / studio/cmd 0.1.1 / runtime 0.1.1 — 2026-10-02

Patch releases for the fixes found after the 0.7.0 programme, tagged in
dependency order behind `thread/v0.9.0`: obsdb, then obsdb/clickhouse
and otel, then studio, then studio/cmd and runtime (which requires
thread v0.9.0). The root module is unchanged at v0.7.0.

### obsdb

- `DeriveSpan`/`DeriveRecord` read a numeric string attribute as the
  number it spells: thread mints `weft.turn` through metadata
  (`strconv.Itoa`) and the core stamps every metadata value as
  `attribute.String`, so the real chain delivered `"3"` — a spelling
  `attrIntOr` rejected, and every studio/obsdb run row read turn 0
  (sqlite never set the column; the recorded "thread off-by-one"
  diagnosis is refuted, the mint was always correct). Numeric
  spellings still read as before; a non-numeric string stays the
  default. ClickHouse already parsed the string via `toInt32OrZero`
  in its views — now pinned by a live test too. The pushed
  `obsdb/v0.1.0` and `obsdb/clickhouse/v0.1.0` tags carry the
  Go-side read bug (fixed by this patch tag).
- `Session(id)` (sqlite and clickhouse) reads the session's own
  grouped row directly instead of scanning the newest 500 sessions —
  a session older than the newest page 404'd in the detail while the
  list still showed it. Pinned on both backends
  (`TestSessionBeyondNewestPage`: 502 sessions, the oldest resolves).

### studio

- The step 8b routes no longer escape S4.6's panel-token scoping rule
  (programme audit P1-2): the fixtures export (`POST
  /api/playground/fixtures`) scopes by public id like every run-id
  route; the runtime link (`/api/runtime/register|commands|acks`,
  mounted behind a new server-identity guard via
  `RuntimeServer.MountGuarded`) and the breakpoints control refuse
  panel tokens outright — they are server-to-server and not
  public-id-shaped (register could overwrite a victim runtime's
  registration, the commands stream could replace its feed, acks
  could forge the state steer/approval routing trusts); steer now
  mirrors the approval route's fallback (the db row's public id; a
  run with no public id is outside every panel token). Pinned by
  `TestStep8RoutesRefusePanelTokens` (read-scoped token → 403 on each
  surface; the server token keeps working). The pushed `studio/v0.3.0`
  tag carries the gap (fixed by this patch tag).
- The web app's run page sends the bearer token on its paged events
  walk (the one raw fetch without it — under setups B/C every page
  401'd and the run page showed the error instead of the story);
  pinned by `use-run-events.test.tsx` (P1-3).
- The panel follows `next_after` past a terminal full page in all
  three event walks (`done` only means the run ended — a finished
  run with more than one page of events silently lost the rest);
  pinned by a panel test with `done:true` + `next_after` set (P1-5).
- The panel's experiment drawer omits an unchanged instructions
  override (the drawer pre-fills the registered prompt, and the
  scripted engine 400s on an instructions override — agents that
  register instructions could never run scripted); pinned in
  `playground.test.ts` (P1-8).
- CORS `Access-Control-Allow-Methods` includes `PUT` — the breakpoints
  control goes through the panel's `panelPut`, and the preflight
  failed it in cross-origin setups B/C; the CORS pin asserts the verb
  (P1-9).
- The runtime link's full-feed drop terminates the stalled stream
  (the feed channel closes; the SSE ends instead of pinging forever
  while every POST 503s on the nil feed) — `TestFullFeedEndsStalledStream`;
  and a late accepted-ack that resurrects a row the lost sweep took
  arms the finish watch, so a runtime that never finishes cannot leave
  it accepted forever — `TestLateAcceptedAckArmsFinishWatch`.

### otel

- `weftVersion()` reports v0.7.0 — the release step that owns the bump
  missed it (its own comment says so), so every span carried
  `weft.version=v0.6.0` past the release. The pushed `otel/v0.1.0` tag
  carries the stale string (fixed by this patch tag).
- The drop counter's logger is fixed at construction
  (`newDropCounter`): `dropped()`'s lazy `d.log` assignment wrote a
  plain field outside the atomics, a data race under concurrent agent
  runs (the existing WARN-throttle test covers the behaviour; tests
  now build the counter through the constructor).
- Content-on chains shape every content class the core's own
  `StripContent` table names: `Steered` message texts are redacted
  like other user text, `RunFinish.Pending[].Args` follows the
  adjudicated `ToolStart.Args` rule (redact-not-cap), and `Nested`
  recurses into the child event (its cut propagates to
  `weft.content.truncated_bytes`). With `ContentConfig.Redact`
  configured, steered user text and pending-approval tool args no
  longer ride unredacted to Studio/Local. Pinned by
  `TestShapeEventRedactsSteeredPendingNested`.

### runtime

- Registration reports `weft_version: v0.7.0` — the same missed bump
  as otel's. The pushed `runtime/v0.1.0` tag carries the stale string (fixed by this patch tag).

### CI / repo

- ci.yml: the `apidiff.sh "" store` step is gone (the store module was
  deleted; the script exits 2 for any module but root and thread, so
  the first CI run after push would have failed), and the gated
  ClickHouse job from `obsdb/clickhouse/README.md` runs the
  conformance suite (plus studio/cmd's hosted-backend test) against a
  `clickhouse-server` service container.
- `studio/web/dist-release/` — where `make studio-panel-asset` stages
  the release asset — is gitignored, so a staged release no longer
  shows as an untracked stray.

### studio/cmd

- The dev token the banner prints is the token the API wall checks:
  `serve` resolves the token once (new `serveBoot`) and hands the one
  value to both the wall and the banner. With no `--token` and no
  `WEFT_STUDIO_TOKEN` the old code drew two independent generated
  tokens, so the printed token 401'd against every `/api` call —
  setup B's documented "token printed at start" hand-off was broken.
  The pushed `studio/cmd/v0.1.0` tag carries the bug (fixed by this patch tag).
- SIGINT/SIGTERM shut the server down gracefully (the listener closes,
  in-flight requests get five seconds, streams that outlive the window
  force-close, then the studio's resources close) — a bare
  `ListenAndServe` cut SSE streams mid-frame and skipped `srv.Close`;
  and the banner no longer echoes a DSN's password
  (`clickhouse://user:***@host`). Pinned by
  `TestListenShutsDownGracefully` and `TestDBLabelMasksPassword`.

### obsdb/clickhouse

- Paging cursors compare in integer nanoseconds
  (`toUnixTimestamp64Nano` with an int64 bind): the driver renders a
  positional `time.Time` bind at whole-second scale, so the runs
  cursor (`Started < ?`) and the sessions cursor
  (`max(LastSeen) < ?`) floored S.<nanos> to S.000 and silently
  skipped every row in the same second before the boundary — rows
  lost at essentially every page boundary. The status cutoff
  (`LastSeen >= ?`) binds nanoseconds for the same reason (no more
  sub-second running band). Pinned by `TestCursorSubSecondPaging`
  (two runs/sessions within one wall second, paged through the
  boundary). The pushed `obsdb/clickhouse/v0.1.0` tag carries the bug (fixed by this patch tag).
- `SaveExperiment` surfaces every prior-read error except
  `ErrNotFound` (only a genuine not-found means "first save"): a
  transient read failure no longer silently resets an update's
  `Created` to the save time. Pinned by `TestExperimentCreatedSurvivesUpdate`.
- Migration 0002's engine shape is pinned offline
  (`TestSpecExperimentsEnginePresent`): `ReplacingMergeTree(InsertTime)`
  and `InsertTime DEFAULT now64(9)` — the two properties the step 8b
  review fixes rest on, previously guarded by nothing offline.

## 0.7.0 — 2026-10-01

The step 8b playground programme (ADR 0024, WEFT-PLAYGROUND.md P1–P5):
`ReplayPolicy` in the core, the playground's P1–P5 verbs across the
runtime link, Studio and the devtools panel, the experiments table in
obsdb, and the debugger rungs 3–4 (breakpoints, steer) on
runtime-started runs. Root is additive; the lanes' modules below
release and date here with it (the step 8 release, 2026-10-01).

### weft 0.7.0

#### Added

- `weft.ReplayPolicy` — a tool's side-effect class for re-runs
  (WEFT-PLAYGROUND.md §6 rule 3): `ReplayNever` (the zero value, and
  what an unannotated tool counts as — its calls are substituted with
  the recorded result or parked, never silently re-fired) and
  `ReplaySafe` (idempotent, side-effect free — a re-run may execute it
  for real). Set with `weft.Replay(weft.ReplaySafe)` beside the tool's
  other options; read back with `ToolDef.ReplayPolicy()`. The manifest
  records `replay_policy: "safe"` — never is the default and renders
  exactly as before, so no committed weft.json churns. This is the root
  change step 8b needed: the runtime link's register payload now
  reports each tool's real class instead of the honest-all-never
  placeholder of step 8a.

Lanes A2, B1, B2, C1 and C2 of the observability-data programme (ADR
0024), merged to main: the two new modules from A2 (the observability
database and the pipeline), B1's Studio rewrite on obsdb with the
store module deleted, B2's ClickHouse backend, C1's devtools panel,
and C2's `weft/runtime` module with the playground API. All of it
dates at the step 8 release.

### obsdb (new module)

- **The observability database** (ADR 0024 S3): the OTLP-shaped model
  (`Span`, `Record`, `Batch`), the derived weft identity (`Weft`,
  `DeriveSpan`/`DeriveRecord`), the `DB` interface (runs, sessions, the
  positioned event page with its gap detector, the transcript replay
  reads, spans by run or trace, public-id resolution), `DeriveStatus`
  (the four-row table: error span → failed; run_finish → succeeded;
  fresh last-seen → running; stale → interrupted, after
  `InterruptedAfter` = 30 s), and the live-lane hub (`Hub`, `Frame`,
  one hub-wide monotonic `Seq`, bounded per-subscriber queues, overflow
  drops the subscriber).
- **`obsdb/sqlite`**: the default backend — the S3.4 schema
  (`obsdb_migrations`, `spans`, `records`, `other_logs`, `runs` and the
  indexes), one writer connection plus a read pool, `Open(path,
  KeepDeltas())`. `Write` is one idempotent transaction: `INSERT OR
  IGNORE` on (run, kind, pos) and (trace, span); deltas counted and
  never stored (their own counter, so their absence never looks like a
  lost event); heartbeats never stored, they only move last-seen; a
  reordered batch's provisional start corrected when `run_start` lands.
  The returned DB implements `Hub()` and publishes every Write's frames
  before returning.
- **`obsdb/obsdbtest`**: the conformance table every backend runs (the
  storetest pattern) — round trip, idempotence, reordering, gaps,
  status at every boundary including the crash, sessions and public
  ids, children, paging cursors, the delta rule, the heartbeat rule,
  non-weft spans and records stored and returned by `Trace`.
- **`obsdb.FromOTLPTraces` / `obsdb.FromOTLPLogs`**: OTLP/HTTP export
  requests decoded into the model (scalars verbatim, arrays as `[]any`,
  kvlists as maps, bytes as base64), with golden protobuf and JSON
  fixtures under `obsdb/testdata` that step 6's ingest and the SDK path
  both pin against.

### otel (new module)

- **`otel.Install(...)`** — one line, several destinations at once, all
  active: `Local(path)` (the obsdb/sqlite sink, written synchronously,
  content on), `Studio(url, token)` (OTLP/HTTP protobuf, bearer token,
  logs 200 ms / spans 1 s batches, content on), `Datadog()` (the local
  Agent's OTLP intake on `localhost:4318`, content off), `Langfuse(host,
  pk, sk)` (`<host>/api/public/otel`, Basic `pk:sk`, traces only),
  `OTLP(url)` and `Exporters(spans, logs)` (content off). The returned
  function flushes and shuts down; call it on exit
  (`defer otel.Install(...)()`). With no options it writes the local
  sink only. `otel.Start` is Install with errors (and `NoGlobal` for
  tests); Install never panics or fails the program.
- **Per-destination content** (`WithContent(cfg…)`, `NoContent()`,
  `Signals`, `NoDeltas()`, `BatchDelay`, `Headers`, `Timeout`,
  `Insecure`, `DatadogEndpoint`): content-off chains clone each record,
  strip it with `weft.StripContent`, mark it `weft.content=stripped`
  and drop `messages` records; content-on chains apply the
  destination's `Redact` and `MaxBytes` (event and delta bodies only —
  never the transcript) and set `weft.content.truncated_bytes` when a
  cap cut. Every processor on the provider answers the Logs API's
  `Enabled` by event name, so a content-off-only pipeline makes the
  core emit no `messages` records at all.
- **Heartbeats**: the run tracker keeps the open runs (from
  `run_start`/`run_finish` records and the `invoke_agent` span end) and
  emits one `weft.heartbeat` record per open run every interval
  (`Heartbeat(d)`, default 10 s, 0 disables) — a long quiet tool call
  reads running, a crashed process stops heartbeating, and the sinks
  never store a heartbeat row.
- **`otel.FromSDKSpans` / `otel.FromSDKRecords`**: SDK data into the
  obsdb model, value-identical to the OTLP path (pinned against
  obsdb's golden fixtures; the SDK's status codes map onto OTLP's
  numbering).
- The standard variables configure extra destinations
  (`WEFT_STUDIO_URL`, `OTEL_EXPORTER_OTLP_ENDPOINT`/`_HEADERS`,
  `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT`, `WEFT_DB`);
  explicit options and env destinations combine, same-URL duplicates
  de-duplicate with the explicit one winning; `NoEnv()` turns them off.
  The core reads no environment variable — this module does, here
  only.

### obsdb/clickhouse (new module)

- The ClickHouse backend of `obsdb` (ADR 0024 S3.6, open-source per
  Q5): `Open(dsn string, opts ...Option) (obsdb.DB, error)` connects
  through clickhouse-go v2 with `async_insert=1,
  wait_for_async_insert=1` (a write returns once the server has
  flushed its batch), creates and versions the schema in
  `obsdb_migrations` — the SQLite backend's numbering rule, refusing a
  database whose schema is newer with `ErrNewerSchema` — and serves
  every `obsdb.DB` read (`Runs` through `GROUP BY RunId` with the same
  aggregates the AggregatingMergeTree columns carry, single-run pages
  through `FINAL`, `Sessions` as a `GROUP BY SessionId` over
  per-run-collapsed rows, status always derived through
  `obsdb.DeriveStatus`, never stored).
- Column compatibility with the OTel Collector ClickHouse exporter,
  pinned at **v0.162.0** (the version named in
  `migrations/0001_init.sql`): `otel_traces` and `otel_logs` keep
  every column name and type its `INSERT` names — `EventName`
  included (the one feature column the exporter probes the table
  for); the optional `*AttributesKeys` columns deliberately absent
  because the pinned exporter's default `INSERT` never names them
  (they belong to its json-mode tables) —
  and add the weft identity as materialized columns with
  `bloom_filter` skip indexes on `SessionId`, `PublicId`, `Agent` and
  `TraceId` (S3.6). A stock collector pinned to that version, with
  `create_schema: false`, writes into the same database; the
  collector-shape test inserts with its verbatim template column
  lists and proves the views feed the weft tables from those rows.
- `weft_records` as `ReplacingMergeTree(InsertTime) ORDER BY (RunId,
  Kind, Pos)` — the transport idempotency key (I4) — filled by a
  materialized view from `otel_logs` where `weft.record IN ('event',
  'messages')`: never `delta` (counted in `weft_runs`' `DeltaCount`
  high-water mark, never stored — D3/Q4), never `heartbeat` (no
  position; it only moves a run's last-seen). It carries its own
  content TTL, because a source table's TTL does not cascade through
  a view.
- `weft_runs` as `AggregatingMergeTree ORDER BY RunId` filled by two
  materialized views (from `otel_logs`, heartbeats included for
  last-seen, `run_start`/`run_finish` bodies parsed in SQL; and from
  `otel_traces`, the `invoke_agent` span detected exactly the way
  `obsdb/sqlite`'s `isInvokeAgent` does), its columns
  `SimpleAggregateFunction`: `min` for started, `max` for finished,
  last-seen, identity strings, the terminal flags and usage. Caller
  metadata is stored as the contract-filtered attribute JSON (the
  `obsdb.MetaOf` exclusion set, embedded in the view and pinned to
  `MetaOf` by a test), so `max` keeps real metadata and the metadata
  contract stays in one implementation.
- `TTL(content, meta time.Duration) Option`: overrides the retention
  windows — content (`otel_logs`, `weft_records`, `weft_deltas`) 30
  days, spans and runs 90 by default. Open applies a configured
  non-default window with `ALTER TABLE ... MODIFY TTL` after the
  migrations (idempotent, so the schema stays inspectable); the
  defaults are the migration's own and skip the ALTER. Values ≤ 0
  keep that class's default.
- `KeepDeltas() Option`: turns delta storage on for debugging —
  `Write` additionally inserts delta rows into `weft_deltas`
  (`ReplacingMergeTree` on `(RunId, Pos)`, their own counter so they
  can never touch the durable sequence); the option lives in `Write`,
  not the schema, because a view cannot be option-gated.
- The conformance table `obsdbtest.Run` (S3.5) runs against a live
  server, gated on `WEFT_CLICKHOUSE_DSN`, each subtest on a fresh
  database; the package README carries the one-line container recipe
  (clickhouse/clickhouse-server:25.8-alpine), the docker-compose
  equivalent, and the CI job with the service container.

### runtime (new module)

#### Added (step 8b)

- The playground's P1–P5 verbs over the link: `transcript_edits`
  (D2/D3 — a patch must name a call in the kept prefix, a rewrite may
  not drop a step's calls, the patched prefix must leave no call
  without a result and end at a step boundary, validated on both
  sides); `side_effects` `substitute` (the default: a parked call that
  matches a recorded call is answered with the recorded result over a
  chain of fresh run ids, the handler provably never re-fires; a miss
  stays parked), `park`, and `allow` (the opted-in set); the scripted
  engine (§5.5) — its own `weft.Model` over the source run's messages
  records, keyed like wefttest's fixtures, zero tokens, "no recorded
  turn" on a key miss, and the prompt trap closed (scripted +
  instructions/model override refused on both sides); fork mode (§5.4)
  — `Fork` at the source turn, the input becomes the fork's next turn,
  the fork stays open to the runtime so the panel can keep chatting;
  approval decisions on parked runs (ADR 0007's Approve/Deny/Resolve
  forwarded as `event: approve` commands); breakpoints (§8.3 — the
  stored tool set parked on every run this runtime starts) and steer
  (§8.4 — every ephemeral run carries a `weft.Steering` source; a fork
  in flight steers through thread's Steer policy).

- **New module `weft/runtime`** (WEFT-PLAYGROUND.md §10.2, ADR 0024
  [D6]): the playground's in-app side. One deferred call is the whole
  integration — `defer runtime.Install(
  runtime.Studio(url, token), runtime.Agents(...), runtime.Models(...),
  runtime.Limits(...), runtime.AllowSideEffects(...),
  runtime.Threads(store), runtime.Enabled(true))()`. Without Install
  nothing opens; even with it, only under `WEFT_ENV=dev` or
  `Enabled(true)` (§6 rule 1). The default endpoint is
  `otel.StudioEndpoint()`.
- The runtime link (§10.3): registers on connect and after every
  reconnect (manifests, model allow-lists, per-agent caps, budget,
  side-effect classes — all tools `never` until `ReplayPolicy` lands),
  receives commands over SSE with `Last-Event-ID` resume, acks every
  command **before** executing it (at-most-once: repeated ids
  ignored), handles `cancel` and `ping`. `runtime.Local(srv)` talks to
  the embedded Studio in-process — no socket.
- The executor (§5.2): `engine: live` + `thread: ephemeral` only
  (fork, scripted and `transcript_edits` are 8b); composes the run
  from plain RunOptions (`Instructions`, `OnlyTools`, `UseModel`,
  `Thinking`, lower-only `MaxSteps`/`Parallelism`, `temperature`,
  `ParkOn` for every tool not opted in with `AllowSideEffects`), and
  labels the run `weft.playground`, `weft.playground.command`,
  `weft.experiment.id`, `weft.forked_from`, `weft.public_id`,
  `weft.playground.actor` — never `weft.session.id`. Source
  transcripts resolve thread storage → local obsdb →
  `GET /api/runs/{id}/transcript`, cut at the `from_step` boundary; a
  whole-turn re-run that sends no input defaults its prompt to the
  source's own user message (§5.1: input *replaces* the turn's user
  message — the original exists by default).
  The runtime re-validates tool/model names and limits against its own
  registry and answers `rejected` when Studio's copy disagrees;
  per-experiment budget caps are counted from each command's own
  usage, a breach rejects the next command (`budget_exceeded`), and
  the app's own runs are never touched.

### studio 0.3.0 (breaking — the step 6 rewrite, the devtools panel, the playground)

#### Added (step 8b)

- The playground's P1–P5 in both surfaces: the panel's experiment
  drawer (§3 — registered-config pre-fill, tools off with the
  side-effect warning, model, thinking, input, engine, side-effect
  mode, thread mode `ephemeral | fork`, transcript edits on the kept
  steps, and the rung-3 breakpoint set), the result streaming in
  place labelled t·xN with the inline diff (the shared `lib/diff`),
  continue/skip/resolve on a parked experiment run
  (`POST /api/runs/{id}/approvals`, routed to the runtime that started
  it — the app's own turns are 403 there, viewer-only per PQ7), the
  2-way sibling compare (PQ3), and the saves (`keep as prompt` copies
  the text, PQ2 is post-v1; `save as fixture` hands off to Studio).
- The Studio playground (`/playground`, capability-gated): the split
  view with the variant switcher, per-variant runs side by side with
  their metrics (tokens, latency, tool calls), the pairwise diff and
  the compare table, E9's variants × inputs matrix (the definition
  saved via `POST /api/experiments`, every cell issued under the
  experiment's id so the budget caps the whole matrix), and the
  experiment history. The config column carries the thread mode and,
  on a continued run, the kept steps' transcript edits; the run card
  steers an in-flight run and decides parked calls (§2's parity rule —
  every verb renders in the panel or Studio, never neither). The
  panel hands off into it with run, step and
  the current overrides as query params.
- `POST /api/playground/fixtures`: a run's records as wefttest replay
  fixtures — wefttest's own file shape, key and naming, pinned by a
  round-trip (the files load under `wefttest.Replay` and answer
  byte-for-byte).
- The debugger's rungs 3–4 (WEFT-DEVTOOLS §8.3/§8.4):
  `PUT /api/runtimes/{id}/breakpoints` (capability `breakpoints`) and
  `POST /api/runs/{id}/steer` (capability `steer`), both acting on
  runtime-started runs only — `meta.debug_scope` says so, and both UIs
  repeat it (PQ7). The controls render only for the reported
  capabilities: the panel's drawer carries the breakpoint set (the
  rung-3 gate's "all in the panel"), Studio steers the in-flight run —
  both verbs in both surfaces' reach, §2's parity rule.
- obsdb: the `experiments` table (§10.4, PQ4) behind
  `SaveExperiment`/`Experiments`/`Experiment` on both backends (sqlite
  migration 0002; clickhouse 0002 as a ReplacingMergeTree), with
  `RunQuery.ExperimentID` selecting an experiment's runs and an
  obsdbtest conformance subtest.

#### Added

- `New(opts ...Option) *Server` with `Handler()`, `Close()` (closes
  only the database New opened itself — a DB passed through `DB(...)`
  stays its owner's) and `Runtime()` (the runtime link's in-process
  side — `studio/runtime` — when `Playground(true)` built it, else
  nil). Options: `Live(hub)` (default the DB's own hub
  when it implements `Hub()`, else an in-process `obsdb.NewHub()` fed
  by ingest), `NoIngest()`, `IngestToken(tok)`, `Token(tok)`,
  `AllowOrigins(...)`, `Playground(bool)` beside the kept `DB`, `Open`,
  `Base`, `Manifest`, `Title`, `Capabilities`.
- `studio/ingest`: the OTLP/HTTP receiver — `POST /v1/traces` and
  `POST /v1/logs`, protobuf and JSON, optional gzip, a 16 MiB limit
  after decompression (413 above), the publish-then-write pipeline
  (frames reach the hub before `DB.Write`; 503 `unavailable` on a write
  failure so the exporter retries), and the ingest token (loopback
  open without one — `api/meta` says so via `ingest_open`).
- `GET /api/live`: the SSE live stream — exactly one selector
  (`run`/`session`/`public_id`/`agent`), `kinds` over
  event/delta/messages/run (default `event,run`; heartbeats never
  forwarded), frame ids are the hub's Seq, `Last-Event-ID` resume with
  the gap backfilled from the database and deduped on
  `(run, kind, pos)`, a ping every 15 s, and `event: overflow` plus a
  close when a subscriber's queue drops it.
- Routes: `GET runs/{id}/transcript`, `runs/{id}/spans`,
  `traces/{trace_id}` (any trace, weft or not), `sessions`,
  `sessions/{id}`, `public/{public_id}`, `POST /api/panel-tokens`
  (mint an HMAC-signed `{public_id, scope, exp}` scoped to one public
  id; every data route refuses anything outside it, the agent live
  selector included). Runs and sessions lists gained the session,
  public-id and (runs) playground filters.
- `routes.go`'s route groups: the registration point the panel and
  the playground hook into from their own files (`panel.go`,
  `playground.go` set package-level hooks through var initializers;
  `Playground(true)` enables the playground's), with `api/meta`'s
  capabilities computed from the registered groups — never hard-coded.
- The devtools panel `<weft-devtools>` (WEFT-DEVTOOLS.md §5): a
  self-contained custom element built as a separate Vite library-mode
  artifact (`studio/web/vite.panel.config.ts`, committed at
  `studio/dist/panel/panel.js`, 63,475 B raw / 17.0 KiB gzip — budget
  80), sharing `lib/api.ts`, `lib/live.ts`, `lib/events.ts` and
  `lib/format.ts` with the Studio UI (V6, no React in the bundle).
  Rung 1 (§8.1): the header, the turn list scoped by public id with
  `parked` and the (inert-until-step-8) experiment slot, the turn view
  through the shared fold with reasoning collapsed, tool calls
  `name(args)` → result with truncation badges and span times, usage
  with cached/reasoning splits, read-only approvals, the honesty
  rules (interrupted, gaps, stripped content, max_tokens), the raw
  JSON toggle, the live tail with deltas, ⤢ deep links carrying run
  and step, lazy subagent expansion, a spans waterfall, and the §5.2
  keyboard (Alt+W primary — Q4 closed; Ctrl+Shift+W where delivered).
  The live tail upserts `run` frames by id (T20's fix) — one row per
  run, the newest state winning.
- `GET /panel.js` on `studio.Handler` (S4.2): the embedded bundle,
  static and unauthenticated, registered through routes.go's
  panel-group hook (always on, no capability of its own; `live` is
  what rung 1 gates on). A build without the bundle answers 500 in the
  API error shape.
- Panel mounting per §5.2/§5.3: `data-endpoint` / `data-public-id` /
  `data-token` / `data-position` / `data-open` / `data-auto` on the
  script tag or the element, the `window.__WEFT__.publicId` watch by a
  setter, `?weft=debug` and `localStorage.weft_debug=1` overrides, the
  studio-version check ("Studio is newer than this panel; update
  panel.js"), and fail-silent removal when `/api/meta` does not answer
  (one request, no retries, no console output).
- `studio/web/scripts/panel-gate.ts`: the Dv0–Dv2 gate driver (jsdom
  over a live Studio, driving the committed bundle — the self-hosting
  harness page is `examples/studio-local`'s `/`), and
  `scripts/panel-asset.ts`: stages `panel-<version>.js` + sha256 as
  the release asset for non-Go backends (V2; wired as `make
  studio-panel-asset`).
- **New package `studio/runtime`**: the runtime link's server side —
  `POST /api/runtime/register`, `GET /api/runtime/commands` (SSE),
  `POST /api/runtime/acks`, the registry of connected runtimes with
  `last_seen`, and §10.5's lost-command timers (30 s unacked; queued
  lost at disconnect; accepted lost after 10 min without a finish; a
  late ack still lands).
- **`studio/playground.go`** (routes.go's `playgroundGroupHook`,
  enabled by `Playground(true)`): the playground API — `GET
  /api/runtimes`, `POST /api/playground/runs`, `GET
  /api/playground/commands/{id}` — with §10.4's validation table (400
  unknown tool/model name, `input` with `from_step > 0`, and the
  8b-deferred modes answered "not yet available"; 403 raised limits,
  a refused side-effect tool, a panel token out of scope; 404 unknown
  runtime/agent/source run; 409 a reused command id; 503 no connected
  runtime). Capabilities `playground` and `runtimes` appear in
  `GET /api/meta` when `Playground(true)`.

#### Changed

- `Handler` is `Handler(opts ...Option)` over an `obsdb.DB` instead of
  `Handler(s store.Store, opts ...)`: `DB(db)` serves the database you
  pass (setup A: `DB(otel.LocalDB())` — the same handle weft/otel's
  Local destination writes), `Open(path)` opens an obsdb sqlite file
  (created when missing; panics at Handler time when it cannot), and
  with neither option Handler opens the history database at `$WEFT_DB`
  or `./.weft/weft.db`. The five read routes keep their JSON shapes as
  far as the new model allows: `tags` reads the run row's metadata,
  `model` rebuilds from the row's provider/model, `result` is always
  `null` (the store's result document is gone; the transcript route
  that replaces it is step 6), and events page through
  `obsdb.DB.Events` with the same inclusive `after` cursor, `done`
  now also covering rows that read interrupted at derivation time (a
  crash orphan's polling tail terminates).
- The JSON shapes are S4.3's: the run row carries the identity chain
  in full (`parent_run_id`, `trace_id`, `service`, `session_id`,
  `public_id`, `turn`, `playground`, `experiment_id`, `forked_from`,
  `meta`, `last_seen`, `pending`, `stop_reason`, `message_count`);
  `meta` reports `db`, `ingest_open` and `interrupted_after_ms`; event
  page entries carry `time` and the page carries `gaps`; spans map
  OTLP statuses to `unset`/`ok`/`error`. The UI follows: the live
  client (`lib/live.ts`), the transcript overlay on the fold, children
  joined by `parent_call_id` (subagent blocks fetch on expand), the
  time-axis waterfall when a run has spans, and the sessions, traces
  and live routes.
- `Token(tok)` now walls the whole API (bearer, or `?token=` for
  EventSource); CORS defaults to localhost/127.0.0.1 on any port when
  a token is configured, none in setup A.

#### Removed

- The store-era JSON keys `tags` (now `meta`) and `parent_id` (now
  `parent_run_id`), the dead `result` field on the run document, the
  inline `Nested` folding in the UI (children are separate runs), and
  `meta`'s `store` key (now `db`).
- `eventCache`, `storeKind`, everything typed on `store.RunRecord`,
  and the `weft/store` dependency; the 409 `newer_format` mapping went
  with the store's decode errors (unknown ids stay 404, other database
  errors 500 `internal`). `api/meta`'s `store` field reports the
  obsdb backend (best effort, dynamic type).

#### Notes

- The panel changes no public Go API beyond the bundle route:
  `panel.go` registers through the step-6 route-group hook; the panel
  token endpoints and CORS defaults were already step 6's.

#### Release asset

- `panel-v0.3.0.js` (sha256
  `29652795bc9ab349838bcad951b492af610363c5c1f8a6059c370acf78d672e4`
  beside it) — the devtools panel for non-Go backends; serve it from
  your app and add `<script type="module"
  src="/static/panel-v0.3.0.js" data-endpoint=… data-token=…
  data-public-id=…></script>`. Staged by `make studio-panel-asset`
  (RELEASE_DIR, default `dist-release` relative to `studio/web`);
  byte-identical to the committed `studio/dist/panel/panel.js`.

### studio/cmd (new module)

- Setup B's binary (S4.6, §10.1): UI + ingest + a dev token on
  `127.0.0.1:7331`, `--db sqlite://path` (an obsdb sqlite file,
  created when missing) or `--db
  clickhouse://user:pass@host:9000/db` (the hosted backend, wired at
  merge-B — this module is the one place that imports the
  obsdb/clickhouse driver, so the studio library never carries it),
  `--addr`, `--token`; `WEFT_STUDIO_TOKEN` fixes the token, else one
  is generated and printed. `DevToken()` generates it.

### store

#### Removed

- The module, entirely (step 5 of ADR 0024). Consumers read `obsdb`:
  weft-arena through per-scenario `weft/otel` pipelines
  (`weft.LoggerProvider`/`weft.TracerProvider` from
  `otel.Start(otel.Local(":memory:"), otel.NoGlobal())`, metadata via
  `weft.Metadata` on the run), Studio through `studio.DB`.
  `store/v0.1.3` remains resolvable from the module proxy for
  consumers pinned to it by tag.

## thread 0.8.1 — 2026-10-01

The step 8 release's lockstep tag: the module's only change since
0.8.0 is a comment reword in `thread/pool` (the store-era prose T18's
grep gate cleaned up), and the requirement moves to the tagged root
v0.7.0 (the two-phase rule, ADR 0005). Tagged so the step 8 train —
obsdb, otel, studio, runtime — releases against a current thread.

## thread 0.8.0 — 2026-10-01

The observability-data programme's thread step (ADR 0024 S5): the
session stamps its identity on every run it starts, and `thread/sqlite`
owns its migrations table so a session database can share one SQLite
file with the local sink. Additive for source (one option, one
unexported field); the migrations rename is the one breaking edge,
carried by a one-statement rename on Open. Tagged in lockstep with
`thread/sqlite/v0.2.0`, which bumps to weft v0.6.0 / thread v0.8.0 and
carries that rename; the module requires the tagged root v0.6.0.

### Added

- `thread.PublicID(id)` — the session's public id: an opaque,
  browser-safe handle (WEFT-OTEL-DATA-ARCHITECTURE §5), `WithMeta`
  sugar stamped into the header as `weft.public_id`. Create-time only,
  so it is what every backend's `List` Meta filter matches; a later
  `SetInfo` can add other keys but never rotates it.
- Every run a session starts — a send, a resume, an overflow re-run —
  carries `weft.Metadata` with `weft.session.id`, `weft.turn` (the
  counter the run id was minted from; the re-run names its new turn),
  `weft.public_id` when the session has one, `weft.session.forked_from`
  (`<session>#<entry>`) for a fork, and `weft.session.parent` (+
  `weft.session.parent_call`) for a pool child. It is appended after
  the caller's run options, so the session's keys win over a caller's
  colliding `thread.RunOptions(weft.Metadata(...))`; keys the session
  does not claim pass through. On spans and records alike (the core's
  S1.2/S1.3 wiring); the runs read `Session.Meta()` — the header
  overlaid with every info entry — while `List` keeps matching the
  header's create-time layer.
- The OTel API (`otel`, `otel/log`, `otel/trace`) becomes a direct
  requirement of the thread module (test-only imports; the versions
  the root pins). The SDK stays out — the identity tests implement the
  tracer and Logs API providers on the API's embedded types, the root
  module's stance.

### Changed

- `thread/sqlite`: the migrations table is `thread_migrations`
  (renamed from the goose-shaped `schema_migrations`), so a thread
  database and the local sink's `obsdb_migrations` can share one file
  with each module owning its versions (WEFT-OTEL-DATA-ARCHITECTURE
  §3.4). `Open` moves a pre-rename file across with one `ALTER TABLE`,
  run only when the `sessions` table exists and the new name does not:
  a `store` database pointed at the same `Open` keeps its own tracking
  table and rows untouched — including one whose recorded store
  version used to read as ahead of ours and refuse the open. Old
  thread files keep opening; the versions carry across and the
  migrations resume from the recorded number.

### Fixed

- `thread.Storage`'s `Query.Meta` doc claimed the `List` filter
  matched "the merged view `Load` returns"; no backend merges info-entry
  meta and `Load` returns the header as is (review 2026-09-30 §8.1
  item 4). Now says the header's create-time Meta. Doc only; behaviour
  unchanged.

## 0.6.0 — 2026-10-01

The observability-data programme's core step (ADR 0024): the run's
events, deltas and transcript leave the process as standard OpenTelemetry
log records, and the playground's per-run configuration joins the core.
Additive for source — the three dual options widen their return type to
a superset interface, the cycle's three allow-listed apidiff lines — so
root ships a minor.

### Added — observability data out (ADR 0024)

- The OTel Logs API (`go.opentelemetry.io/otel/log v0.22.0`) joins the
  trace API as the core's one dependency: no version moves (it requires
  exactly the pinned otel v1.46.0).
- `weft.LoggerProvider(lp)` selects the Logs API provider (default: the
  global, delegating, a no-op until an SDK registers).
- Every durable event (`run_start`…`run_finish`) and every delta is
  reported from `deliver` as an OTel log record on two counters —
  `weft.event.pos` contiguous from 0, `weft.delta.pos` for deltas — so
  dropping deltas never opens a hole in the durable sequence; `Nested`
  is not reported (the child run numbers its own). Bodies are the wire
  JSON (ADR 0004), stripped when capture is off; an errored
  `tool_finish` carries WARN; `run_start` carries the parent linkage,
  the manifest hash and `weft.version`.
- `messages` records at the transcript's five growth points — the
  repaired input at run start (index 0, never reported before), the
  tool message a resume creates or rebuilds (`attachResults` widened
  from created-only), each assistant message, each tool message, each
  steered batch. Their concatenation equals `RunResult.Messages`
  byte-for-byte; emitted only when capture is on.
- `weft.Metadata(kv)` (RunOption) and `weft.MetadataFromContext(ctx)`:
  caller pairs on every span and record of the run, inherited by
  subagent runs; limits (64 keys / 128 B key / 1 KiB value) drop and
  count (`weft.metadata.dropped`), never truncate.
- `weft.Content(bool)`, `weft.StripContent(ev)`, `ContentKind`: capture
  is resolved at each emission — the agent's option, else the standard
  `Enabled` question — and the core reads no environment variable.
- The run's identity on every span (S1.2): metadata verbatim plus the
  `gen_ai.conversation.id` / `session.id` / `user.id` mirrors; on the
  run span, `weft.parent.run.id` / `weft.parent.call.id` for subagents,
  `weft.manifest.hash` (computed at New for a named agent) and
  `weft.version`.
- `weft.version` rides at v0.6.0 and is pinned to a source
  (`version_test`): the const must equal the newest `v*` tag reachable
  from HEAD or the CHANGELOG's unreleased heading, so it cannot sit
  stale through a release again.
- A whole run with no tracer and no logger provider — the default
  program — is allocation-bounded (128 allocs/op measured, bound 160):
  the no-SDK path pays nothing for observability nobody asked for.
- `examples/otel` proves the emission end to end through the real SDK
  (an in-memory `sdk/log v0.22.0` exporter, a dependency of that
  example module only): every record correlates to the run's trace,
  event positions are contiguous, and the `messages` records rebuild
  the transcript byte-for-byte.

### Added — per-run configuration [D5, D7]

- `Instructions`, `MaxSteps`, `Parallelism` become dual Option/RunOption
  (the `Thinking` shape); per run the two limits may only lower — a
  raise is `ErrInvalidRunOption`, before any model call.
- The three allow-listed widenings (D9): the constructors return
  `InstructionsOption` / `MaxStepsOption` / `ParallelismOption` —
  superset interfaces every existing use as an `Option` still
  satisfies, which is what lets the same constructor work per run.
  The cycle's exact `.apidiff-allow` lines, reset at this tag.
- `weft.OnlyTools(names...)` narrows the run to named registered tools
  (unknown name → `ErrInvalidRunOption` before any model call);
  `weft.UseModel(m)` replaces the model for the run and rebuilds the
  WrapModel chain over it; `weft.ParkOn(tools...)` parks calls at the
  approval boundary as `RequireApproval` would (ADR 0007 applied per
  run). `Manifest` and `Tools()` keep reporting the static set.
- A changed configuration is recorded on the `invoke_agent` span:
  `weft.override.hash` (sha256 over the canonical JSON of every changed
  value) and `weft.override.*` per knob; absent on a plain run.

## thread 0.7.1 — 2026-09-30

The post-0.7.0 audit's one find (report:
`WEFT-POST-0.7.0-AUDIT-2026-09-30.md`), in the conformance harness,
not the session layer. No API change.

### Fixed

- `threadtest.CrashMatrix`: the steer and clear_queue points accept
  both legitimate durable orderings. A steer accepted while the second
  model call is in flight stays queued (receipt only), and a steer
  accepted in the between-steps window is drained into the next call
  and its message persisted after the receipt (ADR 0019's drain point,
  ADR 0011 §7). A `-race -count=10` soak on a loaded box tripped the
  old single-shape assertion. The clear_queue child no longer fails
  when the queue was already drained (n == 0 is a landing).
- The 0.7.0 entry's crash-matrix bullet now names all twelve crash
  points and the fourteen Append sites behind them.

## thread 0.7.0 — 2026-09-30

Hardening (plan §10): no new features. Every decoder fuzzed, every
session-layer write point crashed under kill -9 on both durable
backends, the operator-visible costs budgeted and enforced in CI, and
the security surface re-reviewed. (v0.6 — `thread/sandbox` — was
abandoned by the maintainer before this release; the train went
v0.5.0 → v0.7.0.)

### Added

- The fuzz gate covers the thread module's decoders (step 7.1), 10s
  per target in CI (`make fuzz-thread`): `FuzzDecodeHeader` — the
  envelope rule and the canonical fixpoint over arbitrary headers;
  `FuzzDecideSigned` — the signed-decision verifier over hostile
  input, which must answer only from its error catalogue and record
  nothing without the MAC; `FuzzGrantMatches` — the grant predicate
  engine, deterministic and never panicking over arbitrary grants,
  pointers, globs and arguments; and `FuzzDecodeEntry`'s seeds now
  span every format's goldens (the steering receipts of 3 and the pool
  receipts of 4 joined), `FuzzLoad` keeping the file-level contract.
- The kill -9 crash matrix (step 7.1) on jsonl and sqlite over every
  write point the session layer owns — fourteen storage.Append sites
  behind twelve crash points (`threadtest.CrashMatrix`): the prompt
  entry, the turn-end batch, an approval's park, a decision, a
  compaction, a steer's acceptance receipt, thread/pool's acceptance,
  mirror batch and settlement, a branch, a fork, the steer queue's
  dropped-receipt settlement, Resume's in-flight step persistence, and
  DecideSigned's decision and grant batch; the remaining two sites —
  Resume's expiry sweep and the auto trim's record — write the same
  entry shapes as the decision and compaction points. At each point a
  child process proves the write durable and dies; a fresh process
  reopens and continues the session. sqlite's run re-proves the lock
  takeover from a dead holder at every point.
- The budget suite (step 7.2), enforced in CI like correctness, each
  with its Benchmark for the number: append latency on Memory (time
  and allocs), opening a 100k-entry session on jsonl and sqlite
  (~3s measured, generous bounds), the context build after 50
  compactions, and List over 10k sessions — the Memory walk with a
  metadata filter, sqlite's page, count, title search and meta filter
  in one call each. The suite's finding, recorded for v0.8: both
  durable backends' List is a fleet scan per call (every header
  decoded, sorted in Go, then paged — Meta filtering needs the decoded
  header), so a full cursor walk pays the scan per page; SQL-side
  paging with the freeze's index work is the v0.8 proposal, Total
  already answers "how many" in one call.

### Reviewed, no findings

- Security (step 7.3): HMAC handling (constant-time compare, the
  length-prefixed canonical encoding, the fail-closed check order),
  id validation (`ValidID` vetted at every backend entry point — an
  id is always one path component), file permissions (files 0600
  umask-exact, created directories 0700), and the lock rules (flock
  per session, takeover from a dead holder).
- API (step 7.3): every exported name maps to a feature its ADR
  names. One observation for v0.8's freeze: the AGENTS.md API block
  has grown to ~175 lines across the release train — the one-screen
  property it was born with is gone, and the v0.8 proposal is to
  restructure it around a one-screen core with godoc carrying the
  rest; beside it, the budget suite's List finding (SQL-side paging
  for both durable backends, with the freeze's index work) joins the
  v0.8 list.

## thread 0.5.0 — 2026-09-29

`thread/pool` — bounded concurrent child runs, receipts, nested
approvals, explicit steering forwarding (plan §8, ADR 0022, decided
2026-09-29).

### Added

- `thread/pool.New(max)` — one process-wide FIFO semaphore over every
  child run the pool starts, wrapped or submitted; the bound doubles
  as the depth guard (a sync chain deeper than `max` could only
  deadlock, refused with `SUBAGENT_CYCLE`). `pool.IDs` for
  deterministic child session ids in tests.
- `p.Wrap(name, description, agent, opts...)` — a delegation tool built
  on the core's Subagent under a tool-level `weft.WrapTools`, no core
  change. Sync by default (the call waits, the result is the child
  session's answer); `pool.Async()` returns the receipt line as the
  tool result (model-visible, golden-pinned) for a later turn to read.
  `pool.ToolOptions` forwards weft tool options. Outside a session run
  the wrap falls back to the ordinary subagent path under the slot.
- Every pool child is a session of its own in the parent's storage,
  its header naming the origin (`Header.Lineage`: parent_session,
  parent_call_id) and, for wrapped children, the wrap name in metadata
  — the resume key a restarted process re-Wraps into existence.
- `p.Submit(ctx, parent, agent, prompt)` — the async primitive the
  application calls directly; `p.Cancel(receiptID)`; `p.Close(ctx)`
  (cancels every running child and drains); `pool.Receipts(parent)`
  (entry-driven, restart-safe); `p.Register(sessionID, agent)` — the
  restart hook for children no wrap names.
- Receipts as entries: the `pool_receipt` kind, `"v":4` (format-4
  goldens), the machine accepted → running → done | failed | canceled
  | capped (capped = a budget death: `ErrMaxSteps` or `ErrUsageLimit`),
  settlement carrying the child's stop text and usage.
- `thread.Usage.Delegated` — the ledger's third bucket, summed from
  settled receipts; every pool child bills there (a session-run child
  has no ride on the core's subagent roll-up, which bare `Subagent`
  children keep).
- Nested approvals (ADR 0021 §6 resolved by ADR 0022 §7): a child that
  parks mirrors its requests onto the parent — namespaced call ids
  (`<child>/<call>`), `Child`/`Wrapper` on the request entry — so
  `Pending()` surfaces them with their lineage and hides the parked
  wrapper; `p.Decide(ctx, parent, ds...)` records in the parent,
  replays into the child, resumes it, and completes the parent's parked
  call with the child's answer (resolve for a done child,
  resolve_error otherwise); a re-parking child mirrors again. Signed
  decisions verify in the parent (`s.Request`/`DecideSigned`) and the
  pool's empty `Decide` pumps the children they decided.
- `p.Forward(receiptID, msg)` — explicit steering of a running child
  through its session (ADR 0019 §7); nothing forwards implicitly.
- thread surface the pool builds on: `Session.Storage`, `Lineage`,
  `SessionFromContext` (a run's context carries its session),
  `AppendPoolReceipt`, `AppendApprovalRequests`, `WithLineage`,
  `WithMeta`; `Request.Child`.

## thread 0.4.0 — 2026-09-29

The second backend, per-step durability, and the live tail (plan §7,
ADR 0011 §7's amendment). The root gains `weft.OnMessages` (its own
0.5.0 entry below); thread requires it, released in lockstep —
`thread/v0.4.0` and the new `thread/sqlite/v0.1.0` beside the root
`v0.5.0` and its requirement bumps (openai, anthropic, google 0.3.8,
mcp 0.1.9, store 0.1.3, studio 0.2.1).

### Added

- `thread/sqlite` — its own module (`github.com/weftgo/weft/thread/sqlite`)
  so the modernc driver never enters `thread`'s dependencies: one
  SQLite file for every session, WAL, embedded migrations (a schema
  ahead of the binary refuses to open), the same wire lines as jsonl,
  the full `threadtest` table including the corruption rows, and the
  one-writer rule as a lock row per session — taken over from a holder
  whose process died, judged per-host. Crash tests kill a helper
  mid-append and mid-turn.
- Per-step durability (ADR 0011 §7): a turn's messages append to the
  tree as they join the run's transcript, so a crash mid-turn loses
  nothing emitted. A failed turn whose final form differs from its raw
  tail (an interrupt's golden completions) rewrites the tail on a fresh
  line; an overflow re-run's failed attempt keeps its messages on their
  own branch of the tree.
- The `thread.Watcher` capability on both durable backends:
  `Watch(ctx, session, afterEntryID)` tails a session — the backlog in
  arrival order, then each new entry exactly once, ending with the
  context. Readers never lock; a watcher is a reader that waits.
- `thread.Query` filters: `Meta` (every pair matched exactly against
  the header's metadata) and `TitleSearch` (the session's current title
  — the last info entry's — matched case-insensitively as a substring),
  with the existing cursor and limit paging the filtered set. sqlite
  keeps the title denormalised (migration 0002, backfilled once) so the
  filter never reads a session's entries.

### Changed

- `thread.Load` (both durable backends) reads one snapshot: a session
  deleted under a concurrent load answers `ErrNotFound` or the whole
  session, never a header whose entries vanished.
- A session header carrying a newer envelope fails `Load` as
  `ErrNewerFormat` (both durable backends) — the class the format rules
  name for it — not as line-1 corruption.
- `Resume` joins an in-flight resume through the arming registry: the
  armed resume, not the dangling tail, is the idempotency key.

## 0.5.0 / openai, anthropic, google 0.3.8 / mcp 0.1.9 / store 0.1.3 / studio 0.2.1 — 2026-09-29

Version 4 of the phase-3 plan (plan §7): the transcript observer in
the core, and the session features that need it. Tags cut in two
phases (ADR 0005): the root at v0.5.0, then the sub-modules — openai,
anthropic, google, mcp, store and studio as requirement bumps only —
all requiring the tagged root v0.5.0, beside thread 0.4.0 and the new
thread/sqlite 0.1.0 above.

### Added — the transcript observer (ADR 0006 note, TODO §5.12 shape (b))

- `weft.OnMessages(fn)` — a run option registering an observer the
  loop calls whenever messages join the run's transcript: the step's
  assistant message in its final shape (signed reasoning included),
  the batched tool message, and the messages a steering drain
  delivered. Exact bytes, deep-copied, in transcript order — what an
  incremental persister (a session layer) writes per step equals what
  `RunResult.Messages` holds at the end, without the lossy rebuild
  from deltas. Run-scoped like `Steering`; not inherited by Subagent
  child runs; panics contained and counted (`TapPanics`).

## 0.4.0 / openai, anthropic, google 0.3.7 / mcp 0.1.8 / store 0.1.2 — 2026-09-29

Version 3 of the phase-3 plan (plan §5): the steering hook in the
core, the overflow sentinel, and the modules that learn them. Tags cut
in two phases (ADR 0005): the root at v0.4.0, then the sub-modules —
openai, anthropic, google at v0.3.7 (the overflow mapping rides the
root's adapterkit, with per-adapter fixture tests), mcp at v0.1.8 and
store at v0.1.2 (the Steered event joins the run-id fold; mcp a
requirement bump only) — all requiring the tagged root v0.4.0, beside
studio 0.2.0 and thread 0.3.0 below.

### Added — the steering hook (ADR 0019)

- `weft.Steering(fn)` — a run option installing a steering source for
  that run: `SteerFunc` returns the messages to deliver at a safe point
  or nil (it must not block — drain a queue, do not wait on one), told
  where the run is through `SteerPoint{RunID, Step, Final}`.
- Two drain points: after a step's tool batch, once every call of the
  batch has its result — success, error, truncated, or denied — so the
  call/result pairing cannot be split; and at a final step, where a
  delivered message redirects the run into one more step instead of
  ending it. Never drained at the approval boundary or after a
  `StopWhen` condition fires: an intended end stays an end, and the
  source keeps its messages for a follow-up.
- Delivered messages are ordinary transcript, appended before the next
  step's `PrepareStep` chain runs, so request rewrites see them and the
  transcript stays the single source of truth. A message with any role
  other than `RoleUser` fails the run with `ErrInvalidSteer`.
- `Steered` — the one new event (wire `"steered"`): the delivered
  messages, between that step's `StepFinish` and the next `StepStart`,
  numbered from the run's Seq counter. `Nested` wraps it for child runs
  like any event.
- A redirect consumes a step and goes through the continuation checks
  (`MaxSteps`, `UsageLimit`, `DetectLoops`); on failure the steer is in
  `RunError.Result.Messages`, delivered but unanswered.
- Run option only: a child run started by a `Subagent` tool does not
  inherit the source — forwarding a steer to a child is a session
  decision made explicitly (consumer: `weft/thread` v0.3).
- `wefttest.NewSteers()` — a deterministic, step-keyed steering source
  (`.At(step, msgs...)`, `.Option()`), the replay-safe way to steer a
  recorded conversation: a steer that differs from the recording misses
  its fixture loudly.

### Added — the overflow sentinel (ADR 0020 §5)

- `weft.ErrContextOverflow` — the adapters wrap their provider's
  context-window overflow in it (`openai`, `anthropic`, `google`), both
  links preserved so `errors.Is` finds the sentinel and `errors.As`
  still reaches the vendor SDK's own error. The marker table the
  mapping reads (and `mw.Retry`'s classifier has always read) lives in
  one place now, `internal/adapterkit`; it gains anthropic's second
  shape ("exceed context limit"). `mw.Retry` never retries the
  sentinel, as it never retried the marker text — the consumer is
  `weft/thread` v0.3's compact-and-retry turn.

## thread 0.3.0 — 2026-09-29

Steering, interrupt, overflow (ADR 0019, ADR 0020 §5): everything a
Send can do with a busy session, on the session tree.

### Added

- **Busy policies**: `Steer`, `Interrupt` and `Rollback` join `Queue`
  (the default) and `Reject` — per session with `BusyPolicy(p)`, per
  Send with the new `As(p)`.
- **`Steer`**: the message is accepted at once — a queued receipt
  entry, flushed — and delivered into the running turn at the core's
  drain points (after the tool batch, every call paired with its
  result, or at what would have been the final step, redirecting it).
  A steer meeting a `StopWhen` end or an open approval boundary never
  drains: it defers to a follow-up turn, linked through `Turn.Next`.
- **Receipts** (`"v":3`, format-3 goldens): `queued → delivered |
  deferred | dropped` entries — delivered receipts join the turn's
  end batch atomically, naming the run; deferred entries name the
  follow-up; dropped is `ClearQueue`. `s.Queue()` lists the live
  queue. A queued receipt whose fate never landed (a crash) defers on
  reopen and its follow-up runs — accepted input is durable input.
  `weft.Steering` in `RunOptions` is refused: the session owns the
  steer queue.
- **`Interrupt`**: cancels the in-flight run (the mark survives the
  arm race — an interrupt landing between Send and the run's start
  fells it at birth); calls the partial left without a real result
  record the golden interruption text; an approval boundary the
  interrupt supersedes is denied with the interrupted reason; the
  message runs as the next turn.
- **`Rollback`**: an interrupt that also branches the leaf back to
  before the interrupted turn's receipt entry — the follow-up answers
  as though it never happened, its entries keeping their own line of
  the tree.
- **The overflow re-run**: a turn failing with
  `weft.ErrContextOverflow` compacts (reason `overflow`) and re-runs
  once over the shrunken path under a fresh run id; a second failure
  fails the turn with both errors joined. `ReRunOnOverflow(false)`
  turns it off.

## studio 0.2.0 — 2026-09-29

The Steered event in the Inspector: a steer folds as a user turn
attached to the step it followed, rendered between that step's card
and the next — accent-bordered, replay-jumpable, its words in the
event summary line. `Version` now reads v0.2.0; the module requires
the tagged root v0.4.0 and store v0.1.2.

## thread 0.2.0 — 2026-09-29

Approvals, complete (ADR 0021): the core's approval boundary made
durable, signed, granted and audited on the session tree.

### The 2026-09-29 review round (deep review of the branch)

Eleven fixes from a full review pass over thread v0.2, each pinned by
a test that fails on the old code:

- **Quorum vs the chain (P1)**: a grant match or an Approver's single
  approval under `Quorum(n>=2)` no longer arms an auto-resume that
  denied the call as "no decision" — the chain-decided hand-off arms
  only when every dangling call holds an effective decision, so the
  boundary waits for the second identity.
- **`Always` on a non-approve mints no grant (P1)**: a `Deny` or
  `Resolve` built with `Always: true` — through `Decide`, `DecideSigned`
  or the Approver — records its decision and nothing else; only an
  approve grants (ADR 0021 §4).
- **Repeated call ids stay separate occurrences (P1)**: the approval
  walk resets a call's request and decisions at the message that
  re-issues it, so ADR 0007's repeatable ids can no longer inherit an
  earlier occurrence's verdicts (a grant-approved re-issue read as
  "conflicting decisions").
- **An approve beside a resolve conflicts** (the fold's own words,
  both orders now), instead of silently discarding the approve.
- **A deny-grant's matches count against `MaxUses`** like an approval
  grant's — a bounded standing refusal stops refusing after its uses.
- **`Branch` while a turn runs fails with `ErrBusy`**: the runner holds
  the line the turn's transcript must land on; branching underneath it
  stranded the turn's messages on a context the model never saw.
  Branching off a parked boundary stays the documented escape hatch.
- **Decisions, grants and revocations flush**: under `FsyncOnFlush`,
  `Decide`, `DecideSigned`, `Resume`'s expiry denials and every
  `appendLocked` write (Grant, Revoke, Pin, …) are durable when they
  return — a decision or a revocation no longer lives only in the page
  cache.
- **The `Estimator` hook runs outside the session lock**: one that
  calls back into the Session (`Leaf`, `Pending`) deadlocked the turn's
  own persistence; every other caller hook was already consulted
  unlocked.
- **`nativeOf` never hashes a Model**: the middleware-chain walk
  compares structurally under a depth cap — an unhashable wrapper
  (a struct value with a slice field) panicked the map, and a
  self-wrapping one now terminates instead of walking forever.
- **`jsonl.readHeader` reads to EOF** (`io.ReadFull`): a short read
  made a valid header look torn and `List` silently skipped the
  session.

Also: the compaction log lines carry the `trace` attribute set ADR
0020 §4 promised; ADR 0020 gains a dated amendment recording that the
split turn is summarized in one pass (merged output, one model call);
`SummarizeLeft`'s and `ErrNotImplemented`'s stale docs, the
"stubbed until step 2.2" comment and the phantom
`testdata/approvals` golden reference are corrected; `ArgPrefix`
documents its empty-prefix sharp edge; the write-only `grantRef.shared`
field is gone; the README status line catches up to v0.3.6.

The release pass (same day) added three more, from the full-version
verification against ADR 0021 and plan §4: ADR 0021 gains a dated
amendment deciding the boundary-holds-the-tail-raw rule its citation
pointed at; a shared grant's audit detail is namespaced ("shared
grant …") so a store id colliding with a session grant's entry id
cannot inflate its use count (pinned); and the trigger's re-arm on
the resolving turn — implemented and cited but never pinned — has its
pin.

- New entry kinds with `"v":2` — `approval_request`,
  `approval_decision`, `approval_audit`, `grant`, `grant_revoked` —
  with goldens in `thread/testdata/format2/`; a v0.1 reader fails
  loudly on a session that used approvals, and every format1 golden
  still reads. A parked call becomes an `approval_request` in the same
  Append as its turn; `s.Pending()` rebuilds from entries, so pending
  approvals survive restarts.
- `s.Decide(ctx, decisions...)` records `thread.Approve`/`Deny`/
  `Resolve`/`ResolveError`/`ApproveAlways` durably — `ErrNotPending`
  before anything lands or runs — and resumes the boundary when it
  completes (`thread.AutoResume`, default on; `s.Resume(ctx)` forces
  it, denying undecided calls with the core's "no decision" text).
  The resumed turn links to the parked one through `Turn.Next()`. A
  `Send` while approvals pend queues behind the boundary.
- The decision chain — grants, then an optional `Approver` bounded by
  `thread.ApproverTimeout`, then the park — runs before a request is
  durable, and every step writes an audit entry.
- Grants: one tool plus argument predicates (`thread.ArgEquals`,
  `ArgPrefix`, `ArgGlob` — `*` spans separators, a command is not a
  path), session-scoped as entries (`s.Grant`, `s.Revoke`) or shared
  behind `thread.WithGrantStore`; expiry, `MaxUses` counted from the
  audit trail, deny-grants whose model-visible default "denied by
  grant" is pinned, and "approve and always allow" via
  `thread.ApproveAlways`.
- Signed decisions for transports that cross a process:
  `thread.NewKeyring` + `thread.WithKeyring`, `s.Request(callID)` mints
  an HMAC-SHA256 challenge (nonce, key id, the request's hashes),
  `thread.SignDecision` signs, `s.DecideSigned` verifies fail-closed —
  `ErrBadSignature` (constant-time), `ErrExpired`, `ErrReplay` (nonces
  are entries; the guard survives restarts), `ErrArgsChanged`,
  `ErrUnknownKey` — and `thread.RequireSigned()` closes the unsigned
  door.
- `thread.Quorum(n)`: approvals from n distinct approver identities
  resolve a call; conflicts resolve to deny with the pinned
  "conflicting decisions". `thread.RequestExpiry(d)` lapses undecided
  requests with a stated, pinned reason on the next resume.
  `thread.OnRequest(fn)` notifies when a request parks; `s.Audit()`
  returns the whole approval trail from the file.
- Decisions fold per parked run, never per bare call id: call ids may
  repeat across turns (ADR 0007), and a decision names its own
  boundary.
- `thread/examples/approvals`: park, restart, a signed decision,
  resume, a rejected replay, the audit trail — offline, output pinned.

## thread 0.1.0 — never tagged (shipped inside thread/v0.2.0, 2026-09-29)

The v0.1 window passed without a tag: the module's first tag is
`thread/v0.2.0`, whose history holds everything below.

First release of `weft/thread`: sessions as an append-only entry tree
(ADR 0011), durable through `jsonl.Open(dir)` or `thread.Memory()`,
with compaction (ADR 0020) on top. New module; it requires root
v0.3.7's `Agent.Model()` and imports nothing else from weft.

### Added — the format and the Storage contract (ADR 0011 §2, §5–§6)

- The sealed entry kinds — `message`, `turn`, `compaction`,
  `branch_summary`, `leaf`, `label`, `info`, `custom`,
  `custom_message` — one wire discriminator each; a `message` entry
  embeds the ADR 0001 wire verbatim. The `{"weft":1}` header
  envelope, the "v" minimum-reader rule (loud on the unknown and the
  newer, never a skip), time-sortable ids with an `IDs(func() string)`
  option, and goldens for every kind in `thread/testdata/format1/`.
- `thread.Storage` (Create, atomic multi-entry Append, Load, List,
  Delete) with `ErrNotFound`/`ErrExists`/`ErrLocked`/
  `ErrNewerFormat`/`ErrCorrupt{Line}`, `LoadReport` for torn tails
  and salvaged lines, and the optional `Flusher` and `Watcher`
  capabilities. `thread.Memory()` and `thread/jsonl` (0700/0600,
  id vetting, one-write appends + fsync, advisory locks, bounded
  header-only List) both pass the shared `threadtest` conformance
  table, crash-tested and fuzzed.

### Added — sessions, turns, branching (ADR 0011 §2–§4)

- `thread.Create`/`Open`/`List`/`Delete`; `Session.Context()` walks
  the leaf's path and repairs it; `Entries`, `Leaf`, `Path`, `Label`,
  `SetInfo` (title last-wins, metadata merged), `Custom`,
  `CustomMessage`, and `Usage` with the summarizer costs in their own
  bucket.
- `Send` appends and flushes the prompt before the run starts, runs
  the session's agent under `<session>-t<n>`, and persists the new
  messages plus the turn entry in one atomic batch under a
  `WithoutCancel` window — the partial transcript kept and repaired
  on failure, a cancel recorded as canceled, pending approval calls
  recorded for a `weft.Approve`/`Deny` resume. `Turn` is the receipt
  (`ID`) and handle (`RunID`, replayable `Events`, `Wait`); busy
  sends Queue (default) or Reject (`ErrBusy`); `thread.RunOptions`
  carries extra run options and refuses `weft.Messages`, `Prompt`
  and `RunID`.
- `Branch` navigates (with `SummarizeLeft` writing a branch summary),
  `Fork` copies the path into a self-contained new session, and
  nothing is ever deleted.

### Added — compaction (ADR 0020)

- The trigger: the provider-reported input of the last step plus the
  estimated tail that report cannot cover (recorded together on the
  turn entry), plus an estimated delta against `window − Reserve`; no
  window known means no automatic compaction and one warning. The cut keeps about `KeepRecent` tokens, lands on
  a user or assistant boundary, never between a call and its result,
  and splits turns larger than the window. Signed reasoning older
  than the compaction is stripped from the context the model sees.
- The summary: the session's own model by default (or `SummaryModel`
  with a fallback chain), the golden skeleton prompt behind the fixed
  `<weft-summary>` marker, iterative (each summary fed the previous),
  output capped at 0.8 × Reserve, and the serialized range with tool
  results capped and files as names. `PreviewCompaction`,
  `ApplyCompaction`, `Compact`, `Uncompact`.
- All five configuration layers: the knobs (`ContextWindow`,
  `ModelWindows`/`ModelReserves`, `Reserve`, `KeepRecent`,
  `TriggerFunc`, `MinTurnsBetween`, `MaxPerSession`,
  `WithEstimator`, `Disabled`), the summary options, the swap
  interfaces (`Summarizer`, `Compactor`, `Trimmer`, plus
  `ClearOldToolResults` with its golden stub), the hooks
  (`BeforeCompact` with Proceed/Cancel/Replace, `AfterCompact`,
  `CompactFailed`, `CheckSummary`) — panics contained — and the
  `NativeCompactor` seam with `PreferNative` and the text fallback.
  Extras: `Pin` (an entry survives every compaction) and the cost
  ledger.

### Added — docs and examples

- `thread/examples/session`: turns, a label, a branch, a fork, a
  previewed compaction and a reopen from disk, offline and
  deterministic. README "Sessions" section; AGENTS block 8; this
  changelog.

## 0.3.7 — 2026-09-29

### Added — the model accessors thread needs (ADR 0006 amendment)

- `(*Agent).Model()` — the model as the loop calls it, `WrapModel`
  middleware included, in the installed order. The session layer
  (`weft/thread`, v0.1) summarizes with the session agent's own model
  by default (ADR 0020 §2).
- The `Unwrap() Model` convention on model middleware wrappers, beside
  `Info()`/`InfoOf`: `mw.Retry`, `mw.Fallback` (its primary — the
  chain's first model, the one `Info` names), `mw.Log` and
  `mw.RepairJSON` implement it; `weft.Unwrap(m)` walks one level and
  returns nil on a non-wrapper. Callers walk a chain one middleware at
  a time without knowing the wrapper types.

## studio 0.1.0 — 2026-09-28 (the Inspector, T1)

First release of `weft/studio`: the Inspector over a run store, served
as one read-only `http.Handler`. New module, no core or store change.

### Added — the mount surface (ADR 0018)

- `Handler(store, opts...)` serves the embedded UI and the JSON API;
  options `Base` (the mount path, default `/studio/`, written into the
  shell's `<base href>` per request — the same bundle mounts
  anywhere), `Manifest` (agent/tool cards), `Title`, and
  `Capabilities(...)` — ADR 0018 §8's hosting seam: the open handler
  reports none, a hosted server declares what it carries, and the UI
  gates deployment-specific screens on the one
  `api/meta.capabilities` list.
- The read-only API, golden-pinned on the Go side and type-mirrored in
  TS against the same fixtures: `api/meta`, `api/runs` (agent,
  status, tag, and cursor paging — `parent` absent means top-level
  only), `api/runs/{id}` (row + the store's own result document +
  children), `api/runs/{id}/events` — **paged, never inline**
  (`after`/`limit`, `next_after`, `done` only when the run finished;
  the T2a live tail is this endpoint read from the last position;
  sliced from one `Get` behind a finished-run LRU), `api/manifest`.
  A recording this weft cannot decode is a 409 `newer_format` with
  the upgrade message; unknown-event results likewise.
- `studio/examples/basic`: records a tool-call run, a subagent run,
  and a failing run into SQLite and serves Studio on 127.0.0.1:7331.

### Added — the Inspector UI (T1: A1–A4, B1–B2, B7, B9–B10, H1, L3)

- Runs list: every status shown (crash-orphaned rows read
  interrupted), filters and the paging cursor in the URL, manual
  refresh, copyable mono ids, token columns with cached/reasoning
  splits on hover.
- Run page: steps with model text, reasoning collapsed, tool calls
  with pretty-printed args and their results (error-as-data in
  mustard with the `ToolError` code), subagents inline with usage
  rollups and links to child run pages, truncation badged from the
  loop's own markers, raw JSON one `r` away, the local-recording
  note. Every view, selection, and replay position is a URL.
- Guaranteed replay over the event index (events carry no
  timestamps): play/pause, 1×/4×, scrubber over the seq gutter, `[`
  and `]` step, `t` in the URL.
- Agent and tool cards from the manifest: schemas as trees, per-tool
  policy chips, source file:line.
- Theme: the landing page's paper-loom tokens (Geist/Geist Mono
  self-hosted, offline), the Studio event/status palette
  (contrast-checked), system/light/dark with no flash.

### The UX review pass (2026-09-28, before tagging)

A second look at the T1 UI as a debugging tool, with the fixes it
found:

- Run page: the prompt and the answer sit above the fold, with the
  error for a failed run; facts read as words (`2 steps · 14 events ·
  30 in / 15 out · took 2ms`, elapsed while running). Tool calls are
  one-line rows — name, an args summary, `ok · 29 B` or the
  `ToolError` code, `running…`, `never completed`, the truncation
  badge — opening to the args and result windows; JSON is
  pretty-printed and coloured; long windows fold at 24 lines. A
  failed run's open step says *failed during this step*, not *in
  flight*. Subagent steps render through the same step body as the
  parent's. Every step and call has *replay to here* (the fold
  carries stream positions).
- Trace, the default view: a flow strip (one pill per step, arrows
  for results fed back, the end state), then a waterfall | detail
  split. The waterfall is the run — steps, tool calls and subagent
  runs (their own steps and calls nested) as spans on the event
  position axis, one tree, one playhead; rows carry a kind icon, a
  badge (stop reason, ok, the ToolError code, running, never) and
  the event range; ↑↓ select, ←→ fold, expand/collapse all; a bar
  click seeks. The detail panel reads the selected span as detail
  (the same step and call bodies as the story), events (that span's
  slice of the stream) or json (the folded node), and renders the
  fold AT the playhead, so scrubbing replays inside it too. The
  trace opens on whatever went wrong (a tool error, the step a run
  died in), else the first step. `?sel=` and `?d=` name the
  selection and mode; `e`/`s`/`r` switch trace/story/raw; `j`/`k`
  walk spans. The split keys on the content width (a container
  query, not the viewport — the sidebar takes 16rem) and a toggle
  forces side by side or stacked, remembered per browser. Open
  spans fade, never-completed ones hatch, the step a run died in
  reads red. The `Waterfall` component is axis-
  agnostic (Span[] over a numeric domain) so T2a's timed spans and
  the live view draw on it as is; the step story stays as `story`.
- Replay: the gutter is bucketed (never wider than its box, a click
  lands where it looks), a playhead line, a readout naming the event
  at the playhead, `[`/`]` jump by step or tool boundary, `,`/`.`
  move one event, Home/End on the focused bar.
- Raw: an events explorer (position, kind, type, one-line summary;
  kind filters with counts, full-text search, expand to the event,
  replay-to-here, paged at 500 rows) and a collapsible document tree;
  copy and download for both.
- Runs list: status as dot + word, the error under a failed run's id,
  the whole row opens the run, elapsed for running runs, filters that
  mirror the URL and apply on enter/blur, active-filter chips, a
  "no runs match" state distinct from "no runs recorded", the select
  shows its label. ⌘K jumps to any recent run.
- Fixes: shortcuts no longer fire with ctrl/⌘/alt held (ctrl-r
  toggled raw *and* reloaded); collapsible chevrons rotate; the
  events tail asks from `last+1` instead of re-fetching the last
  event each poll; the stream array is fresh per publish; `?before=`
  seeds the first page; the shell's location-dependent parts render
  client-only so a deep link can never hydration-mismatch (which
  would drop the runtime `<base>` and break every module script).

### Fixed — the release review (2026-09-28, before tagging)

- `api/runs/{id}/events` panicked (negative slice capacity) on an
  `after` past the end of the stream; it now answers the empty,
  done page. Regression cases cover `len+5` and the int64 maximum.
- A missing hashed asset under `assets/` is a 404, not the HTML shell
  served under a script's name.
- `Title` and `Base` are HTML-escaped where they enter the shell.
- The runs list and the ⌘K palette shared one query-cache key with
  different shapes (a plain page vs. an infinite query): opening the
  palette and then the list crashed it. The list has its own key.
- When a run finished, the closing drain read one page only; a burst
  of more than one page at the end stayed truncated. It follows
  `next_after` to the end.
- Pausing replay writes `t`, as the URL contract says.
- A subagent's tool call keyed its span `c:<callID>` like the
  parent's, so a reused id (`call_1`) made `?sel=` pick the wrong
  span. Child call keys carry the child run id, as step keys do.
- Deep subagent trees fold on first load: the fold is applied once
  the spans arrive, not only at mount.
- CI gains the `studio` job (`make studio-check` under Bun 1.3.14):
  the freshness, budget, and web-test gate this section describes
  now runs on every push and PR.

### Engineering

- The UI is a TanStack Start SPA prebuilt into a committed, embedded
  `studio/dist` (~319 KiB gzipped of a 600 KiB budget): no build step
  for users. `make studio-check` gates dist freshness (two clean
  builds are byte-identical), the size budget, and the web
  typecheck/tests; `make test`/`lint` cover the Go side via go.work.
- Fully offline by construction: every resource the app loads is
  same-origin; the CSP allows no external script, style, or
  connection.

## store 0.1.1 — 2026-09-27 (review pass over 0.1.0)

### Fixed — the durability races a second look found

- **Concurrent first Opens on a fresh file no longer fail.** Two
  things raced when two processes opened a brand-new database at once:
  the per-connection `journal_mode(WAL)` DSN pragma (the mode switch
  wants a brief exclusive lock SQLite will not wait for — instant
  `SQLITE_BUSY`, unretried by busy_timeout) and the migration loser,
  whose pre-lock read of `schema_migrations` made it re-run a migration
  the winner had applied ("table already exists"). WAL is now set once
  by the first `Open` through a converging switch (`setWAL`: the
  winner writes WAL into the file header, everyone else reads `wal`
  back and moves on), and each migration re-checks the applied version
  inside its own transaction — the loser skips what the winner applied.
  Steady-state pragmas (synchronous, busy_timeout, foreign_keys,
  `_txlock=immediate`) stay per-connection, as before.
- **A heartbeat touch in flight when the run ends can no longer land
  after the closing write** and resurrect a `running` row that reads as
  interrupted half a minute later: touch and end serialize on the
  run's write lock, end's write last.
- **A run id that restarts in-process closes the old run's log**: its
  orphaned heartbeat ticker otherwise kept re-saving the stale running
  row under the id and clobbered the new run's — including its closing
  write.

### Fixed — loud where 0.1.0 was silent

- `store.UnmarshalResult` now enforces the `{"weft": N}` envelope
  (ADR 0010 §2.3): a result document from a newer format fails with
  `store.ErrNewerFormat`, and one with a result but no envelope fails
  outright — neither decodes with this build's tags. The migrations
  table already enforced the same rule for the schema; this is the
  document-level half.
- Every JSON column the SQLite backend reads (model, usage, tags,
  result) decodes loudly, naming the run and the column, instead of
  coming back with blanks; `List` still never reads events or results,
  so a run that cannot be opened still lists.
- The JSONL event reader names the first skipped line, not just the
  count, as ADR 0010 §2.5 promises.
- `store.Record(nil)` panics at the call site (Subagent's nil-child
  panic is the core's precedent) instead of failing on — or silently
  dropping — every write.

### Fixed — aliasing and matching

- Neither backend aliases a caller's `RunResult` or tags: Memory clones
  through the store's own codec on Save and Get, and every returned
  tags map is a copy — editing a returned record changes nothing on the
  next read.
- A tag a record does not carry never matches a tag query, even when
  the queried value is `""` — sqlite's `json_each` filter and Memory
  now agree; keys that would need escaping in a JSON path still filter,
  because the key is a bound parameter.

## 0.3.6 — 2026-09-27

### Added — the observation accessors (`AgentFromContext`, `Agent.Logger`)

- Writing the store's `Record` from a satellite found the one thing
  composition cannot reach: `weft.Option` is sealed, so a satellite
  composes `weft.Options(weft.Tap(...), weft.OnRunEnd(...))` and never
  receives the agent it was installed on. `weft.AgentFromContext(ctx)`
  returns the agent running on ctx — the ancestry chain's head, nil
  outside a run — and `(*Agent).Logger()` returns the run lines' sink.
  Both observation-scoped, read-only, no seam (ADR 0016's amendment);
  the store records the run's manifest hash through the first
  (ADR 0010).

## 0.3.5 / store 0.1.0 — 2026-09-27

Phase 2b step 1 (`docs/phase2b-store-plan.md`): the run store, and the
core additions it needs. Tags cut in two phases (ADR 0005): the root
at v0.3.5 (OnRunEnd) and v0.3.6 (the observation accessors), then the
sub-modules — openai, anthropic, google at v0.3.6 and mcp at v0.1.7
(requirement bumps only) — plus the new `store/v0.1.0` requiring the
tagged root v0.3.6.

### Added — core: `weft.OnRunEnd(fn)`, the outcome observer

- Called exactly once per run, after `RunFinish` is delivered or the
  `RunError` is built, before `Run` returns. It is the failure signal a
  tap cannot carry — a failed run emits no event after its last
  delivered one (ADR 0004) — and neither middleware seam wraps the
  run. `res` on failure is the `RunError`'s partial transcript; a child
  run's fires inside the parent's tool call; a panicked run fires
  nothing; a panic in an observer is contained and counted with the
  tap panics. Not a third seam (ADR 0006's note); ADR 0016's amendment
  records it as the fourth observation output.

### Added — module `weft/store` (v0.1.0): run records, SQLite first

- **What a record holds** (`store.RunRecord`): the run's identity —
  id, parent id and parent call id when it is a subagent's child, agent
  name, model, manifest hash, weft version, started/finished/
  heartbeat, status, tags — its event stream (every `weft.Event`,
  `Nested` inline, in `Seq` order), and its result (`RunResult`; on
  failure the partial plus the `RunError` text). **What it is not: a
  checkpoint** — weft records what happened and replays it; resume
  stays the approval boundary (ADR 0007). The format is ADR 0010's.
- **`store.Record(s, store.Tags(...))` is the tap**: one `Append` per
  event as it arrives — nothing buffered, so a crash loses nothing that
  was emitted and the Inspector can tail a live run. The row is
  written at `RunStart` as `running`, the heartbeat bumps per event and
  on a ticker while the run is alive but quiet, and `OnRunEnd` closes
  it — `succeeded` with the result, `failed` with the partial and the
  error text. Store errors are logged through the agent's logger and
  never fail the run; a failed append marks the record degraded in its
  `Err`.
- **`interrupted` is derived, never stored**: `status == running` and a
  heartbeat older than `HeartbeatTimeout` (30 s) reads as interrupted —
  a crash leaves evidence, and a live run in another process is not
  mistaken for a corpse.
- **Child runs own records**, linked by `ParentID`/`ParentCallID`, and
  stay inline in the parent's stream (ADR 0004's total order is the
  record's order). `Query` with an empty `ParentID` lists top-level
  runs only, so children do not flood the list view.
- **`List` returns no events and a `Total`** (Mastra's `listTracesLight`
  lesson), paged by a `Before` cursor on `Started`, not offsets
  (LangGraph); `Get` returns everything; `Delete` is in the contract
  from day one — children survive a deleted parent, orphaned by
  design.
- **`store/sqlite`** (`sqlite.Open(path)`; `":memory:"` works): CGO-free
  `modernc.org/sqlite` with Crush's pragmas (WAL, `synchronous=NORMAL`,
  `busy_timeout`, `foreign_keys`, `_txlock=immediate`), embedded
  migrations tracked in `schema_migrations` — a database written by a
  newer weft fails `Open` with `ErrNewerSchema`, never a silent
  misread. An event whose `type` this weft does not know fails `Get`
  with `ErrUnknownEvent` naming it and the run; `List` still works, so
  an older Inspector shows the run and says why it cannot open it.
- `store.Memory()` is the in-process backend (tests, examples, and the
  reference the SQLite suite is compared against); both run the shared
  `storetest` conformance table under `-race`.

### Fixed — release hygiene

- The OTel instrumentation `version` constant rides at the tag again
  (v0.3.5); it had drifted at v0.3.0 through the 0.3.1–0.3.4 releases
  (ADR 0005's amendment: tagging a release sets it).

## 0.3.4 — 2026-09-27

The pass over 0.3.1–0.3.3 (and mcp 0.1.4–0.1.5): the fixes hold, one
of them changed a behaviour it did not document, and two snippets
drifted. Tags cut together: root, openai, anthropic, google at v0.3.4;
`mcp` at v0.1.6. Root API unchanged (apidiff clean); the adapters'
`MaxRetries` gains a meaning for 0.

### Changed — openai, anthropic: `MaxRetries(0)` switches the SDK's retries off

- **0.3.3 put the wait for response headers under the idle timer**, and
  that wait contains the SDK's own transport retries and their sleeps.
  The Anthropic SDK honours any `retry-after` uncapped and the OpenAI
  SDK any under a minute, two retries each by default — so a 429 whose
  ask the SDK would have slept out and retried successfully now fails
  at `IdleTimeout` (60s), as `ErrStreamIdle`, with the 429 and its
  header discarded. `mw.Retry` retries that on backoff rather than the
  provider's ask; without it the caller reads a stall where the cause
  was rate limiting. The documented gap "0 cannot disable it — keep a
  zero-retry client via `Client(c)`" is closed: `MaxRetries(0)` now
  forwards 0 (a negative n is 0; absent, the SDK default of 2 still
  applies). That is the pairing for `mw.Retry`, which then owns every
  retry, honours the ask itself under `MaxWait`, and shows it in `Log`.
  Pinned in both adapters (a 500 draws one request under 0, three
  under the default). `IdleTimeout`'s doc on all three adapters, and
  `mw.Retry`'s, now say the pre-headers wait is the first gap and what
  it contains; google's SDK retries nothing unless asked, so only its
  doc changes.

### Fixed — docs

- README's mcp snippet and `mcp/doc.go` taught `tools, _ :=
  mcp.Tools(...)`, which since mcp 0.1.4 silently discards the
  `*ImportError` naming the skipped tools — the one outcome the ADR
  0015 amendment rules out. Both show the `errors.As` idiom the client
  example already used.
- "The first occurrence stands" for a repeated mcp tool name is the
  first *importable* occurrence: a skipped earlier entry claims
  nothing, so a later valid tool under that name imports. Stated in
  `Tools`' doc, the ADR amendment, and pinned
  (`TestToolsSkippedFirstEntryDoesNotBlockLaterSameName`).

## mcp 0.1.5 — 2026-09-27

Reviewing 0.1.4 found two defects in the same untrusted-input class;
only `weft/mcp` changes (`mcp/v0.1.5`).

### Fixed — `mcp.Tools`

- **An empty server-side name is skipped under a `Prefix` too.** The
  0.3.1 check tested the composed name, so `{"name": ""}` under
  `Prefix("gh_")` imported as a tool called `gh_` whose handler called
  the remote tool `""`. The check reads the server's name.
- **A name repeated in one listing is skipped and reported** (the
  first occurrence stands). Both used to import and `weft.New`
  panicked on the duplicate later — untrusted input escaping as a
  panic.
- `mcp/examples/client` shows the `errors.As(err, &skipped)` idiom the
  ADR 0015 amendment documents.

## mcp 0.1.4 — 2026-09-27

The one item 0.3.3 left open, decided: ADR 0015's 2026-09-27
amendment. Only `weft/mcp` changes (`mcp/v0.1.4`); the root and the
adapters stay at v0.3.3.

### Changed — `mcp.Tools` fails the tool, not the listing

- **A tool that cannot be imported no longer fails the whole import.**
  `Tools` returns every importable tool and, when any was left out, an
  `*mcp.ImportError` naming each skipped tool (`Name`, `Index`, `Err`
  carrying `ParseSchema`'s cause; `Unwrap() []error`). The slice is
  usable whether or not the error is set — `*weft.RunError`'s
  partial-result shape — so the caller decides with `errors.As`
  whether a skipped tool is a warning or a stop. A nil entry and an
  empty name are reported the same way, by index; a listing failure
  (transport, ctx) is still the plain error with no tools. Callers
  that returned on any error see the same outcome as before; callers
  that want the good tools now get them. The field's clients (AI SDK,
  Mastra, pydantic-ai) pass schemas through unparsed and learn of a
  bad one from a provider 400 that fails the whole step — weft keeps
  its stricter import and now uses what it learns to isolate rather
  than to refuse.

## 0.3.3 — 2026-09-26

The P3s the 2026-09-24 review deferred, landed — every one of them
except the mcp schema-import skip-and-aggregate, which contradicts ADR
0015's standing decision (one bad schema fails the import loudly) and
waits for an ADR, not a patch. One of the corpus rows found a defect
of its own. Tags cut together: root, openai, anthropic, google at
v0.3.3; `mcp` at v0.1.3. Root API is additive only (two conformance
helpers, one `Caps` field).

### Fixed — a schema could advertise fields no argument could reach

- **`Tool` and `Output[T]` (reflected schemas) panic at
  construction on an embedded pointer to an unexported struct type**
  (`type input struct{ *base }`). encoding/json can marshal such a
  field when non-nil but can never unmarshal into it ("cannot set
  embedded pointer to unexported struct type"), and every weft schema
  is decoded into a zero value — so the flattened fields were
  advertised, unreachable, and every argument the model sent came
  back as `field "id": expected string, got string`. The panic names
  the type and the fix (embed the value, or export the type). Found
  by the corpus row the review asked for; an embedded pointer to an
  *exported* type is pinned working.

### Fixed — adapters

- **google folds `toolUsePromptTokenCount` into `InputTokens`.** genai
  bills those tokens outside `promptTokenCount`; tool-heavy runs
  undercounted input and `UsageLimit` budgeting skewed with it.
  Pinned on the tool round-trip fixture (9+3).
- **The idle timer now covers the wait for response headers in openai
  and anthropic.** Both opened the stream synchronously, so a server
  that accepted the connection and never answered stalled until the
  caller's ctx deadline — forever without one — and never produced
  `ErrStreamIdle`. The request is opened on the reader goroutine, as
  google's always was. New conformance case
  `idle_timeout_before_headers`, green on all three.
- **openai and google send no tool choice without a catalog**, the
  guard anthropic already had: `tool_choice` / `toolConfig` alongside
  an empty `tools` is a provider 400 for direct `Model.Stream`
  callers (the loop already refuses a forced choice with no tools).

### Fixed — the core fails loud on a nil tool at `New`

- A nil `*ToolDef` handed to `New` panics naming the mistake, as a
  duplicate name does; it used to be skipped silently while the
  runtime snapshot path fails the run with `ErrNilTool` for the same
  thing.

### Added — conformance

- **`provider_error` case**: a 429 with `Retry-After: 7` from the
  provider must reach the caller as a `*RunError` with the SDK's own
  error type still on the chain — `mw.HTTPStatus` reads 429 off it on
  every adapter, and `mw.RetryAfter` reads the header where the SDK
  keeps the response (`Caps.ErrorHeaders`: openai and anthropic yes;
  google no — genai's `APIError` carries no headers, so `mw.Retry`
  backs off on Gemini 429s, a declared gap). This retires part of the
  live debt: Retry's header extraction now runs against the real SDK
  error types offline.
- `conformance.SilentServer` (accepts, never answers) and
  `conformance.ErrorServer` (status + headers + body) back the two new
  cases; `Caps.ErrorHeaders` declares the header capability.

### Fixed — docs, tests, tooling

- `wefttest.Golden -update` logs each file it created or rewrote and
  leaves up-to-date files untouched, so a regeneration run says what
  was stale instead of rewriting everything silently.
- `mw.Allow(nil)` is documented as a pass-through.
- The empty-user-message rules cross-reference each other: openai and
  anthropic keep a visible placeholder, google drops the message.
- Schema corpus rows for `time.Duration` (integer nanoseconds, the
  documented foot-gun, pinned), embedded pointer structs, and nested
  maps; the mcp import's float64 rounding of integers beyond 2^53 is
  pinned (`9007199254740993` arrives as `9007199254740992`).

### Left open (needs an ADR)

- One unparseable input schema still fails the whole mcp import (ADR
  0015: loud over silent). Failing the tool rather than the listing is
  the review's suggestion; it changes documented behaviour and waits
  for a decision.

## 0.3.2 — 2026-09-26

The release that makes the tags installable. Every sub-module tag so
far (`openai`, `anthropic`, `google` at v0.1.0 … v0.3.1; `mcp` at
v0.1.0, v0.1.1) required the root as a placeholder pseudo-version
(`v0.0.0-00010101000000-000000000000`) resolved through a `replace
=> ../` that only the workspace honours. Consumers ignore `replace`,
so `go get github.com/weftgo/weft/anthropic@v0.3.1` failed with
`invalid version: unknown revision 000000000000` — none of the adapter
or mcp tags could be used outside this checkout. Tags cut together:
root, openai, anthropic, google at v0.3.2; `mcp` at v0.1.2. The root
module's Go API is unchanged (apidiff clean against v0.3.1).

### Fixed — the sub-modules require the tagged root

- **`openai`, `anthropic`, `google`, `mcp` require
  `github.com/weftgo/weft v0.3.2` and carry no `replace`.** The
  committed `go.work` keeps in-repo development resolving the root
  from disk exactly as before (ADR 0005: "a tagged adapter requires a
  tagged root"); outside the workspace the published root is used.
  `examples/otel` follows the same rule. Verified from a clean module
  with `GOPROXY=direct`: `go get github.com/weftgo/weft/anthropic@v0.3.2`
  resolves and builds.

### Fixed — `mw.Retry`: the retry-after bound is strict

- `fitsDuration` now rejects a value that lands exactly on
  `MaxInt64/unit`: `float64(math.MaxInt64)` rounds up to 2^63, so the
  boundary value scaled to a float the `int64` conversion could not
  hold (overflow is implementation-defined). No real header reaches
  it; the 0.3.1 fix's `ask >= 0` guard in `delay` already contained
  the consequence. Pinned.

## 0.3.1 — 2026-09-26

The 2026-09-24 review's findings, landed (full report in the research
checkout; `WEFT-CODE-REVIEW-2026-09-24.md`): all six P2s, the doc
drift, and the P3 edges that touched files already open. Tags cut
together: root, openai, anthropic, google at v0.3.1; `mcp` at v0.1.1.
Everything is fixes — apidiff is clean against v0.3.0 with no
allowances, and the adapter request bytes are unchanged (the one
byte-visible change, multi-valued ExtraHeaders, only adds values the
caller explicitly set).

### Fixed — `mcp`: untrusted-input robustness (review §2.5, §2.6)

- **`Tools` fails loudly on a server tool with an empty name** instead
  of panicking inside `RawTool`. The SDK's server-side name check only
  logs and its list filter drops nil tools but not empty names, so a
  hostile or buggy server listing `{"name":""}` reached the import
  path as a panic — untrusted input escaping as a process crash.
- **A schema'd tool returning non-JSON text is an isError result, not
  a wedged session.** Both `AddTools` and `Serve` set
  structuredContent from the result text whenever the tool advertises
  an output schema; text that is not JSON produced a `CallToolResult`
  the SDK cannot serialize, so the server never wrote a reply and the
  client blocked in `CallTool` forever — a *successful* call hanging
  the session. The result now names the output-schema breach; regular
  `Tool` definitions are unaffected (a string `Out` carries no output
  schema, everything else marshals).

### Fixed — `Resolve` with nothing pending (review §2.1)

- **A `Resolve` against a transcript with zero pending calls now fails
  the run loudly at step 0**, as `Resolve`'s doc and ADR 0007's
  2026-09-22 amendment already promised. The resolved-id validation
  ran only inside the resume block, so with nothing to resume the
  payload was silently dropped (`err = nil`) — while the same Resolve
  alongside another pending call failed as documented. Approve/Deny
  keep ignoring unknown ids, the recorded asymmetry.

### Fixed — adapters: multi-valued `ExtraHeaders` truncated (review §2.2)

- **openai + anthropic keep every value of a multi-valued
  `ExtraHeaders` entry.** Both looped `option.WithHeader` over one
  key's values, which has `Set` semantics — `http.Header{"X-Multi":
  {"a","b"}}` (legal, and `CloneHeaders` deliberately preserves the
  whole slice) lost "a" on the wire despite the verbatim-headers doc.
  The first value now sets and the rest add (`WithHeaderAdd`); a
  single-valued header is byte-identical to before. Google's path was
  already correct (genai assigns the whole `http.Header`).

### Fixed — google: a run-level `MaxTokens: 0` no longer lifts a construction cap (review §2.3)

- The fold took the request value unconditionally, then its
  `if maxTokens > 0` gate sent nothing — so `google.MaxTokens(128)`
  plus a run `Params{MaxTokens: 0}` replaced the 128 cap with the
  provider default (effectively unbounded), while the adapter's own
  comment claimed the fold never replaces a construction value with a
  zero. A request `MaxTokens` of 0 now keeps the construction value,
  mirroring anthropic's fold; neither-set still sends nothing, and
  openai keeps sending 0 as a value (pinned). Both cases are pinned
  in `TestFoldParams`.

### Fixed — anthropic: `ReasoningTokens` is mapped (review §2.4)

- The `MessageDeltaEvent` case read only `OutputTokens`, so thinking
  runs reported `ReasoningTokens: 0` forever while openai mapped
  `reasoning_tokens` and google mapped `thoughtsTokenCount`. The
  finish now carries `output_tokens_details.thinking_tokens` — a
  subset of the inclusive `OutputTokens`, per the 2a.4 splits rule —
  pinned on the extended `thinking_then_tool_use` fixture.

### Fixed — the docs the review caught drifting (§3)

- **README's flagship middleware snippet now teaches the order
  `mw/doc.go` documents** — `Log, Fallback, Retry, RepairJSON`. As
  shipped it showed `Retry` outside `Fallback`, so the primary's
  429/503 never reached Retry's classifier and the snippet's own
  "retry-after honoured" comment was wrong for the primary.
  `mw/doc.go`'s "the primary gets exactly one attempt" is corrected
  too (one per retry cycle, up to MaxRetries+1).
- **Three stale comments still describing the tool cache deleted on
  2026-09-18** (anthropic's Model doc and both adapters'
  `convertTool`) now state the per-request conversion ADR 0013
  records.

### Fixed — P3 edges the same review filed (§3)

- **`mw.Retry` rejects a `retry-after` it cannot convert** — `1e19`,
  `Infinity`, `NaN` parse as floats, and the int64 conversion wraps
  them negative, slipping past the `MaxWait` cap and sleeping zero:
  a misbehaving gateway got up to MaxRetries+1 back-to-back requests
  instead of the documented fail-fast. Such asks now report no
  retry-after and Retry falls back to backoff; `delay` keeps a
  defensive non-negative check.
- **`mcp` expose path forwards the client's argument bytes verbatim.**
  `argumentBytes` re-marshalled the raw wire value, compacting
  whitespace and escaping `<`, `>`, `&` — a RawTool echoing or
  hashing its arguments saw different bytes than the client sent.
  The raw bytes go through untouched (empty/null still become `{}`),
  pinned byte-for-byte.
- **The governance examples are safe to copy**: the response-cache
  example's map now carries the mutex the Agent's concurrent-reuse
  promise requires, and the PII scrubber scrubs `err.Error()` too —
  the loop renders error text into the transcript verbatim, so a
  success-only scrubber leaked through every failure.
- **`wefttest` fixture names pad the sequence to five digits**
  (`00001-x.json`, ADR 0017 amendment): the replayer loads in listing
  order, and the three-digit pad sorted `1000-x` before `999-x`, so a
  1000+-request recording with a recurring key replayed out of order.
  Replay matches on the key, so old-name fixtures still replay; the
  committed ones were renamed.

## 0.3.0 — 2026-09-22

The Phase 2a parity round (TODO §2a, `docs/phase2a-plan.md` — not
published with the repo; ADR 0013's 2026-09-22 amendment records the
decisions): the six capability items the cross-check against the
studied frameworks found missing, plus the three stances recorded in
the same ADR pass. Tags cut together: the root and the three adapters
at v0.3.0; `mcp` untouched at v0.1.0. Everything is additive — apidiff
is clean with no allowances. The cycle then went through the repo's
two-axis review process and the fix pass landed inside the same
release: the standards axis found no hard documented-standard
violations (one doc drift, two duplication smells, five
production-readiness findings — all fixed or pinned below), and the
spec axis confirmed the cycle faithful to the plan, with the one
deviation (the OutputDecoder's within-step rule) now a documented,
pinned decision.

### Added — Tool-choice forcing (TODO §2a.1, ADR 0013)

- **`weft.ToolChoice(ToolChoiceConfig{Mode, Name})`** — force a step
  to call some tool (`any`), a named tool (`tool`), or none (`none`,
  with the catalogue still advertised, so a prompt-cache prefix on the
  tool definitions survives a no-calls final step). Works as agent
  option, run option, and per-step via `PrepareStep` — the `Thinking`
  dual shape. Mapped per provider (`required`/named/`"none"`;
  `any`/`tool`/`none` — `disable_parallel_tool_use` merges onto the
  chosen member; `ANY`/`allowedFunctionNames`/`NONE`). New conformance
  case `tool_choice_forcing` (declared via `Caps.ToolChoice`) also
  asserts the request bytes carry the provider's field.

### Added — Request params and the escape hatch (TODO §2a.3, ADR 0013)

- **`weft.Params(RequestParams{Temperature, TopP, MaxTokens, Stop,
  Seed})`** — per-run/per-step sampling folding over the adapters'
  construction options (nil keeps the construction default; a run
  override replaces the struct whole). Adapters gained `TopP`, `Stop`,
  and `Seed` construction options where the vendor has them (anthropic
  has no seed — dropped, documented; google narrows Seed/MaxTokens to
  int32, failing `ErrUnsupported` past the ceiling).
- **`ExtraBody(map[string]any)` / `ExtraHeaders(http.Header)`** per
  adapter — the caller-wins valve for vendor knobs weft has no option
  for: nested maps deep-merge, every other value replaces, and **your
  key wins on conflict** (yours, not weft's — the default-bytes tests
  do not cover what it sends). Construction-time only.

### Added — Richer `Usage` (TODO §2a.4, ADR 0016)

- **`Usage.CachedInputTokens`, `Usage.CacheWriteTokens`,
  `Usage.ReasoningTokens`** — reporting subsets of the two totals
  (which stay inclusive; `UsageLimit` and `Total` are unchanged).
  Filled by all three adapters (anthropic cache read/write, openai
  cached/reasoning details, google implicit-cache and thought tokens).
  On spans under their semconv/v1.41.0 names
  (`gen_ai.usage.cache_read.input_tokens`,
  `…cache_creation.input_tokens`, `…reasoning.output_tokens`), each
  only when non-zero; the slog lines keep the two totals (line
  stability). Wire is omitempty — old event JSON round-trips.

### Added — Prompt caching (TODO §2a.2, ADR 0013)

- **`anthropic.PromptCache()`** — opt-in `cache_control` breakpoints at
  the three stable prefix edges (system block, final tool definition,
  trailing conversation edge); without it, no marker anywhere. Cache
  writes bill 1.25×, reads 0.1×; see the README cost note and the
  `Usage` splits for the measurement.

### Added — Externally-computed tool results (TODO §2a.5, ADR 0007)

- **`weft.Resolve(callID, content)` / `weft.ResolveError(callID,
  content)`** — resume a parked call with a result computed outside the
  process; the handler never runs, the content becomes the tool result
  verbatim (capped by `MaxResultBytes` as any result), composes with
  `Approve`/`Deny` in one resuming call. **Behaviour note:**
  `Resolve` on a call that is not pending is a loud run error at
  step 0 — a deliberate asymmetry with `Approve`/`Deny`, which ignore
  unknown ids.

### Added — Streaming partial structured output (TODO §2a.6)

- **`weft.NewOutputDecoder[Out]()`** — a decoder value fed from the
  run's events; `Feed` returns best-effort partials as `submit_output`'s
  arguments stream (lenient closed-prefix decode, hand-rolled in
  `partial_json.go`, fuzzed), `Result` follows the `OutputOf` rule.
  UI-only; never model-visible.

### Fixed — the review round (2026-09-22)

- **`ExtraBody`/`ExtraHeaders` snapshot at construction** (all three
  adapters): the options captured the caller's nested maps and header
  slices by reference, so mutating them after `Model()` raced
  concurrent runs. Values are deep-copied when the option applies
  (`adapterkit.CloneJSON`, header slices cloned); pinned per adapter
  by `TestExtraBodySnapshot`.
- **A negative `RequestParams.MaxTokens` fails the run at the step
  that carries it** — named in the error text, the `validateToolChoice`
  stance. Before, the adapters improvised: openai forwarded it into an
  opaque API error, google dropped it silently, anthropic folded it to
  the default. Explicit zero keeps its documented per-adapter meaning.
  Pinned by `TestParamsValidation`.

### Changed — internal (the review round)

- **The truncated-JSON closer lives once**, in `internal/jsonclose`,
  imported by both `partial_json.go` and `mw/repairjson.go`. The two
  copies had already drifted (the trailing-comma rule); the plan's
  "duplicated on purpose" rationale was wrong — `mw` importing an
  `internal/` package of its own module breaks no rule (the
  `adapterkit` precedent). Behaviour change rides along for
  `closedPrefix`: a trailing comma *run* now closes (it dropped back
  to the member boundary before) — strictly more salvage.
- **`OutputDecoder`'s within-step rule is last-call-wins, pinned.** A
  fresh `submit_output` `ToolStart` resets the buffer, so `Result`
  follows the last call — the `OutputOf` rule the godoc always named;
  the plan's "concatenates within a step" wording is amended in place
  (two distinct calls' arguments never decode as one document).
  `BenchmarkOutputDecoder` pins the documented cost shape (~0.9µs per
  delta at a 3 KiB form; the per-delta re-close is quadratic in
  submission size by design).
- The loop skips the resume pending-id set when a run carries no
  decisions, and a `cap`-shadowing local is renamed.

### Docs

- ADR 0013 amended (tool choice, params + escape hatch, cache markers,
  the provider-executed-tools stance); ADR 0007 amended (resolve);
  ADR 0014 noted (handoff isolation stands); ADR 0016 noted (usage
  split attributes); ADR 0017 amended (replay key gains `ToolChoice`,
  not `Params`). README: sampling/forcing paragraph, the prompt-cache
  cost note, and "Seams are the product" (governance middleware stays
  examples — TODO §2a.9). Godoc examples: `ExampleToolChoice`,
  `ExampleParams`, `ExampleOutputDecoder`; `examples/approval` gains
  the `run_sql` resolve path.
- The review round's docs: ADR 0013's capability matrix gains the
  Tool choice column (all three adapters ✓, with each provider's wire
  tokens); `RequestParams`, `ExtraBody`/`ExtraHeaders`, and
  `OutputDecoder` godocs state the new rules;
  `docs/phase2a-plan.md` §8.1 carries the two dated amendments.

### Live runs owed (carried debt — no provider keys on the build machine)

v0.3.0 ships on the offline suites, as v0.2.0 did (ADR 0013's
fixtures-and-live-runs rule). When keys exist, `make live` must prove:
the full `conformance.Run` table including `tool_choice_forcing` on
all three providers; `PromptCache`'s measured payoff
(`CachedInputTokens > 0` on step 2+ of a long-transcript run);
openai/google `CachedInputTokens`/`ReasoningTokens` non-zero against
real responses; and `mw.Retry`'s structural `HTTPStatus`/`RetryAfter`
extraction against real openai-go / anthropic-sdk-go / genai error
values (the same §3.5/§4.1 debt, now grown by 2a's request-byte
changes).

## 0.2.0 — 2026-09-19

Everything unreleased since 0.1.0, tagged as one set (ADR 0005): the
middleware seams and the approval boundary (the 2026-09-14 round), the
loop controls and subagents as tools plus two full review passes (the
2026-09-18 round), and MCP both ways, observability, and wefttest
record/replay (the 2026-09-19 round). Tags cut together: the root and
the three adapters at v0.2.0, `mcp` new at v0.1.0. The two groups
marked "read before upgrading" are the behavior changes to check
first.

### Added — Testing conventions: replay, wefttest growth, fuzz in CI (TODO §9, ADR 0017)

- **`wefttest.Record(t, dir, inner)` / `wefttest.Replay(t, dir)`** —
  record/replay at the `weft.Model` seam, so application tests run
  against what a real model actually said, offline and deterministically.
  Fixtures are one pretty-printed JSON file per request (reviewable in a
  diff; re-recording is the review), keyed on the request's messages,
  tool names, thinking level, and sequential flag — not the system
  prompt, so a prompt tweak does not invalidate fixtures. A miss fails
  loudly with `wefttest.ErrNoFixture` naming the directory, key, and
  first user text; repeated identical requests replay in recorded
  order. Both are ordinary `weft.Model`s (middleware, `PrepareStep`,
  and subagents run unchanged above them); the conformance suite is
  green against a `Replay`, and committed recordings under
  `wefttest/testdata/replay/` prove a fresh checkout replays with no
  key and no network. No new dependency; nothing under `weft/` proper
  changed. Adapters keep their wire-level `.sse` fixtures (ADR 0013) —
  replay answers the *application* question, fixtures the *adapter*
  question.
- **`wefttest` scripting helpers**: `Args(v)` (typed tool arguments),
  `Raw(events...)` (verbatim events — the one-liner for contract
  violations and signed reasoning blocks), `SayThenFail(text, err)`
  (mid-stream failure), `Turn.WithUsage(u)`, `Request` matchers
  (`HasTool`, `ToolNames`, `LastText`), and `Model.LastRequest()`.
- **`make fuzz`** runs every fuzz target of the root module (10 s
  apiece, `FUZZTIME` overridable) and a dedicated CI job gates it,
  uploading crashers on failure; a crasher becomes a committed
  regression seed (the `FuzzRepair` precedent).

### Added — Observability: OTel spans and slog lines (TODO §8, ADR 0016)

- **The core's first and only dependency: the OTel API**
  (`go.opentelemetry.io/otel` v1.46.0, `semconv/v1.41.0` — the newest
  semconv package carrying the GenAI group). Every run reports its own
  spans — `invoke_agent` per run, `chat` per model call, `execute_tool`
  per executed tool call, children nested under their parents, GenAI
  semantic attributes, no message or tool-argument content on any span —
  through the global provider, so setting up an SDK is the whole
  integration; no weft option needed. Cost with no SDK registered: six
  allocations and ~230 ns per span (`BenchmarkObserverNoop`).
- **`weft.TracerProvider(tp)`** replaces the global provider for one
  agent (tests and DI programs never touch the global).
- **`weft.Logger(l)`** writes one Debug line per phase — run start, run
  finish, model call, tool call — ids, model, durations, usage, stop
  reasons, outcomes; default `slog.Default`, resolved at log time,
  silent unless Debug is on; lines carry the span context so an
  OTel-bridging handler correlates them for free.
- New module **`examples/otel`** (own `go.mod`, in `go.work`): the real
  SDK with a stdout exporter, plus a test asserting the span tree
  through the SDK's in-memory exporter — offline.
- Amends ADR 0004 (OTel and slog are the loop's own reporting, not the
  tap's) and ADR 0005 (a `version` constant for instrumentation
  version). `Tap` is unchanged and now receives the span-carrying
  context.
- Review pass (2026-09-19): `error.type` for weft's sentinels is a
  snake_case token (`max_steps`, `model_contract`, …; table in ADR
  0016), not the sentinel's sentence; a cancelled tool call reports
  `context.Canceled` like the run; the `chat` span takes the loop's own
  finished flag instead of inferring it from an empty stop reason;
  the README states precisely what error text travels. A second pass
  the same day: a panic nothing contains — a PrepareStep function —
  ends the run span (`run_panicked`) and re-panics instead of leaking
  the span; ADR 0016's dependency closure drops `golang.org/x/sys`
  (it appears only in `examples/otel`'s go.sum); the ADR 0005
  amendment the entry above cites is written.

### Fixed — ParseSchema: the structured view is lenient (ADR 0003 amendment)

- A foreign schema whose keyword shape the `Schema` struct cannot hold
  — `"additionalProperties": false` (what zod-built TypeScript servers
  emit), a type array `["string","null"]`, tuple or boolean `items`, a
  non-string description — no longer fails `ParseSchema`: the keyword
  stays in the stored bytes (what the model sees) and the structured
  view leaves it zero (what readers walk). The document itself is
  still checked: invalid JSON, trailing data, or a non-object top
  level fail at import. The anthropic adapter now forwards every
  top-level keyword it cannot map (a foreign `$defs`, `oneOf`, …)
  onto the wire, so a `$ref` inside properties never dangles; the
  anonymous-field exemption matches `encoding/json`'s exactly (an
  unexported non-struct anonymous field stays out whatever its tag).

### Added — MCP, both ways (TODO §7, ADR 0015)

- **`mcp.AddTools(s, tools...)`** and **`mcp.Serve(s, agent, description)`**
  expose weft tools and agents as an MCP server: each tool listed with
  its own contract, every failure an `isError` result carrying weft's
  pinned text, the agent tool literally `weft.Subagent`, and the agent's
  tools dispatched through its chain (approval answers loudly; `AddTools`
  refuses gated tools outright). New module **`weft/mcp`**; the official
  Go SDK is pinned at v1.8.0 and aliased `sdk`.
- **`mcp.Tools(ctx, sess, opts...)`** imports a connected session's tools
  as ordinary weft tools (`RawTool`; schema bytes verbatim through
  **`weft.ParseSchema`**), shaped by **`mcp.Prefix`**, **`mcp.Policy`**
  and **`mcp.ErrToolError`**. Foreign tools are `Sequential` unless the
  server marks `readOnlyHint`; `structuredContent` wins in result
  rendering; transport failures are `mcp: `-prefixed tool errors.
- Core, additions only (`make apidiff`): **`weft.ParseSchema`** (foreign
  bytes kept whole; `Schema.MarshalJSON` emits them),
  **`(*Agent).Name`**, **`(*ToolDef).RequiresApproval`**.
- **Wire change, named:** unconstrained schema nodes reach OpenAI and
  Anthropic as `{}` where the hand-built map wrote `{"type":""}` — no
  test pinned the old bytes; ADR 0003's amendment records the change.

### Fixed — review pass (2026-09-19)

- `make build/test/vet/lint/tidy/offline/live` fail fast per module: a
  failing module no longer reads green because a later one passes (the
  loop kept only the last status).
- `TestToolsTransportFailureIsData` could hang the suite: the SDK's
  shutdown waits for an in-flight handler whose request ctx cancels only
  after that wait — the severed-transport call now carries its own
  deadline, the way a run's tool `Timeout` bounds it in production.
- Gemini's fallback for a foreign schema the `genai` decoder rejects maps
  the structured fields recursively — nested properties and items
  survive, not the top level alone.
- `weft.ParseSchema` no longer rejects a legal keyword shape the `Schema`
  struct cannot hold — `"additionalProperties": false` (every zod-built
  TypeScript MCP server emits it), a type array, tuple or boolean
  `items`, a non-string description. The structured view leaves such a
  field zero; the bytes still cross whole. One such tool used to fail
  the whole `mcp.Tools` import.
- `mcp.Tools`' image and audio markers report the payload's own byte
  count; the SDK already decodes the wire's base64, so `DecodedLen` on
  it under-reported by a quarter.
- The 2026-09-19 embed rule is narrowed to what `encoding/json` does: a
  tagged anonymous field of an unexported *non-struct* type is ignored
  on the wire, so the schema no longer advertises it as required.
- **Anthropic dropped a foreign schema's top-level keywords** other than
  `properties`/`required`/`additionalProperties`: `$defs` (so every
  `$ref` dangled and the API rejected the tool), `$schema`, top-level
  `oneOf`. Every other top-level key now rides `ExtraFields`; pinned.
- `Serve`'s tools answer with `structuredContent` when they advertise an
  `outputSchema`, as `AddTools`' already did (the spec's MUST).
- A client that omits `arguments` hands an exposed `RawTool` `{}`, not
  `null` — the consume side's rule, now on both sides.
- An `isError` result with no content reads `ErrToolError`'s text
  instead of an empty string; a `ResourceLink` item renders as
  `[resource <uri>]` instead of the opaque `[content]`.
- `ExampleTools_toolSource` guards the refreshed slice with a mutex, as
  the godoc now says: the SDK's `listChanged` handler and the loop read
  it from different goroutines.
- `TestAddToolsRefusesApprovalGated` ran one of its three cases (a
  recover deferred in a loop unwound the test); all three run.

### Added — option composition (TODO §5.10)

- **`weft.Options(opts...)`** composes agent options into one value,
  applied in order — a plugin is `func(deps) weft.Option`, dependencies
  are parameters, never globals. **`weft.ToolOptions(opts...)`** is the
  per-tool counterpart. Nil entries ignored; duplicates still panic.

### Added — PrepareStep, the one loop knob (TODO §5.5, ADR 0006 amendment)

- **`weft.PrepareStep(fn)`** — a function the loop calls before every
  model call with the request it built (raw instructions, transcript,
  tool snapshot). What it returns is what the step both advertises and
  dispatches against (validated like a tool-source snapshot); snippets
  compose after it, from the returned tools; the model seam sees the
  prepared request; the transcript is never touched. Several options
  chain in order; an error fails the run with the caller's sentinel
  reachable. Not a third seam — the loop-level knob beside `StopWhen`
  (ADR 0006 amendment).

### Added — loop detection (TODO §5.4, ADR 0002 amendment)

- **`weft.DetectLoops(repeats)`** fails a run with
  **`ErrLoopDetected`** when `repeats` consecutive steps request the
  same set of tool calls — names and raw argument bytes, sorted, calls
  only (results never enter the signature). Off by default; the
  manifest records it when on.

### Added — model retry hints (TODO §5.2, ADR 0002 amendment)

- **`weft.ModelRetry(hint)`** — a tool error rendering as
  `RETRY: <hint>` that asks the model to try the call again with the
  hint applied. The loop counts RETRY results per tool name (middleware
  retries included) and fails the run with **`ErrModelRetriesExceeded`**
  after more than **`weft.MaxModelRetries(n)`** (default 3) consecutive
  asks; a success resets the count. The manifest policy records
  `max_model_retries` (the only §5 change that touches a committed
  golden; `examples/getting-started/weft.json` regenerated).

### Added — usage limits (TODO §5.3, ADR 0002 amendment)

- **`weft.UsageLimit(max weft.Usage)`** bounds a run's total token
  usage, subagents included, checked at the continuation point — only
  when the loop would otherwise make another model call; a run-ending
  step may overshoot and still succeed. Breach fails the run with
  **`ErrUsageLimit`** (wrapped with the numbers) and the partial
  transcript. Off by default; the manifest policy records it
  (`usage_limit`, zero fields omitted).

### Added — subagents as tools (TODO §5.1, ADR 0014)

- **`weft.Subagent(name, description, child, opts...)`** — a tool whose
  handler runs another agent on the prompt alone; the child's events
  arrive in the parent's stream wrapped in the new **`weft.Nested`**
  event (wire `nested`, recursive through `UnmarshalEvent`), numbered
  from the parent's counter under the parent's ordering lock; the
  child's usage rolls into `RunResult.Usage` and is recorded per call
  on the new `StepRecord.SubagentUsage`. Every `ToolOption` applies to
  the delegation (`Timeout`, `MaxResultBytes`, `RequireApproval`,
  `Sequential`, `WrapTools`); the parent's `Parallelism` bounds
  concurrent delegations.
- **Child failure is data**: `SUBAGENT_FAILED: agent "x" failed at
  step N: …` (the child's `*RunError` on `ToolError.Err`), a child
  ending pending is `SUBAGENT_PENDING: …`, and a delegation to an
  agent already running in the call chain is refused before any model
  call with `SUBAGENT_CYCLE: …`. Codes exported as
  `CodeSubagentFailed`/`CodeSubagentPending`/`CodeSubagentCycle`.
- **Lineage ids**: a child run's id is `<parent>/<step>/<callID>`
  (`<parent>/resume/<callID>` under `Approve`), visible on the nested
  `RunStart` and on `CallFromContext` inside the child.
- **The late-event rule** (ADR 0004 amendment): no `Nested` event and
  no usage record for a call is delivered after that call's
  `ToolFinish` — the close is atomic with the finish under the
  parent's lock, so replayed streams never show a finished call
  continuing.
- **Manifest**: tools from `Subagent` carry `"subagent": "<child
  name>"` (omitempty; existing goldens unchanged). **wefttest**:
  `Flatten` unwraps `Nested` events recursively for assertions.

### Fixed — §5 review pass (2026-09-19)

- `PrepareStep` functions now receive a deep copy of the request: the
  previous slice-level clone shared each message's parts and the frozen
  registry's `*ToolDef` pointers, so an in-place write — dropping a
  part, re-forming a tool call's argument bytes, rewriting a
  definition's fields — corrupted the run transcript and the agent for
  later runs, against the documented "mutate freely" promise (ADR 0006
  amendment). Message parts, argument bytes, and tool definitions are
  cloned at that boundary; the model seam keeps the lighter slice
  copies under the adapter read-only contract.
- A typed-output child that submitted with no argument bytes (empty
  decodes as `{}`) now counts as submitted: the delegation returns the
  empty bytes instead of falling back to the child's final text,
  matching what `OutputOf` decodes (ADR 0014, G4).
- Added the missing godoc example for `ToolOptions`, the untested
  `resume` lineage id (`<parent>/resume/<callID>`, asserted through an
  approved delegation), and a guard comment that described a sort as a
  counter compare.
- `docs/life-of-a-call.md` drew the budgets before the `StopWhen`
  check; the loop checks `StopWhen` first and the budgets only at the
  continuation point (ADR 0002 amendment, AGENTS rule 13). The diagram
  now matches the code. ADR 0012 now records the three §5 policy keys
  (`max_model_retries`, `usage_limit`, `detect_loops`) the manifest
  had been writing without an entry.
- Pinned four behaviours the §5 suite left implicit: cancelling the
  parent mid-child under `Stream` delivers the cancellation last and no
  `RunFinish` at either level; a child's `RETRY` results feed the
  child's counter, never the parent's; concurrent runs on one
  orchestrator keep `Nested.RunID` and `Seq` per run; `PrepareStep`
  over a `ToolSource` consults the source once per step and dispatches
  against the prepared subset.
- Second round (deep review of the §5 plan against the tree):
  `CodeSubagentFailed`'s godoc claimed the child's cause is "never
  shown to the model" while the handler renders it into the
  model-visible message — the comment now states the real contract
  (message carries the cause; the `*RunError` stays on `ToolError.Err`
  for `errors.As`). `MaxModelRetries`' godoc now records that calls
  resumed under `Approve` do not feed the counter (they belong to no
  step, ADR 0007), as ADR 0002 already did. The "last valid
  `submit_output`" walk existed twice — `OutputOf` and a Subagent
  delegation's `submittedJSON` — and is now one `lastSubmitted` helper
  both call, so the "exactly the bytes `OutputOf` would decode" promise
  cannot drift. `nestFromContext` dropped its never-read ok flag.
  `make lint` now works from a bare shell like `make apidiff` does
  (GOPATH/bin on PATH); golangci-lint is 0-issues across the four
  modules.

### Fixed — go1.26 decode-error compatibility (follow-up to the review pass)

- The schema walk behind `INVALID_INPUT` messages now handles toolchains
  (go1.26) that omit map keys from `encoding/json`'s error field path
  (`meta.when` for an error under `meta["k"]`): a segment at a map
  schema is first tried as a property of the value schema before being
  taken as a key, so the expected type named is still the advertised
  one. The `",string"` mismatch tests accept both toolchain renderings
  — the newer UnmarshalTypeError form (field named) and the older
  plain-error form — both speak the schema's vocabulary.

### Changed — the 2026-09-18 code-review pass (all 43 findings)

Schema correctness (ADR 0003's same-day amendment):

- **Embedded shadowing now tracks depth, encoding/json's real rule**:
  a name claimed at two embedding depths keeps the shallower
  contribution (the schema previously cancelled a name the wire still
  carried), and at equal depth exactly one json-tagged claim beats
  untagged ones (the schema previously kept the last writer — for a
  struct with both a tagged int and an untagged bool claiming "Name",
  it advertised a boolean the wire never emitted). Any other
  equal-depth tie cancels the name, as encoding/json drops it. One
  deliberate, conservative divergence: in a double diamond the schema
  cancels where encoding/json's breadth-first resolver may keep — the
  cancelled side never advertises what decode cannot reliably
  deliver.
- **Two fields of one struct claiming one JSON name panic at
  construction** (always both tagged), at any *named* nesting depth:
  one handler field can never receive a value. Inside an embedded
  struct the same collision instead flattens into the parent's
  dominance rules and drops, matching encoding/json. The
  duplicate-name and non-struct-input panics set the precedent.
- **Decode errors speak the schema's vocabulary**: the expected type
  in `INVALID_INPUT: … field "x": expected T, got U` is read from the
  tool's advertised schema, walked along the error's field path, so
  `[]byte` fields report "expected string" and a `,string` integer
  reports `field "n": expected string, got number` — never the Go kind
  the schema did not show. The Go-type mapping is the fallback for a
  path the walk cannot resolve.

Run and loop correctness:

- **Approval-resume results complete the earlier step's tool message
  in the assistant's call order** (ADR 0007 §3), not appended after
  the earlier run's results — Gemini matches functionResponses by name
  and position, so a reordered tool message could attach a result to
  the wrong call. The seams test is order-sensitive now and a contract
  pin covers the mixed approved/earlier shape.
- **The terminal `RunFinish` decides the run's outcome.** One check now
  governs both the event's delivery and the return value: a delivered
  `RunFinish` is always followed by success, and a cancellation that
  lands between the loop's last ctx check and the emit fails the run
  with the cancellation error (the resumable result on it) instead of
  returning success with no `RunFinish` — or, at the approval boundary,
  delivering `RunFinish` and then failing. Rule 4 and `Run.Events`' one
  terminal element now hold under every interleaving; pinned by a tap
  that cancels on the final `StepFinish`.
- `StepFinish`'s doc now says what the loop does: the event follows the
  step's tool events and precedes the stop-condition check (no wire or
  order change).
- **A tool-source snapshot with a nil entry fails the run with the new
  `ErrNilTool`** (the `ErrDuplicateTool` pattern) instead of silently
  advertising a nil every adapter dereferences into a misleading
  `ErrModelContract` panic.

Deny mode is a gate, not an aspiration:

- **`WEFT_MODEL_REQUESTS=deny go test ./...` is green workspace-wide**
  (`make offline`, pinned in CI). The kill switch now guards clients
  the adapters build from credentials; a client injected through the
  adapters' `Client(c)` option is a test double by construction and
  stays reachable (ADR 0013's amended clause) — the switch no longer
  breaks weft's own fixture suites in the mode it exists for. The
  `ModelRequestsAllowed` meta-test is ambient-aware.

Adapters (ADR 0013's appendices updated in the same change):

- **Empty content never reaches the wire** in openai and google, the
  2026-09-14 rules anthropic already had: a reasoning-only assistant
  message is skipped (`{"role":"assistant"}` was API-rejected), empty
  text parts are dropped, and Google skips Contents reduced to zero
  parts.
- **openai normalizes empty tool-call arguments to `{}`** on the
  convert side, matching its own stream side and the other adapters; a
  replayed nil-args transcript no longer 400s as "arguments is not
  valid JSON".
- **openai's `parallel_tool_calls` hint is sent only alongside a tool
  catalog** — several compatible servers reject the hint without one.
- **Synthesised `call_<i>` ids skip ids the server already populated**
  in the same step (openai, google): a collision failed the run with
  `ErrModelContract`, the failure the synthesis exists to prevent.
- **openai overwrites repeated function-name fragments** instead of
  concatenating them ("pingpingping").
- **google streams a thought signature riding an empty non-thought
  text part** — Gemini validates its return on the next request.
- **google's int32 ceilings fail loudly** wrapping `ErrUnsupported`
  (`MaxTokens`, `Thinking.Budget`) instead of wrapping around into a
  garbage wire value.
- **Tool defs are converted per request.** The pointer-keyed
  `sync.Map` never evicted, and a `ToolSource` that rebuilds its
  snapshot per step — the seam's documented use — grew it without
  bound. Measured first (`openai.BenchmarkConvertTool`): ~2–5µs /
  62 allocs per conversion against the network round trip every
  request also pays.
- **SDK-free helpers moved to `internal/adapterkit`** (schema
  rendering, the terminal-error rule, the FilePart guard): three
  byte-identical copies become one; the root module's public API is
  untouched.

The gate and the tooling:

- **The apidiff gate fails closed.** A tree that does not compile, or
  an apidiff run that dies before writing a report, can no longer read
  as "no incompatible changes"; the gate builds both sides first and
  distinguishes execution failure from a clean diff. A self-test
  exercises all three modes (`make apidiff-selftest`), CI pins the
  apidiff version the way golangci-lint is pinned, and the offline
  deny gate is its own CI job.

Test support and middleware:

- The conformance suite gained the two cases the README already
  claimed: `tool_args_delta` (fragments surface live before the call's
  `ToolStart`; `Caps.ToolArgDeltas` declares it — Google's calls
  arrive whole) and `slow_stream` (a dripping stream under a tight
  idle timeout must succeed; only a true stall fails).
- `wefttest`'s `Requests()` records exhausted calls too (real requests
  that they are), and the scripted model yields `ctx.Err()` before its
  scripted error, matching the Model contract the adapters enforce.
- `mw.RepairJSON` strips any fence language tag (```JSON`,
  ```javascript`), not just lowercase `json`, and no longer
  eats literal "json" content after a bare fence. `mw.MaxWait(0)`
  means "no cap"; every mw option's ignore rule is documented like the
  core's. `Retryable`'s `ErrModelContract` exclusion is pinned in the
  classifier test.
- The loop's own failure codes are exported constants —
  `CodeInvalidInput`, `CodeNoSuchTool`, `CodeDenied` — so external
  policy middleware produces the contract strings without duplicating
  them (`mw.Allow` already uses `CodeDenied`).
- `Replay`/`ReplayPolicy` and the manifest's `replay` key are
  **retracted** until the checkpoint store ships (ADR 0006's
  amendment): the annotation had no consumer before `store` exists.
  They return with it.

### Changed — run & event semantics (read before upgrading)

- **A tool-call ID repeated within one step fails the run** with
  `ErrModelContract`, like an empty ID: a repeated ID made `Repair`
  drop the second result and left `Approve`/`Deny` keyed on it
  ambiguous. IDs may still repeat across steps.
- **Tool arguments must be exactly one JSON value.** Trailing data
  after the arguments object (`{"a":1} {"a":2}`, `{"a":1} x`) is an
  `ErrInvalidToolInput` result in lenient and strict modes alike;
  the decoder previously took the first value and ignored the rest.
- **The truncation marker names the bytes omitted.**
  `…[truncated N bytes]` now carries N = bytes the model did not
  receive; the marker previously printed the cap, which read as "N
  bytes missing" however many were cut. The marker's shape is
  unchanged (ADR 0002, 2026-09-18 amendment).
- **Events carry `RunID`.** Every event except `RunStart` (whose `id`
  is the run's) carries `run_id` on the wire, so taps and stream
  consumers can attribute events under concurrent runs; the per-run
  `Seq` counter was already unique only within its run. Old recordings
  without the field decode with an empty `RunID`.
- **Events are snapshots.** `ToolStart.Args` and `RunFinish.Pending`
  no longer alias the transcript's byte slices: writing into a
  received event cannot corrupt the run. The copies ride tool-event
  frequency, not delta frequency — no benchmark movement.
- **A step consults its `ToolSource` exactly once.** Advertising, the
  sequential barrier, and dispatch resolve against one per-step
  snapshot, so a source that changes mid-step can no longer produce an
  advertised-then-`NO_SUCH_TOOL` failure or a barrier that disagrees
  with the executed def. A tool registered mid-step becomes callable
  on the next step (the per-step refresh `TestToolSource` always
  modelled). A snapshot with a duplicate name now fails the run with
  the new `ErrDuplicateTool` instead of silently resolving
  first-wins; `Agent.CallTool` reports the same condition as an error.
- **Registered tools are frozen at `New`.** The agent keeps a deep
  copy; mutating the value you passed in (fields or schema trees)
  after construction no longer reaches dispatch, advertisement, or a
  running run. `Agent.Tools` returns deep copies too. The "immutable,
  reusable, concurrent" contract now holds by construction.
- **`ModelRequest` hands adapters copies.** `Messages` and `Tools`
  are fresh slice copies per request; a hostile or careless `Model`
  can no longer corrupt the transcript or the agent's tool list at
  slice level. `wefttest`'s mock already cloned both — loop, contract,
  and test double now agree.

### Added — the rest of the 2026-09-18 round

- `Run.Close` releases an abandoned run's resources (idempotent; a
  run you will consume needs no Close — Events and Wait release
  everything themselves).
- `Agent.TapPanics` counts contained tap panics, so a dead observer
  is no longer invisible.
- `Schema.AdditionalProperties` types map values (`map[string]int` →
  an object of integers; `map[string]any` stays a bare object — an
  `any` value type has nothing to say) and rides the wire in the
  manifest and the OpenAI and Anthropic adapters. The Google adapter
  drops it: Gemini's schema subset (and the genai SDK's `Schema`) has
  no `additionalProperties` field. Wire output for schemas without
  typed maps is byte-identical.
- `wefttest.ConformInfo` / `ConformInfoT` check that model middleware
  forwards the inner model's identity (the Info convention, checked).
- `ExampleTap_async`: the supported pattern for slow observers — hand
  each event to a queue inside the tap, drain on your own goroutine.
- `Run.Events` and `Tap` docs now state the consumer-speed coupling:
  tool events are emitted under the step's ordering lock, so a slow
  consumer gates the start of subsequent tools.

### Fixed — the rest of the 2026-09-18 round

- Embedded-struct schema derivation now follows encoding/json's
  shadowing rules: a struct's own field wins over an embedded one with
  the same JSON name (its type *and* its required flag, whatever the
  declaration order — previously the property was last-writer-wins and
  `required` could name it twice, an invalid schema), and the same
  name from two embedded structs cancels instead of surviving with a
  nondeterministic type. `required` stays in declaration order.
- The json `,string` option is reflected: the property is typed
  `string`, the quoted wire form. Previously the schema advertised the
  bare type, so a schema-following model's unquoted value was
  rejected (`invalid use of ,string struct tag`).
- `Repair`'s purity is pinned: the input is never mutated
  (fuzz-checked byte-for-byte), and the synthesis append no longer
  relies on whose backing array the kept tool message uses.

### Changed — model-visible contracts (read before upgrading)

- **A `max_tokens` step with tool calls executes none of them.** Every
  call gets the error result `tool call <name> was not executed: the
  response hit the output token limit` and the loop continues so the
  model retries with a full budget. Previously intact calls ran and
  only cut arguments failed decoding. (ADR 0002 amendment, TODO §5.6a.)
- **The loop's own tool failures are coded.** Undecodable arguments
  render `INVALID_INPUT: tool "x": field "days": expected integer, got
  string` (was `weft: tool input is not valid for its schema: …`) and
  an unknown tool `NO_SUCH_TOOL: no tool named "x"` (was `weft: no tool
  with that name: "x"`). `errors.Is` on the sentinels is unchanged.
- `Sequential` returns `PolicyOption` (source-compatible); `RunFinish`
  gained `Pending` and is no longer `==`-comparable.
- `Agent.CallTool` now runs the tool middleware chain, and applies the
  agent-level `StrictInput` exactly as the loop does.
- A run whose context is canceled while calls are parked for approval
  fails with the cancellation error (the parked calls stay resumable on
  `RunError.Result.Pending`) — cancellation wins, as everywhere else.

### Added — the rest of the 2026-09-14 round

- **The two middleware seams** (ADR 0006): `WrapModel(mw
  ...ModelMiddleware)` and `WrapTools(mw ...ToolMiddleware)` — chi-style,
  first listed outermost; `WrapTools` also works on one tool.
  `ToolCaller`, `ToolMiddleware`, `ModelMiddleware`, `InfoOf`.
- **Package `mw`**, the reference middleware: `Retry` (backoff with
  jitter, retry-after honoured, >60s asks fail fast, overflow never
  retried), `Fallback`/`FallbackWhen`, `Log`, `RepairJSON`; `Allow`,
  `Audit`, `MapErrors`.
- **`ToolError`** — `{Code, Message, Err}` renders `CODE: Message`; the
  cause is for middleware and logs only. `weft.Errorf(code, format,
  ...)`; several `%w` verbs keep every cause reachable.
- **The approval boundary** (ADR 0007): `RequireApproval()`,
  `RunResult.Pending`, `RunFinish.Pending`, `Approve(id)`, `Deny(id,
  reason)`, `Call.Approved`, `ErrApprovalRequired`, `ErrApprovalDenied`;
  `examples/approval`.
- Per-tool policy: `Sequential()` on a tool is a barrier;
  `PromptSnippet(text)` composes into the instructions; `Replay(policy)`
  annotates checkpoint restart (`ReplaySafe`/`ReplayNever`). All in the
  manifest. `Timeout(0)` on a tool removes the agent's default for
  that tool alone (the manifest records `"timeout": "0s"`), the
  timeout analogue of `MaxResultBytes(0)`.
- The apidiff gate (`scripts/apidiff.sh`, `make apidiff`, CI job) with
  the pre-1.0 `.apidiff-allow` acknowledgement file.
- `docs/life-of-a-call.md`: where every phase of a step and a tool call
  sits.

- Per-run reasoning control: `weft.Thinking(weft.ThinkingConfig{...})`
  — an agent option sets every run's default, a run option overrides
  one — on a provider-neutral scale (`ThinkOff/Low/Medium/High`, plus
  an optional token `Budget`). Anthropic maps it to
  `thinking:{disabled|adaptive}` or `budget_tokens`; Gemini to
  `thinkingBudget`/`thinkingLevel` (and asks for thought summaries
  back); the OpenAI adapter sends `reasoning_effort` on the official
  API and injects a `thinking` object for the known gateway hosts
  (z.ai, bigmodel.cn, moonshot.ai/cn), with `openai.Dialect(...)`
  pinning the wire form (`DialectNone` for strict servers). Declared
  gaps: Chat Completions has no off switch or budget form — `ThinkOff`
  and `Budget` are dropped in the effort dialect.
- Streamed tool-argument progress: a new `ToolArgsDelta` event (wire
  type `tool_args_delta`) surfaces argument fragments live while the
  model writes a tool call; the assembled call still arrives as
  `ToolStart` when it executes. OpenAI and Anthropic adapters emit it;
  Gemini calls arrive whole and yield none.
- Per-tool policy: trailing options on `Tool` — `Timeout`,
  `MaxResultBytes`, `StrictInput` — override the agent's defaults for
  that tool alone.
- Structured output: `Output[T]`, `GenerateAs`, and `OutputOf`
  constrain the final answer to a struct via a `submit_output` tool
  (ADR 0008); undecodable tool arguments come back as schema-shaped
  errors naming the field.
- Conformance suite: a `thinking_option` case asserting the option
  threads through the public API without breaking the exchange.

### Fixed — the rest of the 2026-09-14 round

- A canceled stream can no longer fabricate a successful
  `ModelFinish`: all three adapters enforce the Model contract's
  `(nil, ctx.Err())` terminal yield when the caller's context ends
  mid-stream — a canceled run previously could report success with
  partial data.
- anthropic: empty tool outputs travel as a visible "(empty tool
  output)" placeholder, empty user messages as "(empty message)", and
  assistant messages with nothing sendable (only unsigned reasoning)
  are skipped — the Messages API rejects empty content, which failed
  the next model call.
- anthropic: an empty stop reason on a later `message_delta` no longer
  overwrites a real one.
- openai: a streamed safety refusal (`delta.refusal`,
  `finish_reason: content_filter`) is surfaced as text instead of
  being dropped.
- google: a transient failure creating the SDK client on first use is
  retried on the next call instead of being cached forever.
- `StopWhen` conditions (`HasToolCall`, structured output's stop) no
  longer panic when called with an empty step slice.
- mw: `Retry` panicked on a `BaseDelay` below 2ns (jitter computed
  `rand.Int64N(0)`); the backoff now returns such delays unchanged.
- mw: `MaxRetries(0)` kept its documented retry-after behaviour only in
  the docs — the attempt-budget check returned the raw error before the
  fail-fast ran, so `ErrRetryAfterTooLong` was unreachable. The fail-fast
  now outranks the budget.
- mw: the doc.go composition example put `Retry` outside `Fallback`,
  which retries the fallback chain as a whole — the primary gets one
  attempt and its transient failures are never retried. The canonical
  order is `Fallback` outside `Retry` (retry, then fail over).
- all adapters: a chunk landing in the same instant as the idle deadline
  could be reported as `ErrStreamIdle` (select picks uniformly among
  ready cases); a waiting chunk now wins over the timer.
- anthropic: `input_json_delta` of non-`tool_use` blocks (server tools
  such as web_search) leaked as `ToolArgsDelta` progress for a call that
  never arrives; only `tool_use` fragments surface now.
- anthropic: an empty assistant text part and a hand-built tool call
  with nil arguments no longer reach the wire (the Messages API rejects
  empty text blocks and `input:null`; the inbound stream already
  normalised empty arguments to `{}`).

### Changed — the 2026-09-14 round (docs)

- Documentation corrected: the anthropic and openai adapters' zero
  configuration inherits the vendor SDK's transport retry default (2
  retries on 429/5xx/connection errors), and `MaxRetries(0)` cannot
  disable retries — supply a zero-retry client via `Client(c)` if you
  need one. No code changed; the previous docs were wrong.
- Docs: ADR 0013 amended (per-run thinking mappings, argument-delta
  progress, enforced cancellation, empty-content and refusal handling);
  adapter package docs updated to match.

## 0.1.0 — 2026-09-11

Initial public release: the core agent loop (tools from plain
functions, parallel tool execution with defined failure semantics,
typed streaming events, transcript repair, the `weft.json` manifest),
the `wefttest` offline model and conformance suite, and the
openai/anthropic/google adapters.
