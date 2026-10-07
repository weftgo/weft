package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The experiments table (WEFT-PLAYGROUND §10.4, PQ4) behind the same
// interface method the sqlite backend serves: ReplacingMergeTree on
// Id, the newest write winning under FINAL — upserts are inserts, the
// house shape this backend gives every mutable row.

// SaveExperiment upserts one experiment by id (an insert; FINAL reads
// the newest).
func (d *DB) SaveExperiment(ctx context.Context, e obsdb.Experiment) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if e.ID == "" {
		return obsdb.ErrNotFound
	}
	now := time.Now().UTC()
	variants, err := json.Marshal(e.Variants)
	if err != nil {
		return err
	}
	inputs, err := json.Marshal(e.Inputs)
	if err != nil {
		return err
	}
	// Created survives an update: the row's own, else now. Only a
	// genuine ErrNotFound means "first save" — any other read error
	// must surface, not silently reset Created on an update (the
	// audit's P2-4: a transient read failure reset every experiment's
	// created-at to the save time).
	created := now
	prior, err := d.Experiment(ctx, e.ID)
	switch {
	case err == nil:
		created = prior.Created
	case errors.Is(err, obsdb.ErrNotFound):
	default:
		return err
	}
	// The two times bind as integer nanoseconds: a positional time.Time
	// renders at whole seconds (the driver's rule the cursors already
	// work around), which floored Created and Updated — the SQLite
	// backend keeps the nanoseconds.
	return d.conn.Exec(ctx, `INSERT INTO experiments
		(Id, Name, Agent, Created, Updated, Variants, Inputs)
		VALUES (?, ?, ?, fromUnixTimestamp64Nano(?), fromUnixTimestamp64Nano(?), ?, ?)`,
		e.ID, e.Name, e.Agent, created.UnixNano(), now.UnixNano(), string(variants), string(inputs))
}

// Experiments lists the saved definitions, newest update first.
func (d *DB) Experiments(ctx context.Context) (_ []obsdb.Experiment, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	// InsertTime, not Updated, orders the FINAL read (step 8b review
	// fix 2): it is the version column FINAL itself resolves by, so the
	// list order and the surviving row can never disagree. (Updated was
	// also written at whole seconds until the nanosecond bind; rows
	// from before it still tie there.)
	rows, err := d.conn.Query(ctx, `SELECT Id, Name, Agent, Created, Updated, Variants, Inputs
		FROM experiments FINAL ORDER BY InsertTime DESC, Id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []obsdb.Experiment
	for rows.Next() {
		var e obsdb.Experiment
		var variants, inputs string
		if err := rows.Scan(&e.ID, &e.Name, &e.Agent, &e.Created, &e.Updated, &variants, &inputs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(variants), &e.Variants); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(inputs), &e.Inputs); err != nil {
			return nil, err
		}
		e.Created, e.Updated = e.Created.UTC(), e.Updated.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Experiment returns one experiment; ErrNotFound otherwise.
func (d *DB) Experiment(ctx context.Context, id string) (_ obsdb.Experiment, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.Experiment{}, err
	}
	defer d.closedErr(&err)
	rows, err := d.conn.Query(ctx, `SELECT Id, Name, Agent, Created, Updated, Variants, Inputs
		FROM experiments FINAL WHERE Id = ? ORDER BY InsertTime DESC`, id)
	if err != nil {
		return obsdb.Experiment{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		// A read that failed is not a missing row: SaveExperiment takes
		// ErrNotFound as "first save" and would reset Created.
		if err := rows.Err(); err != nil {
			return obsdb.Experiment{}, err
		}
		return obsdb.Experiment{}, obsdb.ErrNotFound
	}
	var e obsdb.Experiment
	var variants, inputs string
	if err := rows.Scan(&e.ID, &e.Name, &e.Agent, &e.Created, &e.Updated, &variants, &inputs); err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(variants), &e.Variants); err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(inputs), &e.Inputs); err != nil {
		return e, err
	}
	e.Created, e.Updated = e.Created.UTC(), e.Updated.UTC()
	return e, nil
}
