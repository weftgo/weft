package reqlog

import (
	"testing"
	"time"
)

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"": false, "abc-1.2_3:4": true, "has space": false, "a\nb": false,
		string(make([]byte, maxID+1)): false,
	} {
		if validID(id) != want {
			t.Errorf("validID(%q) = %v", id, !want)
		}
	}
}

// The ingest refusal limit: one line a window per key, the next line
// counting what it suppressed.
func TestAllowWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := New()
	l.now = func() time.Time { return now }
	if ok, n := l.allow("k"); !ok || n != 0 {
		t.Fatalf("first: %v %d", ok, n)
	}
	for range 3 {
		if ok, _ := l.allow("k"); ok {
			t.Fatal("a repeat inside the window was allowed")
		}
	}
	if ok, _ := l.allow("other"); !ok {
		t.Error("another key was limited")
	}
	now = now.Add(limitWindow)
	if ok, n := l.allow("k"); !ok || n != 3 {
		t.Errorf("after the window: %v suppressed=%d, want true 3", ok, n)
	}
}
