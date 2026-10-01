package otel

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// The drop counter: every drop counts, and the WARN is throttled to at
// most one a minute.
func TestDropCounterThrottlesWarn(t *testing.T) {
	buf := &threadSafeBuffer{}
	d := newDropCounter("test")
	d.log = slog.New(slog.NewTextHandler(buf, nil))
	for i := 0; i < 100; i++ {
		d.dropped(1)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var warns int
	for _, l := range lines {
		if strings.Contains(l, "level=WARN") && strings.Contains(l, "dest=test") {
			warns++
		}
	}
	if warns != 1 {
		t.Errorf("%d WARN lines for 100 drops, want 1 (throttled to a minute)", warns)
	}
	if d.count.Load() != 100 {
		t.Errorf("count = %d, want 100", d.count.Load())
	}
	// The window's edge: a drop after the window warns again.
	d.last.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	d.dropped(1)
	if warns = strings.Count(buf.String(), "level=WARN"); warns != 2 {
		t.Errorf("%d WARN lines after the window, want 2", warns)
	}
}

type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Install never fails the program: a destination that cannot be built
// is skipped with a WARN, and the returned shutdown is safe to call
// even when nothing could be installed.
func TestInstallSkipsBrokenDestinations(t *testing.T) {
	t.Setenv("WEFT_DB", t.TempDir()+"/broken.db")
	// http to a non-loopback host without Insecure() cannot build; the
	// only destination failing leaves nothing installed, but Install
	// still returns rather than panicking or exiting.
	shutdown := Install(OTLP("http://collector.invalid:4318"))
	shutdown() // safe even with nothing installed
}
