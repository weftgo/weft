package studio_test

// The experiments routes (WEFT-PLAYGROUND §10.4, PQ4): POST saves a
// definition, GET lists the history, GET /{id} joins the runs the
// experiment's commands labelled — and a panel token cannot write a
// definition (the matrix is Studio's; the panel hands off).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/studio"
)

func newExperimentServer(t *testing.T) (*httptest.Server, obsdb.DB) {
	t.Helper()
	db, err := sqlite.Open(t.TempDir() + "/e.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := studio.New(studio.DB(db), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, db
}

func TestExperimentsRoutes(t *testing.T) {
	ts, db := newExperimentServer(t)

	// A definition lands and reads back in §10.4's shape.
	body := `{
	  "id": "exp_1", "name": "tracking-link prompt", "agent": "acme-support",
	  "variants": [{"key": "A", "overrides": {}}, {"key": "B", "overrides": {"instructions": "new"}}],
	  "inputs": [{"key": "1", "source_run_id": "s_1-t1"}, {"key": "2", "text": "angry user"}]
	}`
	resp, err := http.Post(ts.URL+"/api/experiments", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("post = %v %v", err, resp)
	}
	_ = resp.Body.Close()

	// One run labelled with the experiment joins the detail.
	rec := obsdb.Batch{Records: []obsdb.Record{{
		EventName: "weft.event", Body: `{"type":"run_start","id":"pg_1"}`,
		Attrs: map[string]any{"weft.record": "event", "weft.run.id": "pg_1", "weft.event.type": "run_start", "weft.event.pos": int64(0), "weft.playground": true, "weft.experiment.id": "exp_1"},
	}}}
	if err := db.Write(t.Context(), rec); err != nil {
		t.Fatal(err)
	}

	get, err := http.Get(ts.URL + "/api/experiments/exp_1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = get.Body.Close() }()
	var doc struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Variants []struct {
			Key string `json:"key"`
		} `json:"variants"`
		Runs []struct {
			ID           string `json:"id"`
			ExperimentID string `json:"experiment_id"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(get.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID != "exp_1" || doc.Name != "tracking-link prompt" || len(doc.Variants) != 2 {
		t.Errorf("detail = %+v", doc)
	}
	if len(doc.Runs) != 1 || doc.Runs[0].ID != "pg_1" || doc.Runs[0].ExperimentID != "exp_1" {
		t.Errorf("runs = %+v, want the labelled pg_1", doc.Runs)
	}

	// The history lists it.
	list, err := http.Get(ts.URL + "/api/experiments")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = list.Body.Close() }()
	var ldoc struct {
		Experiments []struct {
			ID string `json:"id"`
		} `json:"experiments"`
	}
	if err := json.NewDecoder(list.Body).Decode(&ldoc); err != nil {
		t.Fatal(err)
	}
	if len(ldoc.Experiments) != 1 || ldoc.Experiments[0].ID != "exp_1" {
		t.Errorf("history = %+v", ldoc)
	}

	// Unknown id is a 404; a definition without variants is a 400.
	if resp, _ := http.Get(ts.URL + "/api/experiments/exp_nope"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown experiment = %d, want 404", resp.StatusCode)
	}
	bad, _ := http.Post(ts.URL+"/api/experiments", "application/json",
		strings.NewReader(`{"id":"exp_2","name":"x","variants":[],"inputs":[]}`))
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("empty definition = %d, want 400", bad.StatusCode)
	}
	_ = bad.Body.Close()
}

// TestExperimentCommandFields (plan F5.1): a definition saved without
// the command's fields reads back without them (absent, never zero
// values), and a command the matrix could not have sent is a 400 that
// names the field.
func TestExperimentCommandFields(t *testing.T) {
	ts, _ := newExperimentServer(t)
	matrix := `"variants":[{"key":"A","overrides":{}}],"inputs":[{"key":"1","text":"x"}]`
	resp, err := http.Post(ts.URL+"/api/experiments", "application/json", strings.NewReader(`{"id":"exp_bare",`+matrix+`}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("post = %v %v", err, resp)
	}
	_ = resp.Body.Close()
	get, err := http.Get(ts.URL + "/api/experiments/exp_bare")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.NewDecoder(get.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	_ = get.Body.Close()
	for _, k := range []string{"engine", "side_effects", "thread", "from_step", "source_run_id", "transcript_edits"} {
		if _, ok := doc[k]; ok {
			t.Errorf("a bare definition carries %s = %v, want it absent", k, doc[k])
		}
	}
	if doc["run_count"] != float64(0) || doc["cost_known"] != true {
		t.Errorf("a definition with no runs: run_count %v, cost_known %v; want 0 and a known zero", doc["run_count"], doc["cost_known"])
	}
	// A null or absent overrides is no override; the web's minted id
	// (exp_ + an ISO second) is a valid id.
	resp, _ = http.Post(ts.URL+"/api/experiments", "application/json",
		strings.NewReader(`{"id":"exp_2026-10-10T19:42:13","variants":[{"key":"A","overrides":null},{"key":"B"}],"inputs":[{"key":"1"}]}`))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("null / absent overrides under a minted id = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	// from_step 0 is a value, not an absence.
	resp, _ = http.Post(ts.URL+"/api/experiments", "application/json", strings.NewReader(`{"id":"exp_zero","from_step":0,`+matrix+`}`))
	var zero map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&zero)
	_ = resp.Body.Close()
	if zero["from_step"] != float64(0) {
		t.Errorf("from_step 0 read back as %v, want 0", zero["from_step"])
	}

	for _, c := range []struct{ body, want string }{
		{`{"id":"e","engine":"turbo",` + matrix + `}`, "engine must be live or scripted"},
		{`{"id":"e","side_effects":"yolo",` + matrix + `}`, "side_effects must be substitute, park or allow"},
		{`{"id":"e","thread":"branch",` + matrix + `}`, "thread must be ephemeral or fork"},
		{`{"id":"e","variants":[{"key":"B","engine":"x"}],"inputs":[{"key":"1"}]}`, `variant \"B\": engine must be live or scripted`},
		{`{"id":"e","from_step":-1,` + matrix + `}`, "from_step must be a step ordinal"},
		{`{"id":"e","transcript_edits":{"kind":"reply"},` + matrix + `}`, "transcript_edits must be a list"},
		{`{"id":"e","variants":[{"key":"A","overrides":{}},{"key":"B","overrides":["model"]}],"inputs":[{"key":"1"}]}`, "variants[1].overrides must be an object"},
		{`{"id":"e","variants":[{"key":"A","overrides":"m2"}],"inputs":[{"key":"1"}]}`, "variants[0].overrides must be an object"},
		{`{"id":"has space",` + matrix + `}`, "id must be 1 to 128 characters of [A-Za-z0-9._:-]"},
		{`{"id":"a/b",` + matrix + `}`, "id must be 1 to 128 characters"},
		{`{"id":"` + strings.Repeat("e", 129) + `",` + matrix + `}`, "id must be 1 to 128 characters"},
	} {
		resp, err := http.Post(ts.URL+"/api/experiments", "application/json", strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), c.want) {
			t.Errorf("%s = %d %s, want 400 naming %q", c.body, resp.StatusCode, b, c.want)
		}
	}
}

// TestExperimentsListBounded (M2): the history lists the most recently
// updated experiments only — 50 by default, ?limit= up to 200 — with
// total, how many are saved, so its per-row summaries stay bounded; a
// bad limit is a 400.
func TestExperimentsListBounded(t *testing.T) {
	ts, db := newExperimentServer(t)
	const saved = 203
	for i := range saved {
		if err := db.SaveExperiment(t.Context(), obsdb.Experiment{
			ID: fmt.Sprintf("exp_%03d", i), Name: "n",
			Variants: []obsdb.ExperimentVariant{{Key: "A"}}, Inputs: []obsdb.ExperimentInput{{Key: "1", Text: "x"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	list := func(query string) (int, []string, int) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/experiments" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var doc struct {
			Experiments []struct {
				ID string `json:"id"`
			} `json:"experiments"`
			Total int `json:"total"`
		}
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
				t.Fatal(err)
			}
		}
		ids := make([]string, len(doc.Experiments))
		for i, e := range doc.Experiments {
			ids[i] = e.ID
		}
		return resp.StatusCode, ids, doc.Total
	}
	for _, c := range []struct {
		query string
		rows  int
	}{{"", 50}, {"?limit=0", 50}, {"?limit=3", 3}, {"?limit=1000", 200}} {
		code, ids, total := list(c.query)
		if code != http.StatusOK || len(ids) != c.rows || total != saved {
			t.Errorf("GET /api/experiments%s = %d, %d rows, total %d; want 200, %d rows, total %d", c.query, code, len(ids), total, c.rows, saved)
			continue
		}
		if ids[0] != fmt.Sprintf("exp_%03d", saved-1) {
			t.Errorf("GET /api/experiments%s starts at %s, want the most recently updated", c.query, ids[0])
		}
	}
	if code, _, _ := list("?limit=x"); code != http.StatusBadRequest {
		t.Errorf("?limit=x = %d, want 400", code)
	}
}
