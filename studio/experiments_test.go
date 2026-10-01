package studio_test

// The experiments routes (WEFT-PLAYGROUND §10.4, PQ4): POST saves a
// definition, GET lists the history, GET /{id} joins the runs the
// experiment's commands labelled — and a panel token cannot write a
// definition (the matrix is Studio's; the panel hands off).

import (
	"encoding/json"
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
