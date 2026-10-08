package main

// The Request pane's gate (plan E1, Done clause a): the demo agent's
// PrepareStep trims its system prompt from step 1 on, and the request
// record says so — step 0's system_hash differs from step 1's, the
// later steps keep step 1's, and step 1's prompt is step 0's minus the
// first-step guidance paragraph. Studio's step card draws exactly this
// as the per-step diff with the "changed by PrepareStep" chip; the web
// fixture (studio/web/src/routes/run-request-pane.test.tsx) is this
// record's shape.
import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
)

func TestPrepareStepTrimsThePrompt(t *testing.T) {
	path := t.TempDir() + "/weft.db"
	shutdown := otel.Install(otel.NoEnv(), otel.Local(path))
	// One steered follow-up at the final step: the echo model answers
	// it with another lookup, so the run has steps after step 1 to show
	// that the trimmed text is stable.
	var once sync.Once
	steer := weft.Steering(func(_ context.Context, at weft.SteerPoint) []weft.Message {
		var out []weft.Message
		if at.Final {
			once.Do(func() { out = []weft.Message{weft.User("and order 43?")} })
		}
		return out
	})
	res, err := demoAgent().Generate(context.Background(), weft.Prompt("where is order 42?"), steer)
	if err != nil {
		t.Fatal(err)
	}
	shutdown() // flush the local sink
	if len(res.Steps) < 3 {
		t.Fatalf("the run took %d steps, want at least 3", len(res.Steps))
	}

	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	reqs, err := db.Requests(ctx, res.ID, obsdb.RequestQuery{})
	if err != nil {
		t.Fatal(err)
	}
	hash := map[int]string{}
	for _, r := range reqs {
		if _, ok := hash[r.Step]; !ok {
			hash[r.Step] = r.SystemHash
		}
	}
	if len(hash) != len(res.Steps) {
		t.Fatalf("request records for %d steps, the run took %d", len(hash), len(res.Steps))
	}
	if hash[0] == hash[1] {
		t.Fatalf("step 1's system_hash %s equals step 0's: the PrepareStep trim is not recorded", hash[1])
	}
	for step := 2; step < len(res.Steps); step++ {
		if hash[step] != hash[1] {
			t.Errorf("step %d's system_hash %s, want step 1's %s (the trimmed text is stable)", step, hash[step], hash[1])
		}
	}
	p0, err := db.Prompt(ctx, res.ID, hash[0])
	if err != nil {
		t.Fatal(err)
	}
	p1, err := db.Prompt(ctx, res.ID, hash[1])
	if err != nil {
		t.Fatal(err)
	}
	if p0.Text != demoInstructions {
		t.Errorf("step 0's prompt %q, want the configured instructions %q", p0.Text, demoInstructions)
	}
	if want := strings.Replace(p0.Text, "\n\n"+firstStepGuidance, "", 1); p1.Text != want || p1.Text == p0.Text {
		t.Errorf("step 1's prompt %q, want step 0's minus the guidance paragraph: %q", p1.Text, want)
	}
}
