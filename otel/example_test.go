package otel_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/weftgo/weft"
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
