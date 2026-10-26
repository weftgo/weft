package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/runtime"
)

// guardTurns is a turn that repeats one tool call at every step.
func guardTurns(tool, args string, n int) []wefttest.Turn {
	turns := make([]wefttest.Turn, n)
	for i := range turns {
		turns[i] = wefttest.ToolCalls(wefttest.Call{Name: tool, Args: args, ID: "call_1"})
	}
	return turns
}

// TestScriptedReplayOfAGuardStopIsKeptSame (P10a-gate-fix1; the phase
// 10a gate's clause a4 on rw.budget-guards): a scripted replay from
// step n of a turn a guard stopped counts the kept steps toward the
// turn's guards (core.KeptSteps), so it stops where the source stopped
// — the poller's loop_detected from step 1 and step 2, the worker's
// max_steps from step 1 and step 0 (its record_step calls park and are
// substituted, leg after leg of one turn) — and the preview, which
// says scripted hits before Run, agrees with the run. The poller's
// compare reads kept below from_step and the same from it, nothing
// changed; the failure text names the source's step ordinal.
func TestScriptedReplayOfAGuardStopIsKeptSame(t *testing.T) {
	poll := core.Tool("poll_job", "Check a batch job's status.", func(context.Context, struct {
		JobID string `json:"job_id"`
	}) (string, error) {
		return "status: pending — poll again with the same job_id", nil
	}, core.Replay(core.ReplaySafe))
	record := core.Tool("record_step", "Record a step.", func(context.Context, struct {
		N int `json:"n"`
	}) (string, error) {
		return "recorded", nil
	})
	cases := []struct {
		name   string
		agent  func(e *e2e) *core.Agent
		froms  []int
		stop   string
		diff   bool
		source string
	}{
		{"poller loop_detected", func(e *e2e) *core.Agent {
			return core.New(wefttest.Script(guardTurns("poll_job", `{"job_id":"J-7"}`, 3)...), core.Name("poller"), poll,
				core.DetectLoops(3), core.TracerProvider(e.p.TracerProvider()), core.LoggerProvider(e.p.LoggerProvider()))
		}, []int{1, 2}, "3 identical steps in a row", true, "poll J-7 until it is done"},
		{"worker max_steps", func(e *e2e) *core.Agent {
			return core.New(wefttest.Script(guardTurns("record_step", `{"n":1}`, 3)...), core.Name("worker"), record,
				core.MaxSteps(2), core.TracerProvider(e.p.TracerProvider()), core.LoggerProvider(e.p.LoggerProvider()))
		}, []int{1, 0}, "run exceeded the maximum number of steps", false, "record the steps"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newE2E(t)
			e.agent = c.agent(e)
			src := "r_" + strings.Fields(c.name)[0]
			_, err := e.agent.Generate(context.Background(), core.RunID(src), core.Prompt(c.source))
			if err == nil || !strings.Contains(err.Error(), "run failed at step 2: ") || !strings.Contains(err.Error(), c.stop) {
				t.Fatalf("source = %v, want %q at step 2", err, c.stop)
			}
			if err := e.p.ForceFlush(context.Background()); err != nil {
				t.Fatal(err)
			}
			e.waitTranscript(t, src, "call_1")
			shutdown := runtime.Install(runtime.Studio(e.ts.URL, ""), runtime.Agents(e.agent), runtime.Enabled(true))
			defer shutdown()
			rt := e.runtimeID(t)
			name := strings.Fields(c.name)[0]

			for _, from := range c.froms {
				cmd := fmt.Sprintf(`"runtime": %q, "agent": %q, "source": {"run_id": %q, "from_step": %d},
				  "engine": "scripted", "side_effects": "substitute", "thread": "ephemeral"`, rt, name, src, from)
				// The preview before Run: scripted hits.
				code, pv := e.api(t, http.MethodPost, "/api/playground/preview", "{"+cmd+"}")
				var p struct {
					Match struct {
						OK bool `json:"ok"`
					} `json:"scripted_key_match"`
				}
				if code != http.StatusOK || json.Unmarshal([]byte(pv), &p) != nil || !p.Match.OK {
					t.Fatalf("from %d: preview = %d %s, want scripted_key_match ok", from, code, pv)
				}
				id := fmt.Sprintf("cmd_%s_%d", name, from)
				row, replay := e.run(t, id, fmt.Sprintf(`{"command_id": %q, %s}`, id, cmd), "finished")
				var r struct {
					Status string          `json:"status"`
					Error  string          `json:"error"`
					Reason json.RawMessage `json:"reason"`
				}
				if err := json.Unmarshal([]byte(row), &r); err != nil {
					t.Fatal(err)
				}
				if r.Status != "failed" || !strings.Contains(r.Error, c.stop) || len(r.Reason) > 0 && string(r.Reason) != "null" ||
					!strings.HasPrefix(r.Error, "weft: run failed at step 2 (the source's step ordinal; this run's step ") {
					t.Errorf("from %d: command = %s, want failed with the source's stop %q at source step 2, no scripted miss", from, row, c.stop)
				}
				if !c.diff {
					continue
				}
				var d struct {
					Steps []struct {
						Step    int  `json:"step"`
						Kept    bool `json:"kept"`
						Changed bool `json:"changed"`
					} `json:"steps"`
					Summary struct {
						ChangedSteps []int `json:"changed_steps"`
					} `json:"summary"`
				}
				deadline := time.Now().Add(5 * time.Second)
				var body string
				for {
					code, body = e.api(t, http.MethodGet, "/api/diff?a="+src+"&b="+replay, "")
					if code == http.StatusOK && json.Unmarshal([]byte(body), &d) == nil && len(d.Steps) >= 3 &&
						!strings.Contains(body, `"status":"running"`) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("from %d: diff never settled: %s", from, body)
					}
					time.Sleep(10 * time.Millisecond)
				}
				words := make([]string, len(d.Steps))
				ok := len(d.Steps) == 3 && len(d.Summary.ChangedSteps) == 0
				for i, s := range d.Steps {
					switch {
					case s.Kept:
						words[i] = fmt.Sprintf("%d kept", s.Step)
					case s.Changed:
						words[i] = fmt.Sprintf("%d changed", s.Step)
					default:
						words[i] = fmt.Sprintf("%d same", s.Step)
					}
					if s.Kept != (s.Step < from) || s.Changed {
						ok = false
					}
				}
				if !ok {
					t.Errorf("from %d: compare rows %v, want kept below %d and the same from it: %s", from, words, from, body)
				}
			}
		})
	}
}
