package clickhouse_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// ADR 0028 §8 through the real pipeline, on both backends: a run whose
// PrepareStep trimmed the middle at step 3, and a thread session's
// first run after a manual compaction. Each backend returns the plain
// transcript unchanged (the growth records concatenate to the run's
// messages), exposes the view with its range and the session marker
// through Compactions, and resolves the step-3 request's messages_ref
// to exactly what the model was sent; the two backends agree. The
// SQLite half always runs; the ClickHouse half needs
// WEFT_CLICKHOUSE_DSN.
func TestCompactionBackendsAgree(t *testing.T) {
	ctx := context.Background()
	on := &capture{}
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.NoEnv(), otel.Heartbeat(0), otel.Exporters(on, on, otel.WithContent()))
	if err != nil {
		t.Fatal(err)
	}
	// The run-scope view.
	turns := make([]wefttest.Turn, 0, 6)
	for i := 0; i < 5; i++ {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"msg":"x"}`}))
	}
	model := wefttest.Script(append(turns, wefttest.Say("done"))...)
	agt := core.New(model, core.Name("support"),
		core.Tool("lookup", "Echo lookup.", func(_ context.Context, in struct {
			Msg string `json:"msg"`
		}) (string, error) {
			return in.Msg, nil
		}),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step == 3 {
				m := req.Messages
				req.Messages = []core.Message{m[0], core.User("summary of the middle"), m[len(m)-2], m[len(m)-1]}
			}
			return req, nil
		}),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	res, err := agt.Generate(ctx, core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	sentAt3, _ := json.Marshal(model.Requests()[3].Messages)
	transcript, _ := json.Marshal(res.Messages)

	// The session marker: a manual compaction, then a turn.
	summarizer := wefttest.Script(wefttest.Say("a1"), wefttest.Say("a2"), wefttest.Say("a3"),
		wefttest.Say("the summary"), wefttest.Say("after"))
	sess, err := thread.Create(ctx, thread.Memory(), core.New(summarizer, core.LoggerProvider(p.LoggerProvider())), thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{strings.Repeat("a", 4000), strings.Repeat("b", 4000), strings.Repeat("c", 4000)} {
		turn, err := sess.Send(ctx, core.User(q))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	var entry thread.CompactionEntry
	for _, e := range sess.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			entry = c
		}
	}
	after, err := sess.Send(ctx, core.User("next"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := after.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close(ctx)
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	eb, _ := json.Marshal(entry)
	sum := sha256.Sum256(eb)
	entryHash := hex.EncodeToString(sum[:])

	type view struct {
		Run, Session []obsdb.Compaction
	}
	read := func(t *testing.T, db obsdb.DB) view {
		t.Helper()
		if err := db.Write(ctx, on.batch()); err != nil {
			t.Fatal(err)
		}
		// The plain transcript is the growth records, byte for byte.
		batches, err := db.TranscriptBatches(ctx, res.ID)
		if err != nil {
			t.Fatal(err)
		}
		var all []json.RawMessage
		for _, b := range batches {
			var msgs []json.RawMessage
			if err := json.Unmarshal(b.Messages, &msgs); err != nil {
				t.Fatal(err)
			}
			all = append(all, msgs...)
		}
		if got := mustCompact(t, all); !bytes.Equal(got, mustCompactRaw(t, transcript)) {
			t.Errorf("transcript =\n%s\nwant\n%s", got, transcript)
		}
		det, err := db.Run(ctx, res.ID)
		if err != nil || det.MessageCount != int64(len(batches)) {
			t.Errorf("MessageCount = %d (%v), want the %d growth records", det.MessageCount, err, len(batches))
		}
		cs, err := db.Compactions(ctx, res.ID)
		if err != nil || len(cs) != 1 {
			t.Fatalf("Compactions = %+v, %v; want the one view", cs, err)
		}
		v := cs[0]
		if v.Scope != obsdb.CompactionRun || v.Step != 3 || v.Index != 7 || v.FromSeq != 1 || v.ToSeq != 5 || v.Hash == "" || v.Entries != 1 {
			t.Errorf("view = %+v", v)
		}
		// The step-3 request names the view; growth up to it with the
		// range replaced is what the model was sent.
		step := 3
		reqs, err := db.Requests(ctx, res.ID, obsdb.RequestQuery{Step: &step})
		if err != nil || len(reqs) != 1 || reqs[0].Body.MessagesRef.Index == nil || *reqs[0].Body.MessagesRef.Index != v.Index {
			t.Fatalf("step-3 request = %+v, %v; want messages_ref naming index %d", reqs, err, v.Index)
		}
		var prefix []json.RawMessage
		for _, b := range batches {
			if b.Index > v.Index {
				break
			}
			var msgs []json.RawMessage
			_ = json.Unmarshal(b.Messages, &msgs)
			prefix = append(prefix, msgs...)
		}
		var body []json.RawMessage
		_ = json.Unmarshal(v.Messages, &body)
		resolved := append(append(append([]json.RawMessage{}, prefix[:v.FromSeq]...), body...), prefix[v.ToSeq:]...)
		if got := mustCompact(t, resolved); !bytes.Equal(got, mustCompactRaw(t, sentAt3)) || len(resolved) != reqs[0].Body.MessagesRef.Count {
			t.Errorf("step-3 ref resolves to\n%s\nthe model was sent\n%s", got, sentAt3)
		}
		for i := range cs {
			cs[i].Messages = mustCompactRaw(t, cs[i].Messages)
		}

		ss, err := db.Compactions(ctx, after.RunID())
		if err != nil || len(ss) != 1 {
			t.Fatalf("session Compactions = %+v, %v; want the marker", ss, err)
		}
		if m := ss[0]; m.Scope != obsdb.CompactionSession || m.Hash != entryHash || m.Reason != "manual" || m.Messages != nil ||
			m.Replaced == 0 || m.Entries != 1 || m.TokensBefore != entry.TokensBefore || m.TokensAfter == 0 {
			t.Errorf("marker = %+v (entry hash %s)", m, entryHash)
		}
		return view{Run: cs, Session: ss}
	}

	lite, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lite.Close() }()
	want := read(t, lite)
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		return
	}
	hosted, _ := openFresh(t)
	if got := read(t, hosted); !reflect.DeepEqual(got, want) {
		t.Errorf("backends disagree:\nclickhouse %+v\nsqlite     %+v", got, want)
	}
}

func mustCompact(t *testing.T, msgs []json.RawMessage) []byte {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	return mustCompactRaw(t, b)
}

func mustCompactRaw(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
