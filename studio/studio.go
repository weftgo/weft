package studio

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/version"
)

// Version is the version Studio reports as api/meta's studio_version
// and the devtools panel is built against: the framework module's tag,
// [version.Version] — one version for every layer. It moves with the
// module's release, nothing else.
const Version = version.Version

const defaultTitle = "weft studio"

// Option configures New.
type Option func(*config)

type config struct {
	db           obsdb.DB
	dbSet        bool   // DB appeared among the options (DB(nil) is an error)
	dbPath       string // set by Open: opened at New time
	live         obsdb.Hub
	noIngest     bool
	ingestToken  string
	token        string
	origins      []string
	base         string
	title        string
	manifest     []byte
	capabilities []string
	playground   bool
}

// DB serves Studio over db (setup A: DB(otel.LocalDB()) — the same
// handle weft/otel's Local destination writes, so the UI reads what
// the run wrote, live, through the handle's own hub). Without DB or
// Open, New opens the history database at $WEFT_DB or ./.weft/weft.db
// (history only: a second handle on the file, no live lane — passing
// the pipeline's handle is what makes setup A live). A nil db panics
// at New time — a Studio with nothing to read is a construction
// error.
func DB(db obsdb.DB) Option {
	return func(c *config) { c.db, c.dbSet, c.dbPath = db, true, "" }
}

// Open is shorthand for DB(sqlite.Open(path)): Studio over an obsdb
// sqlite file, created when missing. Opening happens at New time and
// panics on failure — a Studio that cannot reach its database is a
// construction error, not a serving one.
func Open(path string) Option {
	return func(c *config) { c.dbPath = path }
}

// Live serves the live stream from h (S4.1). The default is the DB's
// own hub when it implements Hub() — setup A's sub-100 ms lane — else
// an in-process obsdb.NewHub() fed by ingest.
func Live(h obsdb.Hub) Option {
	return func(c *config) { c.live = h }
}

// NoIngest turns the OTLP ingest routes off: a read-only Studio. The
// ingest capability disappears from api/meta with them.
func NoIngest() Option {
	return func(c *config) { c.noIngest = true }
}

// IngestToken requires `Authorization: Bearer <tok>` on the OTLP
// ingest routes. Without one, ingest answers loopback peers only
// (setup B's dev mode) — a Studio bound wide without a token refuses
// remote exporters (S4.4).
func IngestToken(tok string) Option {
	return func(c *config) { c.ingestToken = tok }
}

// Token protects the JSON API and the panel-token mint with a bearer
// token, and keys the panel tokens' HMAC signatures (setup C's signing
// key). Without it the API is open to a loopback Host or one
// AllowOrigins names (setup A: embedded, same origin; any other Host
// is refused, the DNS-rebinding guard).
func Token(tok string) Option {
	return func(c *config) { c.token = tok }
}

// AllowOrigins permits these origins on the API and ingest routes
// (CORS for a panel served from another origin). The default, when a
// Token is configured, is localhost and 127.0.0.1 on any port; setup
// A (same origin, no token) sends no CORS headers unless origins are
// listed. Without a Token each origin's host:port is also a Host the
// API answers besides the loopback names (an app served at
// http://myapp.internal:8080 lists exactly that); "*" admits every
// Host.
func AllowOrigins(origins ...string) Option {
	return func(c *config) { c.origins = append(c.origins, origins...) }
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

// Manifest supplies core.Manifest bytes for the agent and tool cards.
// Without it api/manifest answers 404 and the UI hides the Agents nav.
func Manifest(json []byte) Option {
	return func(c *config) { c.manifest = json }
}

// Title overrides the page title (default "weft studio").
func Title(s string) Option {
	return func(c *config) { c.title = s }
}

// Capabilities declares named API capabilities the backing server
// provides beyond what the registered route groups already report
// (ADR 0018 §8). The core groups contribute their own names — live,
// ingest, auth — computed from what is actually registered, never
// hard-coded; this option is for a hosting wrapper's own verbs.
func Capabilities(names ...string) Option {
	return func(c *config) {
		c.capabilities = append(c.capabilities, names...)
	}
}

// Playground turns the runtime link server and the playground routes
// on (studio/playground.go registers its group, which this option
// enables); without it meta.capabilities does not list the playground.
func Playground(on bool) Option {
	return func(c *config) { c.playground = on }
}

// Server is one Studio: the embedded UI, the JSON API, the live
// stream and — unless disabled — OTLP ingest, over one obsdb.DB.
// Build it with New; serve it with Handler.
type Server struct {
	config
	groups []routeGroup
	mux    *http.ServeMux

	// live (config.live, set by New from the Live default) is the hub
	// /api/live streams from (S4.1).
	now func() time.Time

	// ownsDB: New opened the database itself (Open or the default
	// path), so Close closes it. A DB handed in through DB(...) stays
	// the caller's — otel owns it in setup A.
	ownsDB bool

	// runtimeSrv is the runtime link server Playground(true) builds
	// (registerPlayground, one per Server); Runtime() returns it. Nil
	// without the option, and the playground and runtime-link routes
	// do not exist.
	runtimeSrv *RuntimeServer

	shell []byte
	csp   string
}

// New builds a Studio server over one obsdb.DB (S4.1). Handler()
// serves the UI, the panel, the JSON API, the live stream and —
// unless disabled — OTLP ingest. Runtime() is the runtime link
// server's in-process side, which weft/runtime takes in setup A; nil
// unless Playground(true) built it.
func New(opts ...Option) *Server {
	c := config{base: "/studio/", title: defaultTitle}
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	if c.dbSet && c.db == nil {
		panic("studio: New called with a nil DB")
	}
	s := &Server{config: c, now: time.Now, mux: http.NewServeMux()}
	if c.db == nil {
		path := c.dbPath
		if path == "" {
			path = defaultDBPath()
		}
		db, err := sqlite.Open(path)
		if err != nil {
			panic("studio: open " + path + ": " + err.Error())
		}
		s.db, s.ownsDB = db, true
	}
	if s.live == nil {
		if h, ok := s.db.(interface{ Hub() obsdb.Hub }); ok {
			s.live = h.Hub()
		} else {
			s.live = obsdb.NewHub()
		}
	}
	s.prepareShell()
	s.registerGroups()
	s.mux.HandleFunc("/", s.serveUI)
	return s
}

// Handler serves Studio. Mount it under a prefix with
// http.StripPrefix, or at the root of its own mux:
//
//	mux.Handle("/studio/", http.StripPrefix("/studio",
//		studio.Handler(studio.DB(db))))
//
// The handler does not bind a port — the caller chooses the address.
// Bind loopback, or configure Token, before exposing it wider.
func (s *Server) Handler() http.Handler { return s }

// Handler is New(opts...).Handler(): the one-liner for setups A and B
// when the playground is not in play.
func Handler(opts ...Option) http.Handler {
	return New(opts...).Handler()
}

// Runtime returns the runtime link server's in-process side (what
// weft/runtime's runtime.Local takes): the server Playground(true)
// built, nil without the option — and without it the playground
// routes do not exist either.
func (s *Server) Runtime() *RuntimeServer { return s.runtimeSrv }

// Close releases what the server owns: the database when New opened
// it (Open or the default path). A DB passed through DB(...) stays
// its owner's — closing otel's handle out from under the pipeline is
// not Studio's call.
func (s *Server) Close() error {
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

// ingestAuthorized is S4.4's ingest auth: the configured ingest token
// as a bearer, or — with none configured — loopback peers only
// (setup B's dev mode; a Studio bound wide without a token refuses
// remote exporters).
//
// "Loopback" is the socket's peer, and two kinds of remote caller
// arrive from it too, so the open rule refuses both:
//
//   - a request a reverse proxy forwarded (it says so: Forwarded,
//     X-Forwarded-For, X-Real-IP) — behind a proxy on the same host
//     every client is a loopback peer, and the open rule would hand
//     ingest to all of them; such a deployment sets IngestToken;
//   - a browser page on an origin nobody allowed (a POST carries its
//     Origin) — any site can aim a form or a DNS-rebound fetch at
//     127.0.0.1. Localhost origins and AllowOrigins pass, the same
//     rule CORS applies.
func (s *Server) ingestAuthorized(r *http.Request) bool {
	if s.ingestToken != "" {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		return ok && tokenEqual(tok, s.ingestToken)
	}
	if !remoteIsLoopback(r) {
		return false
	}
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Real-Ip"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(s.origins, true, origin) {
		return false
	}
	return true
}

// remoteIsLoopback reports whether the request's peer is on this
// machine, which is the best a library can know of its bind: a
// loopback bind only ever sees loopback peers. All of 127.0.0.0/8,
// ::1 and the IPv4-mapped form (a dual-stack listener reports
// ::ffff:127.0.0.1) are loopback.
func remoteIsLoopback(r *http.Request) bool {
	return isLoopbackName(hostOnly(r.RemoteAddr))
}

// ServeHTTP routes one request: the registered route groups (the API,
// the live stream, ingest) on the mux, then the UI — embedded files
// first, the SPA shell for every other path (deep links survive
// reload).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", s.csp)
	s.routed().ServeHTTP(w, r)
}

// routed wraps the mux with the cross-cutting concerns in order
// (S4.6): CORS on the API and ingest trees, token auth on the API
// tree only — ingest carries its own token (S4.4).
func (s *Server) routed() http.Handler {
	return s.cors(prefixRouter{
		prefixes: []prefixRoute{
			{"/api/", s.auth(s.mux)},
			{"/v1/", s.mux},
		},
		def: s.mux,
	})
}

// prefixRoute routes one path prefix to one handler.
type prefixRoute struct {
	prefix  string
	handler http.Handler
}

// prefixRouter sends each request to the first prefix that fits, else
// to the default (the UI).
type prefixRouter struct {
	prefixes []prefixRoute
	def      http.Handler
}

func (p prefixRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, pr := range p.prefixes {
		if strings.HasPrefix(r.URL.Path, pr.prefix) {
			pr.handler.ServeHTTP(w, r)
			return
		}
	}
	p.def.ServeHTTP(w, r)
}

// serveUI answers with the embedded UI: a build file when the path
// names one, else the shell (the SPA fallback). GET only — the UI is
// static.
func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	// Unknown API and ingest paths are 404s in the error shape, never
	// the SPA shell: a mis-routed API call must fail loudly, not
	// hydrate. (Registered routes never reach here; a wrong-method
	// request got ServeMux's 405 with Allow instead.)
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") {
		s.serveAPINotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed",
			"the studio UI is static: GET only")
		return
	}
	if s.serveFile(w, r, path) {
		return
	}
	if strings.HasPrefix(path, "/assets/") {
		// A missing hashed asset (a stale tab after an upgrade) is a
		// 404, never the HTML shell under a script's name.
		writeError(w, r, http.StatusNotFound, "not_found", "no asset "+path)
		return
	}
	s.serveShell(w, r)
}

// defaultDBPath is S4.1's default history database: $WEFT_DB, else
// ./.weft/weft.db.
func defaultDBPath() string {
	if p := os.Getenv("WEFT_DB"); p != "" {
		return p
	}
	return ".weft/weft.db"
}
