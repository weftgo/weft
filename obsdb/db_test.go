package obsdb

import (
	"strings"
	"testing"
	"time"
)

// The four-row status table (S3.2), pinned at every boundary. DeriveStatus
// is what every backend's reads go through, so the boundaries are tested
// here once, in time, not per backend.
func TestDeriveStatusTable(t *testing.T) {
	now := time.Now()
	lastSeen := now.Add(-10 * time.Second)
	stale := now.Add(-InterruptedAfter - time.Second)
	cases := []struct {
		name                 string
		spanFailed, finished bool
		lastSeen             time.Time
		want                 Status
	}{
		{"error span fails", true, false, lastSeen, StatusFailed},
		{"error span outranks finish", true, true, lastSeen, StatusFailed},
		{"run_finish succeeds", false, true, lastSeen, StatusSucceeded},
		{"fresh and open runs", false, false, lastSeen, StatusRunning},
		{"exactly InterruptedAfter still runs", false, false, now.Add(-InterruptedAfter), StatusRunning},
		{"crash with no terminal and stale last-seen", false, false, stale, StatusInterrupted},
		{"stale but finished stays succeeded", false, true, stale, StatusSucceeded},
		{"stale but failed stays failed", true, false, stale, StatusFailed},
		// ended is a run_finish or the stored invoke_agent span (RootEnded):
		// a root whose run_finish was lost is over, not interrupted.
		{"root ended without run_finish, stale, succeeds", false, true, stale, StatusSucceeded},
	}
	for _, tc := range cases {
		if got := DeriveStatus(tc.spanFailed, tc.finished, tc.lastSeen, now); got != tc.want {
			t.Errorf("%s: DeriveStatus = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// InterruptedAfter is the contract: three missed 10 s heartbeats.
func TestInterruptedAfter(t *testing.T) {
	if InterruptedAfter != 30*time.Second {
		t.Fatalf("InterruptedAfter = %v, want 30s", InterruptedAfter)
	}
}

func TestLimitOf(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 50}, {-3, 50}, {1, 1}, {500, 500}, {501, 500}} {
		if got := LimitOf(tc.in); got != tc.want {
			t.Errorf("LimitOf(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// Status values carry the names Studio's API and the arena port read
// (store.Succeeded and friends mapped onto these).
func TestStatusValues(t *testing.T) {
	for v, want := range map[Status]string{
		StatusRunning: "running", StatusSucceeded: "succeeded",
		StatusFailed: "failed", StatusInterrupted: "interrupted",
	} {
		if string(v) != want {
			t.Errorf("Status %q != %q", v, want)
		}
	}
}

// LostRecordsHole: a weft run with spans and no stored record reads
// not_recorded, cause no_logger (the app exported spans only) — never a
// run with a record of any kind, one with no span, a running one or a
// derived run (stock spans carry derived's own badge).
func TestLostRecordsHole(t *testing.T) {
	ended := RunRow{ID: "r", Status: StatusSucceeded, RootEnded: true, Steps: 2}
	for _, c := range []struct {
		name     string
		row      RunRow
		hasSpans bool
		want     Hole
	}{
		{"tracer only", ended, true, HoleNotRecorded},
		{"tracer only, failed", RunRow{Status: StatusFailed, RootEnded: true}, true, HoleNotRecorded},
		{"spans without a root, interrupted", RunRow{Status: StatusInterrupted}, true, HoleNotRecorded},
		{"no span", ended, false, ""},
		{"an event stored", RunRow{Status: StatusSucceeded, RootEnded: true, EventCount: 1}, true, ""},
		{"a messages record stored", RunRow{Status: StatusSucceeded, RootEnded: true, MessageCount: 1}, true, ""},
		{"a request record stored", RunRow{Status: StatusSucceeded, RootEnded: true, RequestCount: 1}, true, ""},
		{"running", RunRow{Status: StatusRunning}, true, ""},
		{"derived", RunRow{Status: StatusSucceeded, RootEnded: true, Derived: true}, true, ""},
	} {
		h, cause := LostRecordsHole(c.row, c.hasSpans)
		if h != c.want {
			t.Errorf("%s: LostRecordsHole = %q, want %q", c.name, h, c.want)
		}
		if h != "" && cause != CauseNoLogger {
			t.Errorf("%s: cause = %q, want no_logger", c.name, cause)
		}
		if h == "" && cause != CauseDefault {
			t.Errorf("%s: no hole but cause %q", c.name, cause)
		}
	}
	reason, fix := HoleNoteFor(HoleNotRecorded, CauseNoLogger)
	if !strings.Contains(reason, "spans only") || !strings.Contains(fix, "weft.LoggerProvider") || !strings.Contains(fix, "otel.Install") {
		t.Errorf("no_logger words = %q / %q, want the spans-only reason and a fix naming weft.LoggerProvider and otel.Install", reason, fix)
	}
}
