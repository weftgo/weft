package otel_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mw"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/wefttest"
)

// Install is the whole setup: it registers the OTel globals the core
// reads, never fails the program, and returns the shutdown to call on
// exit. NoEnv keeps the example hermetic — no WEFT_* / OTEL_* variable
// can add a destination behind it.
func ExampleInstall() {
	dir, err := os.MkdirTemp("", "weft-otel-example-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	shutdown := otel.Install(
		otel.NoEnv(),
		otel.Local(filepath.Join(dir, "weft.db")),
	)

	// A scripted run through the installed globals — a real program
	// wires nothing: the core reads the providers Install registered.
	agt := weft.New(
		wefttest.Script(wefttest.Say("all done")),
		weft.Name("example"),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go"),
		weft.Metadata(map[string]string{"weft.session.id": "example-session"})); err != nil {
		log.Fatal(err)
	}
	shutdown() // flush every destination, close the local DB last

	// The sink reopens as a plain obsdb.DB: the run row with its
	// derived status, and the transcript the messages records hold.
	db, err := sqlite.Open(filepath.Join(dir, "weft.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	page, err := db.Runs(context.Background(), obsdb.RunQuery{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(page.Runs[0].Status, page.Runs[0].MessageCount)
	// Output: succeeded 2
}

// Slog bridges slog.Default: weft's own Debug run lines (weft.Logger's
// default is slog.Default) and mw.Log's (whose nil logger is too) reach the pipeline's Local and Studio
// destinations, each under the span it was logged in, and read back as
// the run's app logs — what Studio's runs/{id}/logs serves.
func ExampleSlog() {
	dir, err := os.MkdirTemp("", "weft-otel-slog-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "weft.db")
	shutdown := otel.Install(otel.NoEnv(), otel.Local(path), otel.Slog())

	agt := weft.New(wefttest.Script(wefttest.Say("all done")),
		weft.Name("example"), weft.WrapModel(mw.Log(nil)))
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		log.Fatal(err)
	}
	shutdown() // flushes, and puts slog.Default back

	db, err := sqlite.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	page, err := db.OtherLogs(context.Background(), res.ID, obsdb.LogQuery{})
	if err != nil {
		log.Fatal(err)
	}
	for _, l := range page.Logs {
		fmt.Println(obsdb.SeverityText(l.Severity), l.Body)
	}
	// Output:
	// DEBUG run start
	// DEBUG model request
	// DEBUG model finish
	// DEBUG model call
	// DEBUG run finish
}

// SlogHandler bridges a logger the program builds itself: here a tool
// handler's WARN, logged with the tool call's context, is stored under
// that call's execute_tool span — the line and the step it came from,
// joined in Studio. The handler follows the installed pipeline, so it
// may be built before Install.
func ExampleSlogHandler() {
	dir, err := os.MkdirTemp("", "weft-otel-slog-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	logger := slog.New(otel.SlogHandler(slog.NewTextHandler(io.Discard, nil)))
	path := filepath.Join(dir, "weft.db")
	shutdown := otel.Install(otel.NoEnv(), otel.Local(path))

	lookup := weft.Tool("lookup", "Look an order up.", func(ctx context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		logger.WarnContext(ctx, "slow lookup", "id", in.ID)
		return "shipped", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"id":"42"}`}),
		wefttest.Say("shipped")), weft.Name("example"), lookup)
	res, err := agt.Generate(context.Background(), weft.Prompt("where is 42?"))
	if err != nil {
		log.Fatal(err)
	}
	shutdown()

	db, err := sqlite.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	page, err := db.OtherLogs(context.Background(), res.ID, obsdb.LogQuery{})
	if err != nil {
		log.Fatal(err)
	}
	spans, err := db.RunSpans(context.Background(), res.ID)
	if err != nil {
		log.Fatal(err)
	}
	names := map[string]string{}
	for _, s := range spans {
		names[s.SpanID] = s.Name
	}
	for _, l := range page.Logs {
		fmt.Println(obsdb.SeverityText(l.Severity), l.Body, "·", names[l.SpanID], "· id", l.Attrs["id"])
	}
	// Output: WARN slow lookup · execute_tool lookup · id 42
}
