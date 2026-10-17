// Package doctor is `weft doctor` (plan B5): it asks a running
// Studio what it is and how it is wired, and prints one line per
// check. Every line reads one field of GET /api/meta (Lines is the
// table) — or, for the runtime check, the two environment variables the
// doctor's own shell can see (WEFT_ENV, WEFT_STUDIO_URL). The doctor
// computes nothing Studio does not serve: a second source of truth
// would drift.
//
// The logic lives here, apart from the command wiring (cmd/weft), so
// the command stays a thin client of it.
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

	"github.com/weftgo/weft/internal/termsafe"
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
	{"token", []string{"status", "auth_required"}},
	{"db", []string{"db.kind", "db.path", "db.size"}},
	{"content", []string{"content.ingest", "content.latest.run_id", "content.latest.mark", "content.latest.note", "content.latest.fix", "content.error"}},
	{"ingest", []string{"ingest.dropped", "ingest.accepted", "ingest.refused", "ingest.last_error", "ingest.last_at"}},
	{"runtimes", []string{"runtimes", "capabilities", "env:WEFT_ENV", "env:WEFT_STUDIO_URL"}},
	{"panel", []string{"panel_version", "studio_version"}},
	{"weft.json", []string{"has_manifest", "manifest_check.source", "manifest_check.agents", "manifest_check.checked", "manifest_check.stale", "manifest_check.error"}},
}

// meta is the slice of GET /api/meta the doctor reads.
type meta struct {
	WeftVersion   string          `json:"weft_version"`
	StudioVersion string          `json:"studio_version"`
	PanelVersion  string          `json:"panel_version"`
	DB            json.RawMessage `json:"db"`
	HasManifest   bool            `json:"has_manifest"`
	Capabilities  []string        `json:"capabilities"`
	AuthRequired  bool            `json:"auth_required"`
	Content       struct {
		Ingest string `json:"ingest"`
		Error  string `json:"error"`
		Latest *struct {
			RunID string `json:"run_id"`
			Mark  string `json:"mark"`
			Note  string `json:"note"`
			Fix   string `json:"fix"`
		} `json:"latest"`
	} `json:"content"`
	// Ingest is the ingest health (U5): absent from a Studio older
	// than it, and for a panel token (the dev token's alone).
	Ingest *struct {
		Dropped []struct {
			Service   string    `json:"service"`
			Records   int64     `json:"records"`
			LastHeard time.Time `json:"last_heard"`
		} `json:"dropped"`
		// Accepted and Refused count the OTLP batches Studio answered
		// 2xx and 4xx/5xx since it started, LastError / LastAt the last
		// refusal (UX-E-37): nil from a Studio older than them.
		Accepted  *int64     `json:"accepted"`
		Refused   *int64     `json:"refused"`
		LastError string     `json:"last_error"`
		LastAt    *time.Time `json:"last_at"`
	} `json:"ingest"`
	Runtimes      int `json:"runtimes"`
	ManifestCheck *struct {
		Source  string   `json:"source"` // file | runtime (registered, no weft.json)
		Agents  int      `json:"agents"`
		Checked int      `json:"checked"`
		Stale   []string `json:"stale"`
		Error   string   `json:"error"`
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
	// The doctor talks to --url only: a redirect is reported (the
	// default branch below), never followed.
	client := &http.Client{
		Timeout:       Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		p.raw("studio not reachable at %s: %v", url, err)
		p.sub("start one with `weft studio`, or point --url (WEFT_STUDIO_URL) at the one you run")
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
		msg := errorMessage(body)
		if loc := resp.Header.Get("Location"); resp.StatusCode/100 == 3 {
			msg = "a redirect to " + loc + ", not followed: point --url at the Studio itself"
		}
		p.line(fail, "token", "api/meta answered %d: %s", resp.StatusCode, msg)
		return ErrUnhealthy
	}
	var m meta
	if err := json.Unmarshal(body, &m); err != nil {
		p.raw("studio not reachable at %s: api/meta is not JSON: %v", url, err)
		return ErrUnhealthy
	}

	p.line(ok, "studio", "reachable at %s (weft %s, studio %s)", url, m.WeftVersion, m.StudioVersion)
	if m.AuthRequired {
		p.line(ok, "token", "accepted")
	} else {
		p.line(ok, "token", "not required (no Token configured)")
	}
	p.db(m.DB)
	p.content(m)
	p.ingest(m)
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

// printer writes the doctor's lines. Every line passes termsafe.Text:
// what it prints comes from Studio (service names, run ids, notes,
// error text an OTLP sender may have chosen), and a control character
// never reaches the terminal raw.
type printer struct {
	w      io.Writer
	failed bool
}

// line prints one check: status, label, text.
func (p *printer) line(status, label, format string, args ...any) {
	if status == fail {
		p.failed = true
	}
	_, _ = fmt.Fprintf(p.w, "%-4s %-9s %s\n", status, label, termsafe.Text(fmt.Sprintf(format, args...)))
}

// sub prints a continuation of the previous line's check.
func (p *printer) sub(format string, args ...any) {
	_, _ = fmt.Fprintf(p.w, "               - %s\n", termsafe.Text(fmt.Sprintf(format, args...)))
}

// raw prints a line outside the table (the unreachable first line).
func (p *printer) raw(format string, args ...any) {
	_, _ = fmt.Fprintln(p.w, termsafe.Text(fmt.Sprintf(format, args...)))
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
	if m.Content.Error != "" {
		p.line(warn, "content", "%s; the latest run could not be read: %s", policy, m.Content.Error)
		if l != nil {
			p.sub("latest run %s: %s — %s", l.RunID, l.Mark, l.Note)
		}
		return
	}
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

// refusalWindow is how recent the last refused batch must be for the
// ingest line to warn.
const refusalWindow = time.Hour

// ingest prints the ingest health: the OTLP batches this Studio
// accepted and refused (and the last refusal), then the services whose
// heartbeats said their destination lost records on the way
// (weft.records.dropped). A warning, not a failure: the runs say which
// of theirs are missing (the gap badge, "N of M events were lost").
// Refusals warn only within refusalWindow of the last one.
func (p *printer) ingest(m meta) {
	if m.Ingest == nil {
		p.line(ok, "ingest", "not served (no ingest.dropped): a studio older than ingest health, or a panel token — it is the dev token's")
		return
	}
	in := m.Ingest
	counts := ""
	refused := in.Refused != nil && *in.Refused > 0
	if in.Accepted != nil && in.Refused != nil {
		counts = fmt.Sprintf("%d OTLP batches accepted, %d refused; ", *in.Accepted, *in.Refused)
	}
	// A refusal warns while it is recent: the last one within the hour
	// (refusalWindow), or one with no time said. The counts are since
	// Studio started, so one refused batch long ago reads ok, its line
	// still printed below.
	if refused && in.LastAt != nil && time.Since(*in.LastAt) > refusalWindow {
		refused = false
	}
	status := ok
	if refused || len(in.Dropped) > 0 {
		status = warn
	}
	if len(in.Dropped) == 0 {
		p.line(status, "ingest", "%sno app reported lost records (weft.records.dropped on its heartbeats and run brackets)", counts)
	} else {
		p.line(status, "ingest", "%s%d service(s) reported records lost on the way here", counts, len(in.Dropped))
	}
	if in.LastError != "" {
		at := ""
		if in.LastAt != nil {
			at = " at " + in.LastAt.Format(time.RFC3339)
		}
		p.sub("last refused%s: %s", at, in.LastError)
		p.sub("fix: studio's log names each refused batch with its request id (WARN \"studio: ingest batch refused\"); a 401 is the app's ingest token (otel.Studio(url, token), studio.IngestToken)")
	}
	if len(in.Dropped) > 0 {
		for _, d := range in.Dropped {
			svc := d.Service
			if svc == "" {
				svc = "(no service.name)"
			}
			p.sub("%s: %d records dropped by its destination (last heard %s)", svc, d.Records, d.LastHeard.Format(time.RFC3339))
		}
		// weft.records.dropped counts every lost record — a full queue
		// and failed exports alike — so the fix names both; the app's
		// WARN says which it was.
		p.sub("fix: queue full or exports failing — the app's WARN names which (\"weft/otel: destination queue full\": raise it with otel.QueueSize(n), or use otel.Blocking() under WEFT_ENV=dev; \"weft/otel: destination dropped records\": its err is the export's failure)")
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
		p.sub("WEFT_STUDIO_URL is unset in this shell: the app dials the Studio a running `weft studio` names in its discovery file, else its otel Studio destination; set WEFT_STUDIO_URL=%s to pin this one (or runtime.Studio(url, token))", url)
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
		p.line(ok, "weft.json", "no manifest configured (studio.Manifest, or --manifest / WEFT_MANIFEST), nothing to compare")
	case c == nil:
		p.line(ok, "weft.json", "a manifest is present (weft.json or a runtime's registration); not checked for this caller")
	case c.Error != "":
		p.line(warn, "weft.json", "the check failed: %s", c.Error)
	case c.Source == "runtime" && len(c.Stale) > 0:
		p.line(warn, "weft.json", "none configured; stale for %s: the manifest a runtime registered and the latest runs disagree (runs from another build)", strings.Join(c.Stale, ", "))
	case c.Source == "runtime":
		p.line(ok, "weft.json", "none configured; %d agents from a runtime's registration, %d checked against their latest runs", c.Agents, c.Checked)
	case len(c.Stale) > 0:
		p.line(warn, "weft.json", "stale for %s: weft.json and the latest runs disagree: regenerate weft.json, or redeploy the app if weft.json is newer", strings.Join(c.Stale, ", "))
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
