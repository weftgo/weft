package studio

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// Version is the studio module's tag, reported by api/meta. It moves
// when the module is released, nothing else.
const Version = "v0.2.1"

const defaultTitle = "weft studio"

// Option configures Handler.
type Option func(*config)

type config struct {
	db           obsdb.DB
	dbSet        bool   // DB appeared among the options (DB(nil) is an error)
	dbPath       string // set by Open: opened at Handler time
	base         string
	title        string
	manifest     []byte
	capabilities []string
}

// DB serves Studio over db (setup A: DB(otel.LocalDB()) — the same
// handle weft/otel's Local destination writes, so the UI reads what the
// run wrote, live). Without DB or Open, Handler opens the history
// database at $WEFT_DB or ./.weft/weft.db (S4.1's default). A nil db
// panics at Handler time — a Studio with nothing to read is a
// construction error.
func DB(db obsdb.DB) Option {
	return func(c *config) { c.db, c.dbSet, c.dbPath = db, true, "" }
}

// Open is shorthand for DB(sqlite.Open(path)): Studio over an obsdb
// sqlite file, created when missing. Opening happens at Handler time
// and panics on failure — a Studio that cannot reach its database is a
// construction error, not a serving one.
func Open(path string) Option {
	return func(c *config) { c.dbPath = path }
}

// Base is the URL path Studio is mounted at, with leading and trailing
// slash. Default "/studio/". It is written into the shell's
// <base href> per request and becomes the router's basepath, so the
// same bundle mounts anywhere (ADR 0018 §6).
func Base(path string) Option {
	return func(c *config) {
		p := path
		if p == "" {
			p = "/"
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		if !strings.HasSuffix(p, "/") {
			p += "/"
		}
		c.base = p
	}
}

// Manifest supplies weft.Manifest bytes for the agent and tool cards.
// Without it api/manifest answers 404 and the UI hides the Agents nav.
func Manifest(json []byte) Option {
	return func(c *config) { c.manifest = json }
}

// Title overrides the page title (default "weft studio").
func Title(s string) Option {
	return func(c *config) { c.title = s }
}

// Capabilities declares named API capabilities the backing server
// provides beyond the open Handler (ADR 0018 §8). The open handler
// reports none: the UI gates every deployment-specific screen on a
// capability, so a hosted server wraps Handler, adds its auth and
// routes, and declares what it carries — there is no hosted-only UI
// build, and no hostname checks or build flags anywhere.
func Capabilities(names ...string) Option {
	return func(c *config) {
		c.capabilities = append(c.capabilities, names...)
	}
}

// Handler serves Studio: the embedded UI and its read-only JSON API
// over one obsdb.DB (S4.1's interim shape; the full Server — live
// stream, ingest, playground — is step 6). Mount it under a prefix
// with http.StripPrefix, or at the root of its own mux:
//
//	mux.Handle("/studio/", http.StripPrefix("/studio", studio.Handler(studio.DB(db))))
//
// The handler does not bind a port — the caller chooses the address;
// bind loopback until token auth exists (features doc L4).
func Handler(opts ...Option) http.Handler {
	c := config{base: "/studio/", title: defaultTitle, capabilities: []string{}}
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	if c.dbSet && c.db == nil {
		panic("studio: Handler called with a nil DB")
	}
	if c.db == nil {
		path := c.dbPath
		if path == "" {
			path = defaultDBPath()
		}
		db, err := sqlite.Open(path)
		if err != nil {
			panic("studio: open " + path + ": " + err.Error())
		}
		c.db = db
	}
	a := &app{
		config: c,
		now:    time.Now,
	}
	a.prepareShell()
	return a
}

// defaultDBPath is S4.1's default history database: $WEFT_DB, else
// ./.weft/weft.db.
func defaultDBPath() string {
	if p := os.Getenv("WEFT_DB"); p != "" {
		return p
	}
	return ".weft/weft.db"
}

// app is one Handler's serving state: the resolved configuration
// (including the database under config.db, reached as a.db), the
// shell bytes with the CSP for exactly those bytes, and the clock.
type app struct {
	config
	now func() time.Time

	shell []byte
	csp   string
}

// ServeHTTP routes one request: the JSON API under api/, embedded
// files, and the shell for every other GET (the SPA fallback that
// keeps deep links alive on reload). GET only — the API is read-only.
func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", a.csp)

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed",
			"studio is read-only: GET only")
		return
	}
	path := r.URL.Path
	if path == "" {
		path = "/"
	}

	if strings.HasPrefix(path, "/api/") {
		a.serveAPI(w, r, path)
		return
	}
	if a.serveFile(w, r, path) {
		return
	}
	if strings.HasPrefix(path, "/assets/") {
		// A missing hashed asset (a stale tab after an upgrade) is a
		// 404, never the HTML shell under a script's name.
		writeError(w, r, http.StatusNotFound, "not_found", "no asset "+path)
		return
	}
	a.serveShell(w, r)
}
