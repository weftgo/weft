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
// Until tool ReplayPolicy exists (8b), every tool counts as "never":
// a tool the runtime has not opted in with AllowSideEffects is parked
// at the approval boundary (weft.ParkOn) instead of running, so an
// experiment can never silently re-fire a side effect.
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
