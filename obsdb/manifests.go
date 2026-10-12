package obsdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ManifestStore is the optional manifest capability of a DB (U27): each
// agent's configuration by its hash, so a run's ManifestHash — the
// weft.manifest.hash its root span carried (docs/record-contract.md §2)
// — resolves to the configuration it ran with, after that configuration
// stopped being the current one.
//
// A row is one agent's one-agent core.Manifest document, byte for byte:
// the bytes weft.Manifest writes for that agent alone, whose sha256 is
// the hash its runs carry. The store is content-addressed — the hash is
// the body's sha256 (CheckStoredManifest) — and immutable: a second
// PutManifest of a stored hash changes nothing, so FirstSeen and Source
// are the first write's.
//
// Both first-party backends implement it — obsdb/sqlite (migration
// 0014) and obsdb/clickhouse (migration 0016) — and the obsdbtest
// conformance table holds them to the same answers. Studio writes it
// from the weft.json it serves and from runtime registrations.
//
// Retention: manifests are kept forever. They are Studio's bookkeeping,
// not run data — one small row per distinct agent configuration —
// and DB.DeleteRun, DB.DeleteSession, DB.Sweep and DB.Vacuum never
// touch them, so a run that remains always resolves its hash.
type ManifestStore interface {
	// PutManifest stores m unless its hash is stored already (then it
	// is a no-op answering nil). A zero FirstSeen is stamped with the
	// current time. ErrInvalidManifest when CheckStoredManifest
	// refuses m.
	PutManifest(ctx context.Context, m StoredManifest) error
	// Manifest answers the manifest stored under hash; false when none
	// is.
	Manifest(ctx context.Context, hash string) (StoredManifest, bool, error)
}

// StoredManifest is one stored agent configuration.
type StoredManifest struct {
	// Hash is sha256 of Body, lowercase hex.
	Hash string
	// Body is the one-agent core.Manifest document, verbatim.
	Body json.RawMessage
	// Source is where Studio first saw it: ManifestSourceFile or
	// ManifestSourceRuntime ("" when the writer did not say).
	Source string
	// FirstSeen is when it was first stored.
	FirstSeen time.Time
}

// The sources Studio writes manifests from.
const (
	ManifestSourceFile    = "file"    // the weft.json Studio serves
	ManifestSourceRuntime = "runtime" // a runtime registration
)

// ErrInvalidManifest is PutManifest's refusal of a row
// CheckStoredManifest rejects.
var ErrInvalidManifest = errors.New("obsdb: invalid manifest")

// ManifestHash is the hash a manifest body is stored under: sha256 of
// its bytes, lowercase hex — the core's weft.manifest.hash of the same
// bytes.
func ManifestHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// CheckStoredManifest is the rule both backends apply before a write:
// the body is a JSON object and the hash is its sha256 (ManifestHash).
func CheckStoredManifest(m StoredManifest) error {
	if !json.Valid(m.Body) || len(m.Body) == 0 {
		return fmt.Errorf("%w: body is not JSON", ErrInvalidManifest)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(m.Body, &obj) != nil || obj == nil {
		return fmt.Errorf("%w: body is not a JSON object", ErrInvalidManifest)
	}
	if want := ManifestHash(m.Body); m.Hash != want {
		return fmt.Errorf("%w: hash %q is not the body's sha256 %s", ErrInvalidManifest, m.Hash, want)
	}
	return nil
}
