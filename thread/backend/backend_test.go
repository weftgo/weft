package backend_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/backend"
)

// Resolve is the one reading of the open vocabulary: the defaults,
// each option's effect, the last of two opposites winning, and nil
// options skipped.
func TestResolve(t *testing.T) {
	def := backend.Resolve()
	if !def.SyncEveryAppend || def.Salvage || def.NoLock || def.Logger != slog.Default() {
		t.Fatalf("defaults = %+v", def)
	}
	l := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := backend.Resolve(thread.Salvage(), nil, thread.FsyncOnFlush(), thread.NoLock(), thread.OpenLogger(l))
	if !got.Salvage || got.SyncEveryAppend || !got.NoLock || got.Logger != l {
		t.Fatalf("resolved = %+v", got)
	}
	if got := backend.Resolve(thread.FsyncOnFlush(), thread.FsyncEveryAppend()); !got.SyncEveryAppend {
		t.Errorf("the later fsync option did not win: %+v", got)
	}
	if got := backend.Resolve(thread.OpenLogger(nil)); got.Logger != slog.Default() {
		t.Errorf("a nil logger replaced the default: %+v", got)
	}
}
