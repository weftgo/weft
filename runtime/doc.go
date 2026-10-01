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
//	    runtime.AllowSideEffects("lookup_order"),
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
// "never" unless its code vouched weft.Replay(weft.ReplaySafe). In
// side_effects "substitute" (the default), a parked call that matches
// a recorded call of the source (same tool, same args) is answered
// with the recorded result — the runtime acts as ADR 0007's resolver,
// the handler never runs; a miss stays parked for a human (the panel's
// continue / skip / resolve). "park" keeps every side-effect call at
// the boundary; "allow" runs for real, but only tools the runtime
// opted in with AllowSideEffects. The debugger's breakpoints (§8.3)
// park their tools on every run this package starts, and steer (§8.4)
// delivers into a run it holds — the app's own turns are never
// breakable or steerable from here (D7, PQ7).
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
// a later fork command on the fork continues it in place.
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
