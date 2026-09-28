package thread_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

func timeUTC() time.Time { return time.Now().UTC() }

// summaryRecorder is a model that records every request it is given
// and always replies with one text — the summarizer the compaction
// tests watch.
type summaryRecorder struct {
	mu       sync.Mutex
	requests []weft.ModelRequest
	reply    string
}

func (m *summaryRecorder) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	reply := m.reply
	m.mu.Unlock()
	return func(yield func(weft.ModelEvent, error) bool) {
		yield(weft.ModelTextDelta{Text: reply}, nil)
		yield(weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 7, OutputTokens: 3}}, nil)
	}
}

func (m *summaryRecorder) saw() []weft.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]weft.ModelRequest(nil), m.requests...)
}

func TestCompactionEndToEnd(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "the summary text"}
		s, _ := thread.Create(ctx, st, weft.New(rec))

		// Three turns of ~7.5k estimated tokens each: the tail (two
		// of them) fits the keep window, all three do not — the cut
		// lands at the second user message.
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		s = reopenWith(t, ctx, st, s, weft.New(rec))
		before := s.Entries()

		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		got := s.Context()
		if len(got) != 3 {
			t.Fatalf("Context = %d messages, want 3 (summary + two kept)", len(got))
		}
		if want := "<weft-summary>\nthe summary text\n</weft-summary>"; got[0].Text() != want {
			t.Errorf("summary message = %q, want %q", got[0].Text(), want)
		}
		if !strings.HasPrefix(got[1].Text(), "b") || !strings.HasPrefix(got[2].Text(), "c") {
			t.Errorf("kept messages = %q, %q; want the b and c turns", got[1].Text()[:1], got[2].Text()[:1])
		}

		// Nothing is ever deleted: one entry more (the compaction),
		// every original entry still present.
		after := s.Entries()
		if len(after) != len(before)+1 {
			t.Errorf("Entries = %d after Compact, want %d+1", len(after), len(before))
		}
		ids := map[string]bool{}
		for _, e := range after {
			if me, ok := e.(thread.MessageEntry); ok {
				ids[me.ID] = true
			}
		}
		for _, e := range before {
			if me, ok := e.(thread.MessageEntry); ok && !ids[me.ID] {
				t.Errorf("message entry %q did not survive the compaction", me.ID)
			}
		}

		// The compaction entry is the ledger the ADR promises.
		var ce *thread.CompactionEntry
		for _, e := range after {
			if x, ok := e.(thread.CompactionEntry); ok {
				ce = &x
			}
		}
		if ce == nil {
			t.Fatal("no compaction entry")
		}
		if ce.Summary != "the summary text" || ce.Reason != thread.ReasonManual || ce.RangeHash == "" {
			t.Errorf("compaction entry = %+v", ce)
		}
		if ce.SummarizerUsage.OutputTokens != 3 {
			t.Errorf("summarizer usage = %+v", ce.SummarizerUsage)
		}

		// Durable: a reopen reads the same context.
		again := reopen(t, ctx, st, s)
		if fmt.Sprint(again.Context()) != fmt.Sprint(s.Context()) {
			t.Error("reopen changed the compacted context")
		}
	})
}

func TestCompactionIterative(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "summary"}
		s, _ := thread.Create(ctx, st, weft.New(rec))
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
			strings.Repeat("d", 30_000),
		)
		s = reopenWith(t, ctx, st, s, weft.New(rec))
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		reqs := rec.saw()
		if len(reqs) != 2 {
			t.Fatalf("summarizer called %d times, want 2", len(reqs))
		}
		// Both calls carry the skeleton…
		for i, req := range reqs {
			if req.System != skeletonFor(t) {
				t.Errorf("call %d system prompt is not the skeleton", i)
			}
			if req.Params.MaxTokens == nil || *req.Params.MaxTokens <= 0 {
				t.Errorf("call %d has no output cap", i)
			}
		}
		// …and the second feeds the first summary as its first message
		// (iterative compaction), behind the same marker.
		second := reqs[1].Messages
		if len(second) == 0 || second[0].Text() != "<weft-summary>\nsummary\n</weft-summary>" {
			t.Errorf("second summary input = %+v, want the previous summary first", second[0])
		}

		// The context after two compactions shows only the latest
		// summary plus its kept tail.
		got := s.Context()
		if len(got) != 3 || !strings.HasPrefix(got[2].Text(), "d") {
			t.Errorf("Context = %d messages; want summary + two kept ending in d", len(got))
		}
	})
}

func skeletonFor(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "compaction", "summary-prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCompactionUndo(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "summary"}
		s, _ := thread.Create(ctx, st, weft.New(rec))
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		s = reopenWith(t, ctx, st, s, weft.New(rec))
		prior := s.Context()

		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(s.Context()) == fmt.Sprint(prior) {
			t.Fatal("Compact did not change the context")
		}
		if err := s.Uncompact(ctx); err != nil {
			t.Fatalf("Uncompact: %v", err)
		}
		if got, want := fmt.Sprint(s.Context()), fmt.Sprint(prior); got != want {
			t.Errorf("context after undo differs:\n got %s\nwant %s", got[:min(80, len(got))], want[:min(80, len(want))])
		}
		// Undo is a branch: the compaction stays in the file.
		if n := len(s.Entries()); n != 5 { // 3 messages + compaction + the undo leaf
			t.Errorf("Entries = %d, want 5", n)
		}
		// Undo with no compaction on the path is loud.
		fresh, _ := thread.Create(ctx, st, weft.New(rec))
		if err := fresh.Uncompact(ctx); err == nil {
			t.Error("Uncompact with no compaction: no error")
		}
	})
}

func TestCompactionNoWindow(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		agent := weft.New(wefttest.Script(wefttest.Say("r1"), wefttest.Say("r2")),
			weft.Logger(logger))
		s, _ := thread.Create(ctx, st, agent)
		for i := 0; i < 2; i++ {
			turn, err := s.Send(ctx, weft.User("q"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.Wait(); err != nil {
				t.Fatal(err)
			}
		}
		// Both turns ran; the two trigger sites each found no window,
		// and exactly one warning was logged — never one per turn.
		if n := strings.Count(buf.String(), "no context window"); n != 1 {
			t.Errorf("no-window warnings = %d, want 1: %s", n, buf.String())
		}
		for _, e := range s.Entries() {
			if _, ok := e.(thread.CompactionEntry); ok {
				t.Error("compaction ran without a window")
			}
		}
	})
}

func TestCompactionStripsSignedReasoning(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "summary"}
		s, _ := thread.Create(ctx, st, weft.New(rec))

		// One big old prompt (~19k tokens) so the cut lands after it,
		// a signed-reasoning assistant and a small prompt in the kept
		// tail, and — after the compaction — an unsigned-reasoning
		// assistant, whose reasoning must survive: it was recorded
		// over a prefix that already held the summary.
		signed := weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "chain of thought", Signature: "sig1"},
			weft.TextPart{Text: "kept reply"},
		}}
		unsigned := weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "later thinking"},
			weft.TextPart{Text: "after the compaction"},
		}}
		parent := s.Leaf()
		entries := []thread.Entry{
			thread.MessageEntry{ID: "e_old", ParentID: parent, Created: timeUTC(), Message: weft.User(strings.Repeat("a", 84_000))},
			thread.MessageEntry{ID: "e_sig", ParentID: "e_old", Created: timeUTC(), Message: signed},
			thread.MessageEntry{ID: "e_tail", ParentID: "e_sig", Created: timeUTC(), Message: weft.User("small tail")},
		}
		if err := st.Append(ctx, s.ID(), entries...); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, weft.New(rec))
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, s.ID(), thread.MessageEntry{
			ID: "e_post", ParentID: s.Leaf(), Created: timeUTC(), Message: unsigned,
		}); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, weft.New(rec))
		got := s.Context()
		if len(got) != 4 { // summary, stripped assistant, tail prompt, unsigned assistant
			t.Fatalf("Context = %d messages, want 4", len(got))
		}
		if hasSigned(got[1]) {
			t.Error("signed reasoning before the compaction reached the context")
		}
		if got[1].Text() != "kept reply" {
			t.Errorf("stripping removed the assistant's text too: %q", got[1].Text())
		}
		if !hasUnsigned(got[3]) {
			t.Errorf("post-compaction unsigned reasoning was stripped: %+v", got[3].Content)
		}
		// The file keeps everything it held.
		for _, e := range s.Entries() {
			if me, ok := e.(thread.MessageEntry); ok && me.ID == "e_sig" {
				if !hasSignedMsg(me.Message) {
					t.Error("storage lost the signed reasoning")
				}
			}
		}
	})
}

func hasUnsigned(m weft.Message) bool {
	for _, p := range m.Content {
		if r, ok := p.(weft.ReasoningPart); ok && r.Signature == "" && r.Text != "" {
			return true
		}
	}
	return false
}

func hasSigned(m weft.Message) bool { return hasSignedMsg(m) }
func hasSignedMsg(m weft.Message) bool {
	for _, p := range m.Content {
		if r, ok := p.(weft.ReasoningPart); ok && r.Signature != "" {
			return true
		}
	}
	return false
}

func TestCompactionSummarizerView(t *testing.T) {
	// The range the summarizer sees is the serialized transcript, not
	// the model's context: tool results capped, signed reasoning
	// dropped, files reduced to names.
	rec := &summaryRecorder{reply: "summary"}
	ctx := context.Background()
	mem := thread.Memory()
	s, _ := thread.Create(ctx, mem, weft.New(rec))
	ranged := []weft.Message{
		weft.User(strings.Repeat("q", 300)),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "secret chain", Signature: "s"},
			weft.ToolCallPart{ID: "c1", Name: "read", Args: json.RawMessage(`{}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "c1", Name: "read", Content: strings.Repeat("r", 9_000)},
		}},
		{Role: weft.RoleUser, Content: []weft.Part{
			weft.FilePart{MediaType: "text/plain", URL: "file:///docs/spec.md", Data: []byte("xxxx")},
		}},
	}
	parent := s.Leaf()
	var entries []thread.Entry
	for i, m := range ranged {
		id := fmt.Sprintf("e_v%d", i)
		entries = append(entries, thread.MessageEntry{ID: id, ParentID: parent, Created: timeUTC(), Message: m})
		parent = id
	}
	if err := mem.Append(ctx, s.ID(), entries...); err != nil {
		t.Fatal(err)
	}
	open, err := thread.Open(ctx, mem, s.ID(), weft.New(rec))
	if err != nil {
		t.Fatal(err)
	}
	// Make the tail overflow so there is something to summarize.
	msgs(t, ctx, mem, open, strings.Repeat("z", 30_000), strings.Repeat("z", 30_000), strings.Repeat("z", 30_000))
	open = reopenWith(t, ctx, mem, open, weft.New(rec))
	if err := open.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	reqs := rec.saw()
	if len(reqs) != 1 {
		t.Fatalf("summarizer calls = %d", len(reqs))
	}
	view := reqs[0].Messages
	// The tool result arrives capped at 2,000 characters with a
	// visible marker.
	foundCapped, foundSigned, foundFile := false, false, false
	var files []string
	for _, m := range view {
		for _, p := range m.Content {
			switch p := p.(type) {
			case weft.ToolResultPart:
				if len([]rune(p.Content)) > 2100 {
					t.Errorf("tool result not capped: %d runes", len([]rune(p.Content)))
				}
				if strings.Contains(p.Content, "[truncated]") {
					foundCapped = true
				}
			case weft.ReasoningPart:
				if p.Signature != "" {
					foundSigned = true
				}
			case weft.TextPart:
				if strings.Contains(p.Text, "[file: file:///docs/spec.md]") {
					foundFile = true
				}
			case weft.FilePart:
				t.Error("file part reached the summarizer whole")
			}
		}
	}
	if !foundCapped {
		t.Error("no capped tool result in the summarizer view")
	}
	if foundSigned {
		t.Error("signed reasoning reached the summarizer")
	}
	if !foundFile {
		t.Error("the file's name did not reach the summarizer")
	}
	_ = files
}

func TestApplyCompactionValidates(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	if err := s.ApplyCompaction(ctx, nil); err == nil {
		t.Error("ApplyCompaction(nil): no error")
	}
	if err := s.ApplyCompaction(ctx, &thread.Compaction{Summary: "s", FirstKept: "e_missing"}); err == nil {
		t.Error("ApplyCompaction with unknown FirstKept: no error")
	}
	// A small session has nothing to compact.
	err := s.Compact(ctx)
	if err == nil || !strings.Contains(err.Error(), "nothing to compact") {
		t.Errorf("Compact on an empty session: err = %v, want the nothing-to-compact error", err)
	}
}

// The model-visible compaction bytes are golden-pinned (ADR 0020 §2:
// the marker, the skeleton prompt, and a whole compacted context
// change only with an ADR).
func TestCompactionGoldens(t *testing.T) {
	// The marker message, byte for byte.
	marker, err := json.Marshal(weft.User("<weft-summary>\nSUMMARY TEXT\n</weft-summary>"))
	if err != nil {
		t.Fatal(err)
	}
	wantGolden(t, "summary-marker.txt", append(marker, '\n'))

	// A whole compacted context: the summary first behind the marker,
	// then the kept tail from the compaction's first kept entry.
	st := thread.Memory()
	ctx := context.Background()
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_1", Created: timeUTC(), Message: weft.User("hello")},
		thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: timeUTC(), Message: weft.Assistant("hi there")},
	); err != nil {
		t.Fatal(err)
	}
	open, err := thread.Open(ctx, st, s.ID(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	if err := open.ApplyCompaction(ctx, &thread.Compaction{
		Summary: "The work is done.", FirstKept: "e_2", TokensBefore: 42, Reason: thread.ReasonManual,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(open.Context(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wantGolden(t, "compacted-context.txt", append(got, '\n'))
}

func wantGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "compaction", name))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s changed:\n want %q\n got  %q", name, want, got)
	}
}

// recordingModel is the example's deterministic summarizer.
type recordingModel struct{ reply string }

func (m *recordingModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		yield(weft.ModelTextDelta{Text: m.reply}, nil)
		yield(weft.ModelFinish{Reason: weft.StopEndTurn}, nil)
	}
}

// A summarizer error leaves the session unchanged: no entry, the same
// context (ADR 0020 §4: a failed compaction never loses entries — and
// never gains one either).
func TestCompactionSummarizerFailure(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		fail := &failingModel{}
		agent := weft.New(fail)
		s, _ := thread.Create(ctx, st, agent)
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		s = reopenWith(t, ctx, st, s, agent)
		beforeCtx := fmt.Sprint(s.Context())
		beforeEntries := len(s.Entries())
		if err := s.Compact(ctx); err == nil {
			t.Fatal("Compact with a failing summarizer: no error")
		}
		if got := fmt.Sprint(s.Context()); got != beforeCtx {
			t.Error("the context changed on a failed compaction")
		}
		if n := len(s.Entries()); n != beforeEntries {
			t.Errorf("Entries = %d after a failed compaction, want %d", n, beforeEntries)
		}
	})
}

type failingModel struct{}

func (m *failingModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		yield(nil, errors.New("summarizer down"))
	}
}

// SummarizeLeft on a branch with nothing after the branch point is
// loud: there is nothing to summarize.
func TestBranchSummarizeLeftNoDivergence(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "x"}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)
	if err := st.Append(ctx, s.ID(), thread.MessageEntry{
		ID: "e_only", Created: timeUTC(), Message: weft.User("only line"),
	}); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent)
	if err := s.Branch(ctx, "e_only", thread.SummarizeLeft()); err == nil {
		t.Error("SummarizeLeft to the leaf: no error")
	}
	if n := len(s.Entries()); n != 1 {
		t.Errorf("Entries = %d after the rejected SummarizeLeft, want 1", n)
	}
}
