package thread

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/weftgo/weft"
)

// bigText returns text whose wire bytes cost about tokens estimated
// tokens (four bytes a token), so cut points land where the test wants
// them.
func bigText(tokens int64) string {
	b := make([]byte, tokens*4)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

// pathOf builds a root→leaf path of chained entries from shapes: a
// weft.Message becomes a MessageEntry; a string becomes a bookkeeping
// CustomEntry (context-invisible state).
func pathOf(shapes ...any) []Entry {
	path := make([]Entry, len(shapes))
	parent := ""
	for i, sh := range shapes {
		id := fmt.Sprintf("e_%02d", i)
		switch sh := sh.(type) {
		case weft.Message:
			path[i] = MessageEntry{ID: id, ParentID: parent, Message: sh}
		case string:
			path[i] = CustomEntry{ID: id, ParentID: parent, Kind: sh}
		}
		parent = id
	}
	return path
}

func user(text string) weft.Message { return weft.User(text) }
func assist(text string) weft.Message {
	return weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{weft.TextPart{Text: text}}}
}
func assistCall(id, name string) weft.Message {
	return weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{
		weft.ToolCallPart{ID: id, Name: name, Args: []byte("{}")},
	}}
}
func toolResult(id string) weft.Message {
	return weft.Message{Role: weft.RoleTool, Content: []weft.Part{
		weft.ToolResultPart{CallID: id, Name: "n", Content: "ok"},
	}}
}

func TestCutPointTable(t *testing.T) {
	rows := []struct {
		name string
		path []Entry
		keep int64
		want int // -1: nothing to compact
	}{
		{
			name: "small path fits, nothing to compact",
			path: pathOf(user("hi"), assist("there")),
			keep: 10_000,
			want: -1,
		},
		{
			name: "overflow cuts at the user boundary",
			path: pathOf(user(bigText(100)), assist(bigText(100)), user(bigText(100)), assist(bigText(100))),
			keep: 250, // the last big pair fits, the one before does not
			want: 2,
		},
		{
			name: "a cut landing on a call assistant keeps its results with it",
			path: pathOf(
				user(bigText(100)), assistCall("c1", "t"), toolResult("c1"),
				user(bigText(100)), assistCall("c2", "t"), toolResult("c2"),
				user("tail"), assist("end"),
			),
			keep: 130, // the raw cut lands on the second call's assistant: valid, its results stay kept
			want: 4,
		},
		{
			name: "a call is never separated from its result",
			path: pathOf(
				user(bigText(100)), assistCall("c1", "t"), toolResult("c1"),
				user("tail"), assist("end"),
			),
			keep: 75, // the raw cut lands on the result; the walk refuses it
			want: 3,
		},
		{
			name: "bookkeeping entries weigh nothing and are no boundary",
			path: pathOf(user(bigText(100)), "state", "state", user(bigText(100)), assist("end")),
			keep: 105,
			want: 4,
		},
		{
			name: "split turn: the cut lands at an assistant inside one turn",
			path: pathOf(
				user(bigText(100)),
				assist(bigText(100)), toolResult("c1"), // one huge turn
				user("tail"), assist("end"),
			),
			keep: 200, // the turn without its prompt fits, with it it does not: split at the assistant
			want: 1,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got := cutIndex(row.path, row.keep)
			if got != row.want {
				where := "nothing"
				if got >= 0 && got < len(row.path) {
					if m, ok := row.path[got].(MessageEntry); ok {
						where = fmt.Sprintf("index %d (%s)", got, m.Message.Role)
					} else {
						where = fmt.Sprintf("index %d (bookkeeping)", got)
					}
				}
				t.Errorf("cutIndex = %s, want index %d", where, row.want)
			}
		})
	}
}

// The trigger's arithmetic (ADR 0020 §2): the provider-reported input
// of the last step plus the estimated messages since, against window −
// Reserve. The reported number is the signal; only the delta is
// estimated — there is no path where an estimate replaces the
// measurement.
func TestTriggerCondition(t *testing.T) {
	// The production condition, not a mirror of it: window − reserve
	// is the line, measured plus estimated crosses it strictly.
	base := TriggerInput{Window: 100_000, Reserve: 16_384}
	cases := []struct {
		lastInput, estimated int64
		want                 bool
	}{
		{50_000, 10_000, false}, // 60k of 83.6k headroom
		{83_600, 100, true},     // past the line
		{83_616, 1, true},       // exactly past the line
		{83_616, 0, false},      // exactly on the line
		{0, 90_000, true},       // a delta alone can cross
	}
	for _, c := range cases {
		in := base
		in.LastInput, in.Estimated = c.lastInput, c.estimated
		if got := firesAt(in); got != c.want {
			t.Errorf("firesAt(%+v) = %v, want %v", in, got, c.want)
		}
	}
}

// A compaction's summary weighs what it costs in the window: the cut
// walk counts it, so the kept tail is really about KeepRecent.
func TestCutCountsTheSummaryWeight(t *testing.T) {
	path := []Entry{
		MessageEntry{ID: "e_m0", Message: user(bigText(100))},
		MessageEntry{ID: "e_m1", ParentID: "e_m0", Message: user("small")},
		CompactionEntry{ID: "e_c", ParentID: "e_m1", FirstKept: "e_m0", Summary: bigText(60)},
		MessageEntry{ID: "e_u1", ParentID: "e_c", Message: user("small")},
		MessageEntry{ID: "e_a", ParentID: "e_u1", Message: assist("end")},
	}
	// With the summary weighing ~60: the tail (assistant, user, the
	// summary) overflows keep 80 at the compaction entry, and the cut
	// lands on the user past it. Without the summary's weight the
	// same walk fits the whole post-compaction tail and overflows at
	// the big message instead — the cut lands on the small user under
	// the compaction. Same path, different cut: the summary counted.
	if got := cutIndex(path, 80); got != 3 {
		t.Errorf("cutIndex = %d, want 3 (the summary's weight pushes the cut past the compaction)", got)
	}
	if c, ok := path[2].(CompactionEntry); ok {
		c.Summary = ""
		path[2] = c
	}
	if got := cutIndex(path, 80); got != 1 {
		t.Errorf("cutIndex with an empty summary = %d, want 1", got)
	}
}

func TestCutSplitWhenEveryEntryOverflows(t *testing.T) {
	// Messages each larger than the keep window: the smallest valid
	// tail is kept — one message — and the rest is summarized.
	path := pathOf(user(bigText(30_000)), user(bigText(30_000)), user(bigText(30_000)))
	if got := cutIndex(path, 20_000); got != 2 {
		t.Errorf("cutIndex = %d, want 2 (the last message alone)", got)
	}
}

// TestCutProperty runs random transcripts and keep windows through
// cutIndex and checks the two invariants that make a cut legal: the
// kept side starts at a user or assistant message, and the summarized
// side never ends on an assistant message with tool calls — a call is
// never separated from its results.
func TestCutProperty(t *testing.T) {
	seedRand := func(n int) *rand.Rand { return rand.New(rand.NewSource(int64(n))) }
	for seed := 0; seed < 200; seed++ {
		rng := seedRand(seed)
		var shapes []any
		n := 1 + rng.Intn(12)
		openCalls := 0
		for i := 0; i < n; i++ {
			switch rng.Intn(6) {
			case 0:
				shapes = append(shapes, user(bigText(int64(1+rng.Intn(40)))))
			case 1:
				shapes = append(shapes, assist(bigText(int64(1+rng.Intn(40)))))
			case 2:
				id := fmt.Sprintf("c%d", i)
				shapes = append(shapes, assistCall(id, "t"))
				openCalls++
			case 3:
				if openCalls > 0 {
					openCalls--
					shapes = append(shapes, toolResult(fmt.Sprintf("c%d", rng.Intn(i+1))))
				} else {
					shapes = append(shapes, assist("plain"))
				}
			case 4:
				shapes = append(shapes, "state")
			case 5:
				shapes = append(shapes, user("tiny"))
			}
		}
		path := pathOf(shapes...)
		for _, keep := range []int64{1, 5, 25, 60, 125, 250, 1000} {
			cut := cutIndex(path, keep)
			if cut == -1 {
				continue
			}
			first, ok := path[cut].(MessageEntry)
			if !ok || (first.Message.Role != weft.RoleUser && first.Message.Role != weft.RoleAssistant) {
				t.Fatalf("seed %d keep %d: kept side starts at %T", seed, keep, path[cut])
			}
			if cut > 0 {
				if prev, ok := path[cut-1].(MessageEntry); ok && prev.Message.Role == weft.RoleAssistant {
					for _, p := range prev.Message.Content {
						if _, isCall := p.(weft.ToolCallPart); isCall {
							t.Fatalf("seed %d keep %d: cut separates a call from its results", seed, keep)
						}
					}
				}
			}
		}
	}
}
