package studio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// Proposed prompts (plan F6): "keep as prompt" stays a clipboard copy
// until weft/prompt exists, and Studio also remembers the prompt as the
// agent's proposed instructions, which GET /api/manifest shows beside
// the registered ones — visible as "proposed" next to "registered" on
// the Agents page. Nothing reaches the app or your files: the proposal
// lives in this Server's memory, bounded (maxProposals agents, the
// registered-manifest store's bound — the oldest proposal is forgotten
// first) and forgotten on restart, as the registered manifests are.
// WEFT-PROMPT.md's P3 replaces the clipboard with a propose command
// over the runtime link.
//
//	PUT    /api/agents/{name}/proposal   {"instructions": "..."}
//	DELETE /api/agents/{name}/proposal
//
// Both are the dev token's, or setup A's loopback with no Token —
// db.path's rule (mayReadDBPath), as deletion is; every panel token is
// 403, playground-scoped included: a proposed prompt is a system
// prompt, and setting one is not an act inside a public id. The agent
// must be one the manifest file or a registered runtime names (404
// otherwise).

// maxProposals bounds the proposals held: the registered-manifest
// store's bound (studio/runtime's maxManifests).
const maxProposals = 64

// proposal is one agent's proposed instructions.
type proposal struct {
	Instructions     string    `json:"instructions"`
	InstructionsHash string    `json:"instructions_hash"` // sha256 hex, ADR 0028 §4's weft.instructions.hash
	Since            time.Time `json:"since"`
}

// proposals is the Server's in-memory store.
type proposals struct {
	mu sync.Mutex
	m  map[string]proposal
}

// snapshot copies the store for one manifest read.
func (p *proposals) snapshot() map[string]proposal {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.m) == 0 {
		return nil
	}
	out := make(map[string]proposal, len(p.m))
	for k, v := range p.m {
		out[k] = v
	}
	return out
}

// put stores v for name, forgetting the oldest proposal past the bound.
func (p *proposals) put(name string, v proposal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]proposal{}
	}
	p.m[name] = v
	for len(p.m) > maxProposals {
		oldest, at := "", time.Time{}
		for k, x := range p.m {
			if k != name && (oldest == "" || x.Since.Before(at) || x.Since.Equal(at) && k < oldest) {
				oldest, at = k, x.Since
			}
		}
		delete(p.m, oldest)
	}
}

func (p *proposals) remove(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.m[name]
	delete(p.m, name)
	return ok
}

// proposalsGroup is the capability "proposals": the two verbs.
func proposalsGroup() routeGroup {
	return routeGroup{
		name:       "proposals",
		capability: "proposals",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("PUT /api/agents/{name}/proposal", s.servePutProposal)
			mux.HandleFunc("DELETE /api/agents/{name}/proposal", s.serveDeleteProposal)
		},
	}
}

// mayPropose is the proposals' rule: deletion's (the dev token, or
// setup A's loopback with no Token), never a panel token.
func (s *Server) mayPropose(w http.ResponseWriter, r *http.Request) bool {
	id := idFrom(r)
	switch {
	case id.panel != nil:
		writeError(w, r, http.StatusForbidden, "forbidden",
			"a proposed prompt is a system prompt: it answers the dev token (or loopback in setup A) only, never a panel token")
		return false
	case mayReadDBPath(s, r):
		return true
	default:
		writeError(w, r, http.StatusForbidden, "forbidden",
			"a proposed prompt answers a loopback Host only when no studio.Token is configured")
		return false
	}
}

// proposalDoc is both verbs' 200 body.
type proposalDoc struct {
	Agent    string    `json:"agent"`
	Proposed *proposal `json:"proposed"` // null after DELETE
	Note     string    `json:"note"`
}

const proposalNote = "held in studio's memory beside the registered instructions, forgotten on restart; " +
	"nothing is sent to the app or written to your files"

// knownAgent reports whether the manifest file or a registered runtime
// names agent.
func (s *Server) knownAgent(name string) bool {
	if len(s.manifest) > 0 {
		if hashes, err := manifestAgentHashes(s.manifest); err == nil {
			for _, a := range hashes {
				if a.name == name {
					return true
				}
			}
		}
	}
	for _, m := range s.registeredManifests() {
		for _, a := range m.Agents {
			if a.Name == name {
				return true
			}
		}
	}
	return false
}

// servePutProposal answers PUT /api/agents/{name}/proposal.
func (s *Server) servePutProposal(w http.ResponseWriter, r *http.Request) {
	if !s.mayPropose(w, r) {
		return
	}
	name := r.PathValue("name")
	var body struct {
		Instructions *string `json:"instructions"`
	}
	if !decodeBody(w, r, "proposal body", &body) {
		return
	}
	if body.Instructions == nil {
		badRequest(w, r, "proposal body: instructions is required (an empty string proposes no system prompt)")
		return
	}
	if !s.knownAgent(name) {
		notFound(w, r, "no agent "+name+" in the manifest file or any registered manifest")
		return
	}
	sum := sha256.Sum256([]byte(*body.Instructions))
	v := proposal{Instructions: *body.Instructions, InstructionsHash: hex.EncodeToString(sum[:]), Since: s.now().UTC()}
	s.proposals.put(name, v)
	writeJSON(w, r, http.StatusOK, proposalDoc{Agent: name, Proposed: &v, Note: proposalNote})
}

// serveDeleteProposal answers DELETE /api/agents/{name}/proposal: 404
// when the agent has none.
func (s *Server) serveDeleteProposal(w http.ResponseWriter, r *http.Request) {
	if !s.mayPropose(w, r) {
		return
	}
	name := r.PathValue("name")
	if !s.proposals.remove(name) {
		notFound(w, r, "no proposal for agent "+name)
		return
	}
	writeJSON(w, r, http.StatusOK, proposalDoc{Agent: name, Note: "the proposal is forgotten"})
}

// withProposal appends "proposed" to one manifest agent object (a key
// of its own already there is replaced, re-encoded through a map). An
// entry that is not a JSON object is returned as given.
func withProposal(entry json.RawMessage, p proposal) json.RawMessage {
	body := bytes.TrimSpace(entry)
	if len(body) < 2 || body[0] != '{' {
		return entry
	}
	pb, err := json.Marshal(p)
	if err != nil {
		return entry
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return entry
	}
	if _, clash := obj["proposed"]; clash {
		obj["proposed"] = pb
		if out, err := json.Marshal(obj); err == nil {
			return out
		}
		return entry
	}
	head := bytes.TrimRight(body[:len(body)-1], " \t\r\n")
	out := append([]byte(nil), head...)
	if len(obj) > 0 {
		out = append(out, ',')
	}
	out = append(out, `"proposed":`...)
	out = append(out, pb...)
	return append(out, '}')
}

// agentName reads one manifest agent object's name.
func agentName(entry json.RawMessage) string {
	var a struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(entry, &a)
	return a.Name
}

// fileWithProposals adds each proposal to the file manifest's agent of
// that name, the file's other bytes kept verbatim (the agents array is
// rebuilt in place). A file it cannot read as an agents array is
// returned as given.
func fileWithProposals(manifest []byte, props map[string]proposal) []byte {
	if len(props) == 0 {
		return manifest
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(manifest, &obj) != nil {
		return manifest
	}
	raw, ok := obj["agents"]
	if !ok {
		return manifest
	}
	var agents []json.RawMessage
	if json.Unmarshal(raw, &agents) != nil {
		return manifest
	}
	// Each changed entry is spliced into the array's own bytes at its
	// place (Unmarshal copies a RawMessage verbatim, so each entry is
	// found in order from where the last one ended): the array's
	// spacing survives.
	rebuilt := append([]byte(nil), raw...)
	changed, at := false, 0
	for _, a := range agents {
		i := bytes.Index(rebuilt[at:], a)
		if i < 0 {
			return manifest
		}
		i += at
		p, ok := props[agentName(a)]
		if !ok {
			at = i + len(a)
			continue
		}
		e := withProposal(a, p)
		rebuilt = append(rebuilt[:i:i], append(e, rebuilt[i+len(a):]...)...)
		at = i + len(e)
		changed = true
	}
	if !changed {
		return manifest
	}
	// The agents value's own bytes, located in the file: Unmarshal
	// copies a RawMessage verbatim, so the first match is that value
	// (an identical byte run earlier in the file would have to be an
	// agents array itself).
	if at := bytes.Index(manifest, raw); at >= 0 {
		out := append([]byte(nil), manifest[:at]...)
		out = append(out, rebuilt...)
		return append(out, manifest[at+len(raw):]...)
	}
	obj["agents"] = rebuilt
	if out, err := json.Marshal(obj); err == nil {
		return append(out, '\n')
	}
	return manifest
}

// registeredWithProposals is the registered path's per-entry step.
func registeredWithProposals(agents []linkruntime.ManifestAgent, props map[string]proposal) []json.RawMessage {
	entries := make([]json.RawMessage, 0, len(agents))
	for _, a := range agents {
		e := withHash(a.Entry, a.ManifestHash)
		if p, ok := props[a.Name]; ok {
			e = withProposal(e, p)
		}
		entries = append(entries, e)
	}
	return entries
}
