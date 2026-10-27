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
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/weftgo/weft/internal/termsafe"
	"github.com/weftgo/weft/obsdb"
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
		msg = termsafe.Text(e.Error.Message)
	}
	var err error = &statusError{Status: status, Body: body, line: fmt.Sprintf("GET %s: %d %s", path, status, msg)}
	if status == http.StatusUnauthorized {
		err = fmt.Errorf("%w (--token or WEFT_STUDIO_TOKEN)", err)
	}
	return err
}

// statusError is a non-2xx answer: its one line, plus the status and
// the body Studio sent (its error, and a badge with a reason when the
// route gave one) for a caller that passes them on — weft mcp does.
type statusError struct {
	Status int
	Body   []byte
	line   string
}

func (e *statusError) Error() string { return e.line }

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
	ID          string     `json:"id"`
	Agent       string     `json:"agent"`
	Status      string     `json:"status"`
	Started     time.Time  `json:"started"`
	Finished    *time.Time `json:"finished"`
	Steps       int        `json:"steps"`
	StoppedBy   string     `json:"stopped_by"`
	CancelCause string     `json:"cancel_cause"`
	Prompt      string     `json:"prompt_preview"`
	PublicID    string     `json:"public_id"`
	Err         string     `json:"err"`
	ErrorType   string     `json:"error_type"`
	CostUSD     *float64   `json:"cost_usd"`
	CostBadge   string     `json:"cost_badge"`
}

// errorCells is the ERROR cell's width: the error cut to this many
// runes, "…" marking the cut.
const errorCells = 60

// errorCell is the ERROR cell (UX-S5a-05): a failed run's kind
// (error_type) and its error's last clause — what a terminal triages
// on — cut to errorCells runes; "-" when the run carries none. The
// error is untrusted text (an OTLP sender's), never printed raw.
func (r runRow) errorCell() string {
	msg := strings.TrimSpace(termsafe.Text(firstLine(r.Err)))
	// "weft: run failed at step 0: model stream: mw: giving up …": the
	// loop's wrapping prefixes say where (the run page shows them), the
	// rest says why.
	msg = errPrefix.ReplaceAllString(msg, "")
	switch {
	case r.ErrorType != "" && msg != "":
		msg = r.ErrorType + ": " + msg
	case r.ErrorType != "":
		msg = r.ErrorType
	}
	if msg == "" {
		return "-"
	}
	if rs := []rune(msg); len(rs) > errorCells {
		return strings.TrimSpace(string(rs[:errorCells-1])) + "…"
	}
	return msg
}

// errPrefix is the loop's and the middleware's wrapping of a run error.
var errPrefix = regexp.MustCompile(`^(weft: )?(run failed at step \d+: )?(model stream: )?(mw: )?`)

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// duration is the DUR cell: finished − started, "-" while running.
func (r runRow) duration() string {
	if r.Finished == nil || r.Finished.Before(r.Started) {
		return "-"
	}
	d := r.Finished.Sub(r.Started)
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// cost is the COST cell: the run's own cost in USD, "$0" for a
// scripted model's exact zero, "-" when unpriced or unknown (Studio's
// cost_badge says why; --json carries it).
func (r runRow) cost() string {
	if r.CostUSD == nil {
		return "-"
	}
	if *r.CostUSD == 0 {
		return "$0"
	}
	if *r.CostUSD < 0.01 {
		return fmt.Sprintf("$%.4f", *r.CostUSD)
	}
	return fmt.Sprintf("$%.2f", *r.CostUSD)
}

// promptCells is the PROMPT cell's width: the preview's first line cut
// to this many runes, "…" marking the cut.
const promptCells = 40

// prompt is the PROMPT cell: the run's prompt_preview — its first line,
// every control character (tabs, escape sequences, carriage returns) a
// space, cut to promptCells runes with "…" — or "-" when the row
// carries none (a content-off run stores no prompt; Studio's run page
// badges why). The preview is untrusted text — an app's prompt, an
// OTLP sender's record — and never reaches the terminal raw.
func (r runRow) prompt() string {
	line, _, _ := strings.Cut(strings.TrimSpace(r.Prompt), "\n")
	line = strings.TrimSpace(termsafe.Text(line))
	if line == "" {
		return "-"
	}
	if rs := []rune(line); len(rs) > promptCells {
		return strings.TrimSpace(string(rs[:promptCells-1])) + "…"
	}
	return line
}

// stoppedBy is the STOPPED BY cell: what ended the run as Studio names
// it (stopped_by), a canceled run's cause beside it, "-" when nothing
// recorded one (a model error, a run still going, an older run).
func (r runRow) stoppedBy() string {
	switch {
	case r.StoppedBy == "":
		return "-"
	case r.CancelCause != "":
		return cell(r.StoppedBy + " (" + r.CancelCause + ")")
	}
	return cell(r.StoppedBy)
}

// cell is a table cell from free text Studio served (an agent name, a
// cause, an id an OTLP sender chose): control characters as spaces,
// "-" for nothing — the one rule every printed cell follows.
func cell(s string) string {
	s = strings.TrimSpace(termsafe.Text(s))
	if s == "" {
		return "-"
	}
	return s
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
	status := fs.String("status", "", "only runs of this `status`: running, succeeded, failed, interrupted, parked or cancelled (the API's status=)")
	limit := fs.Int("limit", 50, "print at most this many runs (0: every matching run)")
	asJSON := fs.Bool("json", false, "print the rows as Studio serves them, a JSON array")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError("runs takes no arguments (got %s)", gotArgs(pos))
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
	c, _ := clientFor(*base, *token, stderr)
	q := url.Values{}
	if *agent != "" {
		q.Set("agent", *agent)
	}
	st, err := obsdb.ParseStatus(*status)
	if err != nil {
		return usageError("--status: %v", err)
	}
	if *failed {
		// --failed narrows nothing a failed word does not: --status
		// cancelled (a failed run whose context ended) keeps its word.
		if st != "" && st.Stored() != obsdb.StatusFailed {
			return usageError("--failed and --status %s ask for different runs", st)
		}
		if st == "" {
			st = obsdb.StatusFailed
		}
	}
	if st != "" {
		q.Set("status", string(st))
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
	// The PUBLIC ID column appears when a row carries one (the id the
	// app's logs and the panel show; weft open takes it).
	pub := false
	for _, r := range rows {
		pub = pub || r.PublicID != ""
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	head := "ID\tAGENT\tSTATUS\tSTARTED\tSTEPS\tDUR\tCOST\t"
	if pub {
		head += "PUBLIC ID\t"
	}
	_, _ = fmt.Fprintln(tw, head+"PROMPT\tSTOPPED BY\tERROR")
	for _, r := range rows {
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s\t%s\t", cell(r.ID), cell(r.Agent), cell(r.Status),
			r.Started.Local().Format("2006-01-02 15:04:05 -0700"), r.Steps, r.duration(), r.cost())
		if pub {
			line += cell(r.PublicID) + "\t"
		}
		_, _ = fmt.Fprintf(tw, "%s%s\t%s\t%s\n", line, r.prompt(), r.stoppedBy(), r.errorCell())
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// The next step, on stderr: stdout stays the table.
	_, _ = fmt.Fprintln(stderr, "weft: weft open <id> shows one in Studio; weft export <id> --format jsonl writes it out")
	return nil
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

// runOpen is `weft open [id]`: the page of a run, a conversation (a
// public id), a session or a trace — the id is tried as each kind in
// turn against the API (UX-S5a-06) — or, with no id, the Studio root
// (UX-S5a-02: on a fresh Studio with no run, the one signed-in link a
// terminal can print). The link is printed and with --open opened.
func runOpen(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("open", stderr)
	base, token := clientFlags(fs)
	open := fs.Bool("open", false, "open the link in the browser too, the token in its fragment")
	withToken := fs.Bool("with-token", false, "print the link with the token in its fragment (#token=), signed in; the token may be the panel tokens' signing key: keep it out of logs")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 || len(pos) == 1 && pos[0] == "" {
		return usageError("open takes at most one id — a run id, a public id, a session id or a trace id (got %s)", gotArgs(pos))
	}
	c, _ := clientFor(*base, *token, stderr)
	ctx := context.Background()
	page := "/"
	if len(pos) == 1 {
		if page, err = pageOf(ctx, c, pos[0]); err != nil {
			return err
		}
	} else if _, err := c.get(ctx, "/api/meta", nil); err != nil {
		// The root: checked the Studio answers and the token opens it.
		return err
	}
	// The printed link is bare unless asked: a fixed token is the panel
	// tokens' signing key and stays out of logs (serveBoot's banner
	// rule). The browser gets it in the fragment.
	printed := pageLink(c.base, page, "")
	if *withToken {
		printed = pageLink(c.base, page, c.token)
	}
	if _, err := fmt.Fprintln(stdout, printed); err != nil {
		return err
	}
	if *open {
		openBrowser(stdout, pageLink(c.base, page, c.token))
	}
	return nil
}

// idKinds are the ids `weft open` takes, in the order it tries them:
// the API route that knows the id and the UI page that shows it.
var idKinds = []struct{ noun, route, page string }{
	{"run", "/api/runs/", "/runs/"},
	{"public id", "/api/public/", "/conversations/"},
	{"session", "/api/sessions/", "/sessions/"},
	{"trace", "/api/traces/", "/traces/"},
}

// pageOf is the UI page of id: the first kind whose route knows it. A
// 404 tries the next kind; any other failure is the error. An id no
// kind knows is an error naming what was tried.
func pageOf(ctx context.Context, c *client, id string) (string, error) {
	var tried []string
	for _, k := range idKinds {
		_, err := c.get(ctx, k.route+url.PathEscape(id), nil)
		var se *statusError
		switch {
		case err == nil:
			return k.page + url.PathEscape(id), nil
		case errors.As(err, &se) && se.Status == http.StatusNotFound:
			tried = append(tried, k.noun)
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("%s is no %s this Studio knows (%s)", termsafe.Text(id), strings.Join(tried, ", "), c.redacted())
}

// pageLink is a UI page, <base><page>, the token in the fragment when
// there is one (the UI adopts it; a fragment never reaches a server or
// a Referer).
func pageLink(base, page, token string) string {
	link := base + page
	if token != "" {
		link += "#token=" + url.QueryEscape(token)
	}
	return link
}

// runLink is a run's page in the UI, <base>/runs/<id>.
func runLink(base, id, token string) string {
	return pageLink(base, "/runs/"+url.PathEscape(id), token)
}

// ── weft export ────────────────────────────────────────────────────

// runExport is `weft export <id>`: GET /api/runs/<id>/export to
// stdout, or with --wefttest the run's save-as-test tree written into a
// package directory — the very files Studio's "save as test" zip holds
// (UX-S5a-07: one layout): <agent>_replay_test.go beside
// testdata/TestReplay_<run>/*.json, a test that passes under go test.
// --fixtures-only keeps the fixtures alone, under <dir>/<test>, where
// wefttest.Replay(t, dir) reads them for a test of your own.
func runExport(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("export", stderr)
	base, token := clientFlags(fs)
	format := fs.String("format", "json", "the export written to stdout: json, jsonl or otlp")
	dir := fs.String("wefttest", "", "write the run's save-as-test tree (the replay test and its testdata/, Studio's zip) into this package `dir` instead")
	fixturesOnly := fs.Bool("fixtures-only", false, "with --wefttest: write the fixtures alone under <dir>/<test> (a suite's testdata), no test file")
	test := fs.String("test", "", "with --fixtures-only: the `name` of the test that replays them, the fixtures' directory (default: the run id)")
	force := fs.Bool("force", false, "with --wefttest: write over a test file or fixtures already there")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] == "" {
		return usageError("export needs exactly one run id (got %s)", gotArgs(pos))
	}
	id := pos[0]
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	c, _ := clientFor(*base, *token, stderr)
	ctx := context.Background()
	if *dir == "" {
		if set["test"] || set["force"] || set["fixtures-only"] {
			return usageError("--test, --fixtures-only and --force go with --wefttest")
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
	if !*fixturesOnly {
		if set["test"] {
			return usageError("--test goes with --fixtures-only: the save-as-test tree names its test TestReplay_<run>, its fixtures' directory with it")
		}
		body, err := c.get(ctx, runPath(id)+"/export", url.Values{"format": {"wefttest"}})
		if err != nil {
			return err
		}
		tree, err := unzipTree(body, *dir, *force)
		if err != nil {
			return fmt.Errorf("export %s: %w", id, err)
		}
		_, err = fmt.Fprintf(stdout, "weft: wrote %s and %d fixtures under %s in %s; go test there replays run %s (rename the package clause to the directory's package plus _test)\n",
			tree.test, tree.fixtures, tree.testdata, *dir, id)
		return err
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

// exportTree is what unzipTree wrote: the test file's name, the
// fixtures' directory (relative to the package directory) and their
// count.
type exportTree struct {
	test, testdata string
	fixtures       int
}

// unzipTree writes Studio's save-as-test zip into the package
// directory dir exactly as it is laid out — the one root *_test.go and
// testdata/<test>/*.json, nothing else — so the terminal's tree is the
// UI's byte for byte (UX-S5a-07). The zip is checked whole (names,
// the one test file, the one fixture directory, the total size) before
// the first write. A test file or fixtures already there are refused
// without force; with it the test file is replaced and the fixture
// directory's *.json removed first (as wefttest.Record replaces a
// test's fixtures: a stale one would answer in place of
// ErrNoFixture). Nothing is followed through a symlink: a symlinked
// testdata or testdata/<test> is refused before any write, and a
// symlink at a file path is removed, never written through.
func unzipTree(body []byte, dir string, force bool) (exportTree, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return exportTree{}, fmt.Errorf("the wefttest export is not a zip: %w", err)
	}
	plain := func(name string) bool {
		return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
	}
	var (
		tree    exportTree
		testZip *zip.File
		fx      []*zip.File
		names   []string
		testDir string
		total   uint64
	)
	for _, f := range zr.File {
		if !f.Mode().IsRegular() {
			return exportTree{}, fmt.Errorf("the wefttest export holds %q: want a test file and fixture files", f.Name)
		}
		total += f.UncompressedSize64
		if total > uint64(maxUnzip) {
			return exportTree{}, fmt.Errorf("the wefttest export unzips to more than %d bytes", maxUnzip)
		}
		if plain(f.Name) && strings.HasSuffix(f.Name, "_test.go") {
			if testZip != nil {
				return exportTree{}, fmt.Errorf("the wefttest export holds two test files (%q, %q)", testZip.Name, f.Name)
			}
			testZip = f
			continue
		}
		rest, ok := strings.CutPrefix(f.Name, "testdata/")
		test, file, cut := strings.Cut(rest, "/")
		if !ok || !cut || !plain(test) || !plain(file) || testDir != "" && test != testDir {
			return exportTree{}, fmt.Errorf("the wefttest export holds %q: want <name>_test.go and fixtures under one testdata/<test>/", f.Name)
		}
		testDir = test
		fx = append(fx, f)
		names = append(names, file)
	}
	if testZip == nil || len(fx) == 0 {
		return exportTree{}, errors.New("the wefttest export holds no test file or no fixtures: a Studio before save as test (plan F6) — use --fixtures-only")
	}
	tree = exportTree{test: testZip.Name, testdata: "testdata/" + testDir + "/", fixtures: len(fx)}
	fixDir := filepath.Join(dir, "testdata", testDir)
	testPath := filepath.Join(dir, testZip.Name)
	// Nothing under dir is followed through a symlink: testdata and
	// testdata/<test> must be real directories (or absent), else the
	// fixtures would land — and stale *.json be removed — wherever the
	// link points.
	for _, p := range []string{filepath.Join(dir, "testdata"), fixDir} {
		if fi, err := os.Lstat(p); err == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir()) {
			return exportTree{}, fmt.Errorf("%s is a symlink or not a directory: the export writes only into real directories under %s", p, dir)
		}
	}
	if !force {
		if _, err := os.Lstat(testPath); err == nil {
			return exportTree{}, fmt.Errorf("%s exists: --force writes over it", testPath)
		}
		if entries, err := os.ReadDir(fixDir); err == nil && len(entries) > 0 {
			return exportTree{}, fmt.Errorf("%s is not empty: --force writes into it", fixDir)
		}
	}
	read := func(f *zip.File, left int64) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		// The declared sizes passed the cap; the read is bounded by what
		// is left of it, so a lying header cannot exceed it.
		b, err := io.ReadAll(io.LimitReader(rc, left+1))
		if err == nil && int64(len(b)) > left {
			err = fmt.Errorf("the wefttest export unzips to more than %d bytes", maxUnzip)
		}
		return b, err
	}
	left := maxUnzip
	testBody, err := read(testZip, left)
	if err != nil {
		return exportTree{}, err
	}
	left -= int64(len(testBody))
	bodies := make([][]byte, len(fx))
	for i, f := range fx {
		if bodies[i], err = read(f, left); err != nil {
			return exportTree{}, fmt.Errorf("%s: %w", f.Name, err)
		}
		left -= int64(len(bodies[i]))
	}
	if err := os.MkdirAll(fixDir, 0o755); err != nil {
		return exportTree{}, err
	}
	stale, err := filepath.Glob(filepath.Join(fixDir, "*.json"))
	if err != nil {
		return exportTree{}, err
	}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return exportTree{}, fmt.Errorf("clear the target: %w", err)
		}
	}
	if err := writeNew(testPath, testBody); err != nil {
		return exportTree{}, err
	}
	for i := range fx {
		if err := writeNew(filepath.Join(fixDir, names[i]), bodies[i]); err != nil {
			return exportTree{}, err
		}
	}
	return tree, nil
}

// unzipFixtures writes the export's fixtures into dir. The zip holds
// the fixtures under testdata/<test>/ beside a generated replay test
// at its root (plan F6) — or, from a Studio before F6, flat. Every
// fixture entry must be a plain file name inside that one directory
// (or at the root, flat) and the skeleton a root *_test.go, so nothing
// lands outside dir; the skeleton is not written (this command writes
// fixtures where --test names them, which the skeleton's test name
// need not match: the Studio download carries it). The zip is checked
// whole (names and total size) before the first write. The *.json
// files already in dir are removed first, as wefttest.Record replaces
// a test's fixtures: Replay loads every *.json there, and a stale one
// from an earlier export would answer in place of ErrNoFixture. A
// symlink among them is removed, never followed.
func unzipFixtures(body []byte, dir string) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return 0, fmt.Errorf("the wefttest export is not a zip: %w", err)
	}
	plain := func(name string) bool {
		return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
	}
	var (
		fixtures []*zip.File
		names    []string
		testDir  string
	)
	for _, f := range zr.File {
		name := f.Name
		if !f.Mode().IsRegular() {
			return 0, fmt.Errorf("the wefttest export holds %q: want fixture files", f.Name)
		}
		if plain(name) && strings.HasSuffix(name, "_test.go") {
			continue // the replay skeleton (plan F6)
		}
		if rest, ok := strings.CutPrefix(name, "testdata/"); ok {
			test, file, ok := strings.Cut(rest, "/")
			if !ok || !plain(test) || !plain(file) || testDir != "" && test != testDir {
				return 0, fmt.Errorf("the wefttest export holds %q: want fixture files under one testdata/<test>/", f.Name)
			}
			testDir, name = test, file
		} else if !plain(name) {
			return 0, fmt.Errorf("the wefttest export holds %q: want fixture files under testdata/<test>/", f.Name)
		}
		fixtures = append(fixtures, f)
		names = append(names, name)
	}
	if len(fixtures) == 0 {
		return 0, errors.New("the wefttest export holds no fixtures")
	}
	if testDir != "" {
		// F6's layout is all under testdata/<test>/: a flat fixture
		// beside it would land in the same target, a same-named one
		// overwriting the other silently.
		for _, f := range fixtures {
			if !strings.HasPrefix(f.Name, "testdata/") {
				return 0, fmt.Errorf("the wefttest export mixes flat fixtures (%q) with testdata/%s/", f.Name, testDir)
			}
		}
	}
	var total uint64
	for _, f := range fixtures {
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
	for i, f := range fixtures {
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
		if err := writeNew(filepath.Join(dir, names[i]), b); err != nil {
			return 0, err
		}
	}
	return len(fixtures), nil
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
