package studio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// api/meta (S4.3, plan B5): what this Studio is and how it is wired,
// computed from the server's own state, never hard-coded. Every line
// `weft doctor` prints reads one of these fields — the doctor
// computes nothing Studio does not serve.

// metaDoc is api/meta's body.
type metaDoc struct {
	WeftVersion   string `json:"weft_version"`
	StudioVersion string `json:"studio_version"`
	// PanelVersion is the studio version stamped into the embedded
	// /panel.js ("" when the bundle is missing or carries no stamp):
	// equal to studio_version unless the committed dist is stale.
	PanelVersion       string   `json:"panel_version"`
	DB                 metaDB   `json:"db"`
	Title              string   `json:"title"`
	HasManifest        bool     `json:"has_manifest"`
	IngestOpen         bool     `json:"ingest_open"`
	InterruptedAfterMs int64    `json:"interrupted_after_ms"`
	Capabilities       []string `json:"capabilities"`
	// CapabilitiesOff names each capability an option left off, with
	// the option (and CLI flag) that did (plan B4): the UI's empty
	// states say it. Omitted when nothing is off.
	CapabilitiesOff map[string]string `json:"capabilities_off,omitempty"`
	// ManifestSources counts api/manifest's sources: the weft.json
	// (when configured) plus each manifest a runtime registered with,
	// by (service, manifest hash), live or remembered.
	ManifestSources int `json:"manifest_sources"`
	// AuthRequired is whether a Token is configured: the API then reads
	// the bearer (setup B/C); false is setup A, where no token is read.
	AuthRequired bool `json:"auth_required"`
	// DebugScope says what the debugger's write verbs (breakpoints,
	// steer) can act on — the runtime-started runs only. The app's
	// own turns are viewer-only (D7, PQ7); meta says so plainly
	// (WEFT-DEVTOOLS §8.5 item 6).
	DebugScope string      `json:"debug_scope,omitempty"`
	Content    metaContent `json:"content"`
	// Pricing is whether Studio prices usage (false until a pricing
	// table exists, plan A5).
	Pricing bool `json:"pricing"`
	// Retention is the configured retention; null: none configured
	// (Studio has no retention setting yet). The key is always present.
	Retention *string `json:"retention"`
	// Runtimes is how many runtimes hold a live command stream now: 0
	// when the playground is off (no runtime link) or none dialed in.
	Runtimes int `json:"runtimes"`
	// ManifestCheck compares the manifest (weft.json, else the
	// registered agents api/manifest serves; source says which) with
	// the manifest hash each agent's latest stored run recorded. Null
	// without a manifest, and for a panel token (it reads no manifest).
	ManifestCheck *metaManifest `json:"manifest_check"`
	// PID is the serving process's id (plan B2: the CLI's "studio
	// already running at … (pid 1234), reusing"). Under db.path's rule
	// (mayReadDBPath) — loopback with no Token, or the server token —
	// and omitted for anyone else.
	PID int `json:"pid,omitempty"`
}

// metaDB names the database. Kind is everyone's; Path and Size only
// the caller's who could open the file anyway — a loopback request
// with no Token configured (setup A) or the server token (setup B's
// dev token) — and are omitted, never nulled, for anyone else (a panel
// token, an AllowOrigins host). Both are omitted for a database with
// no file (":memory:", ClickHouse).
type metaDB struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	// Size is the file's bytes on disk, its WAL sidecar included.
	Size *int64 `json:"size,omitempty"`
}

// metaContent is what Studio can know about content as a destination:
// its own ingest policy (content is stored as received; Studio never
// strips) and the latest stored run's content mark in the caller's
// scope. The app's per-destination policy is the app's — Studio sees
// only what arrived.
type metaContent struct {
	Ingest string `json:"ingest"` // "as_received"
	// Latest is the latest top-level run's mark; null when no run is
	// stored yet (or it could not be read).
	Latest *metaContentRun `json:"latest"`
	// Error is the read that failed while looking for the latest run
	// (logged too); latest is then null for that reason, not because no
	// run is stored.
	Error string `json:"error,omitempty"`
}

// metaContentRun is one run's content mark with Studio's reading of it.
type metaContentRun struct {
	RunID string `json:"run_id"`
	// Mark is the run's first event's weft.content: full, stripped (a
	// content-off destination chain), none (the agent captured none),
	// or unmarked (no events, or a writer that set no mark).
	Mark string `json:"mark"`
	Note string `json:"note"`
	Fix  string `json:"fix,omitempty"`
}

// metaManifest is the manifest staleness check.
type metaManifest struct {
	// Source is what was checked: file (weft.json) or runtime (the
	// manifests runtimes registered with — no weft.json configured).
	Source  string   `json:"source"`
	Agents  int      `json:"agents"`  // agents in the manifest
	Checked int      `json:"checked"` // of those, agents with a stored run carrying a manifest hash
	Stale   []string `json:"stale"`   // checked agents whose latest run's hash differs; never null
	// Error is the first failure of the check (a manifest that does
	// not parse, a run read that failed; logged too): the counts cover
	// only what was checked before or around it.
	Error string `json:"error,omitempty"`
}

// Content marks api/meta reports (content.latest.mark).
const (
	contentIngestAsReceived = "as_received"
	markFull                = "full"
	markStripped            = "stripped"
	markNone                = "none"
	markUnmarked            = "unmarked"
)

// contentNotes is Studio's reading of each mark: the note, and the fix
// that names the cause in the app.
var contentNotes = map[string][2]string{
	markFull: {"the latest run's content is stored in full", ""},
	markStripped: {"a content-off destination stripped the latest run's content before it reached studio",
		"drop otel.NoContent() from the destination that sends to studio"},
	markNone: {"the agent captured no content for the latest run",
		"drop weft.Content(false) from the agent"},
	markUnmarked: {"the latest run carries no content mark: no events stored, a weft older than the mark, or a non-weft exporter", ""},
}

// serveMeta answers api/meta.
func (s *Server) serveMeta(w http.ResponseWriter, r *http.Request) {
	id := idFrom(r)
	reg := s.registeredManifests()
	doc := metaDoc{
		WeftVersion:        weftVersion(),
		StudioVersion:      Version,
		PanelVersion:       panelVersion(),
		DB:                 metaDB{Kind: dbKind(s.db)},
		Title:              s.title,
		HasManifest:        len(s.manifest) > 0 || len(reg) > 0,
		ManifestSources:    len(s.manifestSources(reg)),
		IngestOpen:         !s.noIngest && s.ingestToken == "",
		InterruptedAfterMs: obsdb.InterruptedAfter.Milliseconds(),
		Capabilities:       s.capabilityList(),
		CapabilitiesOff:    s.capabilitiesOff(),
		DebugScope:         s.debugScope(),
		AuthRequired:       s.token != "",
		Content:            s.latestContent(r.Context(), id),
		Runtimes:           s.connectedRuntimes(),
	}
	if mayReadDBPath(s, r) {
		doc.DB.Path, doc.DB.Size = dbFile(s.db)
		doc.PID = os.Getpid()
	}
	if id.panel == nil {
		doc.ManifestCheck = s.manifestCheck(r.Context(), reg)
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// mayReadDBPath is the db.path/db.size/pid rule: the server token, or no
// Token configured and a loopback Host (setup A's guard, without the
// AllowOrigins widening).
func mayReadDBPath(s *Server, r *http.Request) bool {
	id := idFrom(r)
	if id.panel != nil {
		return false
	}
	if id.server {
		return true
	}
	return s.token == "" && isLoopbackName(hostOnly(r.Host))
}

// dbFile is the database's file and its size on disk (the WAL sidecar
// included), for a backend that names a file (interface{ Path()
// string }: obsdb/sqlite). A file Studio cannot stat reports its path
// and no size.
func dbFile(db obsdb.DB) (string, *int64) {
	p, ok := db.(interface{ Path() string })
	if !ok || p.Path() == "" {
		return "", nil
	}
	path := filepath.Clean(p.Path())
	fi, err := os.Stat(path)
	if err != nil {
		return path, nil
	}
	size := fi.Size()
	if wal, err := os.Stat(path + "-wal"); err == nil {
		size += wal.Size()
	}
	return path, &size
}

// latestContent reads the latest top-level run's content mark, scoped
// to a panel token's public id. Meta never fails on an observation: a
// failed read is logged and reported in content.error.
func (s *Server) latestContent(ctx context.Context, id identity) metaContent {
	out := metaContent{Ingest: contentIngestAsReceived}
	q := obsdb.RunQuery{Limit: 1}
	if id.panel != nil {
		q.PublicID = id.panel.PublicID
	}
	page, err := s.db.Runs(ctx, q)
	if err != nil {
		out.Error = metaReadFailed("content: read the latest run", err)
		return out
	}
	if len(page.Runs) == 0 {
		return out
	}
	run := page.Runs[0]
	mark := markUnmarked
	if run.EventCount > 0 {
		ev, err := s.db.Events(ctx, run.ID, -1, 1)
		switch {
		case err != nil:
			out.Error = metaReadFailed("content: read run "+run.ID+"'s first event", err)
		case len(ev.Events) > 0:
			switch m := ev.Events[0].Content; m {
			case markFull, markStripped, markNone:
				mark = m
			}
		}
	}
	n := contentNotes[mark]
	out.Latest = &metaContentRun{RunID: run.ID, Mark: mark, Note: n[0], Fix: n[1]}
	return out
}

// metaReadFailed logs a meta observation that failed (studio has no
// logger of its own: the process's slog default, as studio/runtime)
// and returns the text meta reports.
func metaReadFailed(what string, err error) string {
	msg := what + ": " + err.Error()
	slog.Warn("studio: api/meta: "+what, "err", err)
	return msg
}

// connectedRuntimes counts the runtimes holding a command stream.
func (s *Server) connectedRuntimes() int {
	if s.runtimeSrv == nil {
		return 0
	}
	n := 0
	for _, v := range s.runtimeSrv.Snapshot() {
		if s.runtimeSrv.Connected(v.ID) {
			n++
		}
	}
	return n
}

// manifestCheck compares each manifest agent's hash — sha256 of the
// agent's own one-agent manifest document, the hash the core stamps as
// weft.manifest.hash — with the latest stored run of that agent
// (subagent runs included). The manifest is the weft.json, else the
// registered agents api/manifest serves; nil when there is neither. A
// manifest that does not parse checks nothing; it and a failed run
// read are logged and reported in error.
func (s *Server) manifestCheck(ctx context.Context, reg []linkruntime.ManifestSource) *metaManifest {
	source, hashes, err := s.checkedManifest(reg)
	if source == "" {
		return nil
	}
	out := &metaManifest{Source: source, Stale: []string{}}
	if err != nil {
		out.Error = metaReadFailed("manifest_check: the manifest does not parse", err)
		return out
	}
	out.Agents = len(hashes)
	for _, a := range hashes {
		page, err := s.db.Runs(ctx, obsdb.RunQuery{Agent: a.name, ParentRunID: "*", Limit: 1})
		if err != nil {
			if out.Error == "" {
				out.Error = metaReadFailed("manifest_check: read agent "+a.name+"'s latest run", err)
			}
			continue
		}
		if len(page.Runs) == 0 || page.Runs[0].ManifestHash == "" {
			continue
		}
		out.Checked++
		if page.Runs[0].ManifestHash != a.hash {
			out.Stale = append(out.Stale, a.name)
		}
	}
	return out
}

type agentHash struct{ name, hash string }

// manifestAgentHashes splits a weft.json into its agents and hashes
// each as the core does (core.Manifest(agent), sha256, lowercase hex):
// the one-agent document re-encoded through the same MarshalIndent,
// which reproduces core's bytes for any agent core.Manifest wrote.
func manifestAgentHashes(manifest []byte) ([]agentHash, error) {
	var doc struct {
		Version int               `json:"weft"`
		Agents  []json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return nil, err
	}
	out := make([]agentHash, 0, len(doc.Agents))
	for _, raw := range doc.Agents {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &named); err != nil {
			return nil, err
		}
		one := struct {
			Version int               `json:"weft"`
			Agents  []json.RawMessage `json:"agents"`
		}{doc.Version, []json.RawMessage{raw}}
		b, err := json.MarshalIndent(one, "", "  ")
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(append(b, '\n'))
		out = append(out, agentHash{named.Name, hex.EncodeToString(sum[:])})
	}
	return out, nil
}

// panelStampRe finds panelStudioVersion's body in the built bundle:
// the define folds it to a single returned string literal. The
// minifier may name it with a $ ("$e"), a valid JS identifier.
var panelStampRe = regexp.MustCompile(`//#region src/panel/version\.ts\s*function [\w$]+\(\)\s*\{\s*return "([^"]*)";?\s*\}`)

var (
	panelVersionOnce sync.Once
	panelVersionVal  string
)

// panelVersion is the embedded panel's version stamp, read once.
func panelVersion() string {
	panelVersionOnce.Do(func() {
		b, err := panelJS.ReadFile("dist/panel/panel.js")
		if err != nil {
			return
		}
		if m := panelStampRe.FindAllSubmatch(b, -1); len(m) == 1 {
			panelVersionVal = string(bytes.Clone(m[0][1]))
		}
	})
	return panelVersionVal
}
