package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/clickhouse"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/studio"
)

// retainFlag adds --retain to a flag set: studio.Retain's duration,
// mirroring WEFT_RETAIN one to one (resolveRetain reads both).
func retainFlag(fs *flag.FlagSet, what string) *string {
	return fs.String("retain", "", "`duration` (Go syntax: 720h, 168h30m) "+what+
		" (default: $WEFT_RETAIN, else none: everything is kept)")
}

// resolveRetain is --retain, else WEFT_RETAIN, as a duration: 0 when
// neither is set (off). A value that does not parse, or a negative one,
// is a usage error naming where it came from.
func resolveRetain(flagVal string) (time.Duration, error) {
	val, from := flagVal, "--retain"
	if val == "" {
		val, from = os.Getenv("WEFT_RETAIN"), "WEFT_RETAIN"
	}
	if val == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, usageError("%s %q: not a duration (720h, 168h30m)", from, val)
	}
	if d < 0 {
		return 0, usageError("%s %q: a retention cannot be negative", from, val)
	}
	return d, nil
}

// retainOptions turns --retain / WEFT_RETAIN into studio options and the
// line the banner prints ("" when off).
func retainOptions(flagVal string) ([]studio.Option, string, error) {
	d, err := resolveRetain(flagVal)
	if err != nil || d == 0 {
		return nil, "", err
	}
	return []studio.Option{studio.Retain(d)}, "studio: retention " + d.String() +
		": runs that are not running and were last seen longer ago are deleted (studio's tables only)", nil
}

// runVacuum is `weft vacuum`: one retention sweep when a retention is
// given (--retain / WEFT_RETAIN), then the backend's Vacuum (SQLite:
// VACUUM; ClickHouse: OPTIMIZE TABLE … FINAL on the tables deletions
// mutate). It touches obsdb's tables only: a SQLite file that also
// holds the app's thread sessions keeps them as they are (VACUUM
// rewrites the file, their contents unchanged).
func runVacuum(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("vacuum", stderr)
	dbFlag := fs.String("db", "",
		"`sqlite://path` or `clickhouse://user:pass@host:9000/db` (default: $WEFT_DB or ./.weft/weft.db)")
	retain := retainFlag(fs, "to sweep first: delete the runs not running last seen longer ago")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: weft vacuum [--db sqlite://path|clickhouse://…] [--retain duration]")
		_, _ = fmt.Fprintln(fs.Output(), "One retention sweep (with --retain or WEFT_RETAIN), then VACUUM (SQLite) or OPTIMIZE TABLE … FINAL (ClickHouse); studio's tables only.")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageError("vacuum takes no arguments, got %q", fs.Args())
	}
	d, err := resolveRetain(*retain)
	if err != nil {
		return err
	}
	db, path, err := openVacuumDB(*dbFlag)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if d > 0 {
		cutoff := time.Now().Add(-d)
		n, err := db.Sweep(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("sweep: %w", err)
		}
		_, _ = fmt.Fprintf(stdout, "vacuum: swept %d runs last seen before %s (retention %s)\n",
			n, cutoff.UTC().Format(time.RFC3339), d)
	}
	before := fileSize(path)
	if err := db.Vacuum(ctx); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	if path != "" {
		_, _ = fmt.Fprintf(stdout, "vacuum: %s %d → %d bytes\n", path, before, fileSize(path))
	} else {
		_, _ = fmt.Fprintf(stdout, "vacuum: %s optimized\n", dbLabel(*dbFlag))
	}
	return nil
}

// openVacuumDB opens --db as the studio subcommand reads it, returning
// the SQLite file's path ("" for ClickHouse).
func openVacuumDB(dbFlag string) (obsdb.DB, string, error) {
	switch {
	case dbFlag == "" || strings.HasPrefix(dbFlag, "sqlite://"):
		path := strings.TrimPrefix(dbFlag, "sqlite://")
		if dbFlag == "" {
			path = os.Getenv("WEFT_DB")
			if path == "" {
				path = ".weft/weft.db"
			}
		}
		if path == "" {
			return nil, "", fmt.Errorf("--db sqlite:// needs a path (sqlite://weft.db, sqlite:///var/lib/weft.db)")
		}
		if path != ":memory:" {
			if _, err := os.Stat(path); err != nil {
				return nil, "", fmt.Errorf("--db %s: %w", path, err)
			}
		}
		db, err := sqlite.Open(path)
		if err != nil {
			return nil, "", err
		}
		if path == ":memory:" {
			path = ""
		}
		return db, path, nil
	case strings.HasPrefix(dbFlag, "clickhouse://"):
		db, err := clickhouse.Open(dbFlag)
		return db, "", err
	default:
		return nil, "", fmt.Errorf("--db must be sqlite://path or clickhouse://user:pass@host:9000/db")
	}
}

// fileSize is a SQLite file's bytes with its WAL sidecar, 0 when it
// cannot be read (or path is "").
func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	var n int64
	for _, p := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}
