package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
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
	return fs.String("retain", "", "`duration` (Go syntax: 720h, 168h30m; at least 1h) "+what+
		" (default: $WEFT_RETAIN, else none: everything is kept)")
}

// resolveRetain is --retain, else WEFT_RETAIN, as a duration: 0 when
// neither is set (off). A value that does not parse, a negative one, or
// one under an hour (studio.Retain's floor, the same words) is a usage
// error naming where it came from.
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
	if d > 0 && d < time.Hour {
		return 0, usageError("%s %q: %s", from, val, retainFloorMsg)
	}
	return d, nil
}

// retainFloorMsg is studio.Retain's refusal under an hour, word for
// word (TestRetainFloorMatchesStudio pins it to the panic New raises).
const retainFloorMsg = "retention must be at least 1h: the sweeper's not-running rule counts a run silent for 30 s " +
	"as interrupted, so a short retention would sweep a live run paused at a breakpoint"

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
		printDefaults(fs)
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageError("vacuum takes no arguments (got %s)", gotArgs(fs.Args()))
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
	beforeMain, beforeWAL := fileSizes(path)
	if err := db.Vacuum(ctx); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	if path != "" {
		// The main file and its WAL apart (UX-S5a-12): Vacuum checkpoints,
		// so the WAL is usually 0 after; one a live reader or writer held says so.
		afterMain, afterWAL := fileSizes(path)
		_, _ = fmt.Fprintf(stdout, "vacuum: %s %d → %d bytes (WAL %d → %d bytes)\n", path, beforeMain, afterMain, beforeWAL, afterWAL)
		if afterWAL > 0 {
			_, _ = fmt.Fprintln(stdout, "vacuum: the WAL is not checkpointed yet (a live reader or writer holds it — a running Studio): the next checkpoint folds it into the file")
		}
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
		path, from := strings.TrimPrefix(dbFlag, "sqlite://"), "--db"
		if dbFlag == "" {
			path, from = os.Getenv("WEFT_DB"), "WEFT_DB"
			if path == "" {
				path, from = "./.weft/weft.db", "the default"
			}
		}
		if path == "" {
			return nil, "", fmt.Errorf("--db sqlite:// needs a path (sqlite://weft.db, sqlite:///var/lib/weft.db)")
		}
		// sqlite.Open creates a missing file: vacuuming a database
		// that is not there must not leave an empty one behind.
		if path != ":memory:" {
			if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
				return nil, "", fmt.Errorf("no database at %s (nothing to vacuum)", path)
			} else if err != nil {
				return nil, "", fmt.Errorf("%s %s: %w", from, path, err)
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

// fileSizes is a SQLite file's bytes and its WAL sidecar's, each 0
// when it cannot be read (or path is "").
func fileSizes(path string) (main, wal int64) {
	if path == "" {
		return 0, 0
	}
	if fi, err := os.Stat(path); err == nil {
		main = fi.Size()
	}
	if fi, err := os.Stat(path + "-wal"); err == nil {
		wal = fi.Size()
	}
	return main, wal
}
