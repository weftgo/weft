package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// The API clients: runs, open and export are thin clients of the
// Studio JSON API. Each request is one documented route; nothing here
// computes what Studio serves.

// client talks to one Studio's API.
type client struct {
	base  string // the Studio's root URL, no trailing slash
	token string
	http  *http.Client
}

func newClient(base, token string) *client {
	return &client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http: &http.Client{
			// An export is a whole run: the bound is generous, never absent.
			Timeout: 2 * time.Minute,
			// The client talks to --url only: a redirect is an error,
			// never followed — Go keeps the bearer on a same-host
			// other-port hop and on https→http (the doctor's rule).
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// redacted is the Studio's URL for a message: a password in its
// userinfo is never printed.
func (c *client) redacted() string {
	u, err := url.Parse(c.base)
	if err != nil {
		return "the --url given"
	}
	return u.Redacted()
}

// maxBody bounds what a client reads from one response.
const maxBody = 512 << 20

// maxUnzip bounds what one wefttest export unzips to, all entries
// together (a variable so a test reaches it).
var maxUnzip int64 = maxBody

// get answers GET path?q with the response body; a non-2xx answer is
// an error carrying Studio's own error message.
func (c *client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("studio not reachable at %s: %w (weft studio starts one; --url or WEFT_STUDIO_URL points at another)", c.redacted(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("GET %s: the response exceeds %d MiB", path, maxBody>>20)
	}
	if resp.StatusCode/100 == 3 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			loc = "(no Location)"
		}
		return nil, fmt.Errorf("GET %s: %d, a redirect to %s, not followed: point --url at the Studio itself", path, resp.StatusCode, loc)
	}
	if resp.StatusCode/100 != 2 {
		return nil, apiError(path, resp.StatusCode, body)
	}
	return body, nil
}

// apiError is a non-2xx answer as one line: Studio's error message
// when the body is its error shape, the status otherwise.
func apiError(path string, status int, body []byte) error {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := http.StatusText(status)
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	err := fmt.Errorf("GET %s: %d %s", path, status, msg)
	if status == http.StatusUnauthorized {
		err = fmt.Errorf("%w (--token or WEFT_STUDIO_TOKEN)", err)
	}
	return err
}

// runPath is /api/runs/<id>: a run id may carry slashes (a subagent's
// child id), escaped the way the UI escapes them.
func runPath(id string) string { return "/api/runs/" + url.PathEscape(id) }

// parseArgs parses fs over args with the positional arguments allowed
// anywhere — `weft export <id> --wefttest dir` — and returns them. A
// "--" ends the flags: everything after it is positional.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := parse(fs, args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(pos, rest...), nil
		}
		pos, args = append(pos, rest[0]), rest[1:]
	}
}

// ── weft runs ──────────────────────────────────────────────────────

// runRow is the part of GET /api/runs' row the table prints; --json
// prints the row as Studio serves it.
type runRow struct {
	ID      string    `json:"id"`
	Agent   string    `json:"agent"`
	Status  string    `json:"status"`
	Started time.Time `json:"started"`
	Steps   int       `json:"steps"`
}

// runsPageSize is the page each GET /api/runs asks for (a variable so
// a test pages through the cursor).
var runsPageSize = 100

// runRuns is `weft runs`: the newest top-level runs, one row each,
// paged through GET /api/runs's cursor.
func runRuns(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("runs", stderr)
	base, token := clientFlags(fs)
	agent := fs.String("agent", "", "only this agent's runs (the API's agent=)")
	since := fs.String("since", "", "only runs started after this: a `duration` back from now (2h, 30m) or an RFC 3339 time")
	failed := fs.Bool("failed", false, "only failed runs (the API's status=failed)")
	limit := fs.Int("limit", 50, "print at most this many runs (0: every matching run)")
	asJSON := fs.Bool("json", false, "print the rows as Studio serves them, a JSON array")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError("runs takes no arguments, got %q", pos)
	}
	if *limit < 0 {
		return usageError("--limit must be 0 or more")
	}
	var after time.Time
	if *since != "" {
		if after, err = parseSince(*since, time.Now()); err != nil {
			return err
		}
	}
	c := newClient(*base, apiToken(*token))
	q := url.Values{}
	if *agent != "" {
		q.Set("agent", *agent)
	}
	if *failed {
		q.Set("status", "failed")
	}
	var raws []json.RawMessage
	var rows []runRow
	ctx := context.Background()
	cut := false // stopped at --limit with more runs to list
	seen := map[string]bool{}
pages:
	for {
		q.Set("limit", strconv.Itoa(runsPageSize))
		body, err := c.get(ctx, "/api/runs", q)
		if err != nil {
			return err
		}
		var page struct {
			Runs         []json.RawMessage `json:"runs"`
			NextBefore   *time.Time        `json:"next_before"`
			NextBeforeID *string           `json:"next_before_id"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("GET /api/runs: %w", err)
		}
		for i, raw := range page.Runs {
			var r runRow
			if err := json.Unmarshal(raw, &r); err != nil {
				return fmt.Errorf("GET /api/runs: %w", err)
			}
			// Newest first: the first run older than --since ends it.
			if !after.IsZero() && r.Started.Before(after) {
				break pages
			}
			raws, rows = append(raws, raw), append(rows, r)
			if *limit > 0 && len(rows) == *limit {
				cut = i < len(page.Runs)-1 || page.NextBefore != nil
				break pages
			}
		}
		// A page with no runs, or a cursor this listing already
		// followed, ends it: a cursor that does not move never loops.
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		before, beforeID := page.NextBefore.Format(time.RFC3339Nano), ""
		if page.NextBeforeID != nil {
			beforeID = *page.NextBeforeID
		}
		if seen[before+"\x00"+beforeID] {
			_, _ = fmt.Fprintln(stderr, "weft: the runs cursor did not move; stopping")
			break
		}
		seen[before+"\x00"+beforeID] = true
		q.Set("before", before)
		q.Del("before_id")
		if beforeID != "" {
			q.Set("before_id", beforeID)
		}
	}
	// Truncation is visible: the limit says so when it hid runs.
	if cut {
		_, _ = fmt.Fprintf(stderr, "weft: showing the newest %d; --limit 0 lists all\n", *limit)
	}
	if *asJSON {
		if raws == nil {
			raws = []json.RawMessage{}
		}
		b, err := json.MarshalIndent(raws, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\n", b)
		return err
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(stderr, "weft: no runs match")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tAGENT\tSTATUS\tSTARTED\tSTEPS")
	for _, r := range rows {
		agent := r.Agent
		if agent == "" {
			agent = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", r.ID, agent, r.Status,
			r.Started.Local().Format("2006-01-02 15:04:05"), r.Steps)
	}
	return tw.Flush()
}

// parseSince reads --since: a duration back from now, or an RFC 3339
// time.
func parseSince(v string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(v); err == nil {
		if d < 0 {
			return time.Time{}, usageError("--since %s: a duration back from now is positive", v)
		}
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, usageError("--since %q: want a duration (2h, 30m) or an RFC 3339 time (2026-10-08T09:00:00Z)", v)
}

// ── weft open ──────────────────────────────────────────────────────

// runOpen is `weft open <id>`: the run's page, checked against
// GET /api/runs/<id>, printed and with --open opened.
func runOpen(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("open", stderr)
	base, token := clientFlags(fs)
	open := fs.Bool("open", false, "open the link in the browser too, the token in its fragment")
	withToken := fs.Bool("with-token", false, "print the link with the token in its fragment (#token=); the token may be the panel tokens' signing key: keep it out of logs")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] == "" {
		return usageError("open takes one run id, got %q", pos)
	}
	id := pos[0]
	c := newClient(*base, apiToken(*token))
	if _, err := c.get(context.Background(), runPath(id), nil); err != nil {
		return err
	}
	// The printed link is bare unless asked: a fixed token is the panel
	// tokens' signing key and stays out of logs (serveBoot's banner
	// rule). The browser gets it in the fragment.
	printed := runLink(c.base, id, "")
	if *withToken {
		printed = runLink(c.base, id, c.token)
	}
	if _, err := fmt.Fprintln(stdout, printed); err != nil {
		return err
	}
	if *open {
		openBrowser(stdout, runLink(c.base, id, c.token))
	}
	return nil
}

// runLink is a run's page in the UI, <base>/runs/<id>, the token in
// the fragment when there is one (the UI adopts it; a fragment never
// reaches a server or a Referer).
func runLink(base, id, token string) string {
	link := base + "/runs/" + url.PathEscape(id)
	if token != "" {
		link += "#token=" + url.QueryEscape(token)
	}
	return link
}

// ── weft export ────────────────────────────────────────────────────

// runExport is `weft export <id>`: GET /api/runs/<id>/export to
// stdout, or with --wefttest the fixtures unzipped where
// wefttest.Replay reads them.
func runExport(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("export", stderr)
	base, token := clientFlags(fs)
	format := fs.String("format", "json", "the export written to stdout: json, jsonl or otlp")
	dir := fs.String("wefttest", "", "write the run's wefttest replay fixtures under this `dir` (a suite's testdata) instead")
	test := fs.String("test", "", "with --wefttest: the `name` of the test that replays them, the fixtures' directory (default: the run id)")
	force := fs.Bool("force", false, "with --wefttest: write into a non-empty target directory")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] == "" {
		return usageError("export takes one run id, got %q", pos)
	}
	id := pos[0]
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	c := newClient(*base, apiToken(*token))
	ctx := context.Background()
	if *dir == "" {
		if set["test"] || set["force"] {
			return usageError("--test and --force go with --wefttest")
		}
		switch *format {
		case "json", "jsonl", "otlp":
		case "wefttest":
			return usageError("--format wefttest is a zip of fixtures: use --wefttest <dir>")
		default:
			return usageError("--format must be json, jsonl or otlp")
		}
		body, err := c.get(ctx, runPath(id)+"/export", url.Values{"format": {*format}})
		if err != nil {
			return err
		}
		_, err = stdout.Write(body)
		return err
	}
	if set["format"] {
		return usageError("--format and --wefttest are two exports: pick one")
	}
	name := *test
	if name == "" {
		name = id
	}
	// A subtest's name (TestX/case) is a nested directory, as
	// wefttest.Replay reads it; anything that climbs out is refused.
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return usageError("--test %q must name a directory inside %s, not an absolute path", name, *dir)
	}
	target := filepath.Join(*dir, filepath.FromSlash(name))
	if rel, err := filepath.Rel(*dir, target); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return usageError("--test %q must name a directory inside %s", name, *dir)
	}
	if !*force {
		if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
			return fmt.Errorf("%s is not empty: --force writes into it", target)
		}
	}
	body, err := c.get(ctx, runPath(id)+"/export", url.Values{"format": {"wefttest"}})
	if err != nil {
		return err
	}
	n, err := unzipFixtures(body, target)
	if err != nil {
		return fmt.Errorf("export %s: %w", id, err)
	}
	_, err = fmt.Fprintf(stdout, "weft: wrote %d fixtures to %s; a test named %s replays them with wefttest.Replay(t, %q)\n",
		n, target, name, *dir)
	return err
}

// unzipFixtures writes the export's flat zip into dir. Every entry
// must be a plain file name — the export zips flat — so nothing lands
// outside dir; the zip is checked whole (names and total size) before
// the first write. The *.json files already in dir are removed first,
// as wefttest.Record replaces a test's fixtures: Replay loads every
// *.json there, and a stale one from an earlier export would answer
// in place of ErrNoFixture. A symlink among them is removed, never
// followed.
func unzipFixtures(body []byte, dir string) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return 0, fmt.Errorf("the wefttest export is not a zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == "" || f.Name == "." || f.Name == ".." || strings.ContainsAny(f.Name, `/\:`) || !f.Mode().IsRegular() {
			return 0, fmt.Errorf("the wefttest export holds %q: want flat fixture files", f.Name)
		}
	}
	if len(zr.File) == 0 {
		return 0, errors.New("the wefttest export holds no fixtures")
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > uint64(maxUnzip) {
			return 0, fmt.Errorf("the wefttest export unzips to more than %d bytes", maxUnzip)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	stale, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return 0, err
	}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return 0, fmt.Errorf("clear the target: %w", err)
		}
	}
	var written int64
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return 0, err
		}
		// The declared sizes passed the total cap; the read is bounded
		// by what is left of it, so a lying header cannot exceed it.
		b, err := io.ReadAll(io.LimitReader(rc, maxUnzip-written+1))
		_ = rc.Close()
		if err != nil {
			return 0, fmt.Errorf("%s: %w", f.Name, err)
		}
		if written += int64(len(b)); written > maxUnzip {
			return 0, fmt.Errorf("the wefttest export unzips to more than %d bytes", maxUnzip)
		}
		if err := writeNew(filepath.Join(dir, f.Name), b); err != nil {
			return 0, err
		}
	}
	return len(zr.File), nil
}

// writeNew writes b to path, replacing whatever is there without
// following it: a symlink planted at path is removed, not written
// through.
func writeNew(path string, b []byte) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
