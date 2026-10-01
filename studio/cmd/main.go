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
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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
	srv, err := serveBoot(dbFlag, addr, tokenFlag, stdout)
	if err != nil {
		return err
	}
	err = listen(&http.Server{Addr: addr, Handler: srv.Handler()}, stdout)
	if cerr := srv.Close(); err == nil {
		err = cerr
	}
	return err
}

// listen runs the HTTP server until SIGINT or SIGTERM — a graceful
// stop (the audit's P2-20: a bare ListenAndServe cut SSE streams mid-
// frame and skipped srv.Close): the listener closes at once, in-flight
// requests get a five-second grace window (an SSE stream ends when its
// request context cancels), and streams that outlive the window are
// force-closed — the studio resources close after, in serve.
func listen(httpSrv *http.Server, stdout io.Writer) error {
	// Signal delivery is armed before the listener starts: a signal
	// that lands while nobody is notified takes the process down.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		_, _ = fmt.Fprintf(stdout, "studio: shutting down (%v)\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			// Streams never go idle: after the grace window the socket
			// force-closes so the process can still exit.
			_ = httpSrv.Close()
		}
		return nil
	}
}

// serveBoot is everything serve does before listening: resolve the
// token, build the server, print the banner. The token is resolved
// exactly once — the wall newServer builds and the banner it prints
// are the same value (a generated dev token is fresh randomness per
// srvToken call; resolving twice minted two different tokens and the
// printed one could not open the API it advertised). Split out so the
// boot path — token resolution, banner, wall — is testable without a
// port.
func serveBoot(dbFlag, addr, tokenFlag string, stdout io.Writer) (*studio.Server, error) {
	token := srvToken(tokenFlag)
	srv, err := newServer(dbFlag, token)
	if err != nil {
		return nil, err
	}
	// The banner is best-effort by design: a closed stdout must not
	// keep the server from serving.
	_, _ = fmt.Fprintf(stdout, "studio: http://%s/\n", addr)
	_, _ = fmt.Fprintf(stdout, "studio: dev token %s (WEFT_STUDIO_TOKEN fixes it)\n", token)
	_, _ = fmt.Fprintf(stdout, "studio: db %s\n", dbLabel(dbFlag))
	return srv, nil
}

// newServer builds setup B's server from the flags: the UI at the
// root, ingest on, the database under --db or the default path, and
// the token wall. The token arrives already resolved (serveBoot is
// the one resolver — the wall and the banner share it). sqlite:// and
// the default open the local sink's file; clickhouse:// opens the
// hosted backend — this is the only place that imports the driver.
func newServer(dbFlag, token string) (*studio.Server, error) {
	opts := []studio.Option{studio.Base("/"), studio.Token(token)}
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

// dbLabel names the database for the banner. A DSN's password is
// never echoed (the audit's P2-20): clickhouse://user:pass@host/db
// prints clickhouse://user:***@host/db — the host and database are
// the useful part of the banner.
func dbLabel(dbFlag string) string {
	switch {
	case dbFlag == "":
		return "$WEFT_DB or ./.weft/weft.db (default)"
	case strings.HasPrefix(dbFlag, "sqlite://"):
		return strings.TrimPrefix(dbFlag, "sqlite://")
	default:
		return maskDSN(dbFlag)
	}
}

// maskDSN replaces a DSN userinfo's password with ***. A DSN without
// a password is returned unchanged; so is anything that does not parse
// as scheme://…@host (the banner stays honest rather than empty).
func maskDSN(dsn string) string {
	scheme, rest, ok := strings.Cut(dsn, "://")
	if !ok {
		return dsn
	}
	userinfo, hostPart, found := strings.Cut(rest, "@")
	if !found {
		return dsn
	}
	user, _, hasPassword := strings.Cut(userinfo, ":")
	if !hasPassword {
		return dsn
	}
	return scheme + "://" + user + ":***@" + hostPart
}
