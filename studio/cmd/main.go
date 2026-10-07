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
// start and fixed by WEFT_STUDIO_TOKEN or --token. --version prints
// the weft version (version.Runtime: the module tag this binary was
// built from) and exits.
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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/weftgo/weft/obsdb/clickhouse"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/version"
)

const defaultAddr = "127.0.0.1:7331"

func main() {
	db := flag.String("db", "",
		"`sqlite://path` or `clickhouse://user:pass@host:9000/db` (default: $WEFT_DB or ./.weft/weft.db)")
	addr := flag.String("addr", defaultAddr, "listen address (loopback by default)")
	token := flag.String("token", "",
		"API token (default: $WEFT_STUDIO_TOKEN, else a generated dev token printed at start)")
	showVersion := flag.Bool("version", false, "print the weft version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Runtime())
		return
	}
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
	err = listen(httpServer(addr, srv.Handler()), stdout)
	if cerr := srv.Close(); err == nil {
		err = cerr
	}
	return err
}

// httpServer builds the listener's server. The header read and idle
// keep-alives are bounded — a client that opens a connection and never
// finishes its request headers must not hold it forever. The write
// side and the body read are deliberately not: /api/live and the
// runtime's command stream are long-lived (each frame carries its own
// write deadline in the handlers), and a 16 MiB ingest upload over a
// slow link is legitimate.
func httpServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

// server is the studio server plus the database handle the binary
// opened itself: studio.Server.Close leaves a DB(...) handle to its
// owner, and for --db clickhouse:// that owner is this binary.
type server struct {
	*studio.Server
	db io.Closer // nil when the studio server owns its database
}

// Close closes the studio server, then the binary's own handle.
func (s *server) Close() error {
	err := s.Server.Close()
	if s.db != nil {
		if cerr := s.db.Close(); err == nil {
			err = cerr
		}
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
	// Shutdown never cancels a request's context — it waits for the
	// connection to go idle, which a stream never does. Every request's
	// context derives from this one, canceled when the shutdown begins,
	// so /api/live and the runtime's command stream end at once instead
	// of holding the grace window.
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	httpSrv.BaseContext = func(net.Listener) context.Context { return base }
	httpSrv.RegisterOnShutdown(cancelBase)
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
func serveBoot(dbFlag, addr, tokenFlag string, stdout io.Writer) (*server, error) {
	token := srvToken(tokenFlag)
	srv, err := newServer(dbFlag, token)
	if err != nil {
		return nil, err
	}
	// The banner is best-effort by design: a closed stdout must not
	// keep the server from serving.
	// Only a generated dev token is printed — nobody knows it
	// otherwise — and then the link carries it too, in the fragment:
	// the UI adopts a token from the page URL, and a fragment never
	// reaches a server or a Referer. A token the operator fixed is
	// theirs already, and it is the panel tokens' signing key (S4.6
	// setup C): it stays out of the log, link included.
	switch {
	case tokenFlag != "":
		_, _ = fmt.Fprintf(stdout, "studio: http://%s/\n", addr)
		_, _ = fmt.Fprintln(stdout, "studio: token from --token")
	case os.Getenv("WEFT_STUDIO_TOKEN") != "":
		_, _ = fmt.Fprintf(stdout, "studio: http://%s/\n", addr)
		_, _ = fmt.Fprintln(stdout, "studio: token from WEFT_STUDIO_TOKEN")
	default:
		_, _ = fmt.Fprintf(stdout, "studio: http://%s/#token=%s\n", addr, token)
		_, _ = fmt.Fprintf(stdout, "studio: dev token %s (WEFT_STUDIO_TOKEN fixes it)\n", token)
	}
	_, _ = fmt.Fprintf(stdout, "studio: db %s\n", dbLabel(dbFlag))
	return srv, nil
}

// newServer builds setup B's server from the flags: the UI at the
// root, ingest on, the database under --db or the default path, and
// the token wall. The token arrives already resolved (serveBoot is
// the one resolver — the wall and the banner share it). sqlite:// and
// the default open the local sink's file; clickhouse:// opens the
// hosted backend — this is the only place that imports the driver.
//
// studio.New panics when it cannot open its database (a construction
// error, for a library). For the binary that is a mistyped --db: the
// panic is returned as the error main prints.
func newServer(dbFlag, token string) (srv *server, err error) {
	opts := []studio.Option{studio.Base("/"), studio.Token(token)}
	var own io.Closer
	defer func() {
		if r := recover(); r != nil {
			if own != nil {
				_ = own.Close()
			}
			srv, err = nil, fmt.Errorf("%v", r)
		}
	}()
	switch {
	case dbFlag == "":
		// The default: the otel local sink's path (S4.1).
	case strings.HasPrefix(dbFlag, "sqlite://"):
		path := strings.TrimPrefix(dbFlag, "sqlite://")
		if path == "" {
			return nil, fmt.Errorf("--db sqlite:// needs a path (sqlite://weft.db, sqlite:///var/lib/weft.db)")
		}
		opts = append(opts, studio.Open(path))
	case strings.HasPrefix(dbFlag, "clickhouse://"):
		// The hosted backend (S3.6): Open takes the full DSN (scheme
		// included), creates and versions the schema, so a fresh
		// database is ready to serve. A closed port surfaces the
		// driver's dial error here.
		db, err := clickhouse.Open(dbFlag)
		if err != nil {
			return nil, err
		}
		opts, own = append(opts, studio.DB(db)), db
	default:
		return nil, fmt.Errorf("--db must be sqlite://path or clickhouse://user:pass@host:9000/db")
	}
	return &server{Server: studio.New(opts...), db: own}, nil
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
	// The userinfo ends at the authority's last '@' (url.Parse's rule,
	// the driver's): a password may hold one.
	authority, path := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return dsn
	}
	userinfo, hostPart := authority[:at], authority[at+1:]+path
	user, _, hasPassword := strings.Cut(userinfo, ":")
	if !hasPassword {
		return dsn
	}
	return scheme + "://" + user + ":***@" + hostPart
}
