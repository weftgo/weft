package opencfg

import (
	"io"
	"log/slog"
	"testing"
)

type salvage struct{}

func (salvage) ApplyOpen(c *Config) { c.Salvage = true }

type logTo struct{ l *slog.Logger }

func (o logTo) ApplyOpen(c *Config) { c.Logger = o.l }

// The defaults: every append fsynced, the lock on, no salvage, the
// process logger — and options fold over them in order, nil ones and
// strangers skipped.
func TestResolve(t *testing.T) {
	def := Resolve[Applier](nil)
	if !def.SyncEveryAppend || def.Salvage || def.NoLock || def.Logger != slog.Default() {
		t.Fatalf("defaults = %+v", def)
	}
	l := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := Resolve([]any{nil, salvage{}, "not an option", logTo{l}})
	if !got.Salvage || got.Logger != l || !got.SyncEveryAppend {
		t.Fatalf("resolved = %+v", got)
	}
	var none Applier
	if got := Resolve([]Applier{none}); got.Salvage {
		t.Fatalf("a nil option applied: %+v", got)
	}
}
