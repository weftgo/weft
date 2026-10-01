package clickhouse

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The experiments table (WEFT-PLAYGROUND §10.4, PQ4) behind the same
// interface method the sqlite backend serves: ReplacingMergeTree on
// Id, the newest write winning under FINAL — upserts are inserts, the
// house shape this backend gives every mutable row.

// SaveExperiment upserts one experiment by id (an insert; FINAL reads
// the newest).
func (d *DB) SaveExperiment(ctx context.Context, e obsdb.Experiment) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
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
	// Created survives an update: read the row's own, else now.
	created := now
	if prior, err := d.Experiment(ctx, e.ID); err == nil {
		created = prior.Created
	}
	return d.conn.Exec(ctx, `INSERT INTO experiments
		(Id, Name, Agent, Created, Updated, Variants, Inputs) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, e.Agent, created, now, string(variants), string(inputs))
}

// Experiments lists the saved definitions, newest update first.
func (d *DB) Experiments(ctx context.Context) ([]obsdb.Experiment, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	rows, err := d.conn.Query(ctx, `SELECT Id, Name, Agent, Created, Updated, Variants, Inputs
		FROM experiments FINAL ORDER BY Updated DESC, Id`)
	if err != nil {
		return nil, err
	}
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
		out = append(out, e)
	}
	return out, rows.Err()
}

// Experiment returns one experiment; ErrNotFound otherwise.
func (d *DB) Experiment(ctx context.Context, id string) (obsdb.Experiment, error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.Experiment{}, err
	}
	rows, err := d.conn.Query(ctx, `SELECT Id, Name, Agent, Created, Updated, Variants, Inputs
		FROM experiments FINAL WHERE Id = ?`, id)
	if err != nil {
		return obsdb.Experiment{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
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
	return e, nil
}
