package scope_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/scope"
)

// Header on the app's own chat endpoint, in one line: every response
// carries the conversation's scope, and the handler narrows it to the
// run once the turn has started. Cross-origin pages read the header
// through Access-Control-Expose-Headers, which Header sets; the app's
// CORS policy still has to allow the page's origin.
func ExampleHeader() {
	chat := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runID := "r_01" // turn.RunID() once the turn is sent
		scope.Set(w, scope.Scope{PublicID: r.URL.Query().Get("c"), RunID: runID})
		_, _ = fmt.Fprintln(w, "hello")
	})
	h := scope.Header(chat, func(r *http.Request) scope.Scope {
		return scope.Scope{PublicID: r.URL.Query().Get("c")}
	})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/chat?c=pub_demo", nil))
	fmt.Println(w.Header().Get("Weft-Scope"))
	fmt.Println(w.Header().Get("Access-Control-Expose-Headers"))
	// Output:
	// pub_demo;run=r_01
	// Weft-Scope
}

func ExampleParse() {
	s := scope.Parse("pub_demo;flow=f_3;tenant=acme;run=r_9")
	fmt.Printf("%q %q %q %q\n", s.PublicID, s.SessionID, s.FlowID, s.RunID)
	fmt.Println(s)
	// Output:
	// "pub_demo" "" "f_3" "r_9"
	// pub_demo;flow=f_3;run=r_9
}

func ExampleScope_String() {
	fmt.Println(scope.Scope{PublicID: "pub_demo", SessionID: "s_1", RunID: "r_2"})
	fmt.Println(scope.Scope{PublicID: "a;b"})
	// Output:
	// pub_demo;session=s_1;run=r_2
	// a%3Bb
}

// Header stamps the page the request came from — its Referer, query
// and fragment dropped — as weft.page_url on every run the handler
// starts: the handler passes no option. Studio's run page reads it back
// for "open in app".
func ExamplePageURLFromContext() {
	agt := core.New(wefttest.Script(wefttest.Say("hi")),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				fmt.Println("run:", core.MetadataFromContext(ctx)[scope.PageURLKey])
			}
		}))
	chat := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := scope.PageURLFromContext(r.Context())
		fmt.Println("handler:", page)
		_, _ = agt.Generate(r.Context(), core.Prompt("hello"))
	})
	h := scope.Header(chat, func(*http.Request) scope.Scope { return scope.Scope{PublicID: "pub_demo"} })

	req := httptest.NewRequest(http.MethodPost, "/chat", nil)
	req.Header.Set("Referer", "https://app.example/support?draft=1#top")
	h.ServeHTTP(httptest.NewRecorder(), req)
	// Output:
	// handler: https://app.example/support
	// run: https://app.example/support
}

// A page of unrelated runs — a test batch — is a tag scope: no public
// id, the runs whose metadata carries every pair.
func ExampleScope_tags() {
	s := scope.Parse("tag.arena.batch=cli-20261012-115753")
	fmt.Printf("%q %q\n", s.PublicID, s.Tags["arena.batch"])
	fmt.Println(scope.Scope{Agent: "director", Tags: map[string]string{"ci.job": "1234"}})
	// Output:
	// "" "cli-20261012-115753"
	// ;agent=director;tag.ci.job=1234
}

// WithFlow stamps a flow on every run started under the context, as
// Header does for a scope with a flow: Studio's reads filter on it
// (flow=f_checkout).
func ExampleWithFlow() {
	agt := core.New(wefttest.Script(wefttest.Say("hi")),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				fmt.Println(core.MetadataFromContext(ctx)[scope.FlowKey])
			}
		}))
	_, _ = agt.Generate(scope.WithFlow(context.Background(), "f_checkout"), core.Prompt("hi"))
	// Output:
	// f_checkout
}
