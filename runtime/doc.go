// Package runtime is the playground's in-app side (WEFT-PLAYGROUND.md
// §10.2, [D6]): it registers the agents your code built with the
// Studio the app is already observed by, receives experiment commands
// over the runtime link, and executes them as real runs of those
// agents — your tools, your model keys, your process. Studio never
// runs your agent; it asks, this package answers.
//
// The whole integration is one deferred call:
//
//	defer runtime.Install(
//	    runtime.Studio(url, token),            // or runtime.Local(srv) in setup A
//	    runtime.Agents(support, billing),      // the agents a runtime exposes
//	    runtime.Models(map[string]weft.Model{  // allowed alternates, by display name
//	        "glm-5.3-flash": glmFlash,
//	    }),
//	    runtime.Limits(runtime.Budget{MaxTokensPerExperiment: 200_000, MaxRunsPerExperiment: 60}),
//	    runtime.AllowSideEffects("send_email"), // real only under side_effects "allow"
//	    runtime.Threads(store),                // thread.Storage; nil = ephemeral only
//	)()
//
// # Safety (WEFT-PLAYGROUND.md §6, non-negotiable)
//
// Without Install no link opens, and even then only when WEFT_ENV=dev
// or Enabled(true) is set — production binaries expose nothing unless
// they opt in explicitly. Commands can only narrow: tools the agent
// registered can be turned off, never added (OnlyTools); models come
// from the Models allow-list; MaxSteps and Parallelism only lower.
// Side effects never re-fire silently (§6 rule 3): a tool counts as
// "never" unless its code vouched weft.Replay(weft.ReplaySafe) — a
// vouched tool runs in every mode. A tool the runtime opted in with
// AllowSideEffects runs for real only when the command asks for
// side_effects "allow"; in the other modes it is a side effect like any
// other. In side_effects "substitute" (the default),
// a parked call that matches a recorded call of the source (same tool,
// same arguments as JSON; repeated calls in the order the source made
// them) is answered with the recorded result — the runtime acts as
// ADR 0007's resolver, the handler never runs; a miss stays parked for
// a human (the panel's continue / skip / resolve). "park" answers
// nothing from the record: every such call waits at the boundary.
// "allow" runs the opted-in tools for real, and is refused unless
// every tool the command leaves on is opted in or vouched ReplaySafe.
// A parked run resumes once each of its parked calls has a
// decision, under all of them; a decision naming a call that is not
// parked is rejected. The debugger's breakpoints (§8.3) park their
// tools on every run this package starts, and steer (§8.4) delivers
// into an ephemeral run it holds — not into a fork's turn, whose
// undelivered steer thread would re-run as a follow-up turn without
// the park rule; the app's own turns are never breakable or steerable
// from here (D7, PQ7).
//
// The rule is default-deny (weft.ParkAllExcept): a run lists the tools
// that may execute — the vouched-safe ones, an Output agent's
// submit_output, and under "allow" the opted-in names — and every other
// call parks, matched
// by name against each step's own tool set. So a tool that reaches the
// run only through weft.ToolSource parks like any unannotated tool, and
// the rule follows a weft.Subagent delegation into the child run: the
// child's own unvouched tools park there. One limit: a child's parked
// call is not the panel's to decide — the delegating call reads
// SUBAGENT_PENDING (ADR 0014) and the parent run carries on, the side
// effect never having fired. Names are matched in parent and child
// alike, so opt a name in only if every tool of that name down the
// delegation may run.
//
// Everything a command carries is re-validated here, whatever Studio
// checked: unknown agents, tools, models, modes and options are
// rejected, never defaulted; the source run id, from_step and the
// transcript edits are checked against the transcript this runtime
// resolved itself. The link holds bounded state — at most 256 commands
// admitted and 16 runs executing at once, the newest 4096 command ids
// for at-most-once, 128 parked runs, 64 forks — and stopping it
// cancels the runs it started.
//
// The engines: "live" runs the agent's own model (or a Models
// alternate); "scripted" (§5.5) answers each model call with the
// source run's recorded turn at zero tokens — its own weft.Model over
// the messages records, keyed like wefttest's fixtures and missing
// loudly ("no recorded turn") when the input changed, never silently
// answering a prompt experiment. Thread modes: "ephemeral" (nothing
// written to thread storage) and "fork" (§5.4) — the source session
// opens read-side, Fork copies it to a new session with lineage
// (thread mints the run ids, stamps weft.session.forked_from), and the
// command's input becomes the fork's next turn under the same shaping;
// the source's approval grants are revoked in the fork (the app user's
// standing consent does not decide an experiment's parked call); a
// later fork command naming the fork's latest turn continues it in
// place, one naming an earlier turn forks from that turn. A fork's
// parked call is the fork session's own approval boundary: a decision
// is recorded in the fork and resumes it as the fork's next turn (the
// record is never consulted in a fork — every such call parks).
//
// Runs this package starts are ordinary weft runs: they flow through
// the weft/otel pipeline to every destination, carrying
// weft.playground = true, weft.playground.command, weft.experiment.id
// and weft.forked_from, and never weft.session.id in ephemeral mode
// (an experiment is not a turn of the session). Per-experiment budget
// caps (Limits) are counted from each command's own usage; a breach
// rejects the next command of that experiment and never touches the
// app's own runs.
//
// # Where it sits
//
// A module of its own because it imports thread (fork mode, transcript
// reads) and carries an executor, a registry and budget accounting —
// none of which belongs in the exporter-wiring module weft/otel
// (review §1.8). Arrows point down only: this package imports root,
// thread, obsdb, otel and studio (studio for *studio.Server alone, in
// runtime.Local); nothing imports it back.
package runtime
