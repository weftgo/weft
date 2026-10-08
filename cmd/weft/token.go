package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/studio"
)

// The dev token (plan B3): stable per database. On a database file's
// first start the token is 32 random bytes (base64url) written beside
// it, <db>.token (.weft/weft.db.token by default), mode 0600; every
// later start on that file serves the same token, so a restart keeps
// the browser's tab, the app's WEFT_STUDIO_TOKEN and the discovery
// file valid, and a second start's port probe can carry it (the reuse
// verdict). --rotate-token writes a new one. --token and
// WEFT_STUDIO_TOKEN override it and leave the file alone. A database
// with no file (":memory:", ClickHouse) gets a token generated for
// this process alone, printed at start as before.
//
// The stable token is the panel tokens' signing key, like a fixed one:
// it stays out of the banner, the one line and every printed link.

// tokenSource is where the served token came from.
type tokenSource int

const (
	tokenGenerated tokenSource = iota // this process alone; printed
	tokenFromFlag                     // --token
	tokenFromEnv                      // WEFT_STUDIO_TOKEN
	tokenFromFile                     // <db>.token, stable per database
)

// resolvedToken is the one token a start serves, and its provenance.
type resolvedToken struct {
	value   string
	source  tokenSource
	path    string // tokenFromFile: the file
	created bool   // tokenFromFile: written by this start (first start)
	rotated bool   // tokenFromFile: rewritten by --rotate-token
}

// printable reports whether the token may be printed: only one
// generated for this process — nobody else holds it.
func (r resolvedToken) printable() bool { return r.source == tokenGenerated }

// resolveToken is the token a start serves: --token, then
// WEFT_STUDIO_TOKEN, then the database's token file (created on first
// use; rewritten with rotate), else a generated one.
func resolveToken(dbFlag, flagTok string, rotate bool) (resolvedToken, error) {
	if flagTok != "" {
		return resolvedToken{value: flagTok, source: tokenFromFlag}, nil
	}
	if env := os.Getenv("WEFT_STUDIO_TOKEN"); env != "" {
		return resolvedToken{value: env, source: tokenFromEnv}, nil
	}
	dbPath, err := dbFile(dbFlag)
	if err != nil {
		return resolvedToken{}, err
	}
	if dbPath == "" {
		return resolvedToken{value: studio.DevToken(), source: tokenGenerated}, nil
	}
	path := tokenPath(dbPath)
	if rotate {
		tok, err := writeTokenFile(path)
		return resolvedToken{value: tok, source: tokenFromFile, path: path, rotated: true}, err
	}
	if tok := readTokenFile(path); tok != "" {
		return resolvedToken{value: tok, source: tokenFromFile, path: path}, nil
	}
	tok, created, err := createTokenFile(path)
	return resolvedToken{value: tok, source: tokenFromFile, path: path, created: created}, err
}

// tokenPath is the token file beside a database file.
func tokenPath(dbPath string) string { return dbPath + ".token" }

// stableProbe is the port probe's per-address bearer (listen.Request
// TokenFor): the database's stable token, only for an address a
// trusted discovery file of this user names — the token is long-lived
// and the panel tokens' signing key, so whatever else holds a port is
// probed bare (a token-walled Studio there is skipped for the next
// port, as before B3). nil without a database file.
func stableProbe(dbPath string) func(addr string) string {
	if dbPath == "" {
		return nil
	}
	return func(addr string) string {
		if discovery.Find("http://"+addr) == "" {
			return ""
		}
		return readTokenFile(tokenPath(dbPath))
	}
}

// probeToken is the bearer a probe of addr sends: the fixed token
// (--token, WEFT_STUDIO_TOKEN), else stableProbe's answer — what a
// reused Studio on the same database serves.
func probeToken(dbPath, flagTok, addr string) string {
	if tok := fixedToken(flagTok); tok != "" {
		return tok
	}
	if f := stableProbe(dbPath); f != nil {
		return f(addr)
	}
	return ""
}

// readTokenFile is the token in path, "" when there is none.
func readTokenFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// newSecret is 32 random bytes, base64url.
func newSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("weft: token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// createTokenFile writes a new token to path unless one is there:
// the token goes to a temporary file, complete, and os.Link publishes
// it — an atomic create-if-absent, so two first starts can never see a
// half-written file or end on different tokens (the loser of the link
// reads the winner's). A .weft directory gets its .gitignore first.
func createTokenFile(path string) (tok string, created bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, fmt.Errorf("token file %s: %w", path, err)
	}
	discovery.GuardDir(dir)
	tmp, tok, err := tempToken(dir)
	if err != nil {
		return "", false, fmt.Errorf("token file %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := linkFile(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			if existing := readTokenFile(path); existing != "" {
				return existing, false, nil
			}
			// An empty or unreadable file: replace it.
			tok, err := writeTokenFile(path)
			return tok, true, err
		}
		return "", false, fmt.Errorf("token file %s: %w", path, err)
	}
	return tok, true, nil
}

// linkFile is os.Link; a variable so a test can lose the race on
// purpose.
var linkFile = os.Link

// tempToken writes a fresh token to a 0600 temporary file in dir.
func tempToken(dir string) (path, tok string, err error) {
	f, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return "", "", err
	}
	tok = newSecret()
	_, werr := f.WriteString(tok + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(f.Name(), 0o600)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", "", werr
	}
	return f.Name(), tok, nil
}

// writeTokenFile replaces path with a new token, atomically, 0600.
func writeTokenFile(path string) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("token file %s: %w", path, err)
	}
	discovery.GuardDir(dir)
	tmp, tok, err := tempToken(dir)
	if err != nil {
		return "", fmt.Errorf("token file %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("token file %s: %w", path, err)
	}
	return tok, nil
}
