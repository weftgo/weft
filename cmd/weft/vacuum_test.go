package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/studio"
)

// vacuumRun writes one finished run last seen at at.
func vacuumRun(t *testing.T, db obsdb.DB, id string, at time.Time) {
	t.Helper()
	rec := func(typ string, pos int64) obsdb.Record {
		return obsdb.Record{Time: at.Add(time.Duration(pos) * time.Millisecond), Body: `{"type":"` + typ + `"}`,
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": id, "weft.event.type": typ, "weft.event.pos": pos}}
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{rec("run_start", 0), rec("run_finish", 1)}}); err != nil {
		t.Fatal(err)
	}
}

// weft vacuum: one sweep with --retain (or WEFT_RETAIN), then VACUUM;
// without a retention it only vacuums.
func TestVacuum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vac.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	vacuumRun(t, db, "old", now.Add(-3*time.Hour))
	vacuumRun(t, db, "older", now.Add(-48*time.Hour))
	vacuumRun(t, db, "fresh", now)
	_ = db.Close()

	var out, errb bytes.Buffer
	if code := run([]string{"vacuum", "--db", "sqlite://" + path}, &out, &errb); code != 0 {
		t.Fatalf("vacuum = %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "swept") || !strings.Contains(out.String(), "vacuum: "+path) {
		t.Errorf("vacuum without a retention said %q", out.String())
	}
	// The main file and the WAL apart, the WAL checkpointed (UX-S5a-12).
	if !strings.Contains(out.String(), " bytes (WAL ") || !strings.HasSuffix(out.String(), " → 0 bytes)\n") {
		t.Errorf("vacuum's size line %q: want the main file and the WAL apart, the WAL at 0", out.String())
	}

	t.Setenv("WEFT_RETAIN", "24h")
	out.Reset()
	if code := run([]string{"vacuum", "--db", "sqlite://" + path}, &out, &errb); code != 0 {
		t.Fatalf("vacuum (WEFT_RETAIN) = %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "vacuum: swept 1 runs") || !strings.Contains(out.String(), "(retention 24h0m0s)") {
		t.Errorf("vacuum with WEFT_RETAIN=24h said %q", out.String())
	}
	out.Reset()
	// --retain wins over WEFT_RETAIN.
	if code := run([]string{"vacuum", "--db", "sqlite://" + path, "--retain", "1h"}, &out, &errb); code != 0 {
		t.Fatalf("vacuum --retain = %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "vacuum: swept 1 runs") || !strings.Contains(out.String(), "(retention 1h0m0s)") {
		t.Errorf("vacuum --retain 1h said %q", out.String())
	}

	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, id := range []string{"old", "older"} {
		if _, err := db.Run(context.Background(), id); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("%s after vacuum = %v, want swept", id, err)
		}
	}
	if _, err := db.Run(context.Background(), "fresh"); err != nil {
		t.Errorf("fresh after vacuum = %v", err)
	}
}

func TestVacuumUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vac.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for _, c := range []struct {
		args []string
		env  string
		code int
		msg  string
	}{
		{[]string{"vacuum", "--db", "sqlite://" + path, "--retain", "soon"}, "", 2, `--retain "soon": not a duration`},
		{[]string{"vacuum", "--db", "sqlite://" + path, "--retain", "-1h"}, "", 2, "cannot be negative"},
		{[]string{"vacuum", "--db", "sqlite://" + path}, "bogus", 2, `WEFT_RETAIN "bogus"`},
		{[]string{"vacuum", "--db", "sqlite://" + path, "extra"}, "", 2, "vacuum takes no arguments"},
		{[]string{"vacuum", "--db", "sqlite://" + filepath.Join(dir, "missing.db")}, "", 1, "missing.db"},
		{[]string{"vacuum", "--db", "postgres://x"}, "", 1, "--db must be"},
		{[]string{"studio", "--retain", "soon"}, "", 2, `--retain "soon"`},
		// Retain's floor: under an hour is a usage error on all three.
		{[]string{"vacuum", "--db", "sqlite://" + path, "--retain", "30m"}, "", 2, `--retain "30m": retention must be at least 1h`},
		{[]string{"vacuum", "--db", "sqlite://" + path}, "59m", 2, `WEFT_RETAIN "59m": retention must be at least 1h`},
		{[]string{"studio", "--retain", "10m"}, "", 2, "retention must be at least 1h"},
		{[]string{"studio"}, "30s", 2, `WEFT_RETAIN "30s": retention must be at least 1h`},
		{[]string{"dev", "--no-watch", "--retain", "30m"}, "", 2, "retention must be at least 1h"},
	} {
		t.Setenv("WEFT_RETAIN", c.env)
		var out, errb bytes.Buffer
		if code := run(c.args, &out, &errb); code != c.code || !strings.Contains(errb.String(), c.msg) {
			t.Errorf("%v (WEFT_RETAIN=%q) = %d %q, want %d naming %q", c.args, c.env, code, errb.String(), c.code, c.msg)
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"vacuum", "-h"}, &out, &errb); code != 0 || !strings.Contains(errb.String(), "Usage: weft vacuum") {
		t.Errorf("vacuum -h = %d %q", code, errb.String())
	}
}

// weft vacuum with no --db and no WEFT_DB, and no ./.weft/weft.db,
// refuses (exit 1) and creates nothing: sqlite.Open would have made an
// empty database to vacuum. A missing --db or WEFT_DB file the same.
func TestVacuumNoDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("WEFT_RETAIN", "")
	for _, c := range []struct {
		env, args []string
		msg       string
	}{
		{nil, []string{"vacuum"}, "no database at ./.weft/weft.db (nothing to vacuum)"},
		{[]string{"WEFT_DB", filepath.Join(dir, "env.db")}, []string{"vacuum"}, "no database at " + filepath.Join(dir, "env.db") + " (nothing to vacuum)"},
		{nil, []string{"vacuum", "--db", "sqlite://flag.db"}, "no database at flag.db (nothing to vacuum)"},
	} {
		t.Setenv("WEFT_DB", "")
		if c.env != nil {
			t.Setenv(c.env[0], c.env[1])
		}
		var out, errb bytes.Buffer
		if code := run(c.args, &out, &errb); code != 1 || !strings.Contains(errb.String(), c.msg) {
			t.Errorf("%v = %d %q, want 1 naming %q", c.args, code, errb.String(), c.msg)
		}
	}
	for _, p := range []string{".weft", "env.db", "flag.db"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after a refused vacuum: %v, want never created", p, err)
		}
	}
}

// --retain / WEFT_RETAIN become studio.Retain and the banner's line.
func TestRetainOptions(t *testing.T) {
	t.Setenv("WEFT_RETAIN", "")
	if opts, note, err := retainOptions(""); err != nil || len(opts) != 0 || note != "" {
		t.Errorf("no retention = %d opts, %q, %v", len(opts), note, err)
	}
	if opts, note, err := retainOptions("0"); err != nil || len(opts) != 0 || note != "" {
		t.Errorf("--retain 0 = %d opts, %q, %v (0 is off)", len(opts), note, err)
	}
	t.Setenv("WEFT_RETAIN", "720h")
	if opts, note, err := retainOptions(""); err != nil || len(opts) != 1 || !strings.Contains(note, "retention 720h0m0s") {
		t.Errorf("WEFT_RETAIN=720h = %d opts, %q, %v", len(opts), note, err)
	}
	if d, err := resolveRetain("1h"); err != nil || d != time.Hour {
		t.Errorf("--retain 1h (the floor) = %v, %v", d, err)
	}
	if d, err := resolveRetain("2h"); err != nil || d != 2*time.Hour {
		t.Errorf("--retain 2h over WEFT_RETAIN = %v, %v", d, err)
	}
}

// The binary's floor message is studio.Retain's, word for word: the
// panic New raises under an hour carries it.
func TestRetainFloorMatchesStudio(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "floor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	defer func() {
		if r := recover(); r != "studio: "+retainFloorMsg {
			t.Errorf("studio.New(Retain(30m)) panicked %v, want %q", r, "studio: "+retainFloorMsg)
		}
	}()
	_ = studio.New(studio.DB(db), studio.Retain(30*time.Minute))
}

// weft studio --retain reaches api/meta as retention.
func TestStudioRetainInMeta(t *testing.T) {
	t.Setenv("WEFT_RETAIN", "")
	opts, _, err := retainOptions("168h")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := newServer("sqlite://"+filepath.Join(t.TempDir(), "weft.db"), "tok", opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/meta", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"retention":"168h0m0s"`) || !strings.Contains(string(b), `"delete"`) {
		t.Errorf("meta = %s", b)
	}
}
