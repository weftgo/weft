package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// U18.1 — the target facts on the wire (A6, A7, R1, R2): a source run
// is replayed on its own agent unless the caller opts in, the preview
// says before Run whether the scripted engine answers each step, a
// failed scripted command says which step and which key component, and
// the runtimes view carries each agent's manifest hash.

// recordAgentRun records one turn of an agent (name "" leaves it
// unnamed) whose one step calls tool, then answers.
func recordAgentRun(t *testing.T, pt *playgroundTestServer, runID, name, tool string) {
	t.Helper()
	withPipeline(t, pt.ts.URL, true, func(prov []core.Option) {
		tl := core.Tool(tool, "A tool.", func(context.Context, struct{}) (string, error) { return "done", nil })
		opts := append([]core.Option{core.Instructions("You help."), tl}, prov...)
		if name != "" {
			opts = append(opts, core.Name(name))
		}
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: tool, Args: `{}`, ID: "c1"}),
			wefttest.Say("all done"),
		), opts...)
		if _, err := agent.Generate(context.Background(), core.RunID(runID), core.Prompt("do it")); err != nil {
			t.Fatal(err)
		}
	})
	waitRun(t, pt.ts, runID, 2, pt.token)
}

// TestPlaygroundRunRefusesCrossAgent pins A6's breaking half (UX-C-03,
// UX-C-02's wire half): a source run recorded by another agent than the
// command's target is refused 400 with both agents and the escape named
// — cross_agent: true replays it anyway; a command that names no agent
// is refused with the source's agent (or its lack of one) named; an
// unnamed source with an explicit target runs.
func TestPlaygroundRunRefusesCrossAgent(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	recordAgentRun(t, pt, "r_orders", "orders", "lookup_order")
	recordAgentRun(t, pt, "r_unnamed", "", "lookup_order")
	body := func(agent, run, extra string) string {
		return `{"runtime":"rt_test","agent":"` + agent + `","source":{"run_id":"` + run + `","from_step":0},"engine":"live"` + extra + `}`
	}

	code, out := pt.post(t, body("acme-support", "r_orders", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("cross-agent run = %d %s, want 400", code, out)
	}
	const want = `source run r_orders is agent orders; this command targets agent acme-support: send agent \"orders\" to replay it as recorded, or cross_agent: true to replay it as acme-support anyway`
	if !strings.Contains(out, want) {
		t.Errorf("cross-agent refusal = %s\nwant %q", out, want)
	}
	stepGolden(t, "playground-cross-agent.golden.json", out)

	if code, out := pt.post(t, body("acme-support", "r_orders", `,"cross_agent":true`)); code != http.StatusAccepted {
		t.Errorf("cross_agent: true = %d %s, want 202", code, out)
	} else {
		pt.waitCommand(t, "cross")
	}

	// No agent named: Studio never picks one.
	if code, out := pt.post(t, body("", "r_orders", "")); code != http.StatusBadRequest ||
		!strings.Contains(out, `a run needs an agent: source run r_orders is agent orders (send agent: \"orders\" to replay it on that agent)`) {
		t.Errorf("no agent, named source = %d %s", code, out)
	}
	if code, out := pt.post(t, body("", "r_unnamed", "")); code != http.StatusBadRequest ||
		!strings.Contains(out, "source run r_unnamed names no agent: name the agent to replay it on (agent), Studio does not pick one") {
		t.Errorf("no agent, unnamed source = %d %s", code, out)
	}
	// An unnamed source with an explicit target: the caller chose.
	if code, out := pt.post(t, body("acme-support", "r_unnamed", "")); code != http.StatusAccepted {
		t.Errorf("unnamed source, named target = %d %s, want 202", code, out)
	} else {
		pt.waitCommand(t, "unnamed")
	}
	// The preview still assembles a cross-agent replay: it warns.
	if code, out := previewPost(t, pt, "/api/playground/preview", "", body("acme-support", "r_orders", "")); code != http.StatusOK ||
		!strings.Contains(out, "the source run is agent orders's; this replays it as agent acme-support") {
		t.Errorf("cross-agent preview = %d %s, want 200 with the agent warning", code, out)
	}
}

// scriptedDocT is the preview's scripted check as a client decodes it.
type scriptedDocT struct {
	Hit []struct {
		Step    int      `json:"step"`
		Hit     bool     `json:"hit"`
		Because []string `json:"because"`
	} `json:"scripted_hit"`
	Match struct {
		OK      bool     `json:"ok"`
		Because []string `json:"because"`
		Step    *int     `json:"step"`
		Message string   `json:"message"`
	} `json:"scripted_key_match"`
}

// TestPreviewScriptedCheck pins A6's preview half (UX-D-03, UX-C-06):
// per step, whether the scripted engine answers the replay's request,
// computed from the record and the registration — a replay that changes
// nothing hits every step, a narrowed tool list, a thinking level or a
// changed input misses at the step it changes, a recorded call to a
// tool the registered agent lacks misses at the next step — and the
// summary's sentence names the step and the component.
func TestPreviewScriptedCheck(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	recordPreviewRun(t, pt, "r_sc")                                // acme-support, lookup_order ×2, reply
	recordAgentRun(t, pt, "r_docs", "acme-support", "search_docs") // a tool the registration lacks

	preview := func(run string, from int, extra string) scriptedDocT {
		t.Helper()
		b := `{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"` + run + `","from_step":` + strconv.Itoa(from) + `},"engine":"scripted"` + extra + `}`
		code, out := previewPost(t, pt, "/api/playground/preview", "", b)
		if code != http.StatusOK {
			t.Fatalf("preview %s = %d %s", b, code, out)
		}
		var d scriptedDocT
		decode(t, out, &d)
		return d
	}
	hits := func(d scriptedDocT) string {
		var parts []string
		for _, s := range d.Hit {
			p := strconv.Itoa(s.Step) + ":"
			if s.Hit {
				p += "hit"
			} else {
				p += strings.Join(s.Because, "+")
			}
			parts = append(parts, p)
		}
		return strings.Join(parts, " ")
	}

	for _, c := range []struct {
		name, run string
		from      int
		extra     string
		hits      string
		message   string
	}{
		{"unchanged", "r_sc", 0, "", "0:hit 1:hit 2:hit", ""},
		{"from step 1", "r_sc", 1, "", "1:hit 2:hit", ""},
		{"narrowed tools", "r_sc", 0, `,"overrides":{"only_tools":["lookup_order"]}`, "0:tools",
			"scripted has no recorded answer for step 0: the tool list differs from the recording — re-run on engine live"},
		{"thinking", "r_sc", 1, `,"overrides":{"thinking":"high"}`, "1:thinking",
			"scripted has no recorded answer for step 1: the request sets a thinking level the recording does not hold — re-run on engine live"},
		{"changed input", "r_sc", 0, `,"input":"where is order 99?"`, "0:messages",
			"scripted has no recorded answer for step 0: the messages differ from the recording (a changed input or tool result) — re-run on engine live"},
		{"recorded call to an unregistered tool", "r_docs", 0, "", "0:hit 1:tools",
			"scripted has no recorded answer for step 1: the tool list differs from the recording — re-run on engine live"},
	} {
		d := preview(c.run, c.from, c.extra)
		if got := hits(d); got != c.hits {
			t.Errorf("%s: scripted_hit = %s, want %s", c.name, got, c.hits)
		}
		if ok := c.message == ""; d.Match.OK != ok || d.Match.Message != c.message {
			t.Errorf("%s: scripted_key_match = %+v, want ok %v message %q", c.name, d.Match, ok, c.message)
		}
		if !d.Match.OK && (d.Match.Step == nil || *d.Match.Step != d.Hit[len(d.Hit)-1].Step) {
			t.Errorf("%s: scripted_key_match.step = %v, want the first miss", c.name, d.Match.Step)
		}
	}
}

// TestCommandReasonNamesScriptedMiss pins R2's server half: a finished
// command whose run failed with weft/runtime's scripted miss carries
// reason{kind, step, components, message} — Studio's one sentence for
// the Studio UI and the panel — beside the raw error; any other error
// carries no reason.
func TestCommandReasonNamesScriptedMiss(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	run := func(id, errText string) string {
		t.Helper()
		if code, out := pt.post(t, `{"runtime":"rt_test","agent":"acme-support","command_id":"`+id+`","input":"go"}`); code != http.StatusAccepted {
			t.Fatalf("enqueue = %d %s", code, out)
		}
		pt.waitCommand(t, id)
		pt.ack(t, `{"command_id":"`+id+`","state":"accepted","run_id":"pg_`+id+`"}`)
		b, _ := json.Marshal(map[string]string{"command_id": id, "state": "finished", "run_id": "pg_" + id, "status": "failed", "error": errText})
		pt.ack(t, string(b))
		_, out := pt.get(t, "/api/playground/commands/"+id)
		return out
	}
	miss := "weft: run failed at step 0: model stream: weft/runtime: no recorded turn for step 2 (scripted engine, key 700b66abcf4561ab): " +
		"the tool list differs from the recording; the messages differ from the recording (a changed input or tool result) [tools, messages]; " +
		"the scripted engine answers only what the source recorded: re-run on engine live"
	out := run("cmd_miss", miss)
	var st struct {
		Error  string              `json:"error"`
		Reason *linkruntime.Reason `json:"reason"`
	}
	decode(t, out, &st)
	if st.Error != miss {
		t.Errorf("error = %q, want the runtime's text verbatim", st.Error)
	}
	want := "scripted has no recorded answer for step 2: the tool list differs from the recording; the messages differ from the recording (a changed input or tool result) — re-run on engine live"
	if st.Reason == nil || st.Reason.Kind != "scripted_miss" || st.Reason.Step != 2 ||
		strings.Join(st.Reason.Components, ",") != "tools,messages" || st.Reason.Message != want {
		t.Errorf("reason = %+v, want scripted_miss at step 2 [tools messages] %q", st.Reason, want)
	}
	golden := regexp.MustCompile(`"(created|updated)": "[^"]*"`).ReplaceAllString(pretty(t, out), `"$1": "(time)"`)
	wefttest.Golden(t, "testdata/api/playground-command-reason.golden.json", []byte(golden))

	if out := run("cmd_other", "weft: run failed at step 0: boom"); strings.Contains(out, `"reason"`) {
		t.Errorf("a plain failure carries a reason: %s", out)
	}
}

// TestRuntimesViewCarriesManifestHash pins A7/R1: each agent of GET
// /api/runtimes carries its manifest hash — the registration's, else
// sha256 of the manifest it registered (the core's weft.manifest.hash
// of the same bytes) — and the Studio URL its link dialled, under
// cwd's rule (the server token or setup A's loopback).
func TestRuntimesViewCarriesManifestHash(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	reg, _ := pt.rs.Registration("rt_test")
	sum := sha256.Sum256([]byte(reg.Agents[0].Manifest))
	_, out := pt.get(t, "/api/runtimes")
	if !strings.Contains(out, `"manifest_hash":"`+hex.EncodeToString(sum[:])+`"`) {
		t.Errorf("runtimes view lacks the computed manifest hash: %s", out)
	}
	reg.Agents[0].ManifestHash = "feedface"
	reg.StudioURL = "http://127.0.0.1:7342"
	b, _ := json.Marshal(reg)
	req, _ := http.NewRequest(http.MethodPost, pt.ts.URL+"/api/runtime/register", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if code, out := pt.do(t, req); code != http.StatusOK {
		t.Fatalf("re-register = %d %s", code, out)
	}
	_, out = pt.get(t, "/api/runtimes")
	if !strings.Contains(out, `"manifest_hash":"feedface"`) || !strings.Contains(out, `"studio_url":"http://127.0.0.1:7342"`) {
		t.Errorf("runtimes view = %s, want the registered hash and studio_url", out)
	}
}
