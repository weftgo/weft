package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/weftgo/weft"
)

// The executor (WEFT-PLAYGROUND.md §5.2): a command becomes one run
// of one registered agent. P0 executes engine "live" and thread
// "ephemeral" only — fork and scripted are 8b; Studio refuses them
// earlier ("not yet available"), and the runtime re-refuses anything
// that reaches it anyway (its copy is authoritative).

// validate re-checks the command against the runtime's own registry
// before the accepted ack (§10.4: Studio's copy can be stale after a
// reconnect; this one is authoritative). A false return rejects the
// command with the given reason — never a run.
func (l *link) validate(cmd command) (string, bool) {
	agent, ok := l.reg.agent(cmd.Agent)
	if !ok || agent == nil {
		return fmt.Sprintf("unknown agent %q", cmd.Agent), false
	}
	switch cmd.Engine {
	case "", "live":
	case "scripted":
		return "engine scripted is not yet available", false
	default:
		return fmt.Sprintf("unknown engine %q", cmd.Engine), false
	}
	switch cmd.Thread {
	case "", "ephemeral":
	case "fork":
		return "thread fork is not yet available", false
	default:
		return fmt.Sprintf("unknown thread mode %q", cmd.Thread), false
	}
	if len(cmd.TranscriptEdits) > 0 {
		return "transcript_edits are not yet available", false
	}
	entry, _ := l.reg.entry(cmd.Agent)
	tools := map[string]bool{}
	for _, t := range agent.Tools() {
		tools[t.Name] = true
	}
	for _, name := range cmd.Overrides.ToolsEnabled {
		if !tools[name] {
			return fmt.Sprintf("unknown tool %q", name), false
		}
	}
	if m := cmd.Overrides.Model; m != "" {
		if _, ok := l.reg.model(m); !ok && m != l.reg.ownModel(cmd.Agent) {
			return fmt.Sprintf("model %q is not on this runtime's allow-list", m), false
		}
	}
	if n := int(cmd.Overrides.Options["max_steps"]); n > 0 && entry.Limits.MaxSteps > 0 && n > entry.Limits.MaxSteps {
		return fmt.Sprintf("max_steps %d raises the agent's cap %d", n, entry.Limits.MaxSteps), false
	}
	if n := int(cmd.Overrides.Options["parallelism"]); n > 0 && entry.Limits.Parallelism > 0 && n > entry.Limits.Parallelism {
		return fmt.Sprintf("parallelism %d raises the agent's cap %d", n, entry.Limits.Parallelism), false
	}
	if cmd.SideEffects == "allow" {
		for _, name := range enabledTools(cmd, tools) {
			if !entry.isAllowed(name) {
				return fmt.Sprintf("tool %q is not opted in for real side effects", name), false
			}
		}
	}
	// The budget cap (§6 rule 6): a breach rejects the next command of
	// that experiment, never a run in flight and never the app's own
	// runs (only commands dispatched here are counted).
	if cmd.ExperimentID != "" {
		l.mu.Lock()
		st := l.tally[cmd.ExperimentID]
		if st == nil {
			st = &budgetState{}
			l.tally[cmd.ExperimentID] = st
		}
		over := st.over(l.cfg.budget, 0)
		l.mu.Unlock()
		if over {
			return "budget_exceeded", false
		}
	}
	return "", true
}

// enabledTools lists the tools this command leaves on: the override
// subset when set, else the agent's full set.
func enabledTools(cmd command, tools map[string]bool) []string {
	if len(cmd.Overrides.ToolsEnabled) > 0 {
		return cmd.Overrides.ToolsEnabled
	}
	var all []string
	for name := range tools {
		all = append(all, name)
	}
	return all
}

// execute runs the command as one ephemeral run of its agent and
// returns the run's status for the finished ack ("succeeded" or
// "failed"). The run's content reaches Studio through the normal OTel
// pipeline; the link carries only the acks.
func (l *link) execute(ctx context.Context, cmd command, runID string) string {
	agent, _ := l.reg.agent(cmd.Agent)
	opts := l.runOptions(cmd, runID)
	res, err := agent.Generate(ctx, opts...)
	status := "succeeded"
	if err != nil {
		status = "failed"
		slog.Warn("weft/runtime: playground run failed",
			"command_id", cmd.CommandID, "run_id", runID, "err", err)
	}
	// The budget tally counts what this command spent (§6 rule 6:
	// "counted from each command's OnRunEnd usage" — this link is the
	// run's caller, so the result's usage is that count).
	if cmd.ExperimentID != "" && res != nil {
		l.mu.Lock()
		st := l.tally[cmd.ExperimentID]
		if st == nil {
			st = &budgetState{}
			l.tally[cmd.ExperimentID] = st
		}
		st.spend(res.Usage.InputTokens + res.Usage.OutputTokens)
		l.mu.Unlock()
	}
	return status
}

// runOptions composes the run exactly as §5.2's snippet does: the
// overrides as plain weft RunOptions (dual Option/RunOption knobs, no
// OverrideSpec), the source transcript as Messages (the loop repairs
// a fed-back transcript), ParkOn for every tool not opted in, and the
// experiment's metadata — weft.playground, weft.experiment.id,
// weft.playground.command, weft.forked_from, weft.public_id — and
// never weft.session.id (an ephemeral experiment is not a turn of the
// session; §5.2).
func (l *link) runOptions(cmd command, runID string) []weft.RunOption {
	var opts []weft.RunOption
	o := cmd.Overrides

	if o.Instructions != "" {
		opts = append(opts, weft.Instructions(o.Instructions))
	}
	if len(o.ToolsEnabled) > 0 {
		opts = append(opts, weft.OnlyTools(o.ToolsEnabled...))
	}
	if m := o.Model; m != "" {
		if alt, ok := l.reg.model(m); ok && alt != nil {
			opts = append(opts, weft.UseModel(alt))
		}
	}
	if lvl, ok := thinkingLevel(o.Thinking); ok {
		opts = append(opts, weft.Thinking(weft.ThinkingConfig{Level: lvl}))
	}
	if n := int(o.Options["max_steps"]); n > 0 {
		opts = append(opts, weft.MaxSteps(n)) // lower only; a raise was rejected above
	}
	if n := int(o.Options["parallelism"]); n > 0 {
		opts = append(opts, weft.Parallelism(n))
	}
	if t, ok := o.Options["temperature"]; ok {
		temp := t
		opts = append(opts, weft.Params(weft.RequestParams{Temperature: &temp}))
	}

	// Side-effect safety until ReplayPolicy exists (8b): every tool
	// the runtime has not opted in parks at the approval boundary
	// instead of running (§6 rule 3, §7 P1's rule).
	if parked := l.reg.parkedTools(cmd.Agent, o.ToolsEnabled); len(parked) > 0 {
		opts = append(opts, weft.ParkOn(parked...))
	}

	// The source turn's context: the transcript through step from_step
	// − 1, repaired by the loop. A fresh command (no source) starts
	// from the input alone.
	if cmd.Source != nil && cmd.Source.RunID != "" {
		msgs, err := l.sourceTranscript(context.Background(), cmd.Source.RunID)
		if err != nil {
			slog.Warn("weft/runtime: source transcript unresolved; running without it",
				"run_id", cmd.Source.RunID, "err", err)
		} else if cut := cutAtStep(msgs, cmd.Source.FromStep); cut > 0 {
			opts = append(opts, weft.Messages(msgs[:cut]...))
		}
	}
	if cmd.Input != nil && *cmd.Input != "" {
		opts = append(opts, weft.Prompt(*cmd.Input))
	}

	meta := map[string]string{
		"weft.playground":         "true",
		"weft.playground.command": cmd.CommandID,
	}
	if cmd.ExperimentID != "" {
		meta["weft.experiment.id"] = cmd.ExperimentID
	}
	if cmd.Source != nil && cmd.Source.RunID != "" {
		meta["weft.forked_from"] = fmt.Sprintf("%s#%d", cmd.Source.RunID, cmd.Source.FromStep)
	}
	if cmd.PublicID != "" {
		meta["weft.public_id"] = cmd.PublicID
	}
	if cmd.Actor != "" {
		meta["weft.playground.actor"] = cmd.Actor
	}
	opts = append(opts, weft.Metadata(meta))
	opts = append(opts, weft.RunID(runID))
	return opts
}

// thinkingLevel maps the wire vocabulary (the arena playground's)
// onto the core's neutral scale.
func thinkingLevel(s string) (weft.ThinkingLevel, bool) {
	switch s {
	case "off":
		return weft.ThinkOff, true
	case "low":
		return weft.ThinkLow, true
	case "medium":
		return weft.ThinkMedium, true
	case "high":
		return weft.ThinkHigh, true
	case "":
		return 0, false
	default:
		return 0, false
	}
}

// cutAtStep implements §5.1's from_step semantics: keep the transcript
// through step N−1 (its tool results included) and run step N fresh.
// The cut is the message index where step N begins: the Nth assistant
// message (steps count from 0, an assistant message opens each step),
// so everything before it is steps 0..N−1 complete. That is the same
// boundary the messages records' weft.step.index draws (each record
// carries its step; keeping the ones below N), expressed over the
// concatenated bodies because no P0 read path exposes the attribute
// yet (obsdb's Transcript returns bodies only; studio/api.go's
// transcript step reads 0 for the same reason). Repair on the fed-back
// transcript guarantees no call is left without a result.
func cutAtStep(msgs []weft.Message, fromStep int) int {
	if fromStep <= 0 {
		return 0 // re-run the whole turn: no prefix kept
	}
	assistants := 0
	for i, m := range msgs {
		if m.Role == weft.RoleAssistant {
			if assistants == fromStep {
				return i
			}
			assistants++
		}
	}
	return len(msgs) // fewer steps than asked: keep it all
}
