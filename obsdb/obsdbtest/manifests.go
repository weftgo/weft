package obsdbtest

import (
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The manifest store (U27, obsdb.ManifestStore): an agent's one-agent
// manifest under its sha256, the first write kept, and never deleted
// by the run deletions — a run that remains always resolves its
// manifest_hash. A backend that does not implement it is skipped: the
// capability is optional.

// manifestBody is a one-agent document in weft.Manifest's layout; the
// bytes are stored and answered verbatim, indentation and the trailing
// newline included.
const manifestBody = "{\n  \"weft\": 1,\n  \"agents\": [\n    {\n      \"name\": \"support\",\n      \"instructions\": \"Be brief.\"\n    }\n  ]\n}\n"

func manifests(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		ms, ok := db.(obsdb.ManifestStore)
		if !ok {
			t.Skip("backend does not implement obsdb.ManifestStore")
		}
		hash := obsdb.ManifestHash([]byte(manifestBody))

		// Refusals: a hash that is not the body's, a body that is not
		// a JSON object. Nothing is stored.
		for _, bad := range []obsdb.StoredManifest{
			{Hash: "deadbeef", Body: []byte(manifestBody)},
			{Hash: obsdb.ManifestHash([]byte(`[1]`)), Body: []byte(`[1]`)},
			{Hash: obsdb.ManifestHash(nil)},
		} {
			if err := ms.PutManifest(ctx(), bad); !errors.Is(err, obsdb.ErrInvalidManifest) {
				t.Errorf("PutManifest(%q) = %v, want ErrInvalidManifest", bad.Hash, err)
			}
		}
		if _, found, err := ms.Manifest(ctx(), hash); err != nil || found {
			t.Fatalf("Manifest before any write = found %v, %v; want not found", found, err)
		}

		// Nanoseconds included: a backend that floors them fails.
		first := time.Unix(0, 1790845923120000123).UTC()
		if err := ms.PutManifest(ctx(), obsdb.StoredManifest{Hash: hash, Body: []byte(manifestBody),
			Source: obsdb.ManifestSourceRuntime, FirstSeen: first}); err != nil {
			t.Fatal(err)
		}
		// The second write of a stored hash changes nothing: the first
		// row's source and time stay.
		if err := ms.PutManifest(ctx(), obsdb.StoredManifest{Hash: hash, Body: []byte(manifestBody),
			Source: obsdb.ManifestSourceFile, FirstSeen: first.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		check := func(when string) {
			t.Helper()
			m, found, err := ms.Manifest(ctx(), hash)
			if err != nil || !found {
				t.Fatalf("Manifest %s = found %v, %v; want it", when, found, err)
			}
			if string(m.Body) != manifestBody || m.Hash != hash || m.Source != obsdb.ManifestSourceRuntime || !m.FirstSeen.Equal(first) {
				t.Errorf("Manifest %s = %q %q %q %v; want the first write verbatim (%q, runtime, %v)",
					when, m.Hash, m.Body, m.Source, m.FirstSeen, manifestBody, first)
			}
		}
		check("after two writes")

		// A zero FirstSeen is stamped.
		other := "{\"weft\":1,\"agents\":[{\"name\":\"other\"}]}\n"
		if err := ms.PutManifest(ctx(), obsdb.StoredManifest{Hash: obsdb.ManifestHash([]byte(other)), Body: []byte(other)}); err != nil {
			t.Fatal(err)
		}
		if m, found, err := ms.Manifest(ctx(), obsdb.ManifestHash([]byte(other))); err != nil || !found || m.FirstSeen.IsZero() || m.Source != "" {
			t.Errorf("Manifest of a zero-time write = %+v, %v, %v; want found, stamped, no source", m, found, err)
		}

		// Retention: kept forever. A run carrying the hash deleted, a
		// sweep of everything, a vacuum — the manifest stays.
		r := delRun{id: "mf_run", session: "s_mf", t: time.Now().UTC().Add(-2 * time.Hour), end: "ok"}
		writeDel(t, db, "mf_run", r)
		if err := db.DeleteRun(ctx(), r.id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Sweep(ctx(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := db.Vacuum(ctx()); err != nil {
			t.Fatal(err)
		}
		check("after DeleteRun, Sweep and Vacuum")
	}
}
