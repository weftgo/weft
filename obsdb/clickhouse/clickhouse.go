// Package clickhouse — see doc.go for the package documentation.
package clickhouse

import (
	"context"
	"fmt"
	"sync"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
)

// DefaultContentTTL and DefaultMetaTTL are S3.6's retention defaults:
// content-bearing rows (otel_logs, weft_records, weft_deltas) 30 days,
// spans and runs 90 days.
const (
	DefaultContentTTL = 30 * 24 * time.Hour
	DefaultMetaTTL    = 90 * 24 * time.Hour
)

// DB is the ClickHouse backend. Create it with Open; the zero value is
// not usable. It is safe for concurrent use: the driver pools
// connections, writers batch per Write.
type DB struct {
	conn       ch.Conn
	keepDeltas bool
	contentTTL time.Duration
	metaTTL    time.Duration
	mu         sync.Mutex
	closed     bool
	closeErr   error
}

// Option configures Open.
type Option interface{ apply(*openConfig) }

type openConfig struct {
	keepDeltas bool
	contentTTL time.Duration
	metaTTL    time.Duration
}

type openOption func(*openConfig)

func (f openOption) apply(c *openConfig) { f(c) }

// KeepDeltas turns delta storage on for debugging (Q4, closed): deltas
// are counted in weft_runs' DeltaCount high-water mark but never stored
// by default — the weft_records view filters them out by construction.
// With this option Write also inserts each delta into weft_deltas
// (ReplacingMergeTree on (RunId, Pos)), which nothing else feeds; the
// table stays empty without the option.
func KeepDeltas() Option {
	return openOption(func(c *openConfig) { c.keepDeltas = true })
}

// TTL overrides the retention windows. content applies to
// otel_logs, weft_records and weft_deltas; meta applies to otel_traces
// and weft_runs. Values ≤ 0 keep the default for that class
// (DefaultContentTTL / DefaultMetaTTL), so TTL(0, 7*24*time.Hour)
// changes only the spans-and-runs window. Open applies non-default
// windows with ALTER TABLE ... MODIFY TTL after the migrations (a no-op
// on the server when the clause is unchanged). An Open with both
// classes at their defaults alters nothing: the tables keep whatever
// windows they already have — the migration's defaults on a fresh
// database, or an earlier Open's override — so a process that opens
// the database without TTL (the studio binary) never undoes the
// windows the writer set. Windows are whole seconds, rounded up.
func TTL(content, meta time.Duration) Option {
	return openOption(func(c *openConfig) {
		if content > 0 {
			c.contentTTL = content
		}
		if meta > 0 {
			c.metaTTL = meta
		}
	})
}

// Open connects using a clickhouse-go DSN
// (clickhouse://user:pass@host:9000/database?...; clickhouses:// or
// secure=1 for TLS), brings the schema up to date, applies TTL
// overrides, and returns an obsdb.DB over the DSN's database. The
// connection carries async_insert=1 and wait_for_async_insert=1 so
// batched writes return once the server has flushed them.
func Open(dsn string, opts ...Option) (obsdb.DB, error) {
	cfg := openConfig{contentTTL: DefaultContentTTL, metaTTL: DefaultMetaTTL}
	for _, o := range opts {
		o.apply(&cfg)
	}
	options, err := ch.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: dsn: %w", err)
	}
	if options.Settings == nil {
		options.Settings = ch.Settings{}
	}
	// S3.6: writes use async inserts, batched. wait_for_async_insert=1
	// keeps write-then-read exact for a caller that just wrote.
	options.Settings["async_insert"] = 1
	options.Settings["wait_for_async_insert"] = 1
	conn, err := ch.Open(options)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}
	ctx := context.Background()
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping: %w", err)
	}
	if err := migrate(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	db := &DB{
		conn:       conn,
		keepDeltas: cfg.keepDeltas,
		contentTTL: cfg.contentTTL,
		metaTTL:    cfg.metaTTL,
	}
	if err := db.applyTTLs(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return db, nil
}

// applyTTLs sets the tables' TTL clauses to the configured windows.
// The migration creates them with the defaults; a TTL(...) override
// rewrites them on every Open, which is idempotent and keeps the
// schema inspectable (SHOW CREATE TABLE tells the truth). With both
// classes at their defaults nothing is altered (see TTL).
func (d *DB) applyTTLs(ctx context.Context) error {
	if d.contentTTL == DefaultContentTTL && d.metaTTL == DefaultMetaTTL {
		return nil
	}
	contentSec, metaSec := ttlSeconds(d.contentTTL), ttlSeconds(d.metaTTL)
	for _, stmt := range []string{
		fmt.Sprintf("ALTER TABLE otel_logs MODIFY TTL Timestamp + toIntervalSecond(%d)", contentSec),
		fmt.Sprintf("ALTER TABLE weft_records MODIFY TTL Time + toIntervalSecond(%d)", contentSec),
		fmt.Sprintf("ALTER TABLE weft_deltas MODIFY TTL Time + toIntervalSecond(%d)", contentSec),
		fmt.Sprintf("ALTER TABLE otel_traces MODIFY TTL Timestamp + toIntervalSecond(%d)", metaSec),
		fmt.Sprintf("ALTER TABLE weft_runs MODIFY TTL LastSeen + toIntervalSecond(%d)", metaSec),
	} {
		if err := d.conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("clickhouse: ttl: %w", err)
		}
	}
	return nil
}

// ttlSeconds renders a window as whole seconds, rounded up: a
// sub-second window truncated to toIntervalSecond(0) would expire every
// row the moment it is written.
func ttlSeconds(d time.Duration) int64 {
	return int64((d + time.Second - 1) / time.Second)
}

// Close closes the connection. Further calls return obsdb.ErrClosed.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return d.closeErr
	}
	d.closed = true
	d.closeErr = d.conn.Close()
	return d.closeErr
}

func (d *DB) checkOpen() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return obsdb.ErrClosed
	}
	return nil
}

// closedErr turns the error of a call that raced Close into ErrClosed:
// checkOpen passed, then Close shut the handle under the call, and the
// driver's own "closed" error is not the documented sentinel. Every
// method defers it on its error result.
func (d *DB) closedErr(err *error) {
	if *err != nil && d.checkOpen() != nil {
		*err = obsdb.ErrClosed
	}
}
