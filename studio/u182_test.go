package studio

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// U18.2 — UX-C-08's server half: every scripted refusal is a sentence a
// user can act on — what differs and the two ways on ("run on live", or
// undo the change) — and never a doc-section reference.

// TestScriptedRefusalsAreActionable pins the five scripted refusals of
// POST /api/playground/runs, word for word (golden
// testdata/api/playground-scripted-refusals.golden.json), none citing
// "§5.5" or ending "(use engine live)".
func TestScriptedRefusalsAreActionable(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	recordAgentRun(t, pt, "r_s", "acme-support", "lookup_order")
	cmd := func(source, extra string) string {
		return `{"runtime":"rt_test","agent":"acme-support","engine":"scripted"` + source + extra + `}`
	}
	src := func(n string) string { return `,"source":{"run_id":"r_s","from_step":` + n + `}` }
	cases := []struct {
		name, body, want string
	}{
		{"prompt", cmd(src("0"), `,"overrides":{"instructions":"Be terse."}`),
			"scripted cannot try a changed system prompt: the recorded answers ignore the prompt, so it would replay the old answer — run on live, or drop the prompt change"},
		{"model", cmd(src("0"), `,"overrides":{"model":"other"}`),
			"scripted cannot try another model: it replays the answers the source's own model recorded — run on live, or drop the model override"},
		{"no source", cmd("", `,"input":"hi"`),
			"scripted replays a recorded run's answers: pick a source run, or run on live"},
		{"edits", cmd(src("1"), `,"transcript_edits":[{"kind":"insert","step":1,"content":"x"}]`),
			"your edit changes step 1's request, and scripted holds only the recorded answer to the original — run on live, or drop the edit"},
		{"past end", cmd(src("2"), ""),
			"scripted has no recorded answer for step 2: the source never answered it — run on live, or replay from an earlier step"},
	}
	got := map[string]string{}
	for _, c := range cases {
		code, out := pt.post(t, c.body)
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(out), &e)
		if code != http.StatusBadRequest || e.Error.Message != c.want {
			t.Errorf("%s = %d %s\nwant 400 %q", c.name, code, out, c.want)
		}
		for _, bad := range []string{"§", "WEFT-PLAYGROUND", "(use engine live)"} {
			if strings.Contains(e.Error.Message, bad) {
				t.Errorf("%s: the refusal cites %q: %s", c.name, bad, e.Error.Message)
			}
		}
		got[c.name] = e.Error.Message
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	stepGolden(t, "playground-scripted-refusals.golden.json", string(b))
}
