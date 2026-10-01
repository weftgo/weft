// Command studio is setup B's local binary (S4.6, §10.1): the UI,
// OTLP ingest, SQLite and a dev token on 127.0.0.1:7331 — the
// zero-infra Studio for any language's app:
//
//	studio                                   # serves UI + OTLP, prints a dev token
//	WEFT_STUDIO_URL=http://127.0.0.1:7331 ./my-go-app
//	OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:7331 python app.py
//
// The database defaults to the otel local sink's path ($WEFT_DB or
// ./.weft/weft.db), so the same file serves an in-process app and the
// binary. --db sqlite://path picks another file; --db
// clickhouse://user:pass@host:9000/db serves the hosted backend
// (obsdb/clickhouse, wired at merge-B). The dev token is printed at
// start and fixed by WEFT_STUDIO_TOKEN or --token.
//
// This is its own module so the studio library never carries what
// only the binary needs — it is the one place that imports the
// clickhouse driver.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/weftgo/weft/obsdb/clickhouse"
	"github.com/weftgo/weft/studio"
)

const defaultAddr = "127.0.0.1:7331"

func main() {
	db := flag.String("db", "",
		"`sqlite://path` or `clickhouse://user:pass@host:9000/db` (default: $WEFT_DB or ./.weft/weft.db)")
	addr := flag.String("addr", defaultAddr, "listen address (loopback by default)")
	token := flag.String("token", "",
		"API token (default: $WEFT_STUDIO_TOKEN, else a generated dev token printed at start)")
	flag.Parse()
	if err := serve(*db, *addr, *token, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "studio:", err)
		os.Exit(1)
	}
}

// serve builds the server, prints where it lives, and listens until
// the process is stopped. Split from main so the construction and the
// banner are testable without a port.
func serve(dbFlag, addr, tokenFlag string, stdout io.Writer) error {
	srv, err := newServer(dbFlag, tokenFlag)
	if err != nil {
		return err
	}
	token := srvToken(tokenFlag)
	// The banner is best-effort by design: a closed stdout must not
	// keep the server from serving.
	_, _ = fmt.Fprintf(stdout, "studio: http://%s/\n", addr)
	_, _ = fmt.Fprintf(stdout, "studio: dev token %s (WEFT_STUDIO_TOKEN fixes it)\n", token)
	_, _ = fmt.Fprintf(stdout, "studio: db %s\n", dbLabel(dbFlag))
	err = http.ListenAndServe(addr, srv.Handler())
	if cerr := srv.Close(); err == nil {
		err = cerr
	}
	return err
}

// newServer builds setup B's server from the flags: the UI at the
// root, ingest on, the database under --db or the default path, and
// the token wall (the dev token by default). sqlite:// and the
// default open the local sink's file; clickhouse:// opens the hosted
// backend — this is the only place that imports the driver.
func newServer(dbFlag, tokenFlag string) (*studio.Server, error) {
	opts := []studio.Option{studio.Base("/"), studio.Token(srvToken(tokenFlag))}
	switch {
	case dbFlag == "":
		// The default: the otel local sink's path (S4.1).
	case strings.HasPrefix(dbFlag, "sqlite://"):
		opts = append(opts, studio.Open(strings.TrimPrefix(dbFlag, "sqlite://")))
	case strings.HasPrefix(dbFlag, "clickhouse://"):
		// The hosted backend (S3.6): Open takes the full DSN (scheme
		// included), creates and versions the schema, so a fresh
		// database is ready to serve. A closed port surfaces the
		// driver's dial error here.
		db, err := clickhouse.Open(dbFlag)
		if err != nil {
			return nil, err
		}
		opts = append(opts, studio.DB(db))
	default:
		return nil, fmt.Errorf("--db must be sqlite://path or clickhouse://user:pass@host:9000/db")
	}
	return studio.New(opts...), nil
}

// srvToken resolves the token: the flag, then WEFT_STUDIO_TOKEN,
// then a generated dev token.
func srvToken(tokenFlag string) string {
	if tokenFlag != "" {
		return tokenFlag
	}
	if env := os.Getenv("WEFT_STUDIO_TOKEN"); env != "" {
		return env
	}
	return studio.DevToken()
}

// dbLabel names the database for the banner.
func dbLabel(dbFlag string) string {
	switch {
	case dbFlag == "":
		return "$WEFT_DB or ./.weft/weft.db (default)"
	case strings.HasPrefix(dbFlag, "sqlite://"):
		return strings.TrimPrefix(dbFlag, "sqlite://")
	default:
		// clickhouse:// and friends: the DSN as given, host and
		// database are the useful part of the banner.
		return dbFlag
	}
}
