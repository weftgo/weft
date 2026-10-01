package clickhouse_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/clickhouse"
)

// Opening the backend: a clickhouse-go DSN — the database in the path
// is the one the schema lands in — plus options composed at call time,
// here a one-week spans-and-runs window with the 30-day content
// default kept, and delta storage on for a debugging session. Open
// brings the schema up to date and returns the obsdb.DB every backend
// shares, so the reads are the same calls the SQLite backend takes.
//
// This example carries no // Output: because it needs a server (the
// README has the one-line container recipe); go test compiles it but
// does not execute it.
func ExampleOpen() {
	db, err := clickhouse.Open(
		"clickhouse://default:weft@127.0.0.1:9000/weft?dial_timeout=10s",
		clickhouse.TTL(0, 7*24*time.Hour),
		clickhouse.KeepDeltas(),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	page, err := db.Runs(context.Background(), obsdb.RunQuery{Agent: "greeter"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("greeter runs:", page.Total)
}
