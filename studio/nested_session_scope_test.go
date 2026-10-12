package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// TestNestedSessionTurnIsNotTheEnclosingPublicIDs drives the nested-
// session case through the real pipeline (thread → otel → OTLP ingest
// → obsdb → the handler): session s_outer, created with
// thread.PublicID("pub_s"), runs a tool whose handler sends a turn to
// session s_inner, created without one; the inner turn parks on an
// approval. The inner turn's run carries s_inner's identity, not the
// enclosing run's public id (thread stamps weft.public_id = "", which
// core reads as none), so:
//   - the server token reads the run with no public id;
//   - pub_s's panel token neither lists nor reads it, and may not
//     decide its approval (403) — it is not pub_s's turn;
//   - the server token's approval, with a runtime.Sessions runtime of
//     the service connected, is refused as appturn.go says: the run is
//     not a turn of a thread session with a public id (before, it read
//     as pub_s's and was forwarded, and the app's resolver refused it:
//     "answered session s_outer for public id pub_s, not the run's").
//
// seqIDs numbers a session's ids (its own first, then its entries')
// under prefix.
func seqIDs(prefix string) thread.SessionOption {
	n := 0
	return thread.IDs(func() string { n++; return fmt.Sprintf("%s_%03d", prefix, n) })
}

func TestNestedSessionTurnIsNotTheEnclosingPublicIDs(t *testing.T) {
	const serverTok, ingestTok, service = "srv-token", "ingest-token", "svc_nested"
	srv := New(Open(t.TempDir()+"/nested.db"), Playground(true), Token(serverTok), IngestToken(ingestTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ingestTok), otel.Service(service), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	obs := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	type refundIn struct{}
	refund := core.Tool("refund", "Refund the order.", func(context.Context, refundIn) (string, error) { return "refunded", nil },
		core.RequireApproval())
	innerAgent := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{}`})),
		append([]core.Option{core.Name("inner"), refund}, obs...)...)
	inner, err := thread.Create(ctx, thread.Memory(), innerAgent, seqIDs("s_inner"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	innerRun := ""
	type askIn struct{}
	ask := core.Tool("ask_inner", "Ask the inner session.", func(ctx context.Context, _ askIn) (string, error) {
		turn, err := inner.Send(ctx, core.User("refund order 7"))
		if err != nil {
			return "", err
		}
		res, err := turn.Wait()
		if err != nil {
			return "", err
		}
		mu.Lock()
		innerRun = res.ID
		mu.Unlock()
		return "asked", nil
	})
	outerAgent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ask_inner", Args: `{}`}),
		wefttest.Say("done"),
	), append([]core.Option{core.Name("outer"), ask}, obs...)...)
	outer, err := thread.Create(ctx, thread.Memory(), outerAgent,
		seqIDs("s_outer"), thread.PublicID("pub_s"))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := outer.Send(ctx, core.User("please refund"))
	if err != nil {
		t.Fatal(err)
	}
	outerRes, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*thread.Session{outer, inner} {
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	innerID := innerRun
	mu.Unlock()
	if innerID == "" {
		t.Fatal("the inner turn never ran")
	}

	// The server token reads the inner run: s_inner's, no public id.
	code, body := b4Do(t, http.MethodGet, ts.URL+"/api/runs/"+innerID, serverTok, "")
	var row struct {
		SessionID string `json:"session_id"`
		PublicID  string `json:"public_id"`
	}
	if code != http.StatusOK {
		t.Fatalf("server GET /api/runs/%s = %d %s", innerID, code, body)
	}
	if err := json.Unmarshal([]byte(body), &row); err != nil {
		t.Fatal(err)
	}
	if row.SessionID != inner.ID() || row.PublicID != "" {
		t.Errorf("inner run = session %q, public id %q; want s_inner and none — not the enclosing pub_s", row.SessionID, row.PublicID)
	}

	code, body = b4Do(t, http.MethodPost, ts.URL+"/api/panel-tokens", serverTok, `{"public_id":"pub_s","playground":true}`)
	if code != http.StatusOK {
		t.Fatalf("mint: %d %s", code, body)
	}
	var m struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	// pub_s's panel token: the outer turn is its, the inner one is not.
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/runs/"+outerRes.ID, m.Token, ""); code != http.StatusOK {
		t.Errorf("panel GET the outer run = %d %s, want 200: it is pub_s's", code, body)
	}
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/runs/"+innerID, m.Token, ""); code != http.StatusForbidden {
		t.Errorf("panel GET the inner run = %d %s, want 403: it is not pub_s's turn", code, body)
	}
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/runs?all=1", m.Token, ""); code != http.StatusOK ||
		strings.Contains(body, innerID) || !strings.Contains(body, outerRes.ID) {
		t.Errorf("panel GET /api/runs?all=1 = %d %s, want the outer run listed and the inner one (recorded as its child run) not", code, body)
	}
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/sessions", m.Token, ""); code != http.StatusOK ||
		strings.Contains(body, inner.ID()) || !strings.Contains(body, outer.ID()) {
		t.Errorf("panel GET /api/sessions = %d %s, want s_outer alone", code, body)
	}
	if code, body := b4Do(t, http.MethodGet, ts.URL+"/api/sessions/"+inner.ID(), m.Token, ""); code == http.StatusOK {
		t.Errorf("panel GET /api/sessions/s_inner = %d %s, want refused: no turn of it is pub_s's", code, body)
	}

	// A runtime.Sessions runtime of the service is connected: the app's
	// own turns with a public id route to it; the inner one has none.
	reg := `{"runtime_id":"rt_nested","instance":"i1","service":"` + service + `","sessions":true,"cwd":"/x","agents":[{"name":"outer"}]}`
	if code, body := b4Do(t, http.MethodPost, ts.URL+"/api/runtime/register", serverTok, reg); code != http.StatusOK {
		t.Fatalf("register: %d %s", code, body)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runtime/commands?runtime=rt_nested", nil)
	req.Header.Set("Authorization", "Bearer "+serverTok)
	stream, err := http.DefaultClient.Do(req)
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("commands stream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Body.Close() })
	go func() { _, _ = io.Copy(io.Discard, stream.Body) }()

	approve := `{"call_id":"call_1","decision":"approve"}`
	code, body = b4Do(t, http.MethodPost, ts.URL+"/api/runs/"+innerID+"/approvals", serverTok, approve)
	if code != http.StatusForbidden ||
		!strings.Contains(body, "app-owned turn · read-only: run "+innerID+" is not a turn of a thread session with a public id") {
		t.Errorf("server approval of the inner turn = %d %s, want 403: not a turn of a thread session with a public id", code, body)
	}
	if code, body := b4Do(t, http.MethodPost, ts.URL+"/api/runs/"+innerID+"/approvals", m.Token, approve); code != http.StatusForbidden {
		t.Errorf("panel approval of the inner turn = %d %s, want 403: not pub_s's", code, body)
	}
}
