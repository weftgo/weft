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
// start and fixed by WEFT_STUDIO_TOKEN or --token. --manifest path
// (default $WEFT_MANIFEST) serves the app's weft.json at /api/manifest,
// read once at start. --version prints
// the weft version (version.Runtime: the module tag this binary was
// built from) and exits.
//
// The port policy (plan B2, internal/listen): 127.0.0.1:7331 is the
// one default. When it is busy, studio asks GET /api/meta there (with
// --token / WEFT_STUDIO_TOKEN as the bearer when set): a Studio serving
// the same database file is reused — "studio already running at
// http://127.0.0.1:7331 (pid 1234), reusing", exit 0, nothing opened —
// and anything else (another program, a Studio on another database, a
// Studio whose meta this token cannot read) moves studio to the next
// free port in 7331–7340, said in one line before the banner, which
// prints the real address. All ten busy is exit 1 naming the range.
// --addr (or WEFT_STUDIO_ADDR) pins the address: busy is exit 1 with
// the address in the error, never a probe or another port.
//
// `studio doctor [--url URL] [--token TOK]` checks a running Studio and
// prints one line per check, each read from its GET /api/meta: reachable,
// token accepted, the database's path and size, the content it stores,
// connected runtimes (and, when none, what this shell's WEFT_ENV and
// WEFT_STUDIO_URL say about why), the panel bundle's version and
// whether weft.json is stale against the latest runs. --url defaults
// to $WEFT_STUDIO_URL, else http://127.0.0.1:7331; --token to
// $WEFT_STUDIO_TOKEN. It exits 1 when a check fails — first of all an
// unreachable Studio, reported on the first line within a short timeout.
//
// It is a package of the framework module (ADR 0027) kept apart from
// the studio library so the library never carries what only the
// binary needs — it is the one place that imports the clickhouse
// driver.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/weftgo/weft/internal/doctor"
	"github.com/weftgo/weft/internal/listen"
	"github.com/weftgo/weft/obsdb/clickhouse"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/version"
)

const defaultAddr = listen.DefaultAddr

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		// A failed doctor check has printed its own lines.
		if !errors.Is(err, flag.ErrHelp) && !errors.Is(err, doctor.ErrUnhealthy) {
			fmt.Fprintln(os.Stderr, "studio:", err)
		}
		os.Exit(1)
	}
}

// run parses the command line and serves, or prints the version
// (version.Runtime) and returns when --version is set. Split from main
// so the flag surface is testable.
func run(args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "doctor" {
		return runDoctor(args[1:], stdout)
	}
	fs := flag.NewFlagSet("studio", flag.ContinueOnError)
	db := fs.String("db", "",
		"`sqlite://path` or `clickhouse://user:pass@host:9000/db` (default: $WEFT_DB or ./.weft/weft.db)")
	addr := fs.String("addr", "",
		"listen `address` (default: $WEFT_STUDIO_ADDR, else "+defaultAddr+"); --addr pins; without it Studio reuses a running one on the same DB or takes the next free port in 7331–7340")
	token := fs.String("token", "",
		"API token (default: $WEFT_STUDIO_TOKEN, else a generated dev token printed at start)")
	manifest := fs.String("manifest", "",
		"`path` to the app's weft.json, served at /api/manifest and checked against the latest runs (default: $WEFT_MANIFEST)")
	showVersion := fs.Bool("version", false, "print the weft version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, version.Runtime())
		return err
	}
	opts, err := manifestOptions(*manifest)
	if err != nil {
		return err
	}
	return serve(*db, wantAddr(*addr, defaultAddr), *token, stdout, opts...)
}

// want is the address the command wants and whether it is pinned.
type want struct {
	addr   string
	pinned bool
	span   int // ports an unpinned start tries (0 = listen.DefaultSpan)
}

// wantAddr resolves the listen address: --addr, then WEFT_STUDIO_ADDR
// (both pin it), else def — the one default, unpinned.
func wantAddr(flagAddr, def string) want {
	if flagAddr != "" {
		return want{addr: flagAddr, pinned: true}
	}
	if env := os.Getenv("WEFT_STUDIO_ADDR"); env != "" {
		return want{addr: env, pinned: true}
	}
	return want{addr: def}
}

// manifestOptions resolves --manifest (the flag, then WEFT_MANIFEST)
// and reads the file once, at start: studio.Manifest takes the bytes.
// No path is no option; an unreadable file is a start error, never a
// Studio silently serving without the manifest it was given.
func manifestOptions(flagPath string) ([]studio.Option, error) {
	path := flagPath
	if path == "" {
		path = os.Getenv("WEFT_MANIFEST")
	}
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--manifest: %w", err)
	}
	return []studio.Option{studio.Manifest(b)}, nil
}

// runDoctor is `studio doctor`: the flags mirror WEFT_STUDIO_URL and
// WEFT_STUDIO_TOKEN one to one; internal/doctor does the checking.
func runDoctor(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("studio doctor", flag.ContinueOnError)
	defURL := os.Getenv("WEFT_STUDIO_URL")
	if defURL == "" {
		defURL = "http://" + defaultAddr
	}
	url := fs.String("url", defURL, "the Studio to check (default: $WEFT_STUDIO_URL, else http://"+defaultAddr+")")
	// The token's default is resolved after parsing: --help must not
	// print it.
	token := fs.String("token", "", "API token (default: $WEFT_STUDIO_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		*token = os.Getenv("WEFT_STUDIO_TOKEN")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("doctor takes no arguments, got %q", fs.Args())
	}
	return doctor.Run(context.Background(), stdout, *url, *token, os.Getenv)
}

// serve applies the port policy (internal/listen, plan B2), then
// builds the server, prints where it lives, and serves until the
// process is stopped. A Studio already serving the same database on
// the wanted address is reused: one line naming it, nil (exit 0), and
// nothing opened. A busy address otherwise moves to the next free port
// of the range with one line saying which and why — unless it is
// pinned (--addr, WEFT_STUDIO_ADDR): then the busy address is the
// error. The port is chosen before the database is opened, so a reuse
// touches nothing and the banner prints the real address.
func serve(dbFlag string, w want, tokenFlag string, stdout io.Writer, extra ...studio.Option) error {
	dbPath, err := dbFile(dbFlag)
	if err != nil {
		return err
	}
	choice, err := listen.Choose(context.Background(), listen.Request{
		Addr:   w.addr,
		Pinned: w.pinned,
		Span:   w.span,
		DBPath: dbPath,
		// The probe carries the token this command was given; a dev
		// token it would generate is nobody else's.
		Token: fixedToken(tokenFlag),
	})
	if err != nil {
		return err
	}
	if choice.Reuse {
		_, _ = fmt.Fprintln(stdout, choice.ReuseLine())
		return nil
	}
	if choice.Note != "" {
		_, _ = fmt.Fprintln(stdout, "studio: "+choice.Note)
	}
	srv, err := serveBoot(dbFlag, choice.Addr, tokenFlag, stdout, extra...)
	if err != nil {
		_ = choice.Listener.Close()
		return err
	}
	err = serveOn(httpServer(choice.Addr, srv.Handler()), choice.Listener, stdout)
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

// serveOn runs the HTTP server on ln until SIGINT or SIGTERM — a graceful
// stop (the audit's P2-20: a bare ListenAndServe cut SSE streams mid-
// frame and skipped srv.Close): the listener closes at once, in-flight
// requests get a five-second grace window (an SSE stream ends when its
// request context cancels), and streams that outlive the window are
// force-closed — the studio resources close after, in serve.
func serveOn(httpSrv *http.Server, ln net.Listener, stdout io.Writer) error {
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
	go func() { errCh <- httpSrv.Serve(ln) }()
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
func serveBoot(dbFlag, addr, tokenFlag string, stdout io.Writer, extra ...studio.Option) (*server, error) {
	token := srvToken(tokenFlag)
	srv, err := newServer(dbFlag, token, extra...)
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
func newServer(dbFlag, token string, extra ...studio.Option) (srv *server, err error) {
	opts := append([]studio.Option{studio.Base("/"), studio.Token(token)}, extra...)
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
	if tok := fixedToken(tokenFlag); tok != "" {
		return tok
	}
	return studio.DevToken()
}

// fixedToken is the token the operator fixed: the flag, then
// WEFT_STUDIO_TOKEN; "" when neither is set.
func fixedToken(tokenFlag string) string {
	if tokenFlag != "" {
		return tokenFlag
	}
	return os.Getenv("WEFT_STUDIO_TOKEN")
}

// dbFile is the absolute database file --db names, the one the port
// policy compares with a running Studio's db.path: the default (the
// local sink's $WEFT_DB or ./.weft/weft.db) and sqlite://path resolve
// as obsdb/sqlite resolves them; ":memory:" and clickhouse:// name no
// file ("", which never matches). A malformed --db is newServer's
// error to report, after the port is chosen.
func dbFile(dbFlag string) (string, error) {
	var path string
	switch {
	case dbFlag == "":
		path = os.Getenv("WEFT_DB")
		if path == "" {
			path = ".weft/weft.db"
		}
	case strings.HasPrefix(dbFlag, "sqlite://"):
		path = strings.TrimPrefix(dbFlag, "sqlite://")
	}
	if path == "" || path == ":memory:" {
		return "", nil
	}
	return filepath.Abs(path)
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
