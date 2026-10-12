package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The manifests a run's manifest_hash resolves to (U27, migration
// 0014): content-addressed, written once per hash, never deleted.

var _ obsdb.ManifestStore = (*DB)(nil)

// PutManifest stores m unless its hash is stored: the first write's
// row stays.
func (d *DB) PutManifest(ctx context.Context, m obsdb.StoredManifest) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if err := obsdb.CheckStoredManifest(m); err != nil {
		return err
	}
	if m.FirstSeen.IsZero() {
		m.FirstSeen = time.Now().UTC()
	}
	_, err = d.writer.ExecContext(ctx, `INSERT INTO manifests (hash, body, source, first_ns)
		VALUES (?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`,
		m.Hash, string(m.Body), m.Source, unixNS(m.FirstSeen))
	return err
}

// Manifest answers the manifest stored under hash.
func (d *DB) Manifest(ctx context.Context, hash string) (_ obsdb.StoredManifest, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.StoredManifest{}, false, err
	}
	defer d.closedErr(&err)
	m := obsdb.StoredManifest{Hash: hash}
	var body string
	var first int64
	err = d.reads.QueryRowContext(ctx, `SELECT body, source, first_ns FROM manifests WHERE hash = ?`, hash).
		Scan(&body, &m.Source, &first)
	if errors.Is(err, sql.ErrNoRows) {
		return obsdb.StoredManifest{}, false, nil
	}
	if err != nil {
		return obsdb.StoredManifest{}, false, err
	}
	m.Body = json.RawMessage(body)
	m.FirstSeen = time.Unix(0, first).UTC()
	return m, true, nil
}
