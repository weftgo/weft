package studio_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/weftgo/weft/studio"

	// The sqlite backend — the same one otel.Local writes.
	"github.com/weftgo/weft/obsdb/sqlite"
)

// ExampleHandler is setup A's shape on one screen: a Studio over an
// obsdb handle, mounted under a prefix. otel.Install's local sink
// writes the handle in a real app; here a batch stands in for it.
func ExampleHandler() {
	dir, err := os.MkdirTemp("", "weft-studio-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := sqlite.Open(dir + "/weft.db")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = db.Close() }()

	// The mount the doc comment shows; the server serves the UI, the
	// API, the live stream and OTLP ingest under /studio/.
	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio",
		studio.Handler(studio.DB(db))))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/studio/api/meta")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleServer shows the Server surface (S4.1): New when the
// playground is in play, Handler() to serve, Close for what New
// opened, Runtime() nil without Playground(true). New without DB or
// Open would open the default path ($WEFT_DB or ./.weft/weft.db) —
// pinned by TestOpenOption — so the example opens a throwaway sqlite
// file instead of writing into the package directory.
func ExampleServer() {
	dir, err := os.MkdirTemp("", "weft-studio-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	srv := studio.New(studio.Open(dir+"/weft.db"), studio.NoIngest()) // read-only: no OTLP receiver
	defer func() { _ = srv.Close() }()                                // New opened that DB: closed here

	_ = srv.Handler() // mount it; the binary in studio/cmd does
	_ = srv.Runtime() // nil without Playground(true) (studio/runtime)
	fmt.Println("ok")
	// Output:
	// ok
}
