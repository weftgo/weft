package studio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// api/manifest (plan B4): the agents Studio knows, from two sources —
// the weft.json passed to Manifest(...) and the manifests runtimes
// registered with (studio/runtime remembers each by (service,
// manifest hash), in memory). The file wins when configured; without
// one the registered manifests serve, live ones first. Either way the
// response's sources array lists every source, each registered one
// marked live (a connected runtime holds it now) or not (remembered
// from a runtime that is gone) — never a remembered one shown as live.

// Manifest source kinds.
const (
	manifestSourceFile    = "file"
	manifestSourceRuntime = "runtime"
)

// Why a capability is off: the option that would turn it on and the
// CLI flag that turned it off (meta.capabilities_off).
const (
	offPlayground = "the playground is off: studio.Playground(false) / weft studio --no-playground"
	offIngest     = "OTLP ingest is off: studio.NoIngest()"
	offAuth       = "no server token to sign panel tokens with: studio.Token(tok) / weft studio --token"
)

// manifestSourceView is one entry of api/manifest's sources.
type manifestSourceView struct {
	Source       string `json:"source"` // file | runtime
	ManifestHash string `json:"manifest_hash"`
	// The registered source's identity and liveness; absent for the
	// file.
	Service      string     `json:"service,omitempty"`
	Live         *bool      `json:"live,omitempty"`
	RegisteredAt *time.Time `json:"registered_at,omitempty"`
	RuntimeID    string     `json:"runtime_id,omitempty"`
	// Agents names the source's agents with each one's own hash (the
	// weft.manifest.hash its runs carry).
	Agents []manifestAgentView `json:"agents"`
}

type manifestAgentView struct {
	Name         string `json:"name"`
	ManifestHash string `json:"manifest_hash"`
}

// registeredManifests is the runtime link's remembered manifests; none
// without the playground.
func (s *Server) registeredManifests() []linkruntime.ManifestSource {
	if s.runtimeSrv == nil {
		return nil
	}
	return s.runtimeSrv.Manifests()
}

// manifestSources lists every source: the file first, then the
// registered ones (live first, newest first).
func (s *Server) manifestSources(reg []linkruntime.ManifestSource) []manifestSourceView {
	out := []manifestSourceView{}
	if len(s.manifest) > 0 {
		sum := sha256.Sum256(s.manifest)
		v := manifestSourceView{Source: manifestSourceFile, ManifestHash: hex.EncodeToString(sum[:]), Agents: []manifestAgentView{}}
		if hashes, err := manifestAgentHashes(s.manifest); err == nil {
			for _, a := range hashes {
				v.Agents = append(v.Agents, manifestAgentView{a.name, a.hash})
			}
		}
		out = append(out, v)
	}
	for _, m := range reg {
		live, at := m.Live, m.RegisteredAt
		v := manifestSourceView{
			Source: manifestSourceRuntime, ManifestHash: m.ManifestHash,
			Service: m.Service, Live: &live, RegisteredAt: &at, RuntimeID: m.RuntimeID,
			Agents: make([]manifestAgentView, 0, len(m.Agents)),
		}
		for _, a := range m.Agents {
			v.Agents = append(v.Agents, manifestAgentView{a.Name, a.ManifestHash})
		}
		out = append(out, v)
	}
	return out
}

// registeredAgents merges the registered manifests into one agent
// list: each agent name once, from the first source that has it in
// Manifests' order — a live manifest before a remembered one, the
// newest before the older.
func registeredAgents(reg []linkruntime.ManifestSource) (version int, agents []linkruntime.ManifestAgent) {
	seen := map[string]bool{}
	version = 1
	for i, m := range reg {
		if i == 0 && m.Version != 0 {
			version = m.Version
		}
		for _, a := range m.Agents {
			if seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			agents = append(agents, a)
		}
	}
	return version, agents
}

// serveManifest answers api/manifest: the file manifest with sources
// added, else the registered manifests merged, else 404 saying why.
func (s *Server) serveManifest(w http.ResponseWriter, r *http.Request) {
	// The manifest carries every agent's system prompt, which a
	// read-scoped panel token's page does not get (serveRuntimes strips
	// the same field for it): only an identity that may start
	// experiments reads it — the file's and the registered ones alike.
	// The panel itself never asks for it.
	if !readsPrompts(r) {
		refuseHidden(w, r, "the manifest carries the agents' system prompts: a read-scoped panel token does not read it")
		return
	}
	reg := s.registeredManifests()
	sources := s.manifestSources(reg)
	if len(s.manifest) > 0 {
		writeRaw(w, r, withSources(s.manifest, sources))
		return
	}
	if len(reg) == 0 {
		why := "no manifest: no weft.json configured (studio.Manifest, or weft studio --manifest) and no runtime has registered"
		if s.runtimeSrv == nil {
			why = "no manifest: no weft.json configured (studio.Manifest, or weft studio --manifest), and " + offPlayground + ", so no runtime can register one"
		}
		notFound(w, r, why)
		return
	}
	version, agents := registeredAgents(reg)
	entries := make([]json.RawMessage, 0, len(agents))
	for _, a := range agents {
		entries = append(entries, a.Entry)
	}
	writeJSON(w, r, http.StatusOK, struct {
		Version int                  `json:"weft"`
		Agents  []json.RawMessage    `json:"agents"`
		Sources []manifestSourceView `json:"sources"`
	}{version, entries, sources})
}

// withSources splices "sources" into the file manifest as its last key,
// the file's own bytes kept verbatim before it. A file that is not a
// JSON object is returned as given (manifest_check reports the parse
// error).
func withSources(manifest []byte, sources []manifestSourceView) []byte {
	var obj map[string]json.RawMessage
	if json.Unmarshal(manifest, &obj) != nil || obj == nil {
		return manifest
	}
	b, err := json.Marshal(sources)
	if err != nil {
		return manifest
	}
	body := bytes.TrimRight(manifest, " \t\r\n")
	head := bytes.TrimRight(body[:len(body)-1], " \t\r\n")
	out := append([]byte(nil), head...)
	if len(obj) > 0 {
		out = append(out, ',')
	}
	out = append(out, `"sources":`...)
	out = append(out, b...)
	return append(out, '}', '\n')
}

// writeRaw writes body as JSON verbatim.
func writeRaw(w http.ResponseWriter, r *http.Request, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// checkedManifest is what manifest_check compares runs with: the file's
// agents, else the registered agents /api/manifest serves. source is
// "" when there is neither.
func (s *Server) checkedManifest(reg []linkruntime.ManifestSource) (source string, hashes []agentHash, err error) {
	if len(s.manifest) > 0 {
		hashes, err := manifestAgentHashes(s.manifest)
		return manifestSourceFile, hashes, err
	}
	if len(reg) == 0 {
		return "", nil, nil
	}
	_, agents := registeredAgents(reg)
	for _, a := range agents {
		hashes = append(hashes, agentHash{a.Name, a.ManifestHash})
	}
	return manifestSourceRuntime, hashes, nil
}

// capabilitiesOff names each capability an option left off and why
// (meta.capabilities_off): the reason names the option and, where the
// CLI has one, its flag. Nil when nothing is off.
func (s *Server) capabilitiesOff() map[string]string {
	off := map[string]string{}
	if !s.playground {
		for _, c := range []string{"playground", "runtimes", "breakpoints", "steer"} {
			off[c] = offPlayground
		}
	}
	if s.noIngest {
		off["ingest"] = offIngest
	}
	if s.token == "" {
		off["auth"] = offAuth
	}
	if len(off) == 0 {
		return nil
	}
	return off
}
