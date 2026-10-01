package rules

import (
	"testing"
	"time"
)

func TestLimitOf(t *testing.T) {
	for in, want := range map[int]int{0: 50, -1: 50, -500: 50, 1: 1, 50: 50, 500: 500, 501: 500, 1 << 40: 500} {
		if got := LimitOf(in); got != want {
			t.Errorf("LimitOf(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestMetaMatch(t *testing.T) {
	meta := map[string]string{"env": "prod", "empty": ""}
	for _, tc := range []struct {
		want map[string]string
		ok   bool
	}{
		{nil, true},
		{map[string]string{"env": "prod"}, true},
		{map[string]string{"env": "prod", "empty": ""}, true},
		{map[string]string{"env": "dev"}, false},
		{map[string]string{"absent": ""}, false}, // a wanted key must be present
		{map[string]string{"env": "prod", "team": "a"}, false},
	} {
		if got := MetaMatch(meta, tc.want); got != tc.ok {
			t.Errorf("MetaMatch(%v) = %v, want %v", tc.want, got, tc.ok)
		}
	}
	if MetaMatch(nil, map[string]string{"k": ""}) {
		t.Error("a nil meta matched a wanted key")
	}
}

func TestTitleRules(t *testing.T) {
	lines := SplitLines([]byte(`{"type":"info","id":"e_1","title":"first"}
{"type":"message","id":"e_2","title":"not an info entry"}
not json
{"type":"info","id":"e_3","title":"Second Title"}
{"type":"info","id":"e_4","meta":{"k":"v"}}
{"type":"info","id":"e_5","title":"torn`))
	if got := TitleOf(lines); got != "Second Title" {
		t.Errorf("TitleOf = %q, want the last non-empty info title", got)
	}
	if TitleOf(nil) != "" {
		t.Error("TitleOf(nil) is not empty")
	}
	if !TitleMatches("Second Title", "second") || !TitleMatches("ÉCOLE", "école") || TitleMatches("first", "second") {
		t.Error("TitleMatches is not a case-folding substring match")
	}
}

func TestAfterCursor(t *testing.T) {
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		created  time.Time
		id       string
		before   time.Time
		beforeID string
		want     bool
	}{
		{"no cursor", at, "s_b", time.Time{}, "", true},
		{"no cursor ignores the id", at, "s_b", time.Time{}, "s_a", true},
		{"older", at.Add(-time.Second), "s_z", at, "s_a", true},
		{"newer", at.Add(time.Second), "s_a", at, "s_z", false},
		{"tie, lower id", at, "s_a", at, "s_b", true},
		{"tie, the cursor itself", at, "s_b", at, "s_b", false},
		{"tie, higher id", at, "s_c", at, "s_b", false},
		{"tie, no id in the cursor", at, "s_a", at, "", false},
		{"same instant, another zone", at.In(time.FixedZone("x", 3600)), "s_a", at, "s_b", true},
	} {
		if got := AfterCursor(tc.created, tc.id, tc.before, tc.beforeID); got != tc.want {
			t.Errorf("%s: AfterCursor = %v, want %v", tc.name, got, tc.want)
		}
	}
	if CompareNewestFirst(at, "s_a", at, "s_b") <= 0 || CompareNewestFirst(at.Add(time.Second), "s_a", at, "s_b") >= 0 {
		t.Error("CompareNewestFirst is not newest first, ties by id descending")
	}
}

func TestLineRules(t *testing.T) {
	for _, tc := range []struct {
		in       string
		lines    int
		torn     bool
		complete int
	}{
		{"", 0, false, 0},
		{"a\n", 1, false, 2},
		{"a\nb\n", 2, false, 4},
		{"a\nb", 1, true, 2},
		{"torn", 0, true, 0},
		{"\n\n", 2, false, 2},
	} {
		b := []byte(tc.in)
		if got := len(SplitLines(b)); got != tc.lines {
			t.Errorf("SplitLines(%q) = %d lines, want %d", tc.in, got, tc.lines)
		}
		if got := Torn(b); got != tc.torn {
			t.Errorf("Torn(%q) = %v, want %v", tc.in, got, tc.torn)
		}
		if got := CompleteLen(b); got != tc.complete {
			t.Errorf("CompleteLen(%q) = %d, want %d", tc.in, got, tc.complete)
		}
	}
	if m := CloneMeta(map[string]string{}); m != nil {
		t.Error("CloneMeta of an empty map is not nil")
	}
	src := map[string]string{"k": "v"}
	if c := CloneMeta(src); c["k"] != "v" || func() bool { c["k"] = "x"; return src["k"] != "v" }() {
		t.Error("CloneMeta aliases its input")
	}
}
