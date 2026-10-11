package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	weftruntime "github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/studio"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// countingModel counts its calls per prompt and holds each until gate
// closes — the commands sit accepted, running, across the restart.
type countingModel struct {
	gate  chan struct{}
	mu    sync.Mutex
	calls map[string]int
}

func (m *countingModel) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "wefttest", Name: "counting"}
}

func (m *countingModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	prompt := ""
	for _, msg := range req.Messages {
		if msg.Role == core.RoleUser {
			prompt = msg.Text()
		}
	}
	m.mu.Lock()
	m.calls[prompt]++
	m.mu.Unlock()
	return func(yield func(core.ModelEvent, error) bool) {
		select {
		case <-m.gate:
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		}
		yield(core.ModelTextDelta{Text: "done"}, nil)
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

func (m *countingModel) count(prompt string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[prompt]
}

// swap serves whichever Studio is current; nil answers 503 (Studio
// down between the two processes).
type swap struct{ h atomic.Pointer[http.Handler] }

func (s *swap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h := s.h.Load(); h != nil {
		(*h).ServeHTTP(w, r)
		return
	}
	http.Error(w, "studio restarting", http.StatusServiceUnavailable)
}

func (s *swap) set(h http.Handler) {
	if h == nil {
		s.h.Store(nil)
		return
	}
	s.h.Store(&h)
}

// repeatLog notes the command ids weft/runtime's admit refused as
// repeated ("repeated command id ignored", at Debug).
type repeatLog struct {
	mu  sync.Mutex
	ids map[string]bool
}

func (h *repeatLog) Enabled(context.Context, slog.Level) bool { return true }

func (h *repeatLog) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "weft/runtime: repeated command id ignored" {
		return nil
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "command_id" {
			h.mu.Lock()
			if h.ids == nil {
				h.ids = map[string]bool{}
			}
			h.ids[a.Value.String()] = true
			h.mu.Unlock()
		}
		return true
	})
	return nil
}

func (h *repeatLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *repeatLog) WithGroup(string) slog.Handler      { return h }

func (h *repeatLog) saw(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ids[id]
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRestartNeverRunsTwice drives a real weft/runtime link through a
// Studio restart over the same database. Two commands are running in
// the runtime when Studio goes away: one stored accepted (as Studio took
// its ack), one rewritten to queued — the state a failed write-through
// would leave. The restarted Studio re-sends the queued one to the same
// process on its reconnect and never the accepted one; the runtime's
// at-most-once set refuses the duplicate. Each runs exactly once, and
// both finish on the new Studio.
func TestRestartNeverRunsTwice(t *testing.T) {
	logs := &repeatLog{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(prev) })
	path := filepath.Join(t.TempDir(), "weft.db")
	model := &countingModel{gate: make(chan struct{}), calls: map[string]int{}}
	agent := core.New(model, core.Name("counter"))

	sw := &swap{}
	ts := httptest.NewServer(sw)
	t.Cleanup(ts.Close)
	srv1 := studio.New(studio.Open(path), studio.Playground(true))
	sw.set(srv1.Handler())

	stop := weftruntime.Install(weftruntime.Studio(ts.URL, ""), weftruntime.Enabled(true), weftruntime.Agents(agent))
	t.Cleanup(stop)
	t.Cleanup(func() {
		select {
		case <-model.gate:
		default:
			close(model.gate)
		}
	})

	var rtID string
	waitFor(t, "the runtime", func() bool {
		for _, v := range srv1.Runtime().Snapshot() {
			if v.Connected {
				rtID = v.ID
				return true
			}
		}
		return false
	})
	// B first, then A: the runtime's resume cursor (Last-Event-ID, the
	// newest frame it admitted) is A, and B's row — rewritten below with
	// a newer Created — sorts after it in the restored order, so the
	// restarted Studio's backlog does re-send it and the runtime's own
	// at-most-once set is what refuses it.
	inA, inB := "command A", "command B"
	b, err := srv1.Runtime().Enqueue(rtID, linkruntime.Command{Agent: "counter", Input: &inB, Engine: "live", Thread: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "B admitted", func() bool { return model.count(inB) == 1 })
	a, err := srv1.Runtime().Enqueue(rtID, linkruntime.Command{Agent: "counter", Input: &inA, Engine: "live", Thread: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both commands running", func() bool {
		sa, _ := srv1.Runtime().Command(a.CommandID)
		sb, _ := srv1.Runtime().Command(b.CommandID)
		return sa.State == linkruntime.StateAccepted && sb.State == linkruntime.StateAccepted &&
			model.count(inA) == 1 && model.count(inB) == 1
	})

	// Studio goes down: requests answer 503 meanwhile, the old server's
	// stream ends, its timers and writes stop, its database closes.
	sw.set(nil)
	if err := srv1.Close(); err != nil {
		t.Fatal(err)
	}
	ts.CloseClientConnections()

	// B's row as a failed write-through would have left it: queued.
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cs := db.(obsdb.CommandStore)
	rows, err := cs.Commands(context.Background(), obsdb.CommandQuery{ID: b.CommandID})
	if err != nil || len(rows) != 1 || rows[0].State != obsdb.CommandAccepted {
		t.Fatalf("stored B = %+v %v, want accepted", rows, err)
	}
	row := rows[0]
	row.State, row.RunID, row.AcceptedAt, row.Created, row.Updated = obsdb.CommandQueued, "", time.Time{}, time.Now(), time.Now()
	if err := cs.PutCommand(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	srv2 := studio.New(studio.Open(path), studio.Playground(true))
	t.Cleanup(func() { _ = srv2.Close() })
	if st, err := srv2.Runtime().Command(a.CommandID); err != nil || st.State != linkruntime.StateAccepted {
		t.Fatalf("A after the restart = %+v %v, want accepted", st, err)
	}
	if v := srv2.Runtime().Snapshot(); len(v) != 1 || v[0].ID != rtID || v[0].Connected || !v[0].Restored {
		t.Fatalf("runtimes after the restart = %+v, want %s restored and disconnected", v, rtID)
	}
	sw.set(srv2.Handler())
	waitFor(t, "the runtime's reconnect", func() bool { return srv2.Runtime().Connected(rtID) })
	// The reconnect's stream opened with B in its backlog: the runtime
	// read it and refused it as a repeated id (its Debug line).
	waitFor(t, "the runtime's refusal of the re-sent B", func() bool { return logs.saw(b.CommandID) })
	if st, _ := srv2.Runtime().Command(b.CommandID); st.State != linkruntime.StateQueued {
		t.Fatalf("B after the reconnect = %s, want still queued (re-sent, refused, not lost)", st.State)
	}

	close(model.gate)
	for _, id := range []string{a.CommandID, b.CommandID} {
		waitFor(t, id+" finished", func() bool {
			st, _ := srv2.Runtime().Command(id)
			return st.State == linkruntime.StateFinished
		})
	}
	if na, nb := model.count(inA), model.count(inB); na != 1 || nb != 1 {
		t.Fatalf("executions: A %d, B %d — want exactly one each", na, nb)
	}

	// And Studio's own route answers the pre-restart command's state.
	resp, err := http.Get(ts.URL + "/api/playground/commands/" + a.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var st linkruntime.CommandStatus
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &st) != nil || st.State != linkruntime.StateFinished {
		t.Errorf("GET /api/playground/commands/%s = %d %s", a.CommandID, resp.StatusCode, body)
	}
}

// TestRestartWithTheLink: Studio and the app restart together (a new
// link, a new instance, the same stable id). The command stored
// accepted is never re-sent; B, stored queued as a failed write-through
// leaves it, is never delivered to the new link — the id stays the old
// process's while A is live there, and the new link works under
// <id>-<pid>. Each ran exactly once.
func TestRestartWithTheLink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.db")
	model := &countingModel{gate: make(chan struct{}), calls: map[string]int{}}
	agent := core.New(model, core.Name("counter"))
	sw := &swap{}
	ts := httptest.NewServer(sw)
	t.Cleanup(ts.Close)
	srv1 := studio.New(studio.Open(path), studio.Playground(true))
	sw.set(srv1.Handler())
	stop1 := weftruntime.Install(weftruntime.Studio(ts.URL, ""), weftruntime.Enabled(true), weftruntime.Agents(agent))
	var rtID string
	waitFor(t, "the runtime", func() bool {
		for _, v := range srv1.Runtime().Snapshot() {
			if v.Connected {
				rtID = v.ID
				return true
			}
		}
		return false
	})
	inA, inB := "command A", "command B"
	b, err := srv1.Runtime().Enqueue(rtID, linkruntime.Command{Agent: "counter", Input: &inB, Engine: "live", Thread: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := srv1.Runtime().Enqueue(rtID, linkruntime.Command{Agent: "counter", Input: &inA, Engine: "live", Thread: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both accepted", func() bool {
		sa, _ := srv1.Runtime().Command(a.CommandID)
		sb, _ := srv1.Runtime().Command(b.CommandID)
		return sa.State == linkruntime.StateAccepted && sb.State == linkruntime.StateAccepted &&
			model.count(inA) == 1 && model.count(inB) == 1
	})
	sw.set(nil)
	if err := srv1.Close(); err != nil {
		t.Fatal(err)
	}
	ts.CloseClientConnections()
	stop1() // the app goes too: its runs are canceled with the link

	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cs := db.(obsdb.CommandStore)
	rows, err := cs.Commands(context.Background(), obsdb.CommandQuery{ID: b.CommandID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored B = %v %v", rows, err)
	}
	row := rows[0]
	row.State, row.RunID, row.AcceptedAt, row.Created, row.Updated = obsdb.CommandQueued, "", time.Time{}, time.Now(), time.Now()
	if err := cs.PutCommand(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	srv2 := studio.New(studio.Open(path), studio.Playground(true))
	t.Cleanup(func() { _ = srv2.Close() })
	sw.set(srv2.Handler())
	close(model.gate)
	stop2 := weftruntime.Install(weftruntime.Studio(ts.URL, ""), weftruntime.Enabled(true), weftruntime.Agents(agent))
	t.Cleanup(stop2)
	suffixed := fmt.Sprintf("%s-%d", rtID, os.Getpid())
	waitFor(t, "the new link under "+suffixed, func() bool { return srv2.Runtime().Connected(suffixed) })
	time.Sleep(300 * time.Millisecond)
	if na, nb := model.count(inA), model.count(inB); na != 1 || nb != 1 {
		t.Fatalf("executions: A %d, B %d — want exactly one each", na, nb)
	}
	if st, _ := srv2.Runtime().Command(b.CommandID); st.State != linkruntime.StateQueued {
		t.Errorf("B = %s, want still queued for the old process (lost by its ack window)", st.State)
	}
	if st, _ := srv2.Runtime().Command(a.CommandID); st.State != linkruntime.StateAccepted {
		t.Errorf("A = %s, want accepted (decided by its watch, never re-sent)", st.State)
	}
}

// TestServerCloseStopsTheTimersWrites: studio.Server.Close drains what
// was decided before it and stops the armed timers — a queued
// command's ack window running out after Close writes nothing, so the
// next Studio over the file decides from what this one stored.
func TestServerCloseStopsTheTimersWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.db")
	agent := core.New(&countingModel{gate: make(chan struct{}), calls: map[string]int{}}, core.Name("counter"))
	sw := &swap{}
	ts := httptest.NewServer(sw)
	t.Cleanup(ts.Close)
	srv := studio.New(studio.Open(path), studio.Playground(true))
	sw.set(srv.Handler())
	stop := weftruntime.Install(weftruntime.Studio(ts.URL, ""), weftruntime.Enabled(true), weftruntime.Agents(agent))
	t.Cleanup(stop)
	var rtID string
	waitFor(t, "the runtime", func() bool {
		for _, v := range srv.Runtime().Snapshot() {
			if v.Connected {
				rtID = v.ID
				return true
			}
		}
		return false
	})
	// Acks from here on answer 503 (the open stream still delivers):
	// the runtime runs nothing on an accepted ack Studio did not take,
	// and the row stays queued in Studio.
	sw.set(nil)
	srv.Runtime().AckDeadline = 100 * time.Millisecond
	in := "never acked"
	cmd, err := srv.Runtime().Enqueue(rtID, linkruntime.Command{Agent: "counter", Input: &in, Engine: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // past the ack window

	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.(obsdb.CommandStore).Commands(context.Background(), obsdb.CommandQuery{ID: cmd.CommandID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("the command was not drained to the store before Close returned: %v %v", rows, err)
	}
	if rows[0].State != obsdb.CommandQueued {
		t.Errorf("stored state after Close and the ack window = %s, want queued (no timer write after Close)", rows[0].State)
	}
}
