package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// The registered manifests (plan B4): every registration carries each
// agent's core.Manifest document, so a Studio with no weft.json still
// knows the agents — it remembers each registration's manifest keyed
// by (service, manifest hash) and says which ones a connected runtime
// holds now. Memory only: a Studio restart forgets them, and the next
// registration (every reconnect registers) brings them back.

// maxManifests bounds the remembered manifests: a dev loop that
// rebuilds on every save registers a new hash each time. Past it the
// oldest one no connected runtime holds is forgotten. The bound is soft:
// while every entry is held by a connected runtime nothing is dropped,
// and an over-full store shrinks by one per registration.
const maxManifests = 64

// ManifestSource is one remembered registration manifest.
type ManifestSource struct {
	Service string `json:"service"`
	// ManifestHash is sha256 (lowercase hex) of the registration's
	// agents as one core.Manifest document — the bytes
	// weft.Manifest(agents...) writes for the same agents in the same
	// order, so a weft.json generated from them hashes the same.
	ManifestHash string `json:"manifest_hash"`
	// Live: a runtime holding a command stream now registered with
	// this manifest. False is remembered, never live.
	Live bool `json:"live"`
	// RegisteredAt is the latest registration with this manifest.
	RegisteredAt time.Time `json:"registered_at"`
	// RuntimeID is the runtime that registered it last.
	RuntimeID string          `json:"runtime_id"`
	Agents    []ManifestAgent `json:"agents"`
	// Version is the manifest documents' "weft" format version.
	Version int `json:"-"`
}

// ManifestAgent is one agent of a registered manifest.
type ManifestAgent struct {
	Name string `json:"name"`
	// ManifestHash is sha256 of the agent's own one-agent manifest —
	// the weft.manifest.hash its runs carry.
	ManifestHash string `json:"manifest_hash"`
	// Entry is the agent's object in its manifest's agents array.
	Entry json.RawMessage `json:"-"`
}

type manifestKey struct{ service, hash string }

// registrationManifest builds a registration's manifest source: each
// agent's one-agent document parsed, hashed as the core hashes it
// (sha256 of the bytes it registered), and the agents joined into one
// document. An agent whose manifest does not parse is left out; ok is
// false when none parses.
func registrationManifest(reg Registration) (ManifestSource, bool) {
	src := ManifestSource{Service: reg.Service, RuntimeID: reg.RuntimeID, Version: 1}
	var entries []json.RawMessage
	for i, a := range reg.Agents {
		var doc struct {
			Version int               `json:"weft"`
			Agents  []json.RawMessage `json:"agents"`
		}
		if err := json.Unmarshal([]byte(a.Manifest), &doc); err != nil || len(doc.Agents) == 0 {
			continue
		}
		if i == 0 && doc.Version != 0 {
			src.Version = doc.Version
		}
		sum := sha256.Sum256([]byte(a.Manifest))
		src.Agents = append(src.Agents, ManifestAgent{
			Name: a.Name, ManifestHash: hex.EncodeToString(sum[:]), Entry: doc.Agents[0],
		})
		entries = append(entries, doc.Agents[0])
	}
	if len(entries) == 0 {
		return ManifestSource{}, false
	}
	b, err := json.MarshalIndent(struct {
		Version int               `json:"weft"`
		Agents  []json.RawMessage `json:"agents"`
	}{src.Version, entries}, "", "  ")
	if err != nil {
		return ManifestSource{}, false
	}
	sum := sha256.Sum256(append(b, '\n'))
	src.ManifestHash = hex.EncodeToString(sum[:])
	return src, true
}

// rememberLocked stores the registration's manifest and points the
// runtime at it. Caller holds mu.
func (rs *RuntimeServer) rememberLocked(c *connected, src ManifestSource, at time.Time) {
	key := manifestKey{src.Service, src.ManifestHash}
	src.RegisteredAt = at
	rs.manifests[key] = &src
	c.manifest = key
	if len(rs.manifests) <= maxManifests {
		return
	}
	held := map[manifestKey]bool{key: true}
	for _, rc := range rs.runtimes {
		if rc.feed != nil {
			held[rc.manifest] = true
		}
	}
	var oldest *manifestKey
	for k, m := range rs.manifests {
		if held[k] {
			continue
		}
		if oldest == nil || m.RegisteredAt.Before(rs.manifests[*oldest].RegisteredAt) {
			k := k
			oldest = &k
		}
	}
	if oldest != nil {
		delete(rs.manifests, *oldest)
	}
}

// Manifests lists the remembered registration manifests: live ones
// first, then the most recently registered. Live is computed now — a
// runtime holding its command stream registered with that manifest.
func (rs *RuntimeServer) Manifests() []ManifestSource {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	live := map[manifestKey]bool{}
	for _, c := range rs.runtimes {
		if c.feed != nil {
			live[c.manifest] = true
		}
	}
	out := make([]ManifestSource, 0, len(rs.manifests))
	for k, m := range rs.manifests {
		src := *m
		src.Agents = append([]ManifestAgent(nil), m.Agents...)
		src.Live = live[k]
		out = append(out, src)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Live != b.Live {
			return a.Live
		}
		if !a.RegisteredAt.Equal(b.RegisteredAt) {
			return a.RegisteredAt.After(b.RegisteredAt)
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.ManifestHash < b.ManifestHash
	})
	return out
}
