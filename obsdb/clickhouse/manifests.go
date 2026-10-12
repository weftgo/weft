package clickhouse

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The manifests a run's ManifestHash resolves to (U27, migration 0016):
// content-addressed, the first write kept, never deleted.

var _ obsdb.ManifestStore = (*DB)(nil)

// PutManifest stores m unless its hash is stored. Two racing writers may
// both insert; the rows' Ver (2^63 - FirstNs) and Manifest's argMin keep
// the earlier one, and the body is the same by construction.
func (d *DB) PutManifest(ctx context.Context, m obsdb.StoredManifest) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if err := obsdb.CheckStoredManifest(m); err != nil {
		return err
	}
	var n uint64
	if err := d.conn.QueryRow(ctx, `SELECT count() FROM manifests WHERE Hash = ?`, m.Hash).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if m.FirstSeen.IsZero() {
		m.FirstSeen = time.Now().UTC()
	}
	first := nsOf(m.FirstSeen)
	return d.conn.Exec(ctx, `INSERT INTO manifests (Hash, Body, Source, FirstNs, Ver) VALUES (?, ?, ?, ?, ?)`,
		m.Hash, string(m.Body), m.Source, first, uint64(math.MaxInt64)-uint64(first))
}

// Manifest answers the manifest stored under hash: its earliest row.
func (d *DB) Manifest(ctx context.Context, hash string) (_ obsdb.StoredManifest, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.StoredManifest{}, false, err
	}
	defer d.closedErr(&err)
	rows, err := d.conn.Query(ctx, `SELECT argMin(Body, FirstNs), argMin(Source, FirstNs), min(FirstNs)
		FROM manifests WHERE Hash = ? GROUP BY Hash`, hash)
	if err != nil {
		return obsdb.StoredManifest{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return obsdb.StoredManifest{}, false, rows.Err()
	}
	m := obsdb.StoredManifest{Hash: hash}
	var body string
	var first int64
	if err := rows.Scan(&body, &m.Source, &first); err != nil {
		return obsdb.StoredManifest{}, false, err
	}
	m.Body = json.RawMessage(body)
	m.FirstSeen = timeOfNS(first)
	return m, true, rows.Err()
}
