package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/version"
)

// `weft mcp` (plan I4.1): a stdio MCP server over the Studio read API,
// so a coding agent (Claude Code, Cursor) reads runs while you debug.
// It is a third client of the same routes `weft runs`/`open`/`export`
// call — it adds no route and computes nothing Studio does not serve:
// each tool is one GET (explain_failure composes three), its output
// the route's JSON, compacted, bounded by max_bytes with a visible note.
//
// Read only, by construction: every tool goes through mcpGet, which
// issues GET and nothing else, and no write verb (approve, deny, steer,
// cancel, delete, …) is registered. Write verbs stay out until the
// approval story for agents exists (PQ5's read half).

// mcpDefaultBytes is a tool's output bound when the call names none —
// weft's own tool-result default; mcpMaxBytes the most a call may ask.
const (
	mcpDefaultBytes = 64 << 10
	mcpMaxBytes     = 4 << 20
)

// mcpInstructions is what an MCP client reads about this server as a
// whole.
const mcpInstructions = `weft mcp is READ-ONLY: it reads a weft Studio's recorded agent runs ` +
	`(the Studio JSON API, GET only); it never approves, denies, steers, cancels, deletes, ` +
	`replays or starts anything. Start with explain_failure <run id> for "why did run X fail"; ` +
	`find runs with runs (newest first, filters) or search (text and filters); ` +
	`drill in with run (the row), step (one step assembled), request (the exact model input), ` +
	`events, tools, diff (two runs step by step), export (the whole run). ` +
	`Outputs are the route's JSON; a cut output says so in a final note (raise max_bytes or narrow the query). ` +
	`Badges and holes ("not_recorded", "stripped", "hidden", "gap", ...) mean the data is absent for that reason — never empty. ` +
	`A 403 means this token may not read that (a read-scoped panel token).`

// mcpTool is one registered tool: its name and the paths it reads,
// for the usage text and the tests.
type mcpTool struct {
	name, routes, use string
}

// mcpTools is the tool table, in the order --help and the docs list it.
var mcpTools = []mcpTool{
	{"explain_failure", "run + step + transcript", "why a run failed (or how it ended): status, error, the failing step's tool calls and results, holes, last assistant text"},
	{"runs", "GET /api/runs", "list runs, newest first (agent, status, session, public_id, experiment_id, all, parent, limit, before cursor)"},
	{"search", "GET /api/search", "find runs by text (prompt, answer, error, tool names) and filters (since/until, model, min_tokens, min_cost, sort)"},
	{"run", "GET /api/runs/{id}", "one run's row: status, error, steps, usage, cost, children, holes"},
	{"step", "GET /api/runs/{id}/steps/{n}", "one step assembled: request, attempts, events, tool calls and results, children, holes"},
	{"request", "GET /api/runs/{id}/requests", "the exact model input per step (prompt, catalog, params); transcript=true adds the step's messages"},
	{"events", "GET /api/runs/{id}/events", "the run's durable event stream, paged (after, limit)"},
	{"tools", "GET /api/runs/{id}/tools", "the tool catalogs the run offered the model"},
	{"diff", "GET /api/diff", "two runs compared step by step (what changed, what is not comparable)"},
	{"export", "GET /api/runs/{id}/export", "the whole run as one document: json, jsonl or otlp (not wefttest)"},
	{"sessions", "GET /api/sessions", "list thread sessions"},
	{"session", "GET /api/sessions/{id}", "one session with its turns"},
	{"meta", "GET /api/meta", "what this Studio is and can do: versions, capabilities, debug_scope, retention"},
}

// mcpToolTable is the tool list as --help prints it.
func mcpToolTable() string {
	var b strings.Builder
	for _, t := range mcpTools {
		fmt.Fprintf(&b, "  %-16s %s\n  %-16s   (%s)\n", t.name, t.use, "", t.routes)
	}
	return b.String()
}

// runMCP is `weft mcp`: resolve the Studio (--url, WEFT_STUDIO_URL,
// the discovery file, the default address), check it answers
// /api/meta, then serve the read tools over stdio until stdin closes.
// A Studio that does not answer yet is no reason to exit: an MCP
// client starts its servers once, at session start, before the
// developer's `weft studio`/`weft dev` may be up — so it WARNs and
// serves, and each tool call looks again (startMCP).
// stdout is the protocol: every line of ours goes to stderr.
func runMCP(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("mcp", stderr)
	base := fs.String("url", "", "the Studio to talk to (default: $WEFT_STUDIO_URL, else the discovery file's, else "+defaultURL+")")
	token := fs.String("token", "", "API token (default: $WEFT_STUDIO_TOKEN, else the discovery file's when it names that Studio; never printed)")
	readOnly := fs.Bool("read-only", true, "implied and the only mode: every tool is a GET; write verbs (approve, deny, steer, cancel, delete, …) are not offered")
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), `Usage: weft mcp [--url] [--token]

A stdio MCP server over a running Studio's read API, for a coding agent
(Claude Code, Cursor) to read runs while you debug. Read only: every tool
is a GET; nothing is approved, denied, steered, cancelled or deleted.

  claude mcp add weft -- weft mcp
  .cursor/mcp.json: {"mcpServers":{"weft":{"command":"weft","args":["mcp"]}}}

The running Studio is found through its discovery file; to name another:
  claude mcp add weft -- weft mcp --url http://127.0.0.1:7331
  .cursor/mcp.json: {"mcpServers":{"weft":{"command":"weft","args":["mcp","--url","http://127.0.0.1:7331"]}}}

The Studio: --url, else $WEFT_STUDIO_URL, else the discovery file a running
weft studio/weft dev wrote (WEFT_DISCOVERY=off ignores it), else `+defaultURL+`.
The token: --token, else $WEFT_STUDIO_TOKEN, else the discovery file's (when it
names that Studio); sent in the Authorization header only, never printed.

Tools (each output is the route's JSON, cut at max_bytes — default 64 KiB — with a note):
`+mcpToolTable()+`
Flags:
`)
		fs.PrintDefaults()
	}
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError("mcp takes no arguments, got %q", pos)
	}
	if !*readOnly {
		return usageError("--read-only=false: weft mcp has no write mode (write verbs stay out until the approval story for agents exists)")
	}
	srv, err := startMCP(context.Background(), *base, *token, stderr)
	if err != nil {
		return err
	}
	t := &sdk.IOTransport{Reader: os.Stdin, Writer: nopWriteCloser{stdout}}
	if err := srv.Run(context.Background(), t); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}

// startMCP resolves the Studio, checks /api/meta and builds the
// server. Nothing answering is a WARN on stderr, not an exit: the
// server serves, and each tool call re-resolves and checks again
// (studioLink). A Studio that answers and refuses (a 401, a 404 from
// something that is not a Studio) is still the command's error.
func startMCP(ctx context.Context, urlFlag, tokenFlag string, stderr io.Writer) (*sdk.Server, error) {
	u, tok, from := mcpStudio(urlFlag, tokenFlag)
	c := newClient(u, tok)
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	var resolve func() (string, string)
	if urlFlag == "" {
		// No --url: every look re-reads WEFT_STUDIO_URL and the
		// discovery file — a Studio started later, or `weft dev` on
		// 7332–7340, is found without a restart. An explicit --url is
		// retried as given.
		resolve = func() (string, string) { u, tok, _ := mcpStudio("", tokenFlag); return u, tok }
	}
	down := false
	if _, err := c.get(ctx, "/api/meta", nil); err != nil {
		var se *statusError
		if errors.As(err, &se) {
			return nil, fmt.Errorf("mcp: no Studio answers /api/meta: %w", err) // main prefixes "weft: "
		}
		down = true
		log.Warn("weft mcp: no Studio answers /api/meta yet; tools will retry per call", "err", err.Error())
	}
	_, _ = fmt.Fprintf(stderr, "weft mcp: %d read-only tools over stdio for the Studio at %s (token %s)\n",
		len(mcpTools), c.redacted(), from)
	link := newStudioLink(c, resolve, down, stderr)
	// The token too comes from the discovery file when neither --token
	// nor WEFT_STUDIO_TOKEN named one: a Studio restarted on the same
	// port with a new token (a fileless DB's per-process one,
	// --rotate-token) answers 401 to the old one, and the next call
	// re-reads the file.
	link.rereadToken = resolve != nil && tokenFlag == "" && os.Getenv("WEFT_STUDIO_TOKEN") == ""
	return newMCPServerLink(link, log), nil
}

// studioLink is the Studio the tools read: the one walled client, and
// — when the last attempt failed with a transport error, or a 401
// while the token is the discovery file's — a fresh look before the
// next request: re-resolve (no --url), then GET /api/meta. Nothing
// answering is the call's error, the startup line with its fix; a
// Studio found again is said on stderr once.
type studioLink struct {
	mu      sync.Mutex
	c       *client
	resolve func() (base, token string) // nil: --url given, retried as given
	down    bool                        // the next request looks again first
	// rereadToken: no --token or WEFT_STUDIO_TOKEN with no --url — a
	// 401 means the file's token may have changed, so it looks again.
	rereadToken bool
	stderr      io.Writer
}

// hint is what a failure that makes the next call look again says.
func (l *studioLink) hint() string {
	if l.resolve != nil {
		return "the next call looks again (WEFT_STUDIO_URL, the discovery file): no restart needed"
	}
	return "the next call retries the --url given"
}

// newStudioLink walls c's transport to GET and HEAD — every client the
// link ever uses shares that http.Client.
func newStudioLink(c *client, resolve func() (string, string), down bool, stderr io.Writer) *studioLink {
	c.http.Transport = readOnlyTransport{next: c.http.Transport}
	if stderr == nil {
		stderr = io.Discard
	}
	return &studioLink{c: c, resolve: resolve, down: down, stderr: stderr}
}

// client is the client the next request goes through: the current one,
// or — after a transport failure — the one a fresh look found
// answering /api/meta; an error when none answers.
func (l *studioLink) client(ctx context.Context) (*client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.down {
		return l.c, nil
	}
	c := l.c
	if l.resolve != nil {
		u, tok := l.resolve()
		nc := *l.c // the same walled http.Client: never a second one
		nc.base, nc.token = strings.TrimRight(u, "/"), tok
		c = &nc
	}
	if _, err := c.get(ctx, "/api/meta", nil); err != nil {
		var se *statusError
		if !errors.As(err, &se) {
			return nil, fmt.Errorf("no Studio answers /api/meta: %w; %s", err, l.hint())
		}
	}
	if c.base != l.c.base {
		_, _ = fmt.Fprintf(l.stderr, "weft mcp: now reading the Studio at %s\n", c.redacted())
	}
	l.c, l.down = c, false
	return c, nil
}

// failed marks the link down after a failure on c that a fresh look
// may cure — a transport error (a request that got no answer), or a
// 401 while the token is the discovery file's (rereadToken) — and
// reports whether it was one: the error then says the next call looks
// again.
func (l *studioLink) failed(c *client, err error) bool {
	if err == nil {
		return false
	}
	var se *statusError
	if errors.As(err, &se) && (se.Status != http.StatusUnauthorized || !l.rereadToken) {
		return false
	}
	l.mu.Lock()
	if l.c == c {
		l.down = true
	}
	l.mu.Unlock()
	return true
}

func (l *studioLink) current() *client {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.c
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// mcpStudio resolves the Studio and its token and says where the token
// came from (never the token): the URL is --url, else WEFT_STUDIO_URL,
// else the discovery file's, else the default address; the token is
// --token, else WEFT_STUDIO_TOKEN, else the discovery file's when that
// file names the resolved URL (a token goes only to the Studio it
// belongs to). WEFT_DISCOVERY=off skips the file.
func mcpStudio(urlFlag, tokenFlag string) (base, token, from string) {
	base = urlFlag
	if base == "" {
		base = studioURL()
	}
	off := strings.EqualFold(strings.TrimSpace(os.Getenv("WEFT_DISCOVERY")), "off")
	var info discovery.Info
	found := false
	if !off {
		// Read the file whatever named the URL: its token is used only
		// when it names the same Studio.
		info, _, found = discovery.Lookup(func(k string) string {
			if k == "WEFT_STUDIO_URL" {
				return ""
			}
			return os.Getenv(k)
		}, slog.New(slog.DiscardHandler))
	}
	if urlFlag == "" && os.Getenv("WEFT_STUDIO_URL") == "" && found {
		base = info.URL
	}
	switch {
	case tokenFlag != "":
		return base, tokenFlag, "from --token"
	case os.Getenv("WEFT_STUDIO_TOKEN") != "":
		return base, os.Getenv("WEFT_STUDIO_TOKEN"), "from WEFT_STUDIO_TOKEN"
	case found && info.Token != "" && strings.TrimRight(info.URL, "/") == strings.TrimRight(base, "/"):
		return base, info.Token, "from the discovery file"
	}
	return base, "", "none"
}

// readOnlyTransport is the wall under every tool: a request with any
// method but GET or HEAD never leaves the process — whatever a handler
// does, a write cannot reach Studio.
type readOnlyTransport struct{ next http.RoundTripper }

func (t readOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, fmt.Errorf("weft mcp is read-only: %s refused", r.Method)
	}
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(r)
}

// studioGetter is all a tool can do with Studio: GET a path (the status
// it answered, its body, and an error for a non-2xx answer or a failed
// request), and name the UI's pages. The tools never hold the client.
type studioGetter interface {
	get(ctx context.Context, path string, q url.Values) (status int, body []byte, err error)
	runLink(id string) string
	redacted() string
}

// linkGetter is studioGetter over the link's current client.
type linkGetter struct{ l *studioLink }

func (g linkGetter) get(ctx context.Context, path string, q url.Values) (int, []byte, error) {
	c, err := g.l.client(ctx)
	if err != nil {
		return 0, nil, err
	}
	b, err := c.get(ctx, path, q)
	if err != nil {
		if g.l.failed(c, err) {
			err = fmt.Errorf("%w; %s", err, g.l.hint())
		}
		var se *statusError
		if errors.As(err, &se) {
			return se.Status, se.Body, err
		}
		return 0, nil, err
	}
	return http.StatusOK, b, nil
}

func (g linkGetter) runLink(id string) string { return runLink(g.l.current().base, id, "") }
func (g linkGetter) redacted() string         { return g.l.current().redacted() }

// newMCPServer builds the server and registers the read tools over c,
// its transport walled to GET and HEAD first, c answering or not.
func newMCPServer(c *client, log *slog.Logger) *sdk.Server {
	return newMCPServerLink(newStudioLink(c, nil, false, nil), log)
}

// newMCPServerLink builds the server and registers the read tools over
// the link.
func newMCPServerLink(link *studioLink, log *slog.Logger) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{
		Name:        "weft",
		Title:       "weft Studio (read-only)",
		Description: "Read a weft Studio's recorded agent runs: GET routes only.",
		Version:     version.Runtime(),
	}, &sdk.ServerOptions{
		Instructions: mcpInstructions,
		Logger:       log,
		// No logging capability (the SDK's historical default); the
		// read-only promise as an experimental capability a client
		// can check.
		Capabilities: &sdk.ServerCapabilities{
			Experimental: map[string]any{"weft": map[string]any{"read_only": true, "methods": []string{"GET"}}},
		},
	})
	m := &mcpReader{s: linkGetter{link}}
	addTool(srv, "explain_failure", `Why did run X fail? Start here. A convenience over run + step + transcript (no new server logic): `+
		`fetches the run row, picks the failing step (the step in flight when the run failed, else its last step), `+
		`fetches that step assembled and the transcript, and answers {status, error, stop_reason, step, tool_calls `+
		`(the step's calls with their results), attempts, run_holes, step_holes, last_assistant_text, url}. `+
		`Works on any run: a succeeded run explains how it ended. Then use step/request for more.`,
		m.explainFailure)
	addTool(srv, "runs", `List runs, newest first (GET /api/runs): one row per run with status, error, steps, usage, cost. `+
		`Top-level runs only unless all=true or parent=<run id>. Filter by agent, status (running|succeeded|failed|interrupted), `+
		`session, public_id, experiment_id. Page with before + before_id from the answer's next_before/next_before_id. `+
		`For text or a time window use search.`,
		m.runs)
	addTool(srv, "search", `Find runs by text and filters (GET /api/search): q terms are case-insensitive substrings of the prompt, `+
		`answer, error or a called tool's name, all required ("quoted phrase" = one term). Filters: agent, tool, status, model, `+
		`error, since/until (RFC 3339), min_tokens, min_cost (USD), sort=started|duration|tokens|cost, limit, offset.`,
		m.search)
	addTool(srv, "run", `One run's row (GET /api/runs/{id}): status, err, steps, stop_reason, usage, cost, its subagent children, `+
		`its holes (why something is absent) and compactions. Use after runs/search to look at one run.`,
		m.run)
	addTool(srv, "step", `One step of a run assembled (GET /api/runs/{id}/steps/{n}, n from 0): status, stop reason, the model asked and `+
		`the one that answered, attempt 1's request, every attempt with its outcome, events, tool calls with args and results, `+
		`child runs, usage, cost, holes. The view for "what happened at step n".`,
		m.step)
	addTool(srv, "request", `The exact model input (GET /api/runs/{id}/requests): one row per model-call attempt with the system prompt, `+
		`tool catalog, params and the messages it carried by reference; step narrows to one step. transcript=true (with step) `+
		`also returns GET /api/runs/{id}/transcript?step=N: the messages step N's model call carried. `+
		`Refused (403) to a read-scoped panel token.`,
		m.request)
	addTool(srv, "events", `The run's durable event stream (GET /api/runs/{id}/events): run_start, step_start, tool_start/finish, `+
		`step_finish, run_finish, … in order. Paged: pass after=<next_after> from the previous answer.`,
		m.events)
	addTool(srv, "tools", `The tool catalogs the run offered the model, by hash (GET /api/runs/{id}/tools): names, descriptions, `+
		`input schemas. Refused (403) to a read-scoped panel token.`,
		m.tools)
	addTool(srv, "diff", `Compare two runs step by step (GET /api/diff?a=&b=): per step ordinal what changed (system, tool_calls, `+
		`tool_results, text, usage) and what is not comparable (unknown), with a summary {changed_steps, first_changed}. `+
		`Use for "what differs between the good run and the bad one".`,
		m.diff)
	addTool(srv, "export", `The whole run as one document (GET /api/runs/{id}/export): format json (default), jsonl or otlp. `+
		`Large: usually cut by max_bytes — prefer run/step/request for questions. The wefttest format is a zip of replay `+
		`fixtures and is not offered here: run "weft export <id> --wefttest dir" in a terminal.`,
		m.export)
	addTool(srv, "sessions", `List thread sessions (GET /api/sessions), newest first: agent and public_id filter, before + before_id page.`,
		m.sessions)
	addTool(srv, "session", `One thread session (GET /api/sessions/{id}): its row and its turns (runs) in order.`,
		m.session)
	addTool(srv, "meta", `What this Studio is and can do (GET /api/meta): versions, database kind, capabilities and capabilities_off `+
		`(why one is off), debug_scope, retention, pricing source. Call when a tool answers 404/403 to see whether the route exists.`,
		m.meta)
	return srv
}

// addTool registers one read tool: its input schema reflected from In
// (the jsonschema tags), no output schema (the output is the route's
// JSON as text), annotated read-only, idempotent and closed-world.
func addTool[In any](srv *sdk.Server, name, desc string, h sdk.ToolHandlerFor[In, any]) {
	sdk.AddTool(srv, &sdk.Tool{Name: name, Description: desc, Annotations: &sdk.ToolAnnotations{
		Title: name, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(bool),
	}}, h)
}

// ── inputs ─────────────────────────────────────────────────────────

// mcpBound is every tool's output bound.
type mcpBound struct {
	MaxBytes int `json:"max_bytes,omitempty" jsonschema:"bound on the output in bytes (default 65536, at most 4194304); a cut output ends with a note saying so"`
}

type mcpMetaIn struct{ mcpBound }

type mcpRunIn struct {
	ID string `json:"id" jsonschema:"the run id (a subagent child's id carries slashes, e.g. parent/0/call)"`
	mcpBound
}

type mcpRunsIn struct {
	Agent        string `json:"agent,omitempty" jsonschema:"only this agent's runs"`
	Status       string `json:"status,omitempty" jsonschema:"running, succeeded, failed, interrupted, parked (an open park: pending calls, no resume) or cancelled (a failed run whose context ended); any other word is a 400"`
	Session      string `json:"session,omitempty" jsonschema:"only this thread session's runs"`
	PublicID     string `json:"public_id,omitempty" jsonschema:"only this public id's runs"`
	ExperimentID string `json:"experiment_id,omitempty" jsonschema:"only this playground experiment's runs"`
	Parent       string `json:"parent,omitempty" jsonschema:"this run's subagent children instead of top-level runs"`
	All          bool   `json:"all,omitempty" jsonschema:"every run, subagent children included"`
	Limit        int    `json:"limit,omitempty" jsonschema:"page size (Studio's default when 0)"`
	Before       string `json:"before,omitempty" jsonschema:"the previous answer's next_before (RFC 3339): the next page"`
	BeforeID     string `json:"before_id,omitempty" jsonschema:"the previous answer's next_before_id, with before"`
	mcpBound
}

type mcpSearchIn struct {
	Q         string  `json:"q,omitempty" jsonschema:"terms: case-insensitive substrings of prompt, answer, error or a tool name, all required"`
	Agent     string  `json:"agent,omitempty" jsonschema:"exact agent name"`
	Tool      string  `json:"tool,omitempty" jsonschema:"runs that called this tool (exact name)"`
	Status    string  `json:"status,omitempty" jsonschema:"running, succeeded, failed, interrupted, parked (an open park: pending calls, no resume) or cancelled (a failed run whose context ended); any other word is a 400"`
	Model     string  `json:"model,omitempty" jsonschema:"exact model name"`
	Error     string  `json:"error,omitempty" jsonschema:"substring of the run's error"`
	Since     string  `json:"since,omitempty" jsonschema:"started at or after (RFC 3339)"`
	Until     string  `json:"until,omitempty" jsonschema:"started before (RFC 3339)"`
	MinTokens int64   `json:"min_tokens,omitempty" jsonschema:"input + output tokens at least"`
	MinCost   float64 `json:"min_cost,omitempty" jsonschema:"USD at least, own + delegated (unpriced runs never match)"`
	Sort      string  `json:"sort,omitempty" jsonschema:"started (default, newest first), duration, tokens or cost (most first)"`
	Session   string  `json:"session,omitempty" jsonschema:"only this thread session's runs"`
	PublicID  string  `json:"public_id,omitempty" jsonschema:"only this public id's runs"`
	All       bool    `json:"all,omitempty" jsonschema:"subagent children too"`
	Limit     int     `json:"limit,omitempty" jsonschema:"page size"`
	Offset    int     `json:"offset,omitempty" jsonschema:"the previous answer's next_offset"`
	mcpBound
}

type mcpStepIn struct {
	ID string `json:"id" jsonschema:"the run id"`
	N  int    `json:"n" jsonschema:"the step ordinal, from 0"`
	mcpBound
}

type mcpRequestIn struct {
	ID         string `json:"id" jsonschema:"the run id"`
	Step       *int   `json:"step,omitempty" jsonschema:"only this step's attempts (ordinal from 0)"`
	From       int    `json:"from,omitempty" jsonschema:"the previous answer's next_from"`
	Limit      int    `json:"limit,omitempty" jsonschema:"page size"`
	Refs       bool   `json:"refs,omitempty" jsonschema:"prompt and catalog by hash instead of inline (smaller)"`
	Transcript bool   `json:"transcript,omitempty" jsonschema:"with step: also the messages that step's model call carried"`
	mcpBound
}

type mcpEventsIn struct {
	ID    string `json:"id" jsonschema:"the run id"`
	After int64  `json:"after,omitempty" jsonschema:"the previous answer's next_after"`
	Limit int    `json:"limit,omitempty" jsonschema:"page size"`
	mcpBound
}

type mcpDiffIn struct {
	A string `json:"a" jsonschema:"the base run id"`
	B string `json:"b" jsonschema:"the run compared with it"`
	mcpBound
}

type mcpExportIn struct {
	ID     string `json:"id" jsonschema:"the run id"`
	Format string `json:"format,omitempty" jsonschema:"json (default), jsonl or otlp; wefttest is not offered over MCP"`
	mcpBound
}

type mcpSessionsIn struct {
	Agent    string `json:"agent,omitempty" jsonschema:"only this agent's sessions"`
	PublicID string `json:"public_id,omitempty" jsonschema:"only this public id's sessions"`
	Limit    int    `json:"limit,omitempty" jsonschema:"page size"`
	Before   string `json:"before,omitempty" jsonschema:"the previous answer's next_before (RFC 3339)"`
	BeforeID string `json:"before_id,omitempty" jsonschema:"the previous answer's next_before_id, with before"`
	mcpBound
}

type mcpSessionIn struct {
	ID string `json:"id" jsonschema:"the session id"`
	mcpBound
}

// ── the reader ─────────────────────────────────────────────────────

// mcpReader is what every tool reads through: GET only.
type mcpReader struct{ s studioGetter }

// mcpErrBytes bounds the Studio body an error carries.
const mcpErrBytes = 2048

// fetch is the one way a tool reaches Studio. A non-2xx answer is the
// tool's error: Studio's own line plus the body it sent (its error and
// any badge, reason and fix), bounded with the same visible note.
func (m *mcpReader) fetch(ctx context.Context, path string, q url.Values) (int, []byte, error) {
	status, body, err := m.s.get(ctx, path, q)
	if err != nil {
		if len(body) > 0 {
			b := compact(body)
			if len(b) > mcpErrBytes {
				b = append(cut(b, mcpErrBytes), fmt.Sprintf("\n[truncated: showing %d of %d bytes of Studio's answer]", len(cut(b, mcpErrBytes)), len(b))...)
			}
			return status, nil, fmt.Errorf("%w\n%s", err, b)
		}
		return status, nil, err
	}
	return status, body, nil
}

// get is fetch without the status.
func (m *mcpReader) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	_, b, err := m.fetch(ctx, path, q)
	return b, err
}

// out is a tool's answer: the summary line, then each body (the
// route's JSON, compacted), cut together at the bound, and a final
// note when anything was cut — truncation visible, never silent.
func out(maxBytes int, summary string, bodies ...[]byte) (*sdk.CallToolResult, any, error) {
	bound := maxBytes
	if bound <= 0 {
		bound = mcpDefaultBytes
	}
	bound = min(bound, mcpMaxBytes)
	res := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: summary}}}
	left, total, shown := bound, 0, 0
	for _, b := range bodies {
		b = compact(b)
		total += len(b)
		if len(b) > left {
			b = cut(b, left)
		}
		left -= len(b)
		shown += len(b)
		if len(b) == 0 {
			continue // cut to nothing: the note says so
		}
		res.Content = append(res.Content, &sdk.TextContent{Text: string(b)})
	}
	if shown < total {
		res.Content = append(res.Content, &sdk.TextContent{Text: fmt.Sprintf(
			"[truncated: showing %d of %d bytes — the JSON above is cut and does not parse; raise max_bytes (at most %d) or narrow the query (limit, step, refs)]",
			shown, total, mcpMaxBytes)})
	}
	return res, nil, nil
}

// compact is b without insignificant whitespace when it is one JSON
// value; anything else (jsonl) as it came.
func compact(b []byte) []byte {
	var buf bytes.Buffer
	if json.Compact(&buf, b) == nil {
		return buf.Bytes()
	}
	return b
}

// cut is b's first n bytes, backed off to a rune boundary.
func cut(b []byte, n int) []byte {
	if n >= len(b) {
		return b
	}
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	return b[:n]
}

func setStr(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func setInt(q url.Values, k string, v int64) {
	if v != 0 {
		q.Set(k, strconv.FormatInt(v, 10))
	}
}

func needID(name, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func (m *mcpReader) meta(ctx context.Context, _ *sdk.CallToolRequest, in mcpMetaIn) (*sdk.CallToolResult, any, error) {
	b, err := m.get(ctx, "/api/meta", nil)
	if err != nil {
		return nil, nil, err
	}
	var meta struct {
		Capabilities []string `json:"capabilities"`
	}
	_ = json.Unmarshal(b, &meta)
	return out(in.MaxBytes, fmt.Sprintf("Studio at %s: capabilities %s", m.s.redacted(), strings.Join(meta.Capabilities, ", ")), b)
}

// pageSummary names how many rows a page holds and how to get the next.
func pageSummary(what string, b []byte, list string) string {
	var p map[string]json.RawMessage
	if json.Unmarshal(b, &p) != nil {
		return what
	}
	var rows []json.RawMessage
	_ = json.Unmarshal(p[list], &rows)
	s := fmt.Sprintf("%s: %d %s", what, len(rows), list)
	if t, ok := p["total"]; ok {
		s += " of " + string(t)
	}
	for _, k := range []string{"next_before", "next_before_id", "next_offset", "next_after", "next_from"} {
		if v, ok := p[k]; ok && string(v) != "null" {
			s += "; " + k + "=" + string(v)
		}
	}
	return s
}

func (m *mcpReader) runs(ctx context.Context, _ *sdk.CallToolRequest, in mcpRunsIn) (*sdk.CallToolResult, any, error) {
	q := url.Values{}
	setStr(q, "agent", in.Agent)
	setStr(q, "status", in.Status)
	setStr(q, "session", in.Session)
	setStr(q, "public_id", in.PublicID)
	setStr(q, "experiment_id", in.ExperimentID)
	setStr(q, "parent", in.Parent)
	if in.All {
		q.Set("all", "1")
	}
	setInt(q, "limit", int64(in.Limit))
	setStr(q, "before", in.Before)
	setStr(q, "before_id", in.BeforeID)
	b, err := m.get(ctx, "/api/runs", q)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("runs", b, "runs"), b)
}

func (m *mcpReader) search(ctx context.Context, _ *sdk.CallToolRequest, in mcpSearchIn) (*sdk.CallToolResult, any, error) {
	q := url.Values{}
	for k, v := range map[string]string{"q": in.Q, "agent": in.Agent, "tool": in.Tool, "status": in.Status, "model": in.Model,
		"error": in.Error, "since": in.Since, "until": in.Until, "sort": in.Sort, "session": in.Session, "public_id": in.PublicID} {
		setStr(q, k, v)
	}
	setInt(q, "min_tokens", in.MinTokens)
	if in.MinCost != 0 {
		q.Set("min_cost", strconv.FormatFloat(in.MinCost, 'f', -1, 64))
	}
	if in.All {
		q.Set("all", "1")
	}
	setInt(q, "limit", int64(in.Limit))
	setInt(q, "offset", int64(in.Offset))
	b, err := m.get(ctx, "/api/search", q)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("search", b, "runs"), b)
}

// runHead is the part of a run row a summary names.
type runHead struct {
	ID         string          `json:"id"`
	Agent      string          `json:"agent"`
	Status     string          `json:"status"`
	Err        string          `json:"err"`
	Steps      int             `json:"steps"`
	Pending    int             `json:"pending"`
	StopReason string          `json:"stop_reason"`
	Holes      json.RawMessage `json:"holes"`
}

func (h runHead) line(link string) string {
	s := fmt.Sprintf("run %s (%s): %s, %d steps", h.ID, orDash(h.Agent), h.Status, h.Steps)
	if h.Err != "" {
		s += ": " + h.Err
	}
	if h.StopReason != "" {
		s += "; stop_reason " + h.StopReason
	}
	if h.Pending > 0 {
		s += fmt.Sprintf("; %d calls pending approval", h.Pending)
	}
	return s + " — " + link
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (m *mcpReader) run(ctx context.Context, _ *sdk.CallToolRequest, in mcpRunIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	b, err := m.get(ctx, runPath(in.ID), nil)
	if err != nil {
		return nil, nil, err
	}
	var h runHead
	_ = json.Unmarshal(b, &h)
	return out(in.MaxBytes, h.line(m.s.runLink(in.ID)), b)
}

// stepHead is the part of a step document a summary names.
type stepHead struct {
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	ToolCalls []struct {
		Name   string `json:"name"`
		Result *struct {
			IsError bool `json:"is_error"`
		} `json:"result"`
		Pending bool `json:"pending"`
	} `json:"tool_calls"`
	Holes []struct {
		Hole string `json:"hole"`
	} `json:"holes"`
}

func (h stepHead) line(id string, n int) string {
	s := fmt.Sprintf("run %s step %d: %s", id, n, h.Status)
	if h.Reason != "" {
		s += " (" + h.Reason + ")"
	}
	if len(h.ToolCalls) > 0 {
		var calls []string
		for _, c := range h.ToolCalls {
			state := "no result"
			switch {
			case c.Pending:
				state = "pending"
			case c.Result != nil && c.Result.IsError:
				state = "error"
			case c.Result != nil:
				state = "ok"
			}
			calls = append(calls, c.Name+" "+state)
		}
		s += "; tool calls: " + strings.Join(calls, ", ")
	}
	if len(h.Holes) > 0 {
		var hs []string
		for _, x := range h.Holes {
			hs = append(hs, x.Hole)
		}
		s += "; holes: " + strings.Join(hs, ", ")
	}
	return s
}

func stepPath(id string, n int) string { return runPath(id) + "/steps/" + strconv.Itoa(n) }

func (m *mcpReader) step(ctx context.Context, _ *sdk.CallToolRequest, in mcpStepIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	if in.N < 0 {
		return nil, nil, errors.New("n is a step ordinal, 0 or more")
	}
	b, err := m.get(ctx, stepPath(in.ID, in.N), nil)
	if err != nil {
		return nil, nil, err
	}
	var h stepHead
	_ = json.Unmarshal(b, &h)
	return out(in.MaxBytes, h.line(in.ID, in.N), b)
}

func (m *mcpReader) request(ctx context.Context, _ *sdk.CallToolRequest, in mcpRequestIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	if in.Transcript && in.Step == nil {
		return nil, nil, errors.New("transcript=true needs step: the messages one step's model call carried")
	}
	q := url.Values{}
	if in.Step != nil {
		q.Set("step", strconv.Itoa(*in.Step))
	}
	setInt(q, "from", int64(in.From))
	setInt(q, "limit", int64(in.Limit))
	if in.Refs {
		q.Set("refs", "1")
	}
	b, err := m.get(ctx, runPath(in.ID)+"/requests", q)
	if err != nil {
		return nil, nil, err
	}
	summary := pageSummary("run "+in.ID+" requests", b, "requests")
	if !in.Transcript {
		return out(in.MaxBytes, summary, b)
	}
	tb, err := m.get(ctx, runPath(in.ID)+"/transcript", url.Values{"step": {strconv.Itoa(*in.Step)}})
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, summary+"; then the transcript of step "+strconv.Itoa(*in.Step), b, tb)
}

func (m *mcpReader) events(ctx context.Context, _ *sdk.CallToolRequest, in mcpEventsIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	q := url.Values{}
	setInt(q, "after", in.After)
	setInt(q, "limit", int64(in.Limit))
	b, err := m.get(ctx, runPath(in.ID)+"/events", q)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("run "+in.ID+" events", b, "events"), b)
}

func (m *mcpReader) tools(ctx context.Context, _ *sdk.CallToolRequest, in mcpRunIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	b, err := m.get(ctx, runPath(in.ID)+"/tools", nil)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, "run "+in.ID+": tool catalogs", b)
}

func (m *mcpReader) diff(ctx context.Context, _ *sdk.CallToolRequest, in mcpDiffIn) (*sdk.CallToolResult, any, error) {
	if err := needID("a", in.A); err != nil {
		return nil, nil, err
	}
	if err := needID("b", in.B); err != nil {
		return nil, nil, err
	}
	b, err := m.get(ctx, "/api/diff", url.Values{"a": {in.A}, "b": {in.B}})
	if err != nil {
		return nil, nil, err
	}
	// Studio's diffSummary (studio/diff.go): changed_steps is the list
	// of changed ordinals. A body that does not decode gets the plain
	// line — never a count it did not read.
	var d struct {
		Summary struct {
			ChangedSteps []int `json:"changed_steps"`
			FirstChanged *int  `json:"first_changed"`
		} `json:"summary"`
	}
	s := fmt.Sprintf("diff %s → %s", in.A, in.B)
	if err := json.Unmarshal(b, &d); err == nil {
		s += fmt.Sprintf(": %d changed steps", len(d.Summary.ChangedSteps))
		if d.Summary.FirstChanged != nil {
			s += fmt.Sprintf(", first at step %d", *d.Summary.FirstChanged)
		}
	}
	return out(in.MaxBytes, s, b)
}

func (m *mcpReader) export(ctx context.Context, _ *sdk.CallToolRequest, in mcpExportIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	format := in.Format
	switch format {
	case "":
		format = "json"
	case "json", "jsonl", "otlp":
	case "wefttest":
		return nil, nil, errors.New(`format wefttest is a zip of replay fixtures, not offered over MCP: run "weft export <id> --wefttest dir" in a terminal`)
	default:
		return nil, nil, fmt.Errorf("format must be json, jsonl or otlp, got %q", format)
	}
	b, err := m.get(ctx, runPath(in.ID)+"/export", url.Values{"format": {format}})
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, fmt.Sprintf("run %s exported as %s (%d bytes)", in.ID, format, len(b)), b)
}

func (m *mcpReader) sessions(ctx context.Context, _ *sdk.CallToolRequest, in mcpSessionsIn) (*sdk.CallToolResult, any, error) {
	q := url.Values{}
	setStr(q, "agent", in.Agent)
	setStr(q, "public_id", in.PublicID)
	setInt(q, "limit", int64(in.Limit))
	setStr(q, "before", in.Before)
	setStr(q, "before_id", in.BeforeID)
	b, err := m.get(ctx, "/api/sessions", q)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("sessions", b, "sessions"), b)
}

func (m *mcpReader) session(ctx context.Context, _ *sdk.CallToolRequest, in mcpSessionIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	b, err := m.get(ctx, "/api/sessions/"+url.PathEscape(in.ID), nil)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, "session "+in.ID, b)
}

// ── explain_failure ────────────────────────────────────────────────

// explanation is explain_failure's answer: the run's fields and the
// chosen step's, each passed through as the routes serve them.
type explanation struct {
	RunID      string          `json:"run_id"`
	URL        string          `json:"url"`
	Agent      string          `json:"agent"`
	Status     string          `json:"status"`
	Error      string          `json:"error"`
	StopReason string          `json:"stop_reason"`
	Pending    int             `json:"pending"`
	Steps      int             `json:"steps"`
	RunHoles   json.RawMessage `json:"run_holes"`
	// Step is the step explained: the one in flight when the run
	// ended, else its last; null when the run has no step.
	Step *int `json:"step"`
	// StepRole says which: "in_flight" (the step the run was in when
	// it ended — a model or tool failure inside it) or "last" (the
	// run's last recorded step: it ended between steps — a budget at
	// the continuation point, a success).
	StepRole string `json:"step_role,omitempty"`
	// StepNote says why the in-flight step is not the one shown: it
	// answered 403 (hidden) or 5xx (unavailable).
	StepNote   string          `json:"step_note,omitempty"`
	StepStatus string          `json:"step_status,omitempty"`
	StepReason string          `json:"step_reason,omitempty"`
	Model      json.RawMessage `json:"model,omitempty"`
	StepHoles  json.RawMessage `json:"step_holes,omitempty"`
	// LastAssistantText is the transcript's last assistant text; null
	// when there is none, TranscriptError when the transcript was not
	// readable (its error line).
	LastAssistantText *string `json:"last_assistant_text"`
	TranscriptError   string  `json:"transcript_error,omitempty"`
	// Next names the reads that go further.
	Next []string `json:"next"`
	// The step's bulk last — attempts, tool calls with their results,
	// children — so a cut at max_bytes takes them and keeps the
	// structured fields above (field order is the JSON's order).
	Attempts  json.RawMessage `json:"attempts,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
	Children  json.RawMessage `json:"children,omitempty"`
}

func (m *mcpReader) explainFailure(ctx context.Context, _ *sdk.CallToolRequest, in mcpRunIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	rb, err := m.get(ctx, runPath(in.ID), nil)
	if err != nil {
		return nil, nil, err
	}
	var h runHead
	if err := json.Unmarshal(rb, &h); err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", runPath(in.ID), err)
	}
	ex := explanation{
		RunID: in.ID, URL: m.s.runLink(in.ID), Agent: h.Agent, Status: h.Status, Error: h.Err,
		StopReason: h.StopReason, Pending: h.Pending, Steps: h.Steps, RunHoles: h.Holes,
	}
	// The step: the one in flight when the run ended (ordinal = the
	// count of finished steps) when it started, else the last finished
	// one. A succeeded run's in-flight step does not exist.
	var sb []byte
	tryStep := func(n int, role string) bool {
		if n < 0 {
			return false
		}
		status, b, err := m.fetch(ctx, stepPath(in.ID, n), nil)
		if status == http.StatusNotFound {
			return false
		}
		if err != nil {
			what := fmt.Sprintf("unavailable (%d)", status)
			switch status {
			case 0: // the request itself failed: no answer to name
				what = "unreachable"
			case http.StatusForbidden:
				what = "hidden (403)"
			}
			note := fmt.Sprintf("step %d is %s", n, what)
			if role == "in_flight" {
				note = fmt.Sprintf("the in-flight step %d is %s; showing the last recorded step", n, what)
			}
			// A failing fallback adds to the in-flight note, never replaces it.
			if ex.StepNote != "" {
				note = ex.StepNote + "; " + note
			}
			ex.StepNote = note
			ex.Next = append(ex.Next, fmt.Sprintf("step %d not readable: %v", n, err))
			return false
		}
		sb, ex.Step, ex.StepRole = b, &n, role
		return true
	}
	if !tryStep(h.Steps, "in_flight") {
		tryStep(h.Steps-1, "last")
	}
	var sh stepHead
	requestHidden := false
	if sb != nil {
		var sd struct {
			Status    string          `json:"status"`
			Reason    string          `json:"reason"`
			Model     json.RawMessage `json:"model"`
			Attempts  json.RawMessage `json:"attempts"`
			ToolCalls json.RawMessage `json:"tool_calls"`
			Children  json.RawMessage `json:"children"`
			Holes     json.RawMessage `json:"holes"`
			Request   struct {
				Badge string `json:"badge"`
			} `json:"request"`
		}
		_ = json.Unmarshal(sb, &sd)
		_ = json.Unmarshal(sb, &sh)
		ex.StepStatus, ex.StepReason, ex.Model, ex.Attempts = sd.Status, sd.Reason, sd.Model, sd.Attempts
		ex.ToolCalls, ex.Children, ex.StepHoles = sd.ToolCalls, sd.Children, sd.Holes
		requestHidden = sd.Request.Badge == "hidden"
	}
	if tb, err := m.get(ctx, runPath(in.ID)+"/transcript", nil); err != nil {
		ex.TranscriptError = err.Error()
	} else {
		ex.LastAssistantText = lastAssistantText(tb)
	}
	if ex.Step != nil {
		ex.Next = append(ex.Next, fmt.Sprintf("step {id:%q, n:%d} for the step whole", in.ID, *ex.Step))
		if !requestHidden { // a read token's request block is the hidden badge: the request tool would 403
			ex.Next = append(ex.Next, fmt.Sprintf("request {id:%q, step:%d, transcript:true} for the exact model input", in.ID, *ex.Step))
		}
	}
	ex.Next = append(ex.Next, fmt.Sprintf("events {id:%q} for the event stream", in.ID))
	b, err := json.Marshal(ex)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, explainLine(ex, sh), b)
}

// explainLine is the answer in one sentence an agent can quote.
func explainLine(ex explanation, sh stepHead) string {
	s := fmt.Sprintf("run %s (%s): %s", ex.RunID, orDash(ex.Agent), ex.Status)
	if ex.Step != nil {
		switch {
		case ex.StepRole == "in_flight" || ex.StepStatus == "error":
			s += fmt.Sprintf(" at step %d", *ex.Step)
		case ex.Status == "failed" || ex.Status == "interrupted":
			s += fmt.Sprintf(" before step %d started (step %d, its last recorded step, finished)", *ex.Step+1, *ex.Step)
		default:
			s += fmt.Sprintf(", last step %d", *ex.Step)
		}
	} else {
		s += ", no step recorded"
	}
	if ex.Error != "" {
		s += ": " + ex.Error
	}
	if ex.StopReason != "" {
		s += "; stop_reason " + ex.StopReason
	}
	if ex.StepNote != "" {
		s += "; " + ex.StepNote
	}
	if ex.Pending > 0 {
		var names []string
		for _, c := range sh.ToolCalls {
			if c.Pending {
				names = append(names, c.Name)
			}
		}
		s += fmt.Sprintf("; parked: %d calls awaiting approval (%s)", ex.Pending, strings.Join(names, ", "))
	}
	if ex.Step != nil {
		if d := sh.line(ex.RunID, *ex.Step); strings.Contains(d, "; ") {
			s += "; " + d[strings.Index(d, "; ")+2:]
		}
	}
	if ex.LastAssistantText != nil {
		t := *ex.LastAssistantText
		if utf8.RuneCountInString(t) > 200 {
			t = string([]rune(t)[:200]) + "…"
		}
		s += fmt.Sprintf("; last assistant text %q", t)
	}
	return s + " — " + ex.URL
}

// lastAssistantText reads GET /api/runs/{id}/transcript's batches and
// returns the last assistant message's text; nil when none has text.
func lastAssistantText(b []byte) *string {
	var tr struct {
		Batches []struct {
			Messages json.RawMessage `json:"messages"`
		} `json:"batches"`
	}
	if json.Unmarshal(b, &tr) != nil {
		return nil
	}
	var last *string
	for _, batch := range tr.Batches {
		var msgs []struct {
			Role  string `json:"role"`
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(batch.Messages, &msgs) != nil {
			continue
		}
		for _, msg := range msgs {
			if msg.Role != "assistant" {
				continue
			}
			var t []string
			for _, p := range msg.Parts {
				if p.Type == "text" && p.Text != "" {
					t = append(t, p.Text)
				}
			}
			if len(t) > 0 {
				s := strings.Join(t, "")
				last = &s
			}
		}
	}
	return last
}
