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
//     directory, so a project's Studio is found by that project's apps —
//     after a default `weft studio` it always exists, so run the app
//     from the directory you ran `weft studio` in, or set
//     WEFT_STUDIO_URL);
//   - $XDG_RUNTIME_DIR/weft/, when XDG_RUNTIME_DIR is set (Linux);
//   - os.UserCacheDir()/weft/ (macOS, Windows, bare containers).
//
// The writer drops a .gitignore ("*") into ./.weft when there is none,
// so neither the file nor the token beside the database is committed.
//
// The reader walks the same order and takes the first file it trusts.
// A file is trusted only when it is a regular file of at most 64 KiB,
// its url names a loopback host (127.0.0.0/8, ::1, localhost), it is
// fresh (started less than 24 hours ago) and its pid is alive — and, on
// unix, it is mode 0600 and owned by the reading user (a file from a
// git checkout is 0644: never trusted). Anything else is skipped with
// one Debug line, and the next writer removes it.
//
// Two live Studios never erase each other's file: a writer that
// displaces another live Studio's file restores it when it exits.
//
// The file is a convenience, never a requirement: WEFT_STUDIO_URL
// always wins (the reader does not look), WEFT_DISCOVERY=off turns the
// read off, and runtime.Studio(url, tok) / otel.Studio(url, tok) name a
// Studio in code (otel.Studio also switches the read off). The core
// never imports this package.
package discovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FileName is the discovery file's name in each location.
const FileName = "studio.json"

// MaxAge is how old a file may be and still be trusted.
const MaxAge = 24 * time.Hour

// maxFileBytes caps a read: a FIFO or a huge file cannot block or bloat
// the reader.
const maxFileBytes = 64 << 10

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
// os.UserCacheDir()/weft. Paths are absolute. ./.weft is relative to
// the working directory: an app finds a Studio's ./.weft file only when
// run from the directory that Studio was started in (else
// WEFT_STUDIO_URL names it).
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

// Stale reports why info is not to be trusted ("" when it is): its url
// is not loopback, its pid is not alive, or it started MaxAge or more
// ago (or in the future).
func Stale(info Info) string {
	age := now().Sub(info.Started)
	switch {
	case info.URL == "":
		return "names no url"
	case !loopbackURL(info.URL):
		return "its url " + strconv.Quote(info.URL) + " is not a loopback http(s) address"
	case info.Started.IsZero():
		return "carries no started time"
	case age >= MaxAge:
		return fmt.Sprintf("started %s ago (more than %s)", age.Round(time.Minute), MaxAge)
	case age < -time.Minute:
		return "started in the future"
	case info.PID <= 0 || info.PID > math.MaxInt32 || !alive(info.PID):
		return fmt.Sprintf("pid %d is not running", info.PID)
	}
	return ""
}

// loopbackURL reports whether raw is an http(s) URL whose host is
// loopback: 127.0.0.0/8, ::1, localhost.
func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Written is a discovery file this process wrote.
type Written struct {
	// Path is the file.
	Path string
	// Info is what was written.
	Info Info
	// prev is the live Studio whose fresh file this one displaced: put
	// back on Remove while that Studio still runs.
	prev *Info
}

// Write writes info to the first location (created 0700 when missing;
// a .weft directory gets its .gitignore) and returns the handle Remove
// takes. Untrusted files in every location are removed first — the
// writer is the one that cleans up. The write is atomic (a temporary
// file renamed over the old one) and the file is 0600: it carries the
// token. Another live Studio's file it displaces is restored by Remove.
func Write(info Info) (*Written, error) {
	dirs := Dirs()
	if len(dirs) == 0 {
		return nil, errors.New("discovery: no location (no ./.weft, no XDG_RUNTIME_DIR, no user cache directory)")
	}
	for _, d := range dirs {
		p := filepath.Join(d, FileName)
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		if old, err := load(p); err != nil || Stale(old) != "" {
			_ = os.Remove(p)
		}
	}
	dir := dirs[0]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	GuardDir(dir)
	path := filepath.Join(dir, FileName)
	w := &Written{Path: path, Info: info}
	if cur, err := load(path); err == nil && Stale(cur) == "" && (cur.PID != info.PID || cur.URL != info.URL) {
		w.prev = &cur
	}
	if err := writeAtomic(path, info); err != nil {
		return nil, err
	}
	return w, nil
}

// Remove removes the file when it is still this Studio's (same pid and
// url) — putting back the live Studio it displaced, if any: a Studio
// never erases the file another live one depends on. Nil-safe.
func (w *Written) Remove() {
	if w == nil || w.Path == "" {
		return
	}
	cur, err := load(w.Path)
	if err != nil || cur.PID != w.Info.PID || cur.URL != w.Info.URL {
		return
	}
	if w.prev != nil && Stale(*w.prev) == "" {
		if writeAtomic(w.Path, *w.prev) == nil {
			return
		}
	}
	_ = os.Remove(w.Path)
}

// GuardDir drops a .gitignore ("*") into dir when dir is a .weft
// directory without one — the project's local data (the database, its
// token, the discovery file) is never committed by accident. An
// existing .gitignore is never touched; failures are ignored (the
// guard is a courtesy, not a gate).
func GuardDir(dir string) {
	if filepath.Base(dir) != ".weft" {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, ".gitignore"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString("# weft's local data (database, dev token, discovery file): never commit it.\n*\n")
	_ = f.Close()
}

// writeAtomic writes info to path through a temporary file, 0600.
func writeAtomic(path string, info Info) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".studio-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}

// Find is the path of a trusted file naming url, "" when no location
// holds one: how a start checks that the Studio on an address is the
// one this user's discovery file names (the probe's token, a reuse's
// missing-file line).
func Find(url string) string {
	for _, d := range Dirs() {
		p := filepath.Join(d, FileName)
		info, err := load(p)
		if err == nil && Stale(info) == "" && strings.TrimSuffix(info.URL, "/") == strings.TrimSuffix(url, "/") {
			return p
		}
	}
	return ""
}

// Lookup is the reader the app side uses: the first trusted file of the
// location order, with its path. It does not look when
// getenv("WEFT_STUDIO_URL") is set (the explicit Studio always wins) or
// getenv("WEFT_DISCOVERY") is "off". A missing file says nothing; an
// untrusted one (malformed, stale, not loopback, not 0600 or not this
// user's) is one Debug line on log (nil: slog.Default()) — observability
// never changes behaviour, and a leftover file is no news. ok is false
// when nothing trusted was found.
func Lookup(getenv func(string) string, log *slog.Logger) (info Info, path string, ok bool) {
	if getenv("WEFT_STUDIO_URL") != "" || strings.EqualFold(strings.TrimSpace(getenv("WEFT_DISCOVERY")), "off") {
		return Info{}, "", false
	}
	if log == nil {
		log = slog.Default()
	}
	for _, d := range Dirs() {
		p := filepath.Join(d, FileName)
		info, err := load(p)
		if errors.Is(err, fs.ErrNotExist) {
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

// load reads and decodes one discovery file, refusing what is not a
// regular file, what this user may not trust (checkFile: on unix mode
// 0600 and owned by this user) and anything over maxFileBytes.
func load(path string) (Info, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return Info{}, err
	}
	if !fi.Mode().IsRegular() {
		return Info{}, fmt.Errorf("not a regular file (%s)", fi.Mode().Type())
	}
	if err := checkFile(fi); err != nil {
		return Info{}, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = f.Close() }()
	if ofi, err := f.Stat(); err != nil || !os.SameFile(fi, ofi) {
		return Info{}, errors.New("changed while being read")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Info{}, err
	}
	if len(b) > maxFileBytes {
		return Info{}, fmt.Errorf("larger than %d bytes", maxFileBytes)
	}
	if t := bytes.TrimSpace(b); len(t) == 0 || t[0] != '{' {
		return Info{}, errors.New("does not parse: not a JSON object")
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return Info{}, fmt.Errorf("does not parse: %w", err)
	}
	return info, nil
}
