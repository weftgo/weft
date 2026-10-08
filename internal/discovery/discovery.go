// Package discovery is how an app finds the running Studio with no
// configuration (plan B3): `weft studio` and `weft dev` write a small
// file, studio.json, naming the Studio they serve, and weft/otel and
// weft/runtime read it when nothing else names one.
//
// The file is {"url","token","db","pid","started","version"}, mode
// 0600, written after the listener is bound (so the port in it is the
// real one) and removed on a clean exit. It lives in the first of:
//
//   - ./.weft/, when that directory exists (the local sink's own
//     directory, so a project's Studio is found by that project's apps);
//   - $XDG_RUNTIME_DIR/weft/, when XDG_RUNTIME_DIR is set (Linux);
//   - os.UserCacheDir()/weft/ (macOS, Windows, bare containers).
//
// The reader walks the same order and takes the first fresh file. A
// stale file — its pid not alive, or its started time 24 hours old or
// more — is never trusted: the reader skips it (one Debug line) and the
// next writer removes it.
//
// The file is a convenience, never a requirement: WEFT_STUDIO_URL
// always wins (the reader does not look), WEFT_DISCOVERY=off turns the
// read off, and runtime.Studio(url, tok) / otel.Studio(url, tok) name a
// Studio in code. The core never imports this package.
package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileName is the discovery file's name in each location.
const FileName = "studio.json"

// MaxAge is how old a file may be and still be trusted.
const MaxAge = 24 * time.Hour

// Info is the discovery file's content.
type Info struct {
	// URL is the Studio's base URL, http://host:port (loopback).
	URL string `json:"url"`
	// Token is the Studio's API token (the file is 0600).
	Token string `json:"token"`
	// DB is the Studio's SQLite file, absolute; "" for a database with
	// no file.
	DB string `json:"db"`
	// PID is the Studio process's id.
	PID int `json:"pid"`
	// Started is when the Studio started (RFC 3339 on the wire).
	Started time.Time `json:"started"`
	// Version is the weft version the Studio runs (version.Runtime).
	Version string `json:"version"`
}

// Dirs is the location order, writer and reader alike. A variable so a
// test points every reader and writer of its process at a temporary
// directory — no test touches the developer's real locations.
var Dirs = DefaultDirs

// DefaultDirs is the documented order: ./.weft when it exists, then
// $XDG_RUNTIME_DIR/weft when XDG_RUNTIME_DIR is set, then
// os.UserCacheDir()/weft. Paths are absolute.
func DefaultDirs() []string {
	var out []string
	if fi, err := os.Stat(".weft"); err == nil && fi.IsDir() {
		if abs, err := filepath.Abs(".weft"); err == nil {
			out = append(out, abs)
		}
	}
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" && filepath.IsAbs(x) {
		out = append(out, filepath.Join(x, "weft"))
	}
	if c, err := os.UserCacheDir(); err == nil {
		out = append(out, filepath.Join(c, "weft"))
	}
	return out
}

// now is the clock; a variable for tests.
var now = time.Now

// Stale reports why info is not to be trusted ("" when it is): its pid
// is not alive, or it started MaxAge or more ago (or in the future).
func Stale(info Info) string {
	age := now().Sub(info.Started)
	switch {
	case info.URL == "":
		return "names no url"
	case info.Started.IsZero():
		return "carries no started time"
	case age >= MaxAge:
		return fmt.Sprintf("started %s ago (more than %s)", age.Round(time.Minute), MaxAge)
	case age < -time.Minute:
		return "started in the future"
	case info.PID <= 0 || !alive(info.PID):
		return fmt.Sprintf("pid %d is not running", info.PID)
	}
	return ""
}

// Write writes info to the first location (created 0700 when missing)
// and returns the file's path. Stale files in every location are
// removed first — the writer is the one that cleans up. The write is
// atomic (a temporary file renamed over the old one) and the file is
// 0600: it carries the token.
func Write(info Info) (string, error) {
	dirs := Dirs()
	if len(dirs) == 0 {
		return "", errors.New("discovery: no location (no ./.weft, no XDG_RUNTIME_DIR, no user cache directory)")
	}
	for _, d := range dirs {
		p := filepath.Join(d, FileName)
		if old, err := readFile(p); err != nil || Stale(old) != "" {
			if !errors.Is(err, os.ErrNotExist) {
				_ = os.Remove(p)
			}
		}
	}
	dir := dirs[0]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".studio-*.json")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o600)
	}
	path := filepath.Join(dir, FileName)
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return "", werr
	}
	return path, nil
}

// Remove removes the file at path when it is still info's (same pid
// and url): a Studio never removes the file another one wrote since.
func Remove(path string, info Info) {
	if path == "" {
		return
	}
	cur, err := readFile(path)
	if err != nil || cur.PID != info.PID || cur.URL != info.URL {
		return
	}
	_ = os.Remove(path)
}

// Find is the path of a fresh file naming url, "" when no location
// holds one: how a reusing start checks the running Studio wrote its
// file.
func Find(url string) string {
	for _, d := range Dirs() {
		p := filepath.Join(d, FileName)
		info, err := readFile(p)
		if err == nil && Stale(info) == "" && strings.TrimSuffix(info.URL, "/") == strings.TrimSuffix(url, "/") {
			return p
		}
	}
	return ""
}

// Lookup is the reader the app side uses: the first fresh file of the
// location order, with its path. It does not look when
// getenv("WEFT_STUDIO_URL") is set (the explicit Studio always wins) or
// getenv("WEFT_DISCOVERY") is "off". A missing file says nothing; a
// malformed or stale one is one Debug line on log (nil: slog.Default())
// — observability never changes behaviour, and a leftover file is no
// news. ok is false when nothing fresh was found.
func Lookup(getenv func(string) string, log *slog.Logger) (info Info, path string, ok bool) {
	if getenv("WEFT_STUDIO_URL") != "" || strings.EqualFold(strings.TrimSpace(getenv("WEFT_DISCOVERY")), "off") {
		return Info{}, "", false
	}
	if log == nil {
		log = slog.Default()
	}
	for _, d := range Dirs() {
		p := filepath.Join(d, FileName)
		info, err := readFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			log.Debug("weft: discovery file ignored", "path", p, "err", err.Error())
			continue
		}
		if why := Stale(info); why != "" {
			log.Debug("weft: discovery file ignored (stale)", "path", p, "why", why)
			continue
		}
		return info, p, true
	}
	return Info{}, "", false
}

// readFile reads and decodes one discovery file.
func readFile(path string) (Info, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return Info{}, fmt.Errorf("does not parse: %w", err)
	}
	return info, nil
}
