// Package doctor is `weft doctor` (plan B5): it asks a running
// Studio what it is and how it is wired, and prints one line per
// check. Every line reads one field of GET /api/meta (Lines is the
// table) — beside it, for the runtime check, the two environment
// variables the doctor's own shell can see (WEFT_ENV, WEFT_STUDIO_URL),
// and for the discovery check the discovery file this shell reads
// (Target.Discovery, the command's resolver's). The doctor computes
// nothing Studio does not serve: a second source of truth would drift.
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
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/weftgo/weft/internal/termsafe"
)

// Timeout bounds the one request the doctor makes: an unreachable or
// hung Studio is a clear first line, never a hang.
const Timeout = 5 * time.Second

// Line is one check the doctor prints: its label and the /api/meta
// fields (or "env:NAME" for a variable of the doctor's own shell,
// "local:discovery" for the discovery file it reads, or "status" for
// the meta response itself) it reads.
type Line struct {
	Label  string
	Fields []string
}

// Lines is the doctor's line table, in print order.
var Lines = []Line{
	{"studio", []string{"status", "weft_version", "studio_version"}},
	{"token", []string{"status", "auth_required"}},
	{"discovery", []string{"discovery_file", "db.path", "local:discovery"}},
	{"db", []string{"db.kind", "db.path", "db.size"}},
	{"content", []string{"content.ingest", "content.latest.run_id", "content.latest.mark", "content.latest.note", "content.latest.fix", "content.error"}},
	{"data", []string{"content.latest.started", "content.latest.weft_version", "content.latest.service", "weft_version"}},
	{"otlp", []string{"ingest_open", "capabilities"}},
	{"ingest", []string{"ingest.dropped", "ingest.accepted", "ingest.refused", "ingest.last_error", "ingest.last_at"}},
	{"runtimes", []string{"runtimes", "capabilities", "capabilities_off", "env:WEFT_ENV", "env:WEFT_STUDIO_URL"}},
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
	// CapabilitiesOff names why each capability is off, the flag that
	// did included (plan B4): the runtimes and otlp lines say it.
	CapabilitiesOff map[string]string `json:"capabilities_off"`
	IngestOpen      bool              `json:"ingest_open"`
	AuthRequired    bool              `json:"auth_required"`
	// DiscoveryFile is the discovery file this Studio wrote (served
	// under db.path's rule while it names the Studio's process).
	DiscoveryFile string `json:"discovery_file"`
	Content       struct {
		Ingest string `json:"ingest"`
		Error  string `json:"error"`
		Latest *struct {
			RunID string `json:"run_id"`
			Mark  string `json:"mark"`
			Note  string `json:"note"`
			Fix   string `json:"fix"`
			// Started, WeftVersion and Service are the newest run's own:
			// the data line (absent from a Studio older than it).
			Started     time.Time `json:"started"`
			WeftVersion string    `json:"weft_version"`
			Service     string    `json:"service"`
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
		// Latest is each checked agent's latest run's manifest hash.
		Latest map[string]string `json:"latest"`
	} `json:"manifest_check"`
}

// Target is the Studio the doctor checks and how the command found it
// (cmd/weft's one resolver: --url, WEFT_STUDIO_URL, the discovery
// file, the default address).
type Target struct {
	// URL is the Studio's root URL.
	URL string
	// Token is the bearer to send, "" for none.
	Token string
	// Explicit is whether --url or WEFT_STUDIO_URL named the Studio.
	Explicit bool
	// Source is where URL came from in words ("--url", "discovery file
	// <path>", "the default address; no discovery file").
	Source string
	// Discovery is the discovery file this shell reads.
	Discovery Discovery
}

// Discovery is the discovery file the doctor's shell reads (the
// resolver's view of it, read with internal/discovery's trust rules).
type Discovery struct {
	// Off is WEFT_DISCOVERY=off in this shell: no file is read.
	Off bool
	// Path is the trusted file found, "" when none.
	Path string
	// URL, DB and PID are what the file names.
	URL, DB string
	PID     int
}

// ErrUnhealthy is Run's error when any check failed: the command exits 1.
var ErrUnhealthy = errors.New("doctor: a check failed")

// Run checks the Studio t names (t.Token "" sends none) and prints one
// line per check to w. getenv reads the doctor's own environment (the
// runtime check reports WEFT_ENV and WEFT_STUDIO_URL as this shell sees
// them). It returns ErrUnhealthy when a check failed — Studio not
// reachable, the token refused, a stale panel bundle — and nil
// otherwise; warnings (no runtime connected, content stripped, a stale
// weft.json, a discovery file naming another Studio) do not fail it.
func Run(ctx context.Context, w io.Writer, t Target, getenv func(string) string) error {
	url, token := strings.TrimRight(t.URL, "/"), t.Token
	p := printer{w: w}
	where := url
	if !t.Explicit && t.Source != "" {
		where = url + " (" + t.Source + ")"
	}
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
		p.raw("studio not reachable at %s: %v", where, err)
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
			p.line(fail, "token", "studio requires one: it is in <db>.token beside the database (the start banner names the file); set WEFT_STUDIO_TOKEN or --token to it")
			p.sub("`weft open --with-token` prints this Studio's link signed in (the token in its #token= fragment)")
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

	p.line(ok, "studio", "reachable at %s (weft %s, studio %s)", where, m.WeftVersion, m.StudioVersion)
	if m.AuthRequired {
		p.line(ok, "token", "accepted")
	} else {
		p.line(ok, "token", "not required (no Token configured)")
	}
	p.discovery(m, url, t.Discovery)
	p.db(m.DB)
	p.content(m)
	p.data(m)
	p.otlp(m, url)
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
		p.sub("fix: queue full or exports failing — the app's WARN names which (\"weft/otel: destination queue full\": raise it with otel.QueueSize(n), or use otel.Blocking() under WEFT_ENV=dev (or otel.DevMode()); \"weft/otel: destination dropped records\": its err is the export's failure)")
	}
}

func (p *printer) runtimes(m meta, url string, getenv func(string) string) {
	if m.Runtimes > 0 {
		p.line(ok, "runtimes", "%d connected", m.Runtimes)
		return
	}
	if !slices.Contains(m.Capabilities, "runtimes") {
		// No runtime link at all: the shell's variables do not apply.
		why := m.CapabilitiesOff["runtimes"]
		if why == "" {
			why = "the playground is off: turn it on with studio.Playground(true); weft studio --no-playground turns it off"
		}
		p.line(ok, "runtimes", "none: studio has no runtime link (%s)", why)
		return
	}
	p.line(warn, "runtimes", "none connected")
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
		p.line(fail, "panel", "/panel.js is %s, studio is %s: this weft build embeds a stale panel bundle", m.PanelVersion, m.StudioVersion)
		p.sub("fix: install weft %s again (go install github.com/weftgo/weft/cmd/weft@%s); in a weft checkout, make studio-build; then reload the page", m.StudioVersion, m.StudioVersion)
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
		p.line(warn, "weft.json", "none configured; %s last run from another build than the one a runtime registered%s: redeploy the app, or ignore it if those runs are old", agentsCount(c.Stale), lastRunHash(c.Stale, c.Latest))
		p.sub("stale: %s", strings.Join(c.Stale, ", "))
	case c.Source == "runtime":
		p.line(ok, "weft.json", "none configured; %d agents from a runtime's registration, %d checked against their latest runs", c.Agents, c.Checked)
	case len(c.Stale) > 0:
		p.line(warn, "weft.json", "stale for %s: weft.json and the latest runs disagree%s: regenerate weft.json, or redeploy the app if weft.json is newer", strings.Join(c.Stale, ", "), lastRunHash(c.Stale, c.Latest))
	case c.Checked == 0:
		p.line(ok, "weft.json", "%d agents; none has a recorded run to compare yet", c.Agents)
	default:
		p.line(ok, "weft.json", "%d agents, %d checked against their latest runs: current", c.Agents, c.Checked)
	}
}

// agentsCount is "1 agent was" / "n agents were".
func agentsCount(names []string) string {
	if len(names) == 1 {
		return "1 agent was"
	}
	return fmt.Sprintf("%d agents were", len(names))
}

// lastRunHash names the first stale agent's latest run's manifest hash
// (manifest_check.latest), 8 characters: " (the latest run of orders
// ran manifest d2151022)"; "" when meta served none.
func lastRunHash(stale []string, latest map[string]string) string {
	if len(stale) == 0 || latest[stale[0]] == "" {
		return ""
	}
	h := latest[stale[0]]
	if len(h) > 8 {
		h = h[:8]
	}
	return fmt.Sprintf(" (the latest run of %s ran manifest %s)", stale[0], h)
}

// discovery prints how apps find this Studio: the discovery file this
// shell reads, against the one this Studio says it wrote. Another
// Studio's file is a warning — apps that set no WEFT_STUDIO_URL send
// there — and two Studios on one database a second one.
func (p *printer) discovery(m meta, url string, d Discovery) {
	var dbPath string
	var db struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(m.DB, &db) == nil {
		dbPath = db.Path
	}
	switch {
	case d.Off:
		p.line(ok, "discovery", "WEFT_DISCOVERY=off in this shell: no file read; apps and this command need WEFT_STUDIO_URL=%s", url)
	case d.Path == "" && m.DiscoveryFile != "":
		p.line(warn, "discovery", "this Studio wrote %s, which this shell does not find: an app run here needs WEFT_STUDIO_URL=%s", m.DiscoveryFile, url)
		p.sub("./.weft is read from the working directory: run from the directory the Studio started in")
	case d.Path == "":
		p.line(warn, "discovery", "no discovery file: an app that sets no WEFT_STUDIO_URL finds no Studio (weft studio and weft dev write one; WEFT_DISCOVERY=off writes none)")
	case strings.TrimRight(d.URL, "/") == url:
		p.line(ok, "discovery", "%s -> %s (this Studio)", d.Path, url)
	default:
		p.line(warn, "discovery", "%s -> %s, ANOTHER Studio (pid %d, db %s): an app that sets no WEFT_STUDIO_URL sends there, not to %s", d.Path, d.URL, d.PID, orNone(d.DB), url)
		if dbPath != "" && d.DB != "" && filepath.Clean(d.DB) == filepath.Clean(dbPath) {
			p.sub("two Studios serve %s: stop one (each has its own live stream; an app's live frames reach only the one it dials)", dbPath)
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// data prints the newest stored run: when it arrived, which weft and
// service sent it — a warning when that weft is not this Studio's (runs
// from an older weft lack what it added; their gaps are badged).
func (p *printer) data(m meta) {
	l := m.Content.Latest
	switch {
	case m.Content.Error != "":
		p.line(warn, "data", "the newest run could not be read (the content line says why)")
		return
	case l == nil:
		p.line(ok, "data", "no run stored yet: an app that records to this Studio fills it")
		return
	case l.Started.IsZero():
		p.line(ok, "data", "newest run %s (this studio predates content.latest.started)", l.RunID)
		return
	}
	ago := time.Since(l.Started)
	if ago < time.Minute {
		ago = ago.Round(time.Second)
	} else {
		ago = ago.Round(time.Minute)
	}
	svc := ""
	if l.Service != "" {
		svc = " (service " + l.Service + ")"
	}
	switch {
	case l.WeftVersion == "":
		p.line(ok, "data", "newest run %s ago%s carries no weft version: a non-weft exporter", ago, svc)
	case l.WeftVersion != m.WeftVersion:
		p.line(warn, "data", "newest run %s ago by weft %s%s, this Studio is weft %s: what the newer one records is missing from it (badged on the run)", ago, l.WeftVersion, svc, m.WeftVersion)
	default:
		p.line(ok, "data", "newest run %s ago by weft %s%s", ago, l.WeftVersion, svc)
	}
}

// otlp prints the OTLP ingest's door: open to a loopback app, walled
// by an ingest token, or off.
func (p *printer) otlp(m meta, url string) {
	switch {
	case !slices.Contains(m.Capabilities, "ingest"):
		why := m.CapabilitiesOff["ingest"]
		if why == "" {
			why = "OTLP ingest is off"
		}
		p.line(warn, "otlp", "%s: an app's spans cannot reach this Studio", why)
	case m.IngestOpen:
		p.line(ok, "otlp", "open: OTLP at %s/v1/traces and /v1/logs takes a loopback app's spans with no ingest token", url)
	default:
		p.line(ok, "otlp", "walled: OTLP at %s/v1/traces and /v1/logs needs the ingest token (otel.Studio(url, token))", url)
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
