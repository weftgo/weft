// Package listen is the Studio command's port policy (plan B2): one
// default port, 7331, stable and reusable, never silently different.
//
// Given the address the command wants, Choose either binds it or
// explains why not, in one line:
//
//   - The address is free: it is bound.
//   - It is busy and pinned (--addr or WEFT_STUDIO_ADDR set
//     explicitly): an error naming the address. No probe, no fallback.
//   - It is busy and a weft Studio answers GET /api/meta there with the
//     same database file (db.path, which B5 serves to loopback and the
//     dev token only): Choose binds nothing and returns a reuse verdict
//     naming that Studio's URL and pid.
//   - It is busy with anything else — another program, a Studio on
//     another database, a Studio whose meta this token cannot read
//     (401/403) or that serves no db.path — the next port of the range
//     is tried (7331–7340 by default), and the Choice's Note says which
//     addresses were skipped and why. All of them busy is an error
//     naming the range.
//
// The token goes to loopback only: the probe attaches Request.Token
// as a bearer when the host it dials is loopback (127.0.0.0/8, ::1,
// localhost; an unspecified host is dialled on 127.0.0.1). A busy
// non-loopback address is probed bare — a fixed token never leaves the
// machine for whatever holds that port — so it is never reused: the
// db.path a reuse needs is served to loopback or the token alone.
//
// The policy is the command's, not the library's: studio.New gains
// nothing, and an app's embedded Studio (setup A) is the app's own
// listener on the app's own port. The logic lives here, apart from the
// command wiring (studio/cmd today, cmd/weft later), so the commands
// share it.
package listen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultAddr is Studio's one default address.
	DefaultAddr = "127.0.0.1:7331"
	// DefaultSpan is how many ports an unpinned Choose tries, from the
	// wanted one up: 7331–7340.
	DefaultSpan = 10
	// ProbeTimeout bounds each /api/meta probe: a port held by
	// something that accepts and never answers costs at most this,
	// never a hang. Shorter than the doctor's 5 s: a Studio on loopback
	// answers meta in milliseconds, and a start may probe the whole
	// range.
	ProbeTimeout = 2 * time.Second
)

// Request is what the command wants.
type Request struct {
	// Addr is the wanted host:port ("" = DefaultAddr).
	Addr string
	// Pinned is whether Addr was set explicitly (--addr,
	// WEFT_STUDIO_ADDR): a busy pinned address is an error, never a
	// probe or another port.
	Pinned bool
	// Span is how many consecutive ports, Addr's first, an unpinned
	// Choose tries (0 = DefaultSpan).
	Span int
	// DBPath is the command's own database file, absolute. "" (an
	// in-memory or fileless database) never matches a running Studio's.
	DBPath string
	// Token is the bearer the probe sends ("" sends none and relies on
	// the loopback rule): the command's own --token / WEFT_STUDIO_TOKEN,
	// never a dev token it generated (nobody else holds that).
	Token string
	// Timeout bounds each probe (0 = ProbeTimeout).
	Timeout time.Duration
}

// Choice is Choose's verdict: a listener, or a Studio to reuse.
type Choice struct {
	// Listener is bound on Addr; nil when Reuse is set. The caller
	// serves on it and closes it.
	Listener net.Listener
	// Addr is the address listened on — or, with Reuse, the running
	// Studio's.
	Addr string
	// Reuse is set when a Studio on the same database already serves
	// Addr: nothing was bound.
	Reuse bool
	// PID is the reused Studio's process id (api/meta's pid; 0 when it
	// served none).
	PID int
	// Note is one line saying why the wanted address was not used —
	// each skipped address and what holds it — and "" when it was.
	Note string
}

// URL is the Choice's address as an http URL.
func (c Choice) URL() string { return "http://" + c.Addr }

// ReuseLine is the line the command prints for a reuse verdict.
func (c Choice) ReuseLine() string {
	pid := "pid unknown"
	if c.PID > 0 {
		pid = "pid " + strconv.Itoa(c.PID)
	}
	return fmt.Sprintf("studio already running at %s (%s), reusing", c.URL(), pid)
}

// Choose applies the port policy to r. ctx bounds the probes.
func Choose(ctx context.Context, r Request) (Choice, error) {
	if r.Addr == "" {
		r.Addr = DefaultAddr
	}
	if r.Span <= 0 {
		r.Span = DefaultSpan
	}
	if r.Timeout <= 0 {
		r.Timeout = ProbeTimeout
	}
	if r.Pinned {
		ln, err := net.Listen("tcp", r.Addr)
		if err != nil {
			return Choice{}, fmt.Errorf("listen on %s (pinned by --addr / WEFT_STUDIO_ADDR, no fallback): %w", r.Addr, err)
		}
		return Choice{Listener: ln, Addr: ln.Addr().String()}, nil
	}
	host, portStr, err := net.SplitHostPort(r.Addr)
	if err != nil {
		return Choice{}, fmt.Errorf("listen address %q: %w", r.Addr, err)
	}
	first, err := strconv.Atoi(portStr)
	if err != nil || first <= 0 || first+r.Span-1 > 65535 {
		return Choice{}, fmt.Errorf("listen address %q: the port must be a number in 1–%d", r.Addr, 65536-r.Span)
	}
	var skipped, reasons []string
	for port := first; port < first+r.Span; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			c := Choice{Listener: ln, Addr: addr}
			if len(skipped) > 0 {
				c.Note = strings.Join(skipped, "; ") + "; listening on " + addr + " instead (--addr pins one)"
			}
			return c, nil
		}
		if !addrInUse(err) {
			return Choice{}, fmt.Errorf("listen on %s: %w", addr, err)
		}
		v := probe(ctx, addr, r)
		if v.reuse {
			return Choice{Addr: addr, Reuse: true, PID: v.pid}, nil
		}
		skipped = append(skipped, addr+" is busy ("+v.why+")")
		reasons = append(reasons, strconv.Itoa(port)+": "+v.why)
	}
	return Choice{}, fmt.Errorf("every port in %s–%d is busy (%s): free one or pin another with --addr",
		r.Addr, first+r.Span-1, strings.Join(reasons, "; "))
}

// addrInUse reports whether a listen error is "address already in
// use" — the one error the policy probes past; any other (a host that
// does not resolve, a privileged port) is the caller's to see.
func addrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var errno syscall.Errno
	// WSAEADDRINUSE: Windows reports its own errno for the same case.
	return runtime.GOOS == "windows" && errors.As(err, &errno) && errno == 10048
}

// verdict is what one probe found at a busy address.
type verdict struct {
	reuse bool
	pid   int
	why   string // a non-reuse verdict's reason, for the Note
}

// probe asks the busy address's GET /api/meta what holds it, with the
// doctor's conventions: one bounded request, the bearer when there is
// one, redirects reported never followed.
func probe(ctx context.Context, addr string, r Request) verdict {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	req, err := probeRequest(ctx, addr, r.Token)
	if err != nil {
		return verdict{why: "not a weft Studio"}
	}
	sentToken := req.Header.Get("Authorization") != ""
	client := &http.Client{
		Timeout:       r.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return verdict{why: "not a weft Studio"}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return verdict{why: "not a weft Studio"}
	}
	var m struct {
		StudioVersion string `json:"studio_version"`
		PID           int    `json:"pid"`
		DB            struct {
			Path string `json:"path"`
		} `json:"db"`
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if json.Unmarshal(body, &m) != nil || m.StudioVersion == "" {
			return verdict{why: "not a weft Studio"}
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		if r.Token != "" && !sentToken {
			return verdict{why: "a Studio that requires a token, which goes to loopback only"}
		}
		if r.Token == "" {
			return verdict{why: "a Studio that requires a token: set WEFT_STUDIO_TOKEN to its token to reuse it"}
		}
		return verdict{why: "a Studio with another token"}
	default:
		return verdict{why: "not a weft Studio"}
	}
	switch {
	case m.DB.Path == "":
		return verdict{why: "a Studio that serves no database path"}
	case r.DBPath == "":
		return verdict{why: "a Studio on another database, " + m.DB.Path}
	}
	same, err := sameFile(m.DB.Path, r.DBPath)
	switch {
	case err != nil:
		return verdict{why: "a Studio on " + m.DB.Path + " (could not compare: " + err.Error() + ")"}
	case !same:
		return verdict{why: "a Studio on another database, " + m.DB.Path}
	}
	return verdict{reuse: true, pid: m.PID}
}

// probeRequest builds the probe's GET /api/meta for the busy addr.
// The bearer is attached only when the dialled host is loopback: a
// fixed token is never sent to whatever holds a non-loopback port.
func probeRequest(ctx context.Context, addr, token string) (*http.Request, error) {
	dial := probeAddr(addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+dial+"/api/meta", nil)
	if err != nil {
		return nil, err
	}
	if host, _, err := net.SplitHostPort(dial); err == nil && token != "" && isLoopback(host) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// isLoopback reports whether host is a loopback IP or "localhost".
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// probeAddr is the address the probe dials: an unspecified host
// (":7331", "0.0.0.0:7331", "[::]:7331") is reached on loopback.
func probeAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// sameFile reports whether two database paths name one file: equal
// once cleaned, or — through a symlink (macOS's /tmp) — the same file
// on disk. A path that does not exist names a different file than one
// that does (the command's file is often not created yet); a stat that
// fails otherwise (permissions) is the error: the paths could not be
// compared.
func sameFile(running, mine string) (bool, error) {
	if filepath.Clean(running) == filepath.Clean(mine) {
		return true, nil
	}
	fa, errA := os.Stat(running)
	fb, errB := os.Stat(mine)
	for _, err := range []error{errA, errB} {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	if errA != nil || errB != nil {
		return false, nil
	}
	return os.SameFile(fa, fb), nil
}
