package obsdb_test

import (
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// The severity names OTLP's ranges give, and the filter spellings
// ParseSeverity reads.
func TestSeverity(t *testing.T) {
	for n, want := range map[int]string{0: "", 1: "TRACE", 4: "TRACE4", 5: "DEBUG", 9: "INFO", 13: "WARN", 14: "WARN2", 17: "ERROR", 21: "FATAL", 24: "FATAL4", 25: "", -1: ""} {
		if got := obsdb.SeverityText(n); got != want {
			t.Errorf("SeverityText(%d) = %q, want %q", n, got, want)
		}
	}
	for s, want := range map[string]int{"trace": 1, "debug": 5, "info": 9, "INFO": 9, "warn": 13, "warning": 13, "WARN2": 14, "error": 17, "fatal": 21, "FATAL4": 24, "13": 13, " 9 ": 9} {
		if got, ok := obsdb.ParseSeverity(s); !ok || got != want {
			t.Errorf("ParseSeverity(%q) = %d, %v; want %d", s, got, ok, want)
		}
	}
	for _, s := range []string{"", "loud", "0", "25", "WARN1", "WARN5", "INFOx", "-3"} {
		if n, ok := obsdb.ParseSeverity(s); ok {
			t.Errorf("ParseSeverity(%q) = %d, accepted", s, n)
		}
	}
	// Every name round-trips.
	for n := 1; n <= 24; n++ {
		if got, ok := obsdb.ParseSeverity(obsdb.SeverityText(n)); !ok || got != n {
			t.Errorf("ParseSeverity(SeverityText(%d)) = %d, %v", n, got, ok)
		}
	}
}
