package main

// The served mode (-serve addr, plan F4): the same deploy gate as a
// small web app whose parked turns are decided from the devtools panel.
// The app opts in with the two runtime options, both at New/Install:
//
//   - runtime.Breakpoints() on the agent: the debugger's breakpoints
//     park the app's own runs (a breakpoint on status parks the status
//     call, as RequireApproval parks every deploy);
//   - runtime.Sessions(resolver) at runtime.Install: Studio's approve /
//     deny / resolve and steer on one of the app's turns reach the
//     app's live Session, which decides and resumes the turn.
//
// Setup A (§10.1): Studio is embedded under /studio/ over its own
// local sink (<dir>/weft.db — never $WEFT_DB, so it shares no file with
// another Studio), the runtime link dials it in-process, and the
// landing page carries the panel's script tag scoped to the session's
// public id. The link opens only under WEFT_ENV=dev:
//
//	WEFT_ENV=dev go run ./thread/examples/approvals -serve 127.0.0.1:7331
//	weft dev -- go run ./thread/examples/approvals -serve 127.0.0.1:8080
//	# open the address, ask "deploy to prod", and decide it in the panel
//
// Under weft dev the runs are exported to that Studio as well (its
// WEFT_STUDIO_URL), but the panel on this page reads the embedded one,
// where the link registers.
//
// Without WEFT_ENV=dev the options are set but the link is closed: no
// runtime of the app's service is connected, so the parked call reads
// "app-owned turn · read-only: no runtime of service … is connected"
// with the fix that opens the link. The model is a deterministic
// stand-in, so the demo runs offline.

import (
	"context"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/scope"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// servePublicID is the served session's public id: the page scopes the
// panel to it, and the resolver answers it.
const servePublicID = "pub_approvals"

// servePage is the landing page: an ask form and the panel's script
// tag, scoped to the session (data-scope).
const servePage = `<!doctype html><html><head><title>approvals example</title></head><body>
<h1>approvals example</h1>
<p>ask "deploy to prod" (a gated call) or "status?" (parks under a breakpoint on status), then decide the parked call in the panel</p>
<form id="ask"><input name="text" value="deploy to prod" size="40"> <button>ask</button></form>
<ol id="answers"></ol>
<script>
document.getElementById("ask").addEventListener("submit", async (e) => {
  e.preventDefault()
  const res = await fetch("/ask", { method: "POST", body: new FormData(e.target).get("text") })
  const li = document.createElement("li")
  li.textContent = await res.text()
  document.getElementById("answers").append(li)
})
</script>
<script type="module" src="/studio/panel.js" data-weft data-scope="` + servePublicID + `" data-open="true"></script>
</body></html>`

// servedAgent is the served mode's agent: deploy gated as in the
// walk-through, status an ordinary read, and runtime.Breakpoints() so
// the debugger's breakpoints park the app's own runs. opts adds the
// caller's (a test's pipeline providers).
func servedAgent(opts ...weft.Option) *weft.Agent {
	return opsAgent(append([]weft.Option{runtime.Breakpoints()}, opts...)...)
}

// opsAgent is the agent without either opt-in: what an app that set
// neither option runs.
func opsAgent(opts ...weft.Option) *weft.Agent {
	return weft.New(opsModel{},
		append([]weft.Option{
			weft.Name("approvals-example"),
			weft.Tool("deploy", "Deploy the service.",
				func(ctx context.Context, in struct {
					Env string `json:"env"`
				}) (string, error) {
					call, _ := weft.CallFromContext(ctx)
					return "deployed to " + in.Env + " (approved: " + fmt.Sprint(call.Approved) + ")", nil
				},
				weft.RequireApproval()),
			weft.Tool("status", "Read the service's status.",
				func(context.Context, struct{}) (string, error) { return "all green", nil },
				weft.Replay(weft.ReplaySafe)),
		}, opts...)...)
}

// app holds the served session: one Session for the process's life
// (thread's writer lease): the stored session under the public id when
// one exists — a restart over the same -dir resumes it, so one public
// id never names two sessions — else created on the first question.
type app struct {
	st    thread.Storage
	agent *weft.Agent
	mu    sync.Mutex
	s     *thread.Session
}

// session answers the app's live Session, opening or creating it once.
func (a *app) session(ctx context.Context) (*thread.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.load(ctx, true)
}

// load opens the newest stored session whose create-time meta carries
// the served public id or, with create and none stored, creates it;
// (nil, nil) without create when none is stored. a.mu is held.
func (a *app) load(ctx context.Context, create bool) (*thread.Session, error) {
	if a.s != nil {
		return a.s, nil
	}
	page, err := thread.List(ctx, a.st, thread.Query{Limit: 1, Meta: map[string]string{"weft.public_id": servePublicID}})
	if err != nil {
		return nil, err
	}
	var s *thread.Session
	switch {
	case len(page.Sessions) > 0:
		s, err = thread.Open(ctx, a.st, page.Sessions[0].ID, a.agent)
	case create:
		s, err = thread.Create(ctx, a.st, a.agent, thread.PublicID(servePublicID))
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.s = s
	return s, nil
}

// resolve is the runtime.Sessions resolver: the app's LIVE Session
// behind its public id (nothing to release) — the stored one opened
// when no question has yet, so a turn parked before a restart is
// decided — or (nil, nil, nil), not this app's, when none is stored.
func (a *app) resolve(ctx context.Context, publicID string) (*thread.Session, func(), error) {
	if publicID != servePublicID {
		return nil, nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, err := a.load(ctx, false)
	if err != nil || s == nil {
		return nil, nil, err
	}
	return s, nil, nil
}

// install is the app's runtime.Install: its agent, its store for
// transcript reads, and the Sessions resolver; extra adds the caller's
// (Enabled in a test; WEFT_ENV=dev opens the link otherwise).
func (a *app) install(link runtime.Option, extra ...runtime.Option) (shutdown func()) {
	return runtime.Install(append([]runtime.Option{
		link,
		runtime.Agents(a.agent),
		runtime.Threads(a.st),
		runtime.Sessions(a.resolve),
	}, extra...)...)
}

// handler is POST /ask: one turn of the session per question, its
// response scoped to the turn (Weft-Scope) and answered once the turn
// lands — parked or done.
func (a *app) handler() http.Handler {
	return scope.Header(http.HandlerFunc(a.ask), func(*http.Request) scope.Scope {
		return scope.Scope{PublicID: servePublicID}
	})
}

func (a *app) ask(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	question := strings.TrimSpace(string(raw))
	if question == "" {
		question = "status?"
	}
	// The turn outlives the request: thread runs it on the session's
	// runner; the timeout bounds it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	s, err := a.session(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// A parked call holds the session's next turn behind it: say so
	// instead of queueing the question behind a decision.
	if p := s.Pending(); len(p) > 0 {
		http.Error(w, "call "+p[0].CallID+" ("+p[0].Tool+") is awaiting its decision: decide it in the panel", http.StatusConflict)
		return
	}
	turn, err := s.Send(ctx, weft.User(question))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	scope.Set(w, scope.Scope{PublicID: servePublicID, RunID: turn.RunID()})
	res, err := turn.Wait()
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(w, "run %s: %v\n", turn.RunID(), err)
	case len(res.Pending) > 0:
		_, _ = fmt.Fprintf(w, "run %s: awaiting approval: %s (call %s) — decide it in the panel\n",
			turn.RunID(), res.Pending[0].Name, res.Pending[0].ID)
	default:
		_, _ = fmt.Fprintf(w, "run %s: %s\n", turn.RunID(), strings.TrimSpace(res.Text()))
	}
}

// serve is the served mode: the local sink, Studio embedded with the
// playground, the runtime link dialling it in-process with the two
// options, the page and /ask.
func serve(addr, dir string) error {
	// The embedded Studio's own file: weft dev's WEFT_DB belongs to its
	// Studio, and two Studios never share one database.
	defer otel.Install(otel.Local(filepath.Join(dir, "weft.db")))()
	served := filepath.Join(dir, "served")
	if err := os.MkdirAll(served, 0o755); err != nil {
		return err
	}
	st, err := jsonl.Open(served)
	if err != nil {
		return err
	}
	a := &app{st: st, agent: servedAgent()}
	srv := studio.New(studio.DB(otel.LocalDB()), studio.Playground(true))
	defer func() { _ = srv.Close() }()
	defer a.install(runtime.Local(srv))()

	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", srv.Handler()))
	mux.Handle("POST /ask", a.handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, servePage)
	})
	log.Printf("approvals: the page at http://%s/ (the panel docks on it), Studio at http://%s/studio/", addr, addr)
	return http.ListenAndServe(addr, mux)
}

// opsModel is the served mode's deterministic model: a question
// mentioning deploy calls deploy, any other calls status — a fresh call
// id per question — and after the tool batch it answers with the
// result.
type opsModel struct{}

func (opsModel) Info() weft.ModelInfo { return weft.ModelInfo{Provider: "demo", Name: "ops"} }

func (opsModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		usage := weft.Usage{InputTokens: 10, OutputTokens: 5}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == weft.RoleUser {
			n := 0
			for _, m := range req.Messages {
				if m.Role == weft.RoleUser {
					n++
				}
			}
			call := weft.ModelToolCall{ID: fmt.Sprintf("call_status_%d", n), Name: "status", Args: []byte(`{}`)}
			if strings.Contains(strings.ToLower(last.Text()), "deploy") {
				call = weft.ModelToolCall{ID: fmt.Sprintf("call_deploy_%d", n), Name: "deploy", Args: []byte(`{"env":"prod"}`)}
			}
			if yield(call, nil) {
				yield(weft.ModelFinish{Reason: weft.StopToolCalls, Usage: usage}, nil)
			}
			return
		}
		result := ""
		for _, p := range last.Content {
			if tr, ok := p.(weft.ToolResultPart); ok {
				result = tr.Content
			}
		}
		if yield(weft.ModelTextDelta{Text: "Done: " + result + "."}, nil) {
			yield(weft.ModelFinish{Reason: weft.StopEndTurn, Usage: usage}, nil)
		}
	}
}
