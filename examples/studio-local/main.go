// Command studio-local is §10.1's setup A, the whole thing: a Go app
// with exactly the five configured lines, running a thread session
// whose turns appear live in Studio, grouped under one session, with
// content and timing — and nothing else configured.
//
//	go run ./examples/studio-local
//	# open http://127.0.0.1:7331/studio/
//	# then, from anywhere:
//	curl -s -XPOST localhost:7331/run -d 'where is order 42?'
//
// The five lines (main): Install the otel defaults (the local sink at
// ./.weft/weft.db, content on, no network), mount Studio over the
// pipeline's own handle — the same DB, the same live hub — and serve.
// The /run endpoint is the demo's own surface, not configuration: one
// turn of a thread session per call, on a scripted model so the
// example runs offline; a real app replaces it with its own agent and
// model and changes nothing else.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7331", "listen address")
	flag.Parse()
	if err := serve(*addr); err != nil {
		log.Fatal(err)
	}
}

// serve is the five lines of setup A (§10.1) plus this demo's own
// /run endpoint. Everything between the two comments is what an
// embedding app writes; nothing else is configured.
func serve(addr string) error {
	defer otel.Install()() // local sink ./.weft/weft.db, content on, no network
	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio",
		studio.Handler(studio.DB(otel.LocalDB())))) // the pipeline's handle: same DB, same live hub [D4]

	// The demo's own surface: one thread turn per call.
	demo := newDemo()
	mux.HandleFunc("POST /run", demo.run)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "studio-local: POST /run with a question, and watch /studio/live\n")
	})

	log.Printf("studio-local: studio at http://%s/studio/ (live at /studio/live)", addr)
	log.Printf("studio-local: try: curl -s -XPOST %s/run -d 'where is order 42?'", addr)
	return http.ListenAndServe(addr, mux)
}

// pacing wraps a model with a beat between its events, so a turn
// takes a moment to stream — the live tail has something to follow
// and the acceptance test can watch the frame ordering.
type pacing struct {
	beat time.Duration
	next weft.Model
}

func (p pacing) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
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

// demo owns one thread session over in-memory storage: every /run is
// a turn of it, so Studio groups the turns under one session — the
// acceptance test's shape. The model is rebuilt per turn (a scripted
// model plays once); the session and its storage are the app's.
type demo struct {
	mu  sync.Mutex
	st  thread.Storage
	s   *thread.Session
	sem chan struct{} // one writer per session (thread's rule)
}

func newDemo() *demo {
	return &demo{st: thread.Memory(), sem: make(chan struct{}, 1)}
}

// run answers one question: a scripted model that streams its reply
// (so the live tail has deltas to show) after a beat of thinking, on
// the session whose public id the panel would carry.
func (d *demo) run(w http.ResponseWriter, r *http.Request) {
	question := strings.TrimSpace(r.FormValue("text"))
	if question == "" {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			question = strings.TrimSpace(string(body))
		}
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

	model := pacing{150 * time.Millisecond, wefttest.Script(
		wefttest.Say(fmt.Sprintf("You asked: %q. Let me look…", question)),
		wefttest.Say(fmt.Sprintf("About %q: order 42 shipped this morning.", question)),
	)}
	d.mu.Lock()
	var err error
	if d.s == nil {
		d.s, err = thread.Create(ctx, d.st, weft.New(model, weft.Name("studio-local")),
			thread.PublicID("pub_demo"))
	} else {
		// The next turn reopens the session over the same storage with
		// a fresh scripted model.
		var reopenErr error
		d.s, reopenErr = thread.Open(ctx, d.st, d.s.ID(), weft.New(model, weft.Name("studio-local")))
		if reopenErr != nil {
			err = reopenErr
		}
	}
	session := d.s
	d.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	turn, err := session.Send(ctx, weft.Message{Role: "user", Content: []weft.Part{
		weft.TextPart{Text: question},
	}})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Answer when the turn lands: the reply is the demo's product, and
	// a watcher of /studio/live saw it stream in meanwhile.
	res, err := turn.Wait()
	if err != nil {
		fmt.Fprintf(w, "run %s (%s): %v\n", turn.RunID(), session.ID(), err)
		return
	}
	fmt.Fprintf(w, "run %s (%s): %s\n", turn.RunID(), session.ID(),
		strings.TrimSpace(res.Text()))
}
