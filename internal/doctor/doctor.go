// Package doctor is `studio doctor` (plan B5): it asks a running
// Studio what it is and how it is wired, and prints one line per
// check. Every line reads one field of GET /api/meta (Lines is the
// table) — or, for the runtime check, the two environment variables the
// doctor's own shell can see (WEFT_ENV, WEFT_STUDIO_URL). The doctor
// computes nothing Studio does not serve: a second source of truth
// would drift.
//
// The logic lives here, apart from the command wiring (studio/cmd
// today, cmd/weft later), so the command moves without it.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Timeout bounds the one request the doctor makes: an unreachable or
// hung Studio is a clear first line, never a hang.
const Timeout = 5 * time.Second

// Line is one check the doctor prints: its label and the /api/meta
// fields (or "env:NAME" for a variable of the doctor's own shell, or
// "status" for the meta response itself) it reads.
type Line struct {
	Label  string
	Fields []string
}

// Lines is the doctor's line table, in print order.
var Lines = []Line{
	{"studio", []string{"status", "weft_version", "studio_version"}},
	{"token", []string{"status"}},
	{"db", []string{"db.kind", "db.path", "db.size"}},
	{"content", []string{"content.ingest", "content.latest.run_id", "content.latest.mark", "content.latest.note", "content.latest.fix"}},
	{"runtimes", []string{"runtimes", "capabilities", "env:WEFT_ENV", "env:WEFT_STUDIO_URL"}},
	{"panel", []string{"panel_version", "studio_version"}},
	{"weft.json", []string{"has_manifest", "manifest_check.agents", "manifest_check.checked", "manifest_check.stale"}},
}

// meta is the slice of GET /api/meta the doctor reads.
type meta struct {
	WeftVersion   string          `json:"weft_version"`
	StudioVersion string          `json:"studio_version"`
	PanelVersion  string          `json:"panel_version"`
	DB            json.RawMessage `json:"db"`
	HasManifest   bool            `json:"has_manifest"`
	Capabilities  []string        `json:"capabilities"`
	Content       struct {
		Ingest string `json:"ingest"`
		Latest *struct {
			RunID string `json:"run_id"`
			Mark  string `json:"mark"`
			Note  string `json:"note"`
			Fix   string `json:"fix"`
		} `json:"latest"`
	} `json:"content"`
	Runtimes      int `json:"runtimes"`
	ManifestCheck *struct {
		Agents  int      `json:"agents"`
		Checked int      `json:"checked"`
		Stale   []string `json:"stale"`
	} `json:"manifest_check"`
}

// ErrUnhealthy is Run's error when any check failed: the command exits 1.
var ErrUnhealthy = errors.New("doctor: a check failed")

// Run checks the Studio at url (token "" sends none) and prints one
// line per check to w. getenv reads the doctor's own environment (the
// runtime check reports WEFT_ENV and WEFT_STUDIO_URL as this shell sees
// them). It returns ErrUnhealthy when a check failed — Studio not
// reachable, the token refused, a stale panel bundle — and nil
// otherwise; warnings (no runtime connected, content stripped, a stale
// weft.json) do not fail it.
func Run(ctx context.Context, w io.Writer, url, token string, getenv func(string) string) error {
	url = strings.TrimRight(url, "/")
	p := printer{w: w}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/meta", nil)
	if err != nil {
		p.raw("studio not reachable at %s: %v", url, err)
		return ErrUnhealthy
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: Timeout}).Do(req)
	if err != nil {
		p.raw("studio not reachable at %s: %v", url, err)
		p.sub("start one with `studio`, or point --url (WEFT_STUDIO_URL) at the one you run")
		return ErrUnhealthy
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		p.raw("studio not reachable at %s: %v", url, err)
		return ErrUnhealthy
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		p.line(ok, "studio", "reachable at %s", url)
		if token == "" {
			p.line(fail, "token", "studio requires one: set WEFT_STUDIO_TOKEN or --token (studio prints its dev token at start)")
		} else {
			p.line(fail, "token", "refused: it is not this studio's token (WEFT_STUDIO_TOKEN / --token)")
		}
		return ErrUnhealthy
	default:
		p.line(ok, "studio", "reachable at %s", url)
		p.line(fail, "token", "api/meta answered %d: %s", resp.StatusCode, errorMessage(body))
		return ErrUnhealthy
	}
	var m meta
	if err := json.Unmarshal(body, &m); err != nil {
		p.raw("studio not reachable at %s: api/meta is not JSON: %v", url, err)
		return ErrUnhealthy
	}

	p.line(ok, "studio", "reachable at %s (weft %s, studio %s)", url, m.WeftVersion, m.StudioVersion)
	if token == "" {
		p.line(ok, "token", "none needed: studio has no token (setup A, loopback)")
	} else {
		p.line(ok, "token", "accepted")
	}
	p.db(m.DB)
	p.content(m)
	p.runtimes(m, url, getenv)
	p.panel(m)
	p.manifest(m)
	if p.failed {
		return ErrUnhealthy
	}
	return nil
}

const (
	ok   = "ok"
	warn = "warn"
	fail = "FAIL"
)

type printer struct {
	w      io.Writer
	failed bool
}

// line prints one check: status, label, text.
func (p *printer) line(status, label, format string, args ...any) {
	if status == fail {
		p.failed = true
	}
	_, _ = fmt.Fprintf(p.w, "%-4s %-9s %s\n", status, label, fmt.Sprintf(format, args...))
}

// sub prints a continuation of the previous line's check.
func (p *printer) sub(format string, args ...any) {
	_, _ = fmt.Fprintf(p.w, "               - %s\n", fmt.Sprintf(format, args...))
}

// raw prints a line outside the table (the unreachable first line).
func (p *printer) raw(format string, args ...any) {
	_, _ = fmt.Fprintf(p.w, format+"\n", args...)
}

func (p *printer) db(raw json.RawMessage) {
	var db struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
		Size *int64 `json:"size"`
	}
	if err := json.Unmarshal(raw, &db); err != nil {
		// A Studio older than this doctor serves db as the kind alone.
		var kind string
		_ = json.Unmarshal(raw, &kind)
		p.line(ok, "db", "%s (this studio predates db.path)", kind)
		return
	}
	switch {
	case db.Path != "" && db.Size != nil:
		p.line(ok, "db", "%s %s (%s)", db.Kind, db.Path, humanBytes(*db.Size))
	case db.Path != "":
		p.line(ok, "db", "%s %s", db.Kind, db.Path)
	default:
		p.line(ok, "db", "%s (no file path served to this caller)", db.Kind)
	}
}

func (p *printer) content(m meta) {
	policy := "studio stores content as received"
	if m.Content.Ingest != "as_received" {
		policy = "studio ingest policy " + m.Content.Ingest
	}
	l := m.Content.Latest
	if l == nil {
		p.line(ok, "content", "%s; no run stored yet", policy)
		return
	}
	status := ok
	if l.Mark != "full" {
		status = warn
	}
	p.line(status, "content", "%s; latest run %s: %s — %s", policy, l.RunID, l.Mark, l.Note)
	if l.Fix != "" {
		p.sub("fix: %s", l.Fix)
	}
}

func (p *printer) runtimes(m meta, url string, getenv func(string) string) {
	if m.Runtimes > 0 {
		p.line(ok, "runtimes", "%d connected", m.Runtimes)
		return
	}
	p.line(warn, "runtimes", "none connected")
	if !slices.Contains(m.Capabilities, "runtimes") {
		p.sub("studio has no runtime link: the playground is off (studio.Playground(true))")
	}
	if env := getenv("WEFT_ENV"); env != "dev" {
		shown := "unset"
		if env != "" {
			shown = "=" + env
		}
		p.sub("WEFT_ENV is %s in this shell: the app opens the runtime link only with WEFT_ENV=dev (or runtime.Enabled(true))", shown)
	} else {
		p.sub("WEFT_ENV=dev in this shell: check the app's own environment, and that it calls runtime.Install")
	}
	switch su := strings.TrimRight(getenv("WEFT_STUDIO_URL"), "/"); {
	case su == "":
		p.sub("WEFT_STUDIO_URL is unset in this shell: the app dials its otel Studio destination; set WEFT_STUDIO_URL=%s (or runtime.Studio(url, token))", url)
	case su != url:
		p.sub("WEFT_STUDIO_URL=%s in this shell, not %s: the app dials the former", su, url)
	}
}

func (p *printer) panel(m meta) {
	switch {
	case m.PanelVersion == "":
		p.line(fail, "panel", "/panel.js carries no version stamp: this studio's build lacks the panel bundle")
	case m.PanelVersion != m.StudioVersion:
		p.line(fail, "panel", "/panel.js is %s, studio is %s: the embedded bundle is stale (make studio-build)", m.PanelVersion, m.StudioVersion)
	default:
		p.line(ok, "panel", "/panel.js matches studio %s", m.StudioVersion)
	}
}

func (p *printer) manifest(m meta) {
	c := m.ManifestCheck
	switch {
	case !m.HasManifest:
		p.line(ok, "weft.json", "no manifest configured (studio.Manifest), nothing to compare")
	case c == nil:
		p.line(ok, "weft.json", "configured; not checked for this caller")
	case len(c.Stale) > 0:
		p.line(warn, "weft.json", "stale for %s: the latest runs carry another manifest hash; regenerate weft.json (its golden test with -update)", strings.Join(c.Stale, ", "))
	case c.Checked == 0:
		p.line(ok, "weft.json", "%d agents; none has a recorded run to compare yet", c.Agents)
	default:
		p.line(ok, "weft.json", "%d agents, %d checked against their latest runs: current", c.Agents, c.Checked)
	}
}

// errorMessage reads a Studio error body's message, or the body.
func errorMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return strings.TrimSpace(string(body))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
