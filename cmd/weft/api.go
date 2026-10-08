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
		// An export is a whole run: the bound is generous, never absent.
		http: &http.Client{Timeout: 2 * time.Minute},
	}
}

// maxBody bounds what a client reads from one response.
const maxBody = 512 << 20

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
		return nil, fmt.Errorf("studio not reachable at %s: %w (weft studio starts one; --url or WEFT_STUDIO_URL points at another)", c.base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("GET %s: the response exceeds %d MiB", path, maxBody>>20)
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
		for _, raw := range page.Runs {
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
				break pages
			}
		}
		if page.NextBefore == nil {
			break
		}
		q.Set("before", page.NextBefore.Format(time.RFC3339Nano))
		q.Del("before_id")
		if page.NextBeforeID != nil {
			q.Set("before_id", *page.NextBeforeID)
		}
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
	open := fs.Bool("open", false, "open the link in the browser too")
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
	link := runLink(c.base, id, c.token)
	if _, err := fmt.Fprintln(stdout, link); err != nil {
		return err
	}
	if *open {
		openBrowser(stdout, link)
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
// outside dir; the zip is checked whole before the first write.
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return 0, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxBody+1))
		_ = rc.Close()
		if err != nil {
			return 0, fmt.Errorf("%s: %w", f.Name, err)
		}
		if len(b) > maxBody {
			return 0, fmt.Errorf("%s exceeds %d MiB", f.Name, maxBody>>20)
		}
		if err := os.WriteFile(filepath.Join(dir, f.Name), b, 0o644); err != nil {
			return 0, err
		}
	}
	return len(zr.File), nil
}
