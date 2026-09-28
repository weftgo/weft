package jsonl_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// FuzzLoad (plan §3.4): Load over arbitrary bytes never panics and
// never returns a partial tree without an error or a report. The
// invariant, exactly: when Load returns no error, the file's complete
// lines (the ones ending in '\n') all decoded — the header plus one
// entry each — and the only dropped line, if any, is the torn tail the
// report names. Under Salvage the same holds with malformed complete
// lines counted in the report instead; ErrNewerFormat still fails the
// load, salvage or not.
func FuzzLoad(f *testing.F) {
	// Seeds: the committed format-1 session (clean), and the shapes
	// the load rules exist for — torn tail, malformed middle, unknown
	// kind, newer envelope, empties.
	golden, err := os.ReadFile(filepath.Join("..", "testdata", "format1", "session.jsonl"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(golden)
	f.Add([]byte(`{"type":"session","weft":1,"id":"s_f"}` + "\n" + `{"type":"message","id":"e_1"}` + "\n" + `{"type":"mess`))
	f.Add([]byte(`{"type":"session","weft":1,"id":"s_f"}` + "\n" + "not json\n"))
	f.Add([]byte(`{"type":"session","weft":1,"id":"s_f"}` + "\n" + `{"type":"approval","id":"e_1"}` + "\n"))
	f.Add([]byte(`{"type":"session","weft":2,"id":"s_f"}` + "\n"))
	f.Add([]byte(""))
	f.Add([]byte("\n"))
	f.Add([]byte(`{"type":"session","weft":1,"id":"s_f"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		const id = "s_fuzz"
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		strict, err := jsonl.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		salvaging, err := jsonl.Open(dir, thread.Salvage())
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		complete := strings.Count(string(data), "\n") // lines ending in '\n'

		_, entries, report, err := strict.Load(ctx, id)
		if err == nil {
			if complete == 0 {
				t.Fatalf("loaded a file with no complete header line: %q", data)
			}
			if len(entries) != complete-1 {
				t.Fatalf("no error, %d complete lines, but %d entries (partial tree): %q", complete, len(entries), data)
			}
			if report == nil {
				if len(data) > 0 && data[len(data)-1] != '\n' {
					t.Fatalf("torn tail without a report: %q", data)
				}
			} else if report.Torn != complete+1 || len(report.Skipped) != 0 {
				t.Fatalf("report = %+v for %d complete lines: %q", report, complete, data)
			}
		}

		_, entries, report, err = salvaging.Load(ctx, id)
		if err == nil {
			if complete == 0 {
				t.Fatalf("salvage loaded a file with no complete header line: %q", data)
			}
			// Every complete line decoded or was skipped — nothing in
			// between, nothing dropped silently.
			if got := len(entries) + reportSkipped(report); got != complete-1 {
				t.Fatalf("salvage: %d entries + %d skipped, want %d complete lines: %q",
					len(entries), reportSkipped(report), complete-1, data)
			}
			if report != nil && report.Torn == 0 && len(report.Skipped) == 0 {
				t.Fatalf("empty report returned: %+v", report)
			}
		}
	})
}

func reportSkipped(r *thread.LoadReport) int {
	if r == nil {
		return 0
	}
	return len(r.Skipped)
}
