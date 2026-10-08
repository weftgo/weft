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
// own (setup A): it keeps its port. The binary (studio/cmd) started
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
// waterfall have a tool to show. A real app replaces the model with
// its own and changes nothing else.
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
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// page is the host app's own landing page: a plain HTML document
// whose only weft-ness is the panel's script tag (setup A, §5.3 —
// same origin as Studio, no token, the session's public id baked in).
const page = `<!doctype html><html><head><title>host app</title></head><body>
<h1>the host app's own page</h1>
<p>the devtools panel below is a script tag and nothing else</p>
<script type="module" src="/studio/panel.js" data-public-id="pub_demo" data-open="true"></script>
</body></html>`

func main() {
	addr := flag.String("addr", "127.0.0.1:7331", "listen address")
	flag.Parse()
	if err := serve(*addr); err != nil {
		log.Fatal(err)
	}
}

// demoAgent is the app's one agent: built once here, and the
// acceptance test runs the same value. The model is a deterministic
// echo — offline, and prompt-dependent, so a playground experiment
// with an edited input visibly answers differently.
func demoAgent() *weft.Agent {
	return weft.New(paced{150 * time.Millisecond, echoModel{}},
		weft.Name("studio-local"),
		weft.Instructions("You are the studio-local demo agent."),
		lookupOrder)
}

// serve is setup A (§10.1) plus this demo's own /run endpoint.
// Everything between the two comments is what an embedding app
// writes; nothing else is configured.
func serve(addr string) error {
	defer otel.Install()() // local sink ./.weft/weft.db, content on, no network

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
	defer runtime.Install(
		runtime.Local(srv),
		runtime.Agents(agent),
		runtime.Threads(store),
		runtime.Enabled(true),
	)()

	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", srv.Handler()))

	// The demo's own surface: one thread turn per call, and the plain
	// page the panel docks on (the gate harness drives both).
	demo := newDemo(store, agent)
	mux.HandleFunc("POST /run", demo.run)
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

// echoModel is the demo's deterministic model: it answers the last
// user text with one lookup_order call, then a reply that quotes both
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
			yield(weft.ModelToolCall{ID: "call_1", Name: "lookup_order",
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
}

func newDemo(st thread.Storage, agent *weft.Agent) *demo {
	return &demo{st: st, agent: agent, sem: make(chan struct{}, 1)}
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
		s, err := thread.Create(ctx, d.st, d.agent, thread.PublicID("pub_demo"))
		if err != nil {
			d.mu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.s = s
	}
	session := d.s
	d.mu.Unlock()
	turn, err := session.Send(ctx, weft.User(question))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Answer when the turn lands: the reply is the demo's product, and
	// a watcher of /studio/live saw it stream in meanwhile.
	res, err := turn.Wait()
	if err != nil {
		_, _ = fmt.Fprintf(w, "run %s (%s): %v\n", turn.RunID(), session.ID(), err)
		return
	}
	_, _ = fmt.Fprintf(w, "run %s (%s): %s\n", turn.RunID(), session.ID(),
		strings.TrimSpace(res.Text()))
}
