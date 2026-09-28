package studio

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"html"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// dist is the committed web build (ADR 0018 §3): contributors rebuild
// it from web/ with Bun (make studio-build); users of the module get
// it embedded, and the freshness gate (make studio-check) keeps the
// two in sync.
//
//go:embed all:dist
var dist embed.FS

var (
	// inlineScript finds the shell's inline <script> blocks — each one
	// needs its sha256 in the CSP (only external scripts would need no
	// hash, and the theme bootstrap must run before paint, so it stays
	// inline).
	inlineScript = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)

	titleTag = regexp.MustCompile(`(?is)<title[^>]*>.*?</title>`)

	// baseTag is the rewrite target the shell template must carry
	// (<base href="/">); serveShell replaces it with the mount base.
	baseTag = []byte(`<base href="/">`)
)

// prepareShell resolves the shell bytes once: the embedded
// dist/index.html, its title swapped for the configured one, and the
// CSP for exactly those bytes — inline script hashes included, so the
// build can change the bootstrap without a Go change (plan §2).
func (a *app) prepareShell() {
	shell, err := dist.ReadFile("dist/index.html")
	if err != nil {
		panic("studio: dist/index.html missing — the committed web build is required (make studio-build)")
	}
	if a.title != "" {
		if t := titleTag.FindIndex(shell); t != nil {
			shell = bytes.Replace(shell, shell[t[0]:t[1]],
				[]byte("<title>"+html.EscapeString(a.title)+"</title>"), 1)
		}
	}
	a.shell = shell
	a.csp = cspFor(shell)
}

// cspFor builds the Content-Security-Policy for the shell:
// self-only everywhere, data: images, inline styles (Tailwind's
// runtime needs them), and scripts from self plus a sha256 per inline
// block — never 'unsafe-inline' for scripts (plan §2).
func cspFor(shell []byte) string {
	csp := "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'"
	for _, m := range inlineScript.FindAllSubmatch(shell, -1) {
		attrs, body := m[1], m[2]
		if strings.Contains(strings.ToLower(string(attrs)), "src") {
			continue // external script, covered by 'self'
		}
		if len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		// The HTML parser replaces NUL bytes in script text with
		// U+FFFD, and the browser hashes the parsed text — so the
		// router's "__root__\x00" match id must be normalized the
		// same way before hashing.
		body = bytes.ReplaceAll(body, []byte{0}, []byte("\uFFFD"))
		sum := sha256.Sum256(body)
		csp += " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	return csp
}

// serveFile answers with an embedded build file when the path names
// one (anything but the shell itself): hashed assets under assets/
// are immutable; every other file is revalidated. Returns false when
// the path is not a file — the caller serves the shell instead.
func (a *app) serveFile(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	name := strings.TrimPrefix(path.Clean(urlPath), "/")
	if name == "" || name == "index.html" {
		return false
	}
	f, err := dist.Open("dist/" + name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return false
	}
	if strings.HasPrefix(urlPath, "/assets/") {
		// Content-hashed filenames: the bytes at a name never change.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, dist, "dist/"+name)
	return true
}

// serveShell answers with the SPA shell, its <base href> rewritten to
// the mount base — the history fallback that keeps a deep link like
// /studio/runs/r_123 alive on reload (plan §2).
func (a *app) serveShell(w http.ResponseWriter, r *http.Request) {
	body := a.shell
	if bytes.Contains(body, baseTag) {
		body = bytes.Replace(body, baseTag,
			[]byte(`<base href="`+html.EscapeString(a.base)+`">`), 1)
	} else if i := bytes.Index(body, []byte("<head>")); i >= 0 {
		// The build dropped the marker: inject rather than misroute.
		i += len("<head>")
		body = append(append([]byte{}, body[:i]...),
			append([]byte(`<base href="`+html.EscapeString(a.base)+`">`), body[i:]...)...)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// files is a helper for tests: every path in dist other than the
// shell itself.
func files() []string {
	var out []string
	_ = fs.WalkDir(dist, "dist", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != "dist/index.html" {
			out = append(out, strings.TrimPrefix(p, "dist/"))
		}
		return nil
	})
	return out
}
