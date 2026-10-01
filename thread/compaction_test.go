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
		// Compacting again with nothing new past the kept boundary is
		// the nothing-to-compact refusal, not a re-summary of the whole
		// history: the iterative chain is fed only what it lacks.
		if err := s.Compact(ctx); err == nil || !strings.Contains(err.Error(), "nothing to compact") {
			t.Fatalf("Compact with nothing new: err = %v, want the nothing-to-compact refusal", err)
		}
		// The session grows past the window again, and the second
		// compaction folds the first summary in with only the new
		// messages — the kept boundary onward.
		msgs(t, ctx, st, s,
			strings.Repeat("e", 30_000),
			strings.Repeat("f", 30_000),
			strings.Repeat("g", 30_000),
		)
		s = reopenWith(t, ctx, st, s, weft.New(rec))
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
		for _, m := range second[1:] {
			if txt := m.Text(); strings.Contains(txt, strings.Repeat("a", 100)) || strings.Contains(txt, strings.Repeat("b", 100)) {
				t.Error("the first compaction's summarized range was re-serialized into the second call")
			}
		}

		// The context after two compactions shows only the latest
		// summary plus its kept tail.
		got := s.Context()
		if len(got) != 3 || !strings.HasPrefix(got[2].Text(), "g") {
			t.Errorf("Context = %d messages; want summary + two kept ending in g", len(got))
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
			_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
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
	abandon(t, st, s.ID())
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

// A trim record on top of a summary compaction keeps that summary's
// boundary: the trim layers its stubs over the kept range, and the
// context stays summary + kept (stubbed) — never the whole raw
// history back. The bug: writeTrim's FirstKept named the root, and a
// trim governing the walk resurfaced everything the summary replaced.
func TestTrimAfterCompactionKeepsTheBoundary(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "the summary"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent, thread.ClearOldToolResults(0))
		callPair := []weft.Message{
			{Role: weft.RoleAssistant, Content: []weft.Part{
				weft.ToolCallPart{ID: "c1", Name: "read", Args: json.RawMessage(`{}`)},
			}},
			{Role: weft.RoleTool, Content: []weft.Part{
				weft.ToolResultPart{CallID: "c1", Name: "read", Content: strings.Repeat("r", 500)},
			}},
		}
		entries := []thread.Entry{
			thread.MessageEntry{ID: "e_old", Created: timeUTC(), Message: weft.User(strings.Repeat("a", 120_000))},
			thread.MessageEntry{ID: "e_mid", ParentID: "e_old", Created: timeUTC(), Message: weft.User(strings.Repeat("m", 5_000))},
		}
		parent := "e_mid"
		for i, m := range callPair {
			id := fmt.Sprintf("e_c%d", i)
			entries = append(entries, thread.MessageEntry{ID: id, ParentID: parent, Created: timeUTC(), Message: m})
			parent = id
		}
		entries = append(entries, thread.MessageEntry{ID: "e_kept", ParentID: parent, Created: timeUTC(), Message: weft.User("kept tail")})
		if err := st.Append(ctx, s.ID(), entries...); err != nil {
			t.Fatal(err)
		}
		abandon(t, st, s.ID())
		if again, err := thread.Open(ctx, st, s.ID(), agent, thread.ClearOldToolResults(0)); err != nil {
			t.Fatal(err)
		} else {
			s = again
		}
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		// A trim lands next — keeping the boundary the compaction left,
		// the value writeTrim resolves on the automatic path.
		if err := s.ApplyCompaction(ctx, &thread.Compaction{
			FirstKept: "e_mid", Reason: thread.ReasonTrim, TokensBefore: 1,
			Trim: &thread.TrimRecord{Stubs: []thread.TrimStub{
				{Entry: "e_c1", CallID: "c1", Content: "[cleared tool result read c1]"},
			}},
		}); err != nil {
			t.Fatalf("ApplyCompaction trim: %v", err)
		}
		if again, err := thread.Open(ctx, st, s.ID(), agent, thread.ClearOldToolResults(0)); err != nil {
			t.Fatal(err)
		} else {
			s = again
		}
		got := s.Context()
		// Summary marker, the kept mid message, the kept call pair with
		// its result stubbed (keepLast 0), then the kept tail — never
		// the 120k message the summary replaced.
		if len(got) != 5 {
			t.Fatalf("Context after the trim = %d messages, want 5 (summary, mid, call, stubbed result, kept tail)", len(got))
		}
		if !strings.HasPrefix(got[0].Text(), "<weft-summary>") || !strings.Contains(got[0].Text(), "the summary") {
			t.Errorf("first message = %q, want the summary marker", got[0].Text()[:min(60, len(got[0].Text()))])
		}
		if !strings.HasPrefix(got[1].Text(), strings.Repeat("m", 10)) {
			t.Errorf("kept mid message = %q", got[1].Text()[:min(30, len(got[1].Text()))])
		}
		if got[2].Role != weft.RoleAssistant || len(got[2].Content) == 0 {
			t.Errorf("call message = %+v, want the kept assistant call", got[2])
		}
		res, ok := got[3].Content[0].(weft.ToolResultPart)
		if !ok || res.Content != "[cleared tool result read c1]" {
			t.Errorf("result part = %+v, want the cleared stub", got[3].Content[0])
		}
		if got[4].Text() != "kept tail" {
			t.Errorf("kept message = %q, want the kept tail", got[4].Text())
		}
		for _, m := range got {
			if strings.Contains(m.Text(), strings.Repeat("a", 100)) {
				t.Error("the summarized history resurfaced through the trim")
			}
		}
	})
}

// Iterative compaction summarizes from the previous kept boundary
// (ADR 0020 §1): the second summarizer call carries the previous
// summary first and only the messages past that boundary — never the
// whole history again, an input that would itself outgrow the window.
func TestIterativeRangeStartsAtKeptBoundary(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "summary"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent)
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
			strings.Repeat("d", 30_000),
		)
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		msgs(t, ctx, st, s,
			strings.Repeat("e", 30_000),
			strings.Repeat("f", 30_000),
			strings.Repeat("g", 30_000),
			strings.Repeat("h", 30_000),
		)
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		reqs := rec.saw()
		if len(reqs) != 2 {
			t.Fatalf("summarizer calls = %d, want 2", len(reqs))
		}
		second := reqs[1].Messages
		if len(second) == 0 || !strings.HasPrefix(second[0].Text(), "<weft-summary>") {
			t.Fatalf("second call's first message is not the previous summary: %+v", second[0])
		}
		for _, m := range second {
			if txt := m.Text(); strings.Contains(txt, strings.Repeat("a", 100)) || strings.Contains(txt, strings.Repeat("b", 100)) {
				t.Error("the first compaction's summarized range was re-serialized into the second call")
			}
		}
		sawE, sawD := false, false
		for _, m := range second {
			if strings.Contains(m.Text(), strings.Repeat("e", 100)) {
				sawE = true
			}
			if strings.Contains(m.Text(), strings.Repeat("d", 100)) {
				sawD = true
			}
		}
		if !sawD || !sawE {
			t.Errorf("second range = kept-boundary onward: saw d=%v e=%v, want both", sawD, sawE)
		}
		// And the walk after two compactions shows the latest summary
		// plus its kept tail.
		if got := s.Context(); len(got) != 3 {
			t.Errorf("Context = %d messages, want 3 (summary + two kept)", len(got))
		}
	})
}

// SummarizeLeft across branches: the branch being left is summarized
// back to the COMMON ANCESTOR of the leaf and the target (ADR 0020
// §6) — a target on another branch works, and FromEntry names the
// ancestor, not the target.
func TestSummarizeLeftAcrossBranches(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "what B did"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent)
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_r", Created: timeUTC(), Message: weft.User("root")},
			thread.MessageEntry{ID: "e_a1", ParentID: "e_r", Created: timeUTC(), Message: weft.Assistant("line A")},
			thread.MessageEntry{ID: "e_a2", ParentID: "e_a1", Created: timeUTC(), Message: weft.Assistant("line A end")},
		); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Branch(ctx, "e_r"); err != nil { // grow branch B off the root
			t.Fatal(err)
		}
		if err := st.Append(ctx, s.ID(), thread.MessageEntry{
			ID: "e_b1", ParentID: "e_r", Created: timeUTC(), Message: weft.Assistant("line B"),
		}); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Branch(ctx, "e_a2", thread.SummarizeLeft()); err != nil {
			t.Fatalf("cross-branch SummarizeLeft: %v", err)
		}
		// The summarizer saw B's line, not A's.
		if reqs := rec.saw(); len(reqs) != 1 || !strings.Contains(fmt.Sprint(reqs[0].Messages), "line B") {
			t.Fatalf("summarizer input = %+v, want line B only", reqs)
		}
		var bs *thread.BranchSummaryEntry
		for _, e := range s.Entries() {
			if b, ok := e.(thread.BranchSummaryEntry); ok {
				bs = &b
			}
		}
		if bs == nil || bs.FromEntry != "e_r" {
			t.Errorf("branch_summary FromEntry = %+v, want e_r (the common ancestor)", bs)
		}
		// The new line's context: root, A's messages, the B summary.
		got := contextTexts(s)
		want := []string{"root", "line A", "line A end", "<weft-summary>\nwhat B did\n</weft-summary>"}
		if !equalStrings(got, want) {
			t.Errorf("Context = %q, want %q", got, want)
		}
	})
}

// ApplyCompaction's validation: the kept boundary must sit on the
// leaf's path, at or after the previous compaction's, and a
// summary-less compaction must be a trim.
func TestApplyCompactionValidatesTheBoundary(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_1", Created: timeUTC(), Message: weft.User(strings.Repeat("a", 60_000))},
		thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: timeUTC(), Message: weft.User(strings.Repeat("b", 30_000))},
		thread.MessageEntry{ID: "e_3", ParentID: "e_2", Created: timeUTC(), Message: weft.User(strings.Repeat("c", 30_000))},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent)
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	// FirstKept before the previous compaction's boundary.
	if err := s.ApplyCompaction(ctx, &thread.Compaction{Summary: "x", FirstKept: "e_1"}); err == nil {
		t.Error("ApplyCompaction into the summarized range: no error")
	}
	// FirstKept held but off the leaf's path (an abandoned branch).
	if err := s.Branch(ctx, "e_1"); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, s.ID(), thread.MessageEntry{
		ID: "e_side", ParentID: "e_1", Created: timeUTC(), Message: weft.User("side"),
	}); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent)
	// (back on the main line, where e_side is held but off-path)
	if err := s.Branch(ctx, "e_3"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCompaction(ctx, &thread.Compaction{Summary: "x", FirstKept: "e_side"}); err == nil {
		t.Error("ApplyCompaction with an off-path FirstKept: no error")
	}
	// A summary-less compaction that is not a trim.
	if err := s.ApplyCompaction(ctx, &thread.Compaction{FirstKept: "e_3", Reason: thread.ReasonManual}); err == nil {
		t.Error("ApplyCompaction without a summary or a trim reason: no error")
	}
}

// The turn entry's LastInput is the trigger's baseline: the final
// step's reported input plus the estimated tail that report cannot
// cover (the final step's own messages) — one number, so the live
// session and a reopen read the same delta from the same mark.
func TestLastInputCarriesTheUnreportedTail(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		// A turn of two steps: the first answers with a tool call, the
		// second (the final step) answers in text — its reported input
		// covers everything but its own reply.
		agent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}).WithUsage(weft.Usage{InputTokens: 5_000, OutputTokens: 10}),
			wefttest.Say(strings.Repeat("final answer ", 400)).WithUsage(weft.Usage{InputTokens: 6_000, OutputTokens: 20}),
		),
			weft.Tool("echo", "replies", func(ctx context.Context, in struct{}) (string, error) {
				return "ok", nil
			}),
		)
		s, _ := thread.Create(ctx, st, agent)
		turn, err := s.Send(ctx, weft.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
		var te *thread.TurnEntry
		for _, e := range s.Entries() {
			if x, ok := e.(thread.TurnEntry); ok {
				te = &x
			}
		}
		if te == nil {
			t.Fatal("no turn entry")
		}
		// The final step's input was 6,000; the tail it could not
		// report is its own ~1,200-token reply: the baseline carries
		// both, and a reopen reads the same number from the entry.
		if te.LastInput <= 6_000 {
			t.Errorf("LastInput = %d, want more than the final step's reported 6,000", te.LastInput)
		}
		again := reopen(t, ctx, st, s)
		var te2 *thread.TurnEntry
		for _, e := range again.Entries() {
			if x, ok := e.(thread.TurnEntry); ok {
				te2 = &x
			}
		}
		if te2 == nil || te2.LastInput != te.LastInput {
			t.Errorf("reopened LastInput = %+v, want the same %d", te2, te.LastInput)
		}
	})
}

// A trigger whose measurement mark is off the leaf's path — a Branch
// moved the line since — stands down instead of firing on a stale
// number: no threshold compaction runs until a turn on the new path
// measures again (the same rule as a never-measured session).
func TestTriggerStandsDownOnAnOffPathMark(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(
		wefttest.Say("first").WithUsage(weft.Usage{InputTokens: 95_000, OutputTokens: 5}),
		wefttest.Say("the summary"),
		wefttest.Fail(errors.New("model down")),
	))
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.ContextWindow(100_000))
	// Real bulk, so the post-turn trigger has something to compact.
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_b1", Created: timeUTC(), Message: weft.User(strings.Repeat("a", 120_000))},
		thread.MessageEntry{ID: "e_b2", ParentID: "e_b1", Created: timeUTC(), Message: weft.User(strings.Repeat("b", 120_000))},
	); err != nil {
		t.Fatal(err)
	}
	abandon(t, st, s.ID())
	s, err := thread.Open(ctx, st, s.ID(), agent, thread.ContextWindow(100_000))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil { // the post-turn trigger follows the turn
		t.Fatal(err)
	}
	compacted := false
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonThreshold {
			compacted = true
		}
	}
	if !compacted {
		t.Fatal("the first turn's post-turn trigger did not compact — the scenario is not measuring what it should")
	}
	// Undo it and branch below the measured turn: the mark goes off
	// the path while the stale 95k report would still cross the line.
	if err := s.Uncompact(ctx); err != nil {
		t.Fatalf("Uncompact: %v", err)
	}
	if err := s.Branch(ctx, "e_b1"); err != nil {
		t.Fatal(err)
	}
	before := map[string]bool{}
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonThreshold {
			before[c.ID] = true
		}
	}
	// The next turn fails before any step reports: the pre-turn
	// trigger must stand down rather than compact on the stale 95k.
	if turn, err := s.Send(ctx, weft.User("two")); err != nil {
		t.Fatal(err)
	} else if _, err := turn.Wait(); err == nil {
		t.Fatal("the scripted failure did not fail")
	}
	_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonThreshold && !before[c.ID] {
			t.Errorf("threshold compaction %s ran off a mark that is not on the path", c.ID)
		}
	}
}

// A summarizer stream that breaks the Model contract — no ModelFinish,
// or events after it — is an error wrapping weft.ErrModelContract, the
// same enforcement the loop applies; the text that happened to arrive
// is never accepted as a summary.
func TestSummarizerStreamContract(t *testing.T) {
	ctx := context.Background()
	for name, model := range map[string]weft.Model{
		"no finish": &contractModel{events: []weft.ModelEvent{weft.ModelTextDelta{Text: "half a summary"}}},
		"after finish": &contractModel{events: []weft.ModelEvent{
			weft.ModelTextDelta{Text: "a summary"},
			weft.ModelFinish{Reason: weft.StopEndTurn},
			weft.ModelTextDelta{Text: "and more"},
		}},
	} {
		st := thread.Memory()
		s, _ := thread.Create(ctx, st, weft.New(model))
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		s, err := thread.Open(ctx, st, s.ID(), weft.New(model))
		if err != nil {
			t.Fatal(err)
		}
		err = s.Compact(ctx)
		if !errors.Is(err, weft.ErrModelContract) {
			t.Errorf("%s: Compact err = %v, want weft.ErrModelContract", name, err)
		}
		for _, e := range s.Entries() {
			if _, ok := e.(thread.CompactionEntry); ok {
				t.Errorf("%s: a compaction entry landed from a contract-violating stream", name)
			}
		}
	}
}

// contractModel plays a fixed event slice verbatim — the shape a
// contract-violating adapter would produce.
type contractModel struct{ events []weft.ModelEvent }

func (m *contractModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		for _, ev := range m.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// A fork counts the turns it copied: its first Send mints
// <fork>-t<n+1>, matching what Open's recovery would number the same
// file.
func TestForkMintsRunIDsPastTheCopiedTurns(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(wefttest.Say("one"), wefttest.Say("in the fork")))
		s, _ := thread.Create(ctx, st, agent)
		turn, err := s.Send(ctx, weft.User("first"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
		f, err := s.Fork(ctx, s.Leaf())
		if err != nil {
			t.Fatal(err)
		}
		ft, err := f.Send(ctx, weft.User("second"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ft.Wait(); err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%s-t2", f.ID())
		if got := ft.RunID(); got != want {
			t.Errorf("fork's first run id = %q, want %q", got, want)
		}
	})
}

// A pin keeps ANY message-kind entry in the context through a
// compaction — a custom_message the application injected below the
// cut survives exactly like a plain message (ADR 0020 §4).
func TestPinKeepsACustomMessageThroughCompaction(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "s"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent, thread.KeepRecent(100))
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_big", Created: timeUTC(), Message: weft.User(strings.Repeat("a", 60_000))},
			thread.CustomMessageEntry{ID: "e_note", ParentID: "e_big", Created: timeUTC(), Kind: "app/note", Message: weft.User("SERVICE NOTE: the API key rotates Friday")},
			thread.MessageEntry{ID: "e_tail", ParentID: "e_note", Created: timeUTC(), Message: weft.User("tail")},
		); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
		if err := s.Pin(ctx, "e_note"); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		found := false
		for _, m := range s.Context() {
			if strings.Contains(m.Text(), "SERVICE NOTE") {
				found = true
			}
		}
		if !found {
			t.Error("the pinned custom_message did not survive the compaction")
		}
		var last *thread.CompactionEntry
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok {
				last = &c
			}
		}
		if last == nil || len(last.Pinned) == 0 {
			t.Errorf("the entry records no pinned ids: %+v", last)
		}
	})
}

// A branch summary is made from what the model was shown of the
// branch being left — its compacted view: the branch's own compaction
// summary (the only record the context kept of the range it replaced)
// plus the entries from that compaction's first kept entry. The bug:
// the summarizer was fed the raw pre-boundary range AND the summary of
// that same range.
func TestSummarizeLeftKeepsTheBranchCompaction(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "MAIN-SUMMARY"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent)
		msgs(t, ctx, st, s,
			strings.Repeat("a", 60_000),
			strings.Repeat("b", 60_000),
			strings.Repeat("c", 60_000),
		)
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		s = reopenWith(t, ctx, st, s, agent)
		mainLeaf := s.Leaf()
		// Branch away from the compacted line, grow a compactable side
		// branch, and compact IT — then leave that branch with a
		// summary back to the main line.
		firstID := ""
		for _, e := range s.Entries() {
			if m, ok := e.(thread.MessageEntry); ok {
				firstID = m.ID
				break
			}
		}
		if err := s.Branch(ctx, firstID); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_side1", ParentID: firstID, Created: timeUTC(),
				Message: weft.User("SIDE-ONE " + strings.Repeat("s", 60_000))},
			thread.MessageEntry{ID: "e_side2", ParentID: "e_side1", Created: timeUTC(),
				Message: weft.User("SIDE-TWO " + strings.Repeat("t", 60_000))},
		); err != nil {
			t.Fatal(err)
		}
		rec.mu.Lock()
		rec.reply = "SIDE-SUMMARY"
		rec.mu.Unlock()
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("the side branch's own Compact: %v", err)
		}
		var side thread.CompactionEntry
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok {
				side = c
			}
		}
		if side.FirstKept != "e_side2" {
			t.Fatalf("the side compaction keeps from %q; the scenario expects e_side2", side.FirstKept)
		}
		rec.mu.Lock()
		rec.reply = "BRANCH-SUMMARY"
		rec.mu.Unlock()
		if err := s.Branch(ctx, mainLeaf, thread.SummarizeLeft()); err != nil {
			t.Fatalf("SummarizeLeft over a compacted branch: %v", err)
		}
		// The summarizer saw the branch's compacted view: its
		// compaction's summary and the kept entry — never the raw range
		// that summary had replaced.
		saw := fmt.Sprint(rec.saw()[len(rec.saw())-1].Messages)
		if !strings.Contains(saw, "SIDE-TWO") {
			t.Error("the branch summary input lost the side branch's kept messages")
		}
		if !strings.Contains(saw, "SIDE-SUMMARY") {
			t.Error("the branch summary input lost the branch's own compaction summary")
		}
		if strings.Contains(saw, "SIDE-ONE") {
			t.Error("the branch summary input carries the raw range its own compaction summary already replaced")
		}
		if strings.Contains(saw, strings.Repeat("a", 100)) {
			t.Error("the branch summary input carries entries below the divergence")
		}
	})
}

// A fork of a compacted session keeps working: the walk reads the same
// context as the original, and the fork can compact again on its own.
func TestForkOfACompactedSession(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "summary"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent)
		msgs(t, ctx, st, s,
			strings.Repeat("a", 60_000),
			strings.Repeat("b", 60_000),
			strings.Repeat("c", 60_000),
		)
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		before := fmt.Sprint(s.Context())
		f, err := s.Fork(ctx, s.Leaf())
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(f.Context()); got != before {
			t.Error("the fork's context differs from the compacted original's")
		}
		// The fork compacts again once it grows — from the copied
		// boundary, with the copied summary fed in.
		msgs(t, ctx, st, f, strings.Repeat("d", 30_000), strings.Repeat("e", 30_000))
		f = reopenWith(t, ctx, st, f, agent)
		if err := f.Compact(ctx); err != nil {
			t.Fatalf("the fork's own Compact: %v", err)
		}
		if got := len(f.Context()); got != 3 {
			t.Errorf("fork context after its own compaction = %d messages, want 3", got)
		}
	})
}

// Uncompact of a trim record: the undo branches back to the entry
// before the trim, and the raw results return (the stubs were a view,
// never a rewrite).
func TestUncompactOfATrim(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.ClearOldToolResults(0))
	callPair := []thread.Entry{
		thread.MessageEntry{ID: "e_c0", Created: timeUTC(), Message: weft.Message{Role: weft.RoleAssistant,
			Content: []weft.Part{weft.ToolCallPart{ID: "c1", Name: "read", Args: json.RawMessage(`{}`)}}}},
		thread.MessageEntry{ID: "e_c1", ParentID: "e_c0", Created: timeUTC(), Message: weft.Message{Role: weft.RoleTool,
			Content: []weft.Part{weft.ToolResultPart{CallID: "c1", Name: "read", Content: strings.Repeat("r", 900)}}}},
	}
	entries := []thread.Entry{thread.MessageEntry{ID: "e_old", Created: timeUTC(), Message: weft.User(strings.Repeat("a", 120_000))}}
	entries = append(entries, callPair...)
	if err := st.Append(ctx, s.ID(), entries...); err != nil {
		t.Fatal(err)
	}
	abandon(t, st, s.ID())
	s, err := thread.Open(ctx, st, s.ID(), agent, thread.ClearOldToolResults(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCompaction(ctx, &thread.Compaction{FirstKept: "e_c0", Reason: thread.ReasonTrim, TokensBefore: 1,
		Trim: &thread.TrimRecord{Stubs: []thread.TrimStub{{Entry: "e_c1", CallID: "c1", Content: "[cleared tool result read c1]"}}},
	}); err != nil {
		t.Fatalf("trim: %v", err)
	}
	// The stub view: the result reads as the cleared stub.
	abandon(t, st, s.ID())
	s, _ = thread.Open(ctx, st, s.ID(), agent, thread.ClearOldToolResults(0))
	stubbed := false
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok && r.Content == "[cleared tool result read c1]" {
				stubbed = true
			}
		}
	}
	if !stubbed {
		t.Fatal("the trim did not stub the result — the scenario is not measuring what it should")
	}
	// Undo: the raw result returns.
	if err := s.Uncompact(ctx); err != nil {
		t.Fatalf("Uncompact of a trim: %v", err)
	}
	s, _ = thread.Open(ctx, st, s.ID(), agent, thread.ClearOldToolResults(0))
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok && r.Content == "[cleared tool result read c1]" {
				t.Error("the stub outlived the undo — the file was rewritten, not viewed")
			}
		}
	}
}

// leafReadingEstimator is an Estimator that calls back into the
// Session it serves — Leaf, like any UI observer would — the thing
// every other caller hook is allowed to do. The turn's persistence
// must consult it outside the session lock, or the session deadlocks
// on its own mutex (the 2026-09-29 review's finding).
type leafReadingEstimator struct{ s *thread.Session }

func (e leafReadingEstimator) Estimate(msgs []weft.Message) int64 {
	_ = e.s.Leaf()
	return int64(len(msgs))
}

func TestEstimatorMayCallTheSession(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(wefttest.Say("hello")), weft.Name("estimator-reentry"))
	est := &leafReadingEstimator{}
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithEstimator(est))
	if err != nil {
		t.Fatal(err)
	}
	est.s = s
	done := make(chan error, 1)
	go func() {
		turn, err := s.Send(ctx, weft.User("hi"))
		if err != nil {
			done <- err
			return
		}
		_, err = turn.Wait()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session deadlocked consulting its Estimator under the lock")
	}
}

// The trigger re-arms on the turn that resolves a parked boundary:
// while the boundary is open no threshold compaction runs (the
// dangling calls must stay raw for their decisions, ADR 0021's
// amendment), and the resume's own post-turn trigger fires the moment
// the boundary is gone — the hold is a hold, not an off switch.
func TestTriggerReArmsAfterTheBoundaryResolves(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}).WithUsage(weft.Usage{InputTokens: 95_000, OutputTokens: 5}),
		wefttest.Say("done").WithUsage(weft.Usage{InputTokens: 95_000, OutputTokens: 5}),
		wefttest.Say("the summary"),
	)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.ContextWindow(100_000))
	// Real bulk, so the held trigger has something to compact.
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_b1", Created: timeUTC(), Message: weft.User(strings.Repeat("a", 120_000))},
		thread.MessageEntry{ID: "e_b2", ParentID: "e_b1", Created: timeUTC(), Message: weft.User(strings.Repeat("b", 120_000))},
	); err != nil {
		t.Fatal(err)
	}
	abandon(t, st, s.ID())
	s, err := thread.Open(ctx, st, s.ID(), agent, thread.ContextWindow(100_000))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("Pending after the parked turn: got %d, want 1", len(got))
	}
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			t.Fatalf("compaction %s ran while the boundary was open", c.ID)
		}
	}
	// Resolving the boundary re-arms the trigger: the resume's own
	// post-turn site fires and the summary lands.
	rt, err := s.Decide(ctx, thread.Approve(s.Pending()[0].CallID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil { // the post-turn trigger follows the turn
		t.Fatal(err)
	}
	rearmed := false
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonThreshold {
			rearmed = true
		}
	}
	if !rearmed {
		t.Fatal("the trigger did not re-arm on the turn that resolved the boundary")
	}
}
