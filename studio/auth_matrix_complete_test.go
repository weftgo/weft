package studio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The auth matrix's completeness check: every route Studio registers
// has a TestAuthMatrix row, and every row reaches a registered route
// (a row marked noRoute pins the API's 404 and must reach none). The
// registered set is read, not listed: the mux patterns are the string
// literals the package's sources pass to HandleFunc / Handle / the
// runtime link's mount (each one then resolved on the matrix's own
// server, so a pattern a group leaves unmounted is caught too), and
// the two subtrees' sub-routes are the live tables their dispatchers
// walk (Server.runRoutes, Server.sessionSubRoutes).

// matrixRow is what the check needs of one TestAuthMatrix row: its
// name, its method and the concrete path it requests.
type matrixRow struct {
	name, method, path string
	noRoute            bool
}

// muxPattern matches a ServeMux pattern literal: an optional method,
// then a path.
var muxPattern = regexp.MustCompile(`^((GET|HEAD|POST|PUT|PATCH|DELETE) )?/`)

// sourcePatterns returns every mux pattern literal the non-test Go
// files under the studio package directory register (examples/ is a
// program with a mux of its own, web/ the UI's sources). A registering
// call whose pattern is not a literal is a problem — the check could
// not see it — except the runtime link's mount helper, whose pattern
// parameter is fed by the literals at its call sites.
func sourcePatterns(t *testing.T) (patterns []string, problems []string) {
	t.Helper()
	skip := map[string]bool{"examples": true, "web": true, "dist": true, "testdata": true, "screenshots": true, "node_modules": true}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			var fn string
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				fn = f.Sel.Name
			case *ast.Ident:
				fn = f.Name
			}
			if fn != "HandleFunc" && fn != "Handle" && fn != "mount" {
				return true
			}
			switch a := call.Args[0].(type) {
			case *ast.BasicLit:
				if a.Kind != token.STRING {
					return true
				}
				p, err := strconv.Unquote(a.Value)
				if err != nil || !muxPattern.MatchString(p) {
					return true
				}
				patterns = append(patterns, p)
			case *ast.Ident:
				if a.Name != "pattern" { // MountGuarded's mount(pattern, h)
					problems = append(problems, fset.Position(call.Pos()).String()+": a route registered with a non-literal pattern ("+a.Name+"): the auth matrix's completeness check cannot see it")
				}
			default:
				problems = append(problems, fset.Position(call.Pos()).String()+": a route registered with a non-literal pattern: the auth matrix's completeness check cannot see it")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(patterns)
	return slices.Compact(patterns), problems
}

// wildcard is a ServeMux path wildcard ({name} or {name...}).
var wildcard = regexp.MustCompile(`\{[^}]*\}`)

// patternRequest builds a request a pattern matches.
func patternRequest(pattern string) *http.Request {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		method, path = http.MethodGet, pattern
	}
	return httptest.NewRequest(method, wildcard.ReplaceAllString(path, "x"), nil)
}

// subtreeKey is the route key of a subtree pattern's own item: the id
// that follows its prefix.
func subtreeKey(pattern string) string { return pattern + "{id}" }

// registeredRoutes is every route the matrix's server registers, as
// route keys: an exact pattern as written, a subtree pattern as
// <pattern>{id}, its sub-routes as <pattern>{id}/<ext>[/{item}], and
// the UI's catch-all "/".
func registeredRoutes(t *testing.T, srv *Server) (routes map[string]bool, problems []string) {
	t.Helper()
	patterns, problems := sourcePatterns(t)
	if len(patterns) == 0 {
		t.Fatal("no route patterns found in the studio sources")
	}
	routes = map[string]bool{}
	for _, p := range patterns {
		if _, got := srv.mux.Handler(patternRequest(p)); got != p {
			problems = append(problems, "route "+p+" is registered in the sources but the matrix's server resolves it to "+strconv.Quote(got)+" (a group the matrix's options leave off?)")
			continue
		}
		switch {
		case p == "/":
			routes["/"] = true
		case strings.HasSuffix(p, "/"):
			routes[subtreeKey(p)] = true
			switch p {
			case "GET /api/runs/":
				for _, sub := range srv.runRoutes {
					if sub.item != nil {
						routes[subtreeKey(p)+"/"+sub.ext+"/{item}"] = true
					} else {
						routes[subtreeKey(p)+"/"+sub.ext] = true
					}
				}
			case "GET /api/sessions/":
				for _, sub := range srv.sessionSubRoutes() {
					routes[subtreeKey(p)+"/"+sub.ext] = true
				}
			}
		default:
			routes[p] = true
		}
	}
	return routes, problems
}

// childRunID is a run id's shape: a root id, then <step>/<callID>
// pairs for each subagent level (core's id rule).
var childRunID = regexp.MustCompile(`^[^/]+(/[0-9]+/[^/]+)*$`)

// routeOf names the registered route a request reaches, the way the
// mux and the two subtree dispatchers route it; "" when it reaches
// none (the API's 404, or ServeMux's 405).
func routeOf(srv *Server, method, target string) string {
	path, _, _ := strings.Cut(target, "?")
	_, pat := srv.mux.Handler(httptest.NewRequest(method, path, nil))
	switch {
	case pat == "":
		return ""
	case pat == "/":
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") || path == "/api" {
			return "" // serveUI's API 404
		}
		return "/"
	case !strings.HasSuffix(pat, "/"):
		return pat
	}
	_, prefix, _ := strings.Cut(pat, " ")
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return ""
	}
	switch pat {
	case "GET /api/runs/": // serveRunRoutes' order
		for _, sub := range srv.runRoutes {
			if sub.item != nil {
				i := strings.LastIndexByte(rest, '/')
				if i < 0 {
					continue
				}
				if id, ok := strings.CutSuffix(rest[:i], "/"+sub.ext); ok && id != "" && rest[i+1:] != "" {
					return subtreeKey(pat) + "/" + sub.ext + "/{item}"
				}
				continue
			}
			if id, ok := strings.CutSuffix(rest, "/"+sub.ext); ok && id != "" {
				return subtreeKey(pat) + "/" + sub.ext
			}
		}
		if !childRunID.MatchString(rest) {
			return "" // a stale sub-route read as a run id
		}
	case "GET /api/sessions/": // serveSessionRoutes' order
		for _, sub := range srv.sessionSubRoutes() {
			if sid, ok := strings.CutSuffix(rest, "/"+sub.ext); ok && sid != "" && !strings.Contains(sid, "/") {
				return subtreeKey(pat) + "/" + sub.ext
			}
		}
		if strings.Contains(rest, "/") {
			return ""
		}
	case "DELETE /api/runs/":
		if !childRunID.MatchString(rest) {
			return ""
		}
	default:
		if strings.Contains(rest, "/") {
			return ""
		}
	}
	return subtreeKey(pat)
}

// authMatrixGaps is the check: each registered route with no row, and
// each row whose route is not registered (or, marked noRoute, is).
func authMatrixGaps(srv *Server, routes map[string]bool, rows []matrixRow) []string {
	var gaps []string
	covered := map[string]bool{}
	for _, r := range rows {
		got := routeOf(srv, r.method, r.path)
		switch {
		case r.noRoute && got != "":
			gaps = append(gaps, "TestAuthMatrix row "+strconv.Quote(r.name)+" is marked noRoute but reaches route "+got)
		case r.noRoute:
		case got == "" || !routes[got]:
			gaps = append(gaps, "TestAuthMatrix row "+strconv.Quote(r.name)+" ("+r.method+" "+r.path+") names no registered route")
		default:
			covered[got] = true
		}
	}
	keys := make([]string, 0, len(routes))
	for k := range routes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !covered[k] {
			gaps = append(gaps, "route "+k+" has no TestAuthMatrix row: add one for every identity (studio/auth_matrix_test.go)")
		}
	}
	return gaps
}

// TestAuthMatrixCompletenessCheck pins the check itself: a row per
// registered route passes; a route with no row and a row naming no
// route are each reported by name.
func TestAuthMatrixCompletenessCheck(t *testing.T) {
	srv := New(Open(t.TempDir()+"/c.db"), Playground(true), Token("srv"), IngestToken("ing"))
	t.Cleanup(func() { _ = srv.Close() })
	routes, problems := registeredRoutes(t, srv)
	for _, p := range problems {
		t.Error(p)
	}
	// Every kind the sources register is in the set.
	for _, k := range []string{"/", "GET /api/meta", "POST /v1/traces", "POST /api/runtime/register",
		"GET /api/runs/{id}", "GET /api/runs/{id}/events", "GET /api/runs/{id}/steps/{item}",
		"GET /api/sessions/{id}/timeline", "GET /api/sessions/{id}/public_id", "DELETE /api/sessions/{id}",
		"PUT /api/agents/{name}/proposal"} {
		if !routes[k] {
			t.Errorf("registered routes lack %s: %v", k, routes)
		}
	}
	var rows []matrixRow
	for k := range routes {
		method, path, ok := strings.Cut(k, " ")
		if !ok {
			method, path = http.MethodGet, k
		}
		path = strings.ReplaceAll(path, "{id}", "run_x")
		rows = append(rows, matrixRow{name: k, method: method, path: wildcard.ReplaceAllString(path, "0")})
	}
	rows = append(rows, matrixRow{name: "GET /api/nope", method: http.MethodGet, path: "/api/nope", noRoute: true})
	if gaps := authMatrixGaps(srv, routes, rows); len(gaps) != 0 {
		t.Fatalf("a row per route: gaps %q", gaps)
	}

	var dropped []matrixRow
	for _, r := range rows {
		if r.name != "GET /api/runs/{id}/logs" {
			dropped = append(dropped, r)
		}
	}
	dropped = append(dropped,
		matrixRow{name: "GET /api/runs/{id}/gone", method: http.MethodGet, path: "/api/runs/run_x/gone"},
		matrixRow{name: "GET /api/sessions/{id}/gone", method: http.MethodGet, path: "/api/sessions/s_x/gone"},
		matrixRow{name: "POST /api/gone", method: http.MethodPost, path: "/api/gone"},
		matrixRow{name: "GET /api/meta (noRoute)", method: http.MethodGet, path: "/api/meta", noRoute: true})
	got := authMatrixGaps(srv, routes, dropped)
	want := []string{
		`TestAuthMatrix row "GET /api/runs/{id}/gone" (GET /api/runs/run_x/gone) names no registered route`,
		`TestAuthMatrix row "GET /api/sessions/{id}/gone" (GET /api/sessions/s_x/gone) names no registered route`,
		`TestAuthMatrix row "POST /api/gone" (POST /api/gone) names no registered route`,
		`TestAuthMatrix row "GET /api/meta (noRoute)" is marked noRoute but reaches route GET /api/meta`,
		`route GET /api/runs/{id}/logs has no TestAuthMatrix row: add one for every identity (studio/auth_matrix_test.go)`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("gaps =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
