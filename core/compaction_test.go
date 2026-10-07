package core

import (
	"encoding/json"
	"testing"
)

// compactionRange is ADR 0028 §8's range: the longest common prefix,
// then the longest common suffix not overlapping it, by wire bytes.
func TestCompactionRange(t *testing.T) {
	u := func(s string) Message { return User(s) }
	a := func(s string) Message { return Message{Role: RoleAssistant, Content: []Part{TextPart{Text: s}}} }
	for _, c := range []struct {
		name     string
		tr, sent []Message
		ok       bool
		from, to int
		body     []string
	}{
		{"equal", []Message{u("1"), a("2")}, []Message{u("1"), a("2")}, false, 0, 0, nil},
		{"both empty", nil, nil, false, 0, 0, nil},
		{"middle replaced", []Message{u("1"), a("2"), u("3"), a("4")}, []Message{u("1"), u("s"), a("4")}, true, 1, 3, []string{"s"}},
		{"worked example", []Message{u("1"), a("2"), u("3")}, []Message{u("1"), u("s")}, true, 1, 3, []string{"s"}},
		{"head dropped", []Message{u("1"), a("2"), u("3")}, []Message{a("2"), u("3")}, true, 0, 1, nil},
		{"tail dropped", []Message{u("1"), a("2"), u("3")}, []Message{u("1")}, true, 1, 3, nil},
		{"inserted", []Message{u("1"), a("2")}, []Message{u("1"), u("x"), a("2")}, true, 1, 1, []string{"x"}},
		{"appended", []Message{u("1")}, []Message{u("1"), u("x")}, true, 1, 1, []string{"x"}},
		{"all replaced", []Message{u("1"), a("2")}, []Message{u("s")}, true, 0, 2, []string{"s"}},
		{"emptied", []Message{u("1"), a("2")}, nil, true, 0, 2, nil},
		// The suffix never overlaps the prefix: [x x] → [x] is one
		// message dropped at the end, not a negative range.
		{"repeated", []Message{u("x"), u("x")}, []Message{u("x")}, true, 1, 2, nil},
		{"repeated grow", []Message{u("x")}, []Message{u("x"), u("x")}, true, 1, 1, []string{"x"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			from, to, body, ok := compactionRange(c.tr, c.sent)
			if ok != c.ok || from != c.from || to != c.to || len(body) != len(c.body) {
				t.Fatalf("range = [%d, %d) body %d ok %v, want [%d, %d) body %d ok %v", from, to, len(body), ok, c.from, c.to, len(c.body), c.ok)
			}
			for i, m := range body {
				if m.Text() != c.body[i] {
					t.Errorf("body[%d] = %q, want %q", i, m.Text(), c.body[i])
				}
			}
		})
	}
}

// A message whose tool-call arguments are not JSON compares by its
// quoted encoding, so a transcript holding one still compares equal to
// an untouched request (no spurious view).
func TestCompactionRangeInvalidArgs(t *testing.T) {
	bad := Message{Role: RoleAssistant, Content: []Part{ToolCallPart{ID: "c", Name: "t", Args: json.RawMessage(`{"cut`)}}}
	tr := []Message{User("q"), bad}
	sent := []Message{User("q"), bad}
	if _, _, _, ok := compactionRange(tr, sent); ok {
		t.Error("equal transcripts with invalid args produced a range")
	}
}

// The equality is decided structurally first: a message and its deep
// copy (what a PrepareStep that changed nothing hands back) compare
// equal without being encoded.
func TestSameMessageSkipsEncodingForCopies(t *testing.T) {
	m := Message{Role: RoleAssistant, Content: []Part{
		TextPart{Text: "hi"},
		ToolCallPart{ID: "c", Name: "t", Args: json.RawMessage(`{"a":1}`)},
	}}
	cp := cloneMessages([]Message{m})[0]
	allocs := testing.AllocsPerRun(100, func() {
		if !sameMessage(m, cp) {
			t.Fatal("a deep copy compared unequal")
		}
	})
	if allocs > 2 {
		t.Errorf("sameMessage on a copy allocated %.0f times; it encoded", allocs)
	}
}
