package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// A per-tool Timeout turns a slow handler into an error result the model
// sees; the run itself continues. A handler that ignores ctx is
// abandoned rather than allowed to hang the run.
func TestToolTimeout(t *testing.T) {
	polite := core.Tool("polite", "", func(ctx context.Context, _ struct{}) (string, error) {
		select {
		case <-time.After(5 * time.Second):
			return "late", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, core.Timeout(20*time.Millisecond))
	block := make(chan struct{})
	defer close(block)
	rude := core.Tool("rude", "", func(_ context.Context, _ struct{}) (string, error) {
		<-block
		return "never", nil
	}, core.Timeout(20*time.Millisecond))
	quick := core.Tool("quick", "", func(_ context.Context, _ struct{}) (string, error) {
		return "fast", nil
	}, core.Timeout(time.Second))

	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "polite"}, wefttest.Call{Name: "rude"}, wefttest.Call{Name: "quick"}),
		wefttest.Say("ok"),
	), polite, rude, quick)

	start := time.Now()
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("run took %s: an abandoned handler held the run", took)
	}
	results := res.Steps[0].Results
	for _, i := range []int{0, 1} {
		if !results[i].IsError {
			t.Errorf("result %d should be a timeout error, got %q", i, results[i].Content)
		}
		if want := `timed out after 20ms`; !strings.Contains(results[i].Content, want) {
			t.Errorf("result %d = %q, want it to contain %q", i, results[i].Content, want)
		}
	}
	if results[2].IsError || results[2].Content != "fast" {
		t.Errorf("quick tool under a generous timeout = %+v", results[2])
	}
	if res.Text() != "ok" {
		t.Errorf("run did not continue after the timeouts: %q", res.Text())
	}
}

// Timeout on the agent is the default; a tool's own Timeout overrides it.
func TestAgentTimeoutIsTheDefault(t *testing.T) {
	sleepy := func(ctx context.Context, _ struct{}) (string, error) {
		select {
		case <-time.After(100 * time.Millisecond):
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	inherits := core.Tool("inherits", "", sleepy)
	overrides := core.Tool("overrides", "", sleepy, core.Timeout(2*time.Second))
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "inherits"}, wefttest.Call{Name: "overrides"}),
		wefttest.Say("ok"),
	), core.Timeout(10*time.Millisecond), inherits, overrides)

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	results := res.Steps[0].Results
	if !results[0].IsError || !strings.Contains(results[0].Content, "timed out after 10ms") {
		t.Errorf("tool under the agent default = %+v", results[0])
	}
	if results[1].IsError || results[1].Content != "done" {
		t.Errorf("tool with its own timeout = %+v", results[1])
	}
}

// Timeout(0) on a tool removes the agent's default for that tool alone
// — the timeout analogue of MaxResultBytes(0).
func TestTimeoutZeroOnToolLiftsAgentDefault(t *testing.T) {
	sleepy := func(ctx context.Context, _ struct{}) (string, error) {
		select {
		case <-time.After(80 * time.Millisecond):
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	exempt := core.Tool("exempt", "", sleepy, core.Timeout(0))
	inherits := core.Tool("inherits", "", sleepy)
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "exempt"}, wefttest.Call{Name: "inherits"}),
		wefttest.Say("ok"),
	), core.Name("m"), core.Timeout(20*time.Millisecond), exempt, inherits)

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	results := res.Steps[0].Results
	if results[0].IsError || results[0].Content != "done" {
		t.Errorf("Timeout(0) tool = %+v, want it exempt from the agent default", results[0])
	}
	if !results[1].IsError || !strings.Contains(results[1].Content, "timed out after 20ms") {
		t.Errorf("tool without its own timeout = %+v, want the agent default", results[1])
	}
	// The manifest records the explicit lift, like max_result_bytes: 0.
	b, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"timeout": "0s"`) {
		t.Errorf("manifest lacks the Timeout(0) lift:\n%s", b)
	}
}

// Cancelling the run during a timed call reports the cancellation, not
// a timeout.
func TestTimeoutReportsCancellationAsSuch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wait := core.Tool("wait", "", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}, core.Timeout(time.Minute))
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("ok"),
	), wait)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := agt.Generate(ctx, core.Prompt("x"))
	var runErr *core.RunError
	if !errors.As(err, &runErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a RunError wrapping context.Canceled", err)
	}
	if r := runErr.Result.Steps[0].Results[0]; strings.Contains(r.Content, "timed out") {
		t.Errorf("cancellation was reported as a timeout: %q", r.Content)
	}
}

// A per-tool MaxResultBytes overrides the agent's cap in both directions.
func TestPerToolMaxResultBytes(t *testing.T) {
	big := func(_ context.Context, _ struct{}) (string, error) {
		return strings.Repeat("x", 1000), nil
	}
	tight := core.Tool("tight", "", big, core.MaxResultBytes(10))
	whole := core.Tool("whole", "", big, core.MaxResultBytes(0))
	inherits := core.Tool("inherits", "", big)
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "tight"}, wefttest.Call{Name: "whole"}, wefttest.Call{Name: "inherits"}),
		wefttest.Say("ok"),
	), core.MaxResultBytes(100), tight, whole, inherits)

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	results := res.Steps[0].Results
	if !strings.HasPrefix(results[0].Content, strings.Repeat("x", 10)+"\n…[truncated 990 bytes]") {
		t.Errorf("tight = %q", results[0].Content)
	}
	if len(results[1].Content) != 1000 {
		t.Errorf("whole: len = %d, want 1000 (per-tool MaxResultBytes(0) lifts the cap)", len(results[1].Content))
	}
	if !strings.HasSuffix(results[2].Content, "[truncated 900 bytes]") {
		t.Errorf("inherits = %q, want the agent cap", results[2].Content[len(results[2].Content)-40:])
	}
}

// Decode failures name the field in schema vocabulary so the model can
// map the error back to the schema it saw. Undeclared fields are ignored
// by default and rejected under StrictInput.
func TestDecodeErrorsNameTheField(t *testing.T) {
	type In struct {
		City string `json:"city"`
		Days int    `json:"days,omitempty"`
	}
	lenient := core.Tool("forecast", "", func(_ context.Context, in In) (string, error) {
		return in.City, nil
	})
	strict := core.Tool("forecast", "", func(_ context.Context, in In) (string, error) {
		return in.City, nil
	}, core.StrictInput())
	// The two kinds where the schema's wire type differs from the plain
	// Go kind: decode errors must speak the schema's vocabulary, not
	// the Go kind mapping.
	blob := core.Tool("blob", "", func(_ context.Context, in struct {
		Data []byte `json:"data"`
	}) (string, error) {
		return string(in.Data), nil
	})
	quoted := core.Tool("quoted", "", func(_ context.Context, in struct {
		N int `json:"n,string"`
	}) (string, error) {
		return "", nil
	})
	// Nested paths: the error's field path may carry array indices and
	// map keys ("items.0.qty", "meta.k.when"); the schema walk must step
	// through Items and AdditionalProperties to reach the advertised
	// leaf type.
	type item struct {
		Qty int `json:"qty,string"`
	}
	nested := core.Tool("nested", "", func(_ context.Context, in struct {
		Items []item `json:"items"`
		Meta  map[string]struct {
			When string `json:"when"`
		} `json:"meta"`
	}) (string, error) {
		return "", nil
	})
	ctx := context.Background()

	cases := []struct {
		name string
		tool *core.ToolDef
		args string
		want string // substring the error must contain; "" means success
		// wantAny lists renderings that vary by toolchain; when set, the
		// error must contain at least one of them instead of want.
		// encoding/json reports a ",string" mismatch as an
		// UnmarshalTypeError on newer toolchains (so the field is named)
		// and as a plain error on older ones (no field available), and
		// map keys appear in the error path only on some versions —
		// describeDecodeError must speak the schema's vocabulary either
		// way.
		wantAny []string
	}{
		{"type mismatch", lenient, `{"city":"Oslo","days":"three"}`, `field "days": expected integer, got string`, nil},
		{"[]byte speaks the schema", blob, `{"data":{}}`, `field "data": expected string, got object`, nil},
		{",string speaks the schema", quoted, `{"n":9}`, "", []string{
			`field "n": expected string, got number`,
			`expected string: a ",string" field's value must arrive inside quotes`}},
		{"nested ,string through an array", nested, `{"items":[{"qty":5}]}`, "", []string{
			`: expected string, got number`,
			`expected string: a ",string" field's value must arrive inside quotes`}},
		{"nested through a map value", nested, `{"meta":{"k":{"when":7}}}`, `: expected string, got number`, nil},
		{"array itself", nested, `{"items":"x"}`, `field "items": expected array, got string`, nil},
		{"top-level mismatch", lenient, `[1,2]`, `expected object at the top level, got array`, nil},
		{"syntax", lenient, `{"city":}`, `invalid JSON at offset`, nil},
		{"truncated", lenient, `{"city":`, `invalid JSON: unexpected end of input`, nil},
		{"unknown field ignored", lenient, `{"city":"Oslo","units":"C"}`, "", nil},
		{"unknown field rejected", strict, `{"city":"Oslo","units":"C"}`, `unknown field "units": not in the schema`, nil},
		{"trailing second object", lenient, `{"city":"Oslo"} {"city":"Rome"}`, "trailing data after the JSON arguments", nil},
		{"trailing garbage", strict, `{"city":"Oslo"} x`, "trailing data after the JSON arguments", nil},
		{"trailing whitespace only", lenient, `{"city":"Oslo"} `, "", nil},
		{"empty args", lenient, ``, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.tool.Invoke(ctx, []byte(tc.args))
			if tc.want == "" && tc.wantAny == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, core.ErrInvalidToolInput) {
				t.Fatalf("err = %v, want ErrInvalidToolInput", err)
			}
			if len(tc.wantAny) > 0 {
				for _, want := range tc.wantAny {
					if strings.Contains(err.Error(), want) {
						return
					}
				}
				t.Errorf("err = %q, want it to contain one of %q", err, tc.wantAny)
				return
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to contain %q", err, tc.want)
			}
		})
	}

	// Agent-level StrictInput applies to every tool in the loop.
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "forecast", Args: `{"city":"Oslo","units":"C"}`}),
		wefttest.Say("ok"),
	), core.StrictInput(), lenient)
	res, err := agt.Generate(ctx, core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; !r.IsError || !strings.Contains(r.Content, `unknown field "units"`) {
		t.Errorf("agent-level StrictInput not applied: %+v", r)
	}
}

// Per-tool policy is visible in the manifest, and only when set.
func TestManifestRecordsToolPolicy(t *testing.T) {
	plain := core.Tool("plain", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	tuned := core.Tool("tuned", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		core.Timeout(30*time.Second), core.MaxResultBytes(0), core.StrictInput())
	agt := core.New(wefttest.Script(), core.Name("m"), core.Timeout(time.Minute), plain, tuned)
	b, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, want := range []string{`"timeout": "1m0s"`, `"timeout": "30s"`, `"max_result_bytes": 0`, `"strict_input": true`} {
		if !strings.Contains(doc, want) {
			t.Errorf("manifest lacks %s:\n%s", want, doc)
		}
	}
	if n := strings.Count(doc, `"timeout"`); n != 2 {
		t.Errorf("timeout appears %d times, want 2 (agent + tuned only):\n%s", n, doc)
	}
}

// RequiresApproval is the read side of the RequireApproval option: the
// manifest reads the field internally, and weft/mcp's AddTools refuses
// such a tool at registration (no approval channel over MCP), which
// needs it exported.
func TestRequiresApprovalAccessor(t *testing.T) {
	gated := core.Tool("refund", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		core.RequireApproval())
	if !gated.RequiresApproval() {
		t.Errorf("gated tool reports false")
	}
	plain := core.Tool("lookup", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	if plain.RequiresApproval() {
		t.Errorf("plain tool reports true")
	}
}
