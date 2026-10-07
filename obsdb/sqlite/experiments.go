package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The experiments table (WEFT-PLAYGROUND §10.4, PQ4): the definition
// rows — name, variants, inputs — keyed by the id the runs carry as
// weft.experiment.id. Upserted by id; the runs stay in runs and join
// by experiment_id (RunQuery.ExperimentID).

// SaveExperiment upserts one experiment by id. A new id stamps
// Created; an existing one keeps it and moves Updated.
func (d *DB) SaveExperiment(ctx context.Context, e obsdb.Experiment) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if e.ID == "" {
		return obsdb.ErrNotFound
	}
	now := time.Now().UTC()
	variants, err := marshalJSON(e.Variants)
	if err != nil {
		return err
	}
	inputs, err := marshalJSON(e.Inputs)
	if err != nil {
		return err
	}
	_, err = d.writer.ExecContext(ctx, `INSERT INTO experiments
		(id, name, agent, created_ns, updated_ns, variants, inputs)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  agent = excluded.agent,
		  updated_ns = excluded.updated_ns,
		  variants = excluded.variants,
		  inputs = excluded.inputs`,
		e.ID, e.Name, e.Agent, now.UnixNano(), now.UnixNano(), variants, inputs)
	return err
}

// Experiments lists the saved experiments, newest update first.
func (d *DB) Experiments(ctx context.Context) (_ []obsdb.Experiment, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	rows, err := d.reads.QueryContext(ctx,
		`SELECT id, name, agent, created_ns, updated_ns, variants, inputs
		 FROM experiments ORDER BY updated_ns DESC, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []obsdb.Experiment
	for rows.Next() {
		e, err := scanExperiment(rows)
		if err != nil {
			return nil, err
		}
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
	rows, err := d.reads.QueryContext(ctx,
		`SELECT id, name, agent, created_ns, updated_ns, variants, inputs
		 FROM experiments WHERE id = ?`, id)
	if err != nil {
		return obsdb.Experiment{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		// A read that failed is not "no such experiment".
		if err := rows.Err(); err != nil {
			return obsdb.Experiment{}, err
		}
		return obsdb.Experiment{}, obsdb.ErrNotFound
	}
	return scanExperiment(rows)
}

func scanExperiment(rows *sql.Rows) (obsdb.Experiment, error) {
	var e obsdb.Experiment
	var created, updated int64
	var variants, inputs string
	if err := rows.Scan(&e.ID, &e.Name, &e.Agent, &created, &updated, &variants, &inputs); err != nil {
		return e, err
	}
	e.Created = time.Unix(0, created).UTC()
	e.Updated = time.Unix(0, updated).UTC()
	if err := json.Unmarshal([]byte(variants), &e.Variants); err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(inputs), &e.Inputs); err != nil {
		return e, err
	}
	return e, nil
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
