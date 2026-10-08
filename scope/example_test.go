package scope_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

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
