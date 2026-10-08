// Command studio-local is §10.1's setup A, the whole thing: a Go app
// with exactly the five configured lines, running a thread session
// whose turns appear live in Studio, grouped under one session, with
// content and timing — and nothing else configured. Its own landing
// page carries the devtools panel's script tag (WEFT-DEVTOOLS §5.3
// setup A), so the example is the panel demo too; and since step 8b
// it also runs the playground's runtime link
// (runtime.Install(runtime.Local(srv))), so the panel's experiment
// drawer re-runs this app's own agent — the demo is the P1 gate's
// shape as well.
//
//	go run ./examples/studio-local
//	# open http://127.0.0.1:7331/        (the panel docks on the plain page)
//	# open http://127.0.0.1:7331/studio/
//	# then, from anywhere:
//	curl -s -XPOST localhost:7331/run -d 'where is order 42?'
//
// 7331 is Studio's one default port, and this listener is the app's
// own (setup A): it keeps its port. The binary (`weft studio`) started
// beside it does not collide: it finds 7331 busy with something that
// is not a Studio at /api/meta (this app mounts Studio under /studio/)
// and moves to the next free port, saying so in one line (plan B2).
//
// The five lines (serve): Install the otel defaults (the local sink at
// ./.weft/weft.db, content on, no network), build the Studio server
// with Playground(true) over the pipeline's own handle — the same DB,
// the same live hub — mount its Handler, and pass it to
// runtime.Install. The /run endpoint is the demo's own surface, not
// configuration: one turn of a thread session per call, on a
// deterministic echo model so the example runs offline; the turn makes
// one lookup_order tool call so the panel's step story and the
// waterfall have a tool to show; a question about a refund calls
// refund_order instead, a weft.RequireApproval tool, so that turn parks
// on an approval (the panel's on("parked") fires once, with the call's
// id) and the next question denies it before it runs. A real app
// replaces the model with its own and changes nothing else.
//
// Under `weft dev` (plan B1.2) the same binary is the app beside the
// command's Studio:
//
//	weft dev -- go run ./examples/studio-local
//
// weft dev sets WEFT_STUDIO_URL, WEFT_STUDIO_TOKEN, WEFT_DB and
// WEFT_ENV=dev; with WEFT_STUDIO_URL set — or, run bare beside a
// `weft studio`, with the Studio its discovery file names — the demo
// listens on 127.0.0.1:8080 (that Studio holds 7331), the pipeline
// exports to that Studio besides the local sink, and the runtime link
// registers there instead of with the embedded server: serve reads
// otel.StudioEndpoint() after Install. The
// app then writes the shared WEFT_DB twice, through its local sink and
// through Studio's ingest: harmless, the sqlite writer is INSERT OR
// IGNORE. -addr still overrides 8080.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"net/url"
	"regexp"
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

// page is the host app's own landing page: a plain HTML document
// whose only weft-ness is the panel's script tag (setup A, §5.3 —
// same origin as Studio, no token). The tag names no scope: the
// page's own ask form POSTs /run with fetch, and the panel reads the
// response's Weft-Scope header (detection rung 2, on by default on
// loopback with no token) — so the first turn scopes it to pub_demo
// and pins that turn's run. A page that wants to name its scope
// itself adds data-scope="pub_demo" to the tag.
//
// The page also drives the panel from its own UI through the one
// global the script tag adds, window.weft.devtools (plan C4): each
// reply gets a "debug this" button that scopes the panel to that turn
// (scope("pub_demo;run=<id>")) and selects its first step (select(id,
// 0)); a "report this run" link follows on("run") and asks the panel
// for the Studio link (studioLink(runId, step) — the panel builds it,
// the page knows no Studio URL); and on("parked") counts the parked
// calls into <body data-parked-count>, one per call.
const page = `<!doctype html><html><head><title>host app</title></head><body data-parked-count="0">
<h1>the host app's own page</h1>
<p>the devtools panel below is a script tag and nothing else; ask about a refund to see a turn park on an approval</p>
<form id="ask"><input name="text" value="where is order 42?" size="40"> <button>ask</button></form>
<ol id="answers"></ol>
<p><a id="report" target="_blank" rel="noopener" hidden>report this run</a></p>
<script>
const answers = document.getElementById("answers")
document.getElementById("ask").addEventListener("submit", async (e) => {
  e.preventDefault()
  const res = await fetch("/run", { method: "POST", body: new FormData(e.target).get("text") })
  const run = ((res.headers.get("Weft-Scope") || "").split(";").find((p) => p.startsWith("run=")) || "").slice(4)
  const li = document.createElement("li")
  const answer = document.createElement("pre")
  answer.textContent = await res.text()
  li.append(answer)
  if (run) {
    const debug = document.createElement("button")
    debug.type = "button"
    debug.className = "debug-this"
    debug.dataset.run = decodeURIComponent(run)
    debug.textContent = "debug this"
    li.append(debug)
  }
  answers.append(li)
})
// "debug this": the panel follows that turn, at its first step.
answers.addEventListener("click", (e) => {
  const b = e.target.closest("button.debug-this")
  const devtools = window.weft && window.weft.devtools
  if (!b || !devtools) return
  devtools.scope("pub_demo;run=" + b.dataset.run)
  devtools.select(b.dataset.run, 0)
  devtools.open()
})
// The panel's global exists once its module has run (before this event).
document.addEventListener("DOMContentLoaded", () => {
  const devtools = window.weft && window.weft.devtools
  if (!devtools) return
  const report = document.getElementById("report")
  devtools.on("run", (d) => {
    report.href = devtools.studioLink(d.runId, d.step)
    report.textContent = "report this run (" + d.runId + ", " + d.status + ")"
    report.hidden = false
  })
  devtools.on("parked", () => {
    document.body.dataset.parkedCount = String(Number(document.body.dataset.parkedCount) + 1)
  })
})
</script>
<script type="module" src="/studio/panel.js" data-weft data-open="true"></script>
</body></html>`

func main() {
	// 7331 is Studio's one default port, and this listener is the
	// app's own; beside a Studio that already holds it (weft dev sets
	// WEFT_STUDIO_URL, or a running weft studio's discovery file names
	// it) the app takes 8080. -addr always wins.
	addr := flag.String("addr", "127.0.0.1:7331", "listen address (default 127.0.0.1:7331; 127.0.0.1:8080 beside a running Studio)")
	flag.Parse()
	explicit := false
	flag.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "addr" })
	if err := serve(*addr, explicit); err != nil {
		log.Fatal(err)
	}
}

// demoAgent is the app's one agent: built once here, and the
// acceptance test runs the same value. The model is a deterministic
// echo — offline, and prompt-dependent, so a playground experiment
// with an edited input visibly answers differently. Its PrepareStep
// trims the prompt: from step 1 on the first-step guidance paragraph
// is dropped, so Studio's Request pane shows step 1's system prompt
// diffed against step 0's with the "changed by PrepareStep" chip.
func demoAgent() *weft.Agent {
	return weft.New(paced{150 * time.Millisecond, echoModel{}},
		weft.Name("studio-local"),
		weft.Instructions(demoInstructions),
		weft.PrepareStep(trimGuidance),
		lookupOrder, refundOrder)
}

// firstStepGuidance is the paragraph only step 0's prompt carries.
const firstStepGuidance = "First-step guidance: look the order up with lookup_order before answering."

const demoInstructions = "You are the studio-local demo agent.\n\n" + firstStepGuidance

// trimGuidance is the demo's PrepareStep: after step 0 the guidance
// has done its job, so the system text loses that paragraph — the
// same trimmed text on every later step.
func trimGuidance(_ context.Context, step int, req weft.ModelRequest) (weft.ModelRequest, error) {
	if step >= 1 {
		req.System = strings.Replace(req.System, "\n\n"+firstStepGuidance, "", 1)
	}
	return req, nil
}

// serve is setup A (§10.1) plus this demo's own /run endpoint.
// Everything between the two comments is what an embedding app
// writes; nothing else is configured.
func serve(addr string, explicitAddr bool) error {
	// The local sink ($WEFT_DB or ./.weft/weft.db), content on; named
	// explicitly so it stays when the environment adds a destination
	// (WEFT_STUDIO_URL under weft dev): the embedded Studio reads it.
	defer otel.Install(otel.Local(""))()

	// Beside a running Studio — WEFT_STUDIO_URL, or the one the
	// pipeline joined through the discovery file — that Studio holds
	// 7331: this app's own listener takes 8080.
	studioURL, studioToken := otel.StudioEndpoint()
	if studioURL != "" && !explicitAddr {
		addr = "127.0.0.1:8080"
	}

	agent := demoAgent()

	// Setup A, with the playground: the same five lines, one more
	// option and one deferred call. The Studio server is built, its
	// Handler mounted, and the runtime link dials it in-process.
	// The demo's sessions live in one jsonl store; the runtime gets it
	// too, so the playground's fork mode (thread=fork) can branch them.
	store, err := jsonl.Open("./.weft/threads")
	if err != nil {
		return err
	}
	srv := studio.New(studio.DB(otel.LocalDB()), studio.Playground(true))
	// The runtime link dials the embedded server in-process — or, under
	// weft dev, the Studio WEFT_STUDIO_URL names, so its playground
	// drives this app.
	link := runtime.Local(srv)
	if studioURL != "" {
		link = runtime.Studio(studioURL, studioToken)
	}
	defer runtime.Install(
		link,
		runtime.Agents(agent),
		runtime.Threads(store),
		runtime.Enabled(true),
	)()

	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", srv.Handler()))

	// The demo's own surface: one thread turn per call, and the plain
	// page the panel docks on (the gate harness drives both). /run's
	// responses carry the Weft-Scope header (the development rung):
	// the conversation's public id on every answer, narrowed to the
	// turn's run by the handler once the turn has started.
	demo := newDemo(store, agent)
	mux.Handle("POST /run", demo.handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, page)
	})

	log.Printf("studio-local: studio at http://%s/studio/ (live at /studio/live), panel at http://%s/", addr, addr)
	log.Printf("studio-local: try: curl -s -XPOST %s/run -d 'where is order 42?'", addr)
	return http.ListenAndServe(addr, mux)
}

// lookupOrder is the demo's one tool: safe to re-run (a read), so the
// playground executes it for real in experiments — weft.Replay's one
// line, the honest class instead of the never default.
var lookupOrder = weft.Tool("lookup_order", "Look up an order.", func(_ context.Context, in struct {
	OrderID string `json:"order_id"`
}) (string, error) {
	time.Sleep(200 * time.Millisecond) // visible tool timing in the waterfall
	return "order " + in.OrderID + ": shipped this morning", nil
}, weft.Replay(weft.ReplaySafe))

// refundOrder is the demo's one write: it needs an approval
// (weft.RequireApproval), so a turn that calls it ends parked with the
// call pending — the deterministic parking path the panel's
// on("parked") is shown with. Unannotated for replay: the playground
// substitutes or parks it, never re-fires it.
var refundOrder = weft.Tool("refund_order", "Refund an order (needs an approval).", func(_ context.Context, in struct {
	OrderID string `json:"order_id"`
}) (string, error) {
	return "order " + in.OrderID + ": refunded", nil
}, weft.RequireApproval())

// refundCallID is the id of the call the echo model makes on a refund
// question: the id the approval names — the panel's on("parked") ackId.
// The app approves its own turns through its Session,
// s.Decide(ctx, thread.Approve(ackId)); Studio's POST
// /api/runs/{id}/approvals is for playground runs only (it answers 403
// for a run no runtime started, this one included).
const refundCallID = "call_refund"

// echoModel is the demo's deterministic model: it answers the last
// user text with one lookup_order call (refund_order when the text
// asks for a refund), then a reply that quotes both
// the question and the tool's result — so an experiment's edited input
// produces a visibly different answer (the diff has something to
// show), and everything runs offline.
type echoModel struct{}

func (echoModel) Info() weft.ModelInfo { return weft.ModelInfo{Provider: "demo", Name: "echo"} }

var orderNum = regexp.MustCompile(`\d+`)

func (echoModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == weft.RoleUser {
			num := orderNum.FindString(last.Text())
			if num == "" {
				num = "42"
			}
			id, tool := "call_1", "lookup_order"
			if strings.Contains(strings.ToLower(last.Text()), "refund") {
				id, tool = refundCallID, "refund_order"
			}
			yield(weft.ModelToolCall{ID: id, Name: tool,
				Args: []byte(`{"order_id":"` + num + `"}`)}, nil)
			yield(weft.ModelFinish{Reason: weft.StopToolCalls,
				Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
			return
		}
		// After the tool batch: the reply names the question and the
		// result, so the panel's step story reads end to end.
		question := ""
		for _, m := range req.Messages {
			if m.Role == weft.RoleUser {
				question = m.Text()
			}
		}
		result := ""
		for _, p := range last.Content {
			if tr, ok := p.(weft.ToolResultPart); ok {
				result = tr.Content
			}
		}
		yield(weft.ModelTextDelta{Text: fmt.Sprintf("About %q: %s.", question, result)}, nil)
		yield(weft.ModelFinish{Reason: weft.StopEndTurn,
			Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}
}

// paced wraps a model with a beat between its events, so a turn takes
// a moment to stream — the live tail has something to follow and the
// acceptance test can watch the frame ordering.
type paced struct {
	beat time.Duration
	next weft.Model
}

func (p paced) Info() weft.ModelInfo { return weft.InfoOf(p.next) }

func (p paced) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		for ev, err := range p.next.Stream(ctx, req) {
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(ev, nil) {
				return
			}
			select {
			case <-time.After(p.beat):
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
	}
}

// demo owns one thread session over durable jsonl storage: every /run
// is a turn of it, so Studio groups the turns under one session — the
// acceptance test's shape, and a file the byte-compare gate reads. It
// holds the one Session value for as long as it serves.
type demo struct {
	mu    sync.Mutex
	st    thread.Storage
	agent *weft.Agent
	s     *thread.Session
	sem   chan struct{} // one writer per session (thread's rule)
	// decide records the deny-first decisions (Session.Decide; a test
	// stands in a failing one).
	decide func(*thread.Session, context.Context, ...thread.Decision) (*thread.Turn, error)
}

func newDemo(st thread.Storage, agent *weft.Agent) *demo {
	return &demo{st: st, agent: agent, sem: make(chan struct{}, 1), decide: (*thread.Session).Decide}
}

// publicID is the demo session's public id: every /run response's
// Weft-Scope header names it, and the panel scopes itself from that
// header (the script tag carries no scope).
const publicID = "pub_demo"

// handler is /run with its scope header: the one line an app adds to
// its chat endpoint.
func (d *demo) handler() http.Handler {
	return scope.Header(http.HandlerFunc(d.run), func(*http.Request) scope.Scope {
		return scope.Scope{PublicID: publicID}
	})
}

// run answers one question: one turn of the session whose public id
// the panel carries.
func (d *demo) run(w http.ResponseWriter, r *http.Request) {
	// The body is the question — curl -d 'where is order 42?' sends the
	// bare words under a form content type, so FormValue would read
	// them as a field name with no value. A text=… field works too.
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	question := strings.TrimSpace(string(raw))
	if form, err := url.ParseQuery(question); err == nil && form.Get("text") != "" {
		question = strings.TrimSpace(form.Get("text"))
	}
	if question == "" {
		question = "hello"
	}
	select {
	case d.sem <- struct{}{}:
		defer func() { <-d.sem }()
	default:
		http.Error(w, "a turn is already running", http.StatusConflict)
		return
	}
	// The turn outlives the request that started it: thread runs it on
	// the session's runner, and a handler's ctx cancels on return —
	// WithoutCancel keeps the run alive, the timeout bounds it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()

	// One Session for the demo's life: a Session is its session's one
	// writer from its first write until its Close (thread's writer
	// lease), so the process keeps the value it created instead of
	// opening the session again per request — a second Session would
	// be refused with thread.ErrLocked while the first is open.
	d.mu.Lock()
	if d.s == nil {
		s, err := thread.Create(ctx, d.st, d.agent, thread.PublicID(publicID))
		if err != nil {
			d.mu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.s = s
	}
	session := d.s
	d.mu.Unlock()
	// A refund the previous question parked is still awaiting its
	// approval, and a parked session holds every next turn behind it:
	// the demo has no approver, so the new question denies it first
	// (one resume run reads the denial, then this turn runs).
	if pending := session.Pending(); len(pending) > 0 {
		ds := make([]thread.Decision, 0, len(pending))
		for _, p := range pending {
			ds = append(ds, thread.Deny(p.CallID, "the user asked something else"))
		}
		resume, err := d.decide(session, ctx, ds...)
		if err != nil {
			// Undenied, the call still holds the session: a Send now would
			// wait behind it until its context ended. Say so instead.
			http.Error(w, "could not deny the pending call: "+err.Error(), http.StatusConflict)
			return
		}
		if resume != nil {
			_, _ = resume.Wait()
		}
	}
	turn, err := session.Send(ctx, weft.User(question))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The run id exists once the turn is sent: the response's scope
	// narrows to it before anything is written.
	scope.Set(w, scope.Scope{PublicID: publicID, RunID: turn.RunID()})
	// Answer when the turn lands: the reply is the demo's product, and
	// a watcher of /studio/live saw it stream in meanwhile.
	res, err := turn.Wait()
	if err != nil {
		_, _ = fmt.Fprintf(w, "run %s (%s): %v\n", turn.RunID(), session.ID(), err)
		return
	}
	if len(res.Pending) > 0 {
		names := make([]string, 0, len(res.Pending))
		for _, c := range res.Pending {
			names = append(names, c.Name+" (call "+c.ID+")")
		}
		_, _ = fmt.Fprintf(w, "run %s (%s): awaiting approval: %s\n", turn.RunID(), session.ID(),
			strings.Join(names, ", "))
		return
	}
	_, _ = fmt.Fprintf(w, "run %s (%s): %s\n", turn.RunID(), session.ID(),
		strings.TrimSpace(res.Text()))
}
