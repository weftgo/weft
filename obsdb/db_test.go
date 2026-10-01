package obsdb

import (
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
