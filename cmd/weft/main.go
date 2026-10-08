// Command weft is the framework's one binary (plan B1): setup B's
// local Studio and a terminal over the Studio API, for scripts and CI.
//
//	go install github.com/weftgo/weft/cmd/weft@latest
//
//	weft studio [--addr] [--db] [--token] [--manifest] [--open] [--no-playground]
//	weft dev [studio's flags] [--no-watch] [--watch dir] [-- command args…]
//	weft runs [--agent] [--since] [--failed] [--limit] [--json]
//	weft open <run id> [--open] [--with-token]
//	weft export <run id> [--wefttest dir [--test name] [--force]] [--format json|jsonl|otlp]
//	weft doctor
//	weft version
//
// Every subcommand is a convenience over what an app or a script can
// do itself: `weft studio` is studio.New with setup B's options, and
// runs, open, export and doctor are thin clients of the Studio JSON API
// (GET /api/runs, /api/runs/{id}, /api/runs/{id}/export, /api/meta). A
// team that dislikes the CLI loses nothing.
//
// Every connection flag mirrors an environment variable one to one: --addr
// WEFT_STUDIO_ADDR, --db WEFT_DB, --token WEFT_STUDIO_TOKEN, --manifest
// WEFT_MANIFEST, --url WEFT_STUDIO_URL (the Studio the API clients talk
// to, default http://127.0.0.1:7331).
//
// # weft studio
//
// Setup B (S4.6, §10.1): the UI, OTLP ingest, SQLite and a dev token on
// 127.0.0.1:7331 — the zero-infra Studio for any language's app:
//
//	weft studio                              # serves UI + OTLP, prints a dev token
//	WEFT_STUDIO_URL=http://127.0.0.1:7331 ./my-go-app
//	OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:7331 python app.py
//
// The database defaults to the otel local sink's path ($WEFT_DB or
// ./.weft/weft.db), so the same file serves an in-process app and the
// binary. --db sqlite://path picks another file; --db
// clickhouse://user:pass@host:9000/db serves the hosted backend
// (obsdb/clickhouse). The dev token is printed at start and fixed by
// WEFT_STUDIO_TOKEN or --token. The playground is on (studio.Playground):
// inert until an app's runtime (weft/runtime) dials in; --no-playground
// turns it off. The manifest served at /api/manifest is --manifest
// (default $WEFT_MANIFEST), else the nearest weft.json from the working
// directory upward; one line says which file was loaded, or that none
// was found. --open opens the browser on the UI with the token in the
// URL fragment (a fragment never reaches a server or a Referer); it is
// on by default when stdout is a terminal, --open=false turns it off.
// The link, token included, is the opener's argument (xdg-open, open,
// rundll32): briefly visible to local users in the process list
// (/proc/*/cmdline) — acceptable for handing the user's own browser
// its link, and the reason the token is never printed.
//
// The port policy (plan B2, internal/listen): 127.0.0.1:7331 is the
// one default. When it is busy, weft studio asks GET /api/meta there
// (with --token / WEFT_STUDIO_TOKEN as the bearer when set, to loopback
// only): a Studio serving the same database file is reused — "studio
// already running at http://127.0.0.1:7331 (pid 1234), reusing", exit
// 0, no database or listener opened (--open still opens the browser on
// it) — and anything else (another program, a Studio on another
// database, a Studio whose meta this token cannot read) moves Studio to
// the next free port in 7331–7340, said in one line before the banner,
// which prints the real address. All ten busy is exit 1 naming the
// range. --addr (or WEFT_STUDIO_ADDR) pins the address: busy is exit 1
// with the address in the error, never a probe or another port.
//
// # weft runs, weft open, weft export
//
// The API over a terminal. Each takes --url (default $WEFT_STUDIO_URL,
// else http://127.0.0.1:7331) and --token (default $WEFT_STUDIO_TOKEN).
// `weft runs` prints one row per top-level run (id, agent, status,
// started, steps), newest first, from GET /api/runs; --agent and
// --failed filter on the server, --since (a duration such as 2h, or an
// RFC 3339 time) stops the paging at the first older run, --limit
// (default 50, 0 for all) caps the rows and says so on stderr when it
// hid any, --json prints the rows as Studio serves them. `weft open
// <id>` checks the run exists and prints its page, <url>/runs/<id>,
// bare: --with-token prints the #token= fragment too, --open hands
// the browser the link with the token. `weft export <id>`
// writes GET /api/runs/<id>/export to stdout (--format json, jsonl or
// otlp); with --wefttest <dir> it downloads the wefttest fixtures and
// unzips them into <dir>/<name>/, the directory wefttest.Replay(t, dir)
// reads for a test named <name> (--test, a relative name; default the
// run id), refusing a non-empty target without --force. With --force
// the target's *.json fixtures are replaced, never merged (as
// wefttest.Record replaces them): a stale one would answer for a
// request the new run never made.
//
// # weft doctor
//
// `weft doctor [--url URL] [--token TOK]` checks a running Studio and
// prints one line per check, each read from its GET /api/meta
// (internal/doctor): reachable, token accepted, the database's path and
// size, the content it stores, connected runtimes (and, when none, what
// this shell's WEFT_ENV and WEFT_STUDIO_URL say about why), the panel
// bundle's version and whether weft.json is stale against the latest
// runs. It exits 1 when a check fails — first of all an unreachable
// Studio, reported on the first line within a short timeout.
//
// # weft dev
//
// `weft dev [flags] [-- command args…]` (plan B1.2) is `weft studio`
// (in-process: the same flags, the same port policy) plus the app —
// the command after "--", default `go run .` — run with WEFT_ENV=dev
// (kept when already set non-empty), WEFT_STUDIO_URL, WEFT_STUDIO_TOKEN
// and WEFT_DB (the Studio's SQLite file, absolute; unset for
// ClickHouse) added to the environment, the last three overriding the
// shell's. Each is a plain variable the app could be given by hand:
// the command is a convenience, never a requirement. The app runs in
// its own process group; a .go change under the working directory
// (--watch dir, repeatable, replaces it; .git, node_modules, vendor,
// testdata, .weft and dist are skipped) restarts it after 300 ms of
// quiet — SIGTERM to the group, up to five seconds, SIGKILL, then the
// command again. A build failure or an app that exits is reported with
// its exit code and the next save retries; --no-watch turns watching
// off, and weft dev then exits with the app's code. Ctrl-C stops the
// app first (the same signal), then Studio; exit 0. Each start prints
// one line,
//
//	studio http://127.0.0.1:7331/#token=… · app pid 4242 · runtime rt_… registered
//
// the token in the fragment only when it was generated (a fixed one
// stays out of the log); the runtime is the first one GET
// /api/runtimes lists that was not there before the app started,
// waited for up to five seconds, else "no runtime registered yet" and
// a later line when one registers. A Studio already serving the same
// database is reused only with a fixed token: the app needs it, and a
// running Studio's generated token is not known here.
//
// `weft version` prints the weft version (version.Runtime: the module
// tag this binary was built from).
//
// Exit codes: 0 success, 1 a failure (printed as "weft: …" on stderr,
// except doctor, whose lines say it), 2 a usage error.
//
// It is a package of the framework module (ADR 0027) kept apart from
// the studio library so the library never carries what only the
// binary needs — it is the one place that imports the clickhouse
// driver.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

// defaultURL is the Studio the API clients talk to without --url or
// WEFT_STUDIO_URL.
const defaultURL = "http://" + defaultAddr

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// errUsage marks a command-line mistake: exit 2, after the message.
var errUsage = errors.New("usage")

// usageError is a command-line mistake; run exits 2 on it.
func usageError(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errUsage}, args...)...)
}

const usage = `weft — the weft framework's command line

Usage:
  weft studio  [--addr] [--db] [--token] [--manifest] [--open] [--no-playground]
  weft dev     [studio's flags] [--no-watch] [--watch dir] [-- command args…]   (default: go run .)
  weft runs    [--url] [--token] [--agent] [--since] [--failed] [--limit] [--json]
  weft open    <run id> [--url] [--token] [--open] [--with-token]
  weft export  <run id> [--url] [--token] [--format json|jsonl|otlp] [--wefttest dir [--test name] [--force]]
  weft doctor  [--url] [--token]
  weft version

Environment (each mirrors a connection flag): WEFT_STUDIO_ADDR (--addr), WEFT_DB (--db),
WEFT_STUDIO_TOKEN (--token), WEFT_MANIFEST (--manifest), WEFT_STUDIO_URL (--url).
Run "weft <command> -h" for a command's flags.
`

// run is the command line: it dispatches on the subcommand, prints a
// failure as one "weft: …" line on stderr and returns the exit code
// (0 ok, 1 failure, 2 usage). Split from main so every subcommand is
// testable.
func run(args []string, stdout, stderr io.Writer) int {
	err := dispatch(args, stdout, stderr)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, new(exitCode)):
		// weft dev --no-watch: the app's own exit code, already said.
		var code exitCode
		errors.As(err, &code)
		return int(code)
	case errors.Is(err, doctor.ErrUnhealthy):
		// A failed doctor check has printed its own lines.
		return 1
	case errors.Is(err, errUsage):
		_, _ = fmt.Fprintln(stderr, "weft:", strings.TrimPrefix(err.Error(), "usage: "))
		return 2
	case errors.Is(err, errFlag):
		// The flag package printed the error and the command's flags.
		return 2
	default:
		_, _ = fmt.Fprintln(stderr, "weft:", err)
		return 1
	}
}

// errFlag marks a flag-parse failure the flag package already printed.
var errFlag = errors.New("bad flags")

// parse parses a subcommand's flags; a parse error (already printed by
// the flag package, with the flags) is errFlag, -h is flag.ErrHelp.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errFlag, err)
	}
	return nil
}

func dispatch(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return usageError("a command is required")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "studio":
		return runStudio(rest, stdout, stderr)
	case "runs":
		return runRuns(rest, stdout, stderr)
	case "open":
		return runOpen(rest, stdout, stderr)
	case "export":
		return runExport(rest, stdout, stderr)
	case "doctor":
		return runDoctor(rest, stdout, stderr)
	case "version", "--version", "-version":
		if len(rest) > 0 {
			return usageError("version takes no arguments, got %q", rest)
		}
		_, err := fmt.Fprintln(stdout, version.Runtime())
		return err
	case "dev":
		return runDev(rest, stdout, stderr)
	case "help", "-h", "--help", "-help":
		_, _ = fmt.Fprint(stdout, usage)
		return nil
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return usageError("unknown command %q", cmd)
	}
}

// newFlags is a subcommand's flag set: errors and -h go to stderr.
func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("weft "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// runStudio is `weft studio`: setup B's server (studio.New) behind the
// port policy, with the playground on and the manifest found.
func runStudio(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("studio", stderr)
	db := fs.String("db", "",
		"`sqlite://path` or `clickhouse://user:pass@host:9000/db` (default: $WEFT_DB or ./.weft/weft.db)")
	addr := fs.String("addr", "",
		"listen `address` (default: $WEFT_STUDIO_ADDR, else "+defaultAddr+"); --addr pins; without it Studio reuses a running one on the same DB or takes the next free port in 7331–7340")
	token := fs.String("token", "",
		"API token (default: $WEFT_STUDIO_TOKEN, else a generated dev token printed at start)")
	manifest := fs.String("manifest", "",
		"`path` to the app's weft.json, served at /api/manifest and checked against the latest runs (default: $WEFT_MANIFEST, else the nearest weft.json from the working directory upward)")
	open := fs.Bool("open", stdoutIsTTY(),
		"open the browser on the UI, the token in the URL fragment (default: on when stdout is a terminal)")
	noPlayground := fs.Bool("no-playground", false,
		"turn the playground off (it is inert until an app's runtime connects)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageError("studio takes no arguments, got %q", fs.Args())
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	opts, note, err := manifestOptions(*manifest, wd)
	if err != nil {
		return err
	}
	opts = append(opts, studio.Playground(!*noPlayground))
	return serveWith(*db, wantAddr(*addr, defaultAddr), *token, stdout,
		afterBoot{notes: []string{note}, open: *open}, opts...)
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

// manifestOptions resolves the manifest: --manifest, then
// WEFT_MANIFEST, else the nearest weft.json from dir upward. The file
// is read once, at start (studio.Manifest takes the bytes); note is the
// one line that says which file was loaded, or that none was found. A
// named file that cannot be read is a start error, never a Studio
// silently serving without the manifest it was given.
func manifestOptions(flagPath, dir string) (opts []studio.Option, note string, err error) {
	path, from := flagPath, "--manifest"
	if path == "" {
		path, from = os.Getenv("WEFT_MANIFEST"), "WEFT_MANIFEST"
	}
	if path == "" {
		path, from = findUpward(dir, "weft.json"), "found upward"
		if path == "" {
			return nil, "studio: no weft.json from " + dir + " upward; no manifest (--manifest names one)", nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if from == "found upward" {
			return nil, "", fmt.Errorf("manifest %s: %w", path, err)
		}
		return nil, "", fmt.Errorf("%s: %w", from, err)
	}
	// The top-level shape weft.Manifest writes: an object with the
	// format version and the agents. A file that is not is a start
	// error, never served verbatim.
	var shape struct {
		Weft   int               `json:"weft"`
		Agents []json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(b, &shape); err != nil {
		return nil, "", fmt.Errorf("manifest %s: does not parse: %w", path, err)
	}
	return []studio.Option{studio.Manifest(b)}, "studio: manifest " + path + " (" + from + ")", nil
}

// findUpward is the nearest regular file called name in dir or one of
// its parents, "" when there is none up to the filesystem root.
func findUpward(dir, name string) string {
	for {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// studioURL resolves the Studio an API client talks to: --url, then
// WEFT_STUDIO_URL, then the one default address.
func studioURL() string {
	if u := os.Getenv("WEFT_STUDIO_URL"); u != "" {
		return u
	}
	return defaultURL
}

// clientFlags adds --url and --token to an API client's flag set. The
// token's default is resolved after parsing (apiToken): --help must not
// print it.
func clientFlags(fs *flag.FlagSet) (base, token *string) {
	base = fs.String("url", studioURL(), "the Studio to talk to (default: $WEFT_STUDIO_URL, else "+defaultURL+")")
	token = fs.String("token", "", "API token (default: $WEFT_STUDIO_TOKEN)")
	return base, token
}

// apiToken is --token, else WEFT_STUDIO_TOKEN.
func apiToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	return os.Getenv("WEFT_STUDIO_TOKEN")
}

// runDoctor is `weft doctor`: the flags mirror WEFT_STUDIO_URL and
// WEFT_STUDIO_TOKEN one to one; internal/doctor does the checking.
func runDoctor(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("doctor", stderr)
	base, token := clientFlags(fs)
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageError("doctor takes no arguments, got %q", fs.Args())
	}
	return doctor.Run(context.Background(), stdout, *base, apiToken(*token), os.Getenv)
}

// afterBoot is what `weft studio` adds around the port policy: lines
// printed after the banner, and whether to open the browser on the UI.
type afterBoot struct {
	notes []string
	open  bool
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
	return serveWith(dbFlag, w, tokenFlag, stdout, afterBoot{}, extra...)
}

// serveWith is serve plus afterBoot: its notes print after the banner
// (never on a reuse, whose one line is the whole output), and with
// open the browser opens on the UI once the listener is bound — on a
// reuse, on the running Studio, the fixed token in the fragment when
// there is one.
func serveWith(dbFlag string, w want, tokenFlag string, stdout io.Writer, after afterBoot, extra ...studio.Option) error {
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
		if after.open {
			openBrowser(stdout, uiLink(choice.Addr, fixedToken(tokenFlag)))
		}
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
	for _, n := range after.notes {
		if n != "" {
			_, _ = fmt.Fprintln(stdout, n)
		}
	}
	if after.open {
		openBrowser(stdout, uiLink(choice.Addr, srv.token))
	}
	err = serveOn(httpServer(choice.Addr, srv.Handler()), choice.Listener, stdout)
	if cerr := srv.Close(); err == nil {
		err = cerr
	}
	return err
}

// uiLink is the UI's address, the token in the fragment when there is
// one: the UI adopts it, and a fragment never reaches a server.
func uiLink(addr, token string) string {
	link := "http://" + addr + "/"
	if token != "" {
		link += "#token=" + url.QueryEscape(token)
	}
	return link
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
	db    io.Closer // nil when the studio server owns its database
	token string    // the one token serveBoot resolved (the wall's)
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
	return serveUntil(httpSrv, ln, stdout, stop)
}

// serveUntil is serveOn's body: it serves on ln until stop delivers a
// signal (or Serve fails), then stops gracefully. `weft dev` owns its
// signals (the app stops first) and hands its own channel here.
func serveUntil(httpSrv *http.Server, ln net.Listener, stdout io.Writer, stop <-chan os.Signal) error {
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
	srv.token = token
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
	return &server{Server: studio.New(opts...), db: own, token: token}, nil
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
